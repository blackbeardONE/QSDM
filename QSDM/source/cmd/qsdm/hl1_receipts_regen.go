package main

// hl1_receipts_regen.go (HL1 errata E7): the boot step S4r, "complete the tip
// block's receipts" (design rev 4 §5 S4r, §4.6 C5/C6/F1, runbook R-C6).
//
// A crash after H3 or H4 (C5, C6a), or an I/O error at H5 (F1, after R-FS),
// leaves block N durable except for its receipts: the journal holds N (H2
// fsynced), the accounts snapshot is at N, the .h<N-1> generation links exist
// (H1), W is N-1 and H5 never wrote. S4 accepts that state, and S5 rule 6
// then writes W := N, so without S4r N's receipts would never be written. S4r
// runs in the producer role after S4 and before S5, single-threaded under the
// state lock, before OpenChainJournal, before the persistence hook is
// installed and before anything is served. When S5 is about to advance W over
// the tip N (hl1S5Decide), it:
//
//	a. checks the receipts invariants (R-X on failure);
//	b. finds the txs of N without a receipt at height N; none: nothing is
//	   written (C6b, C7), but the receipts file and the state directory are
//	   fsynced, because a kill between an append and its fsync (inside H5, or
//	   at boot:S4r:after-append) can leave N's lines in the page cache only;
//	c. loads the .h<N-1> pair into fresh stores and requires it to reproduce
//	   block N-1's state root (hl1EvaluatePair);
//	d. builds a scratch applier, a clone of the live applier restored to the
//	   N-1 state (no v2wiring.Wire, so no second set of api/monitoring/mining
//	   globals, and no GovApplier, like every follower and R-C4 clone), whose
//	   height is pinned to N;
//	e. replays N on a scratch producer with TryAppendExternalBlock, the
//	   follower and R-C4 path, which regenerates N's receipts; the spec, live
//	   and receipt passes all see height N, as the producer's did;
//	f. compares the replayed post-state with the durable N state: roots,
//	   accounts bytes, canonical enrollment; every tx of N must regenerate with
//	   status 1 (a sealed block holds only txs that applied), and every receipt
//	   already present at N must equal its regenerated one (timestamps
//	   ignored);
//	g. appends only the missing receipts with the H5 writer, fsyncs the file
//	   and the state directory, then stores them in memory.
//
// S4r never writes W; S5 rule 6 writes W := N afterwards. Every refusal and
// every I/O error is returned, and main() exits 78 (fatalRestore) with W
// still at N-1, so the next boot runs S4r again: a complete prefix of the
// appended lines is finished, a fragment is refused by S4 (R-C3).
//
// In-process locks (design §4.5 L9): the scratch producer's
// sealLifecycleMu -> mu, and the leaf locks of the live applier (RLock, for
// the clone) and of the admin receipt store. Never a ledger, guard or store
// lock.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/blackbeardONE/QSDM/internal/legacymining"
	"github.com/blackbeardONE/QSDM/internal/logging"
	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/mining/enrollment"
	"github.com/blackbeardONE/QSDM/pkg/producerpolicy"
)

// Runbook routes named by S4r refusals (runbooks/R-C6.md).
const (
	hl1S4rRouteRX   = "R-X"
	hl1S4rRouteC6RX = "R-C6 (route: R-X)"
	hl1S4rRouteC6C4 = "R-C6 (route: R-C4)"
	hl1S4rRouteC6IO = "R-C6 (route: fix the disk, then R-START)"
)

// hl1S4rScratchReplays counts the scratch replays S4r started (tests).
var hl1S4rScratchReplays atomic.Uint64

// hl1RepairOptions are the inputs of hl1RepairTipReceipts.
type hl1RepairOptions struct {
	// ProducerRole: S4r runs only in the producer role (W exists only there).
	ProducerRole bool

	StateDir       string
	AccountsPath   string // main accounts snapshot (at N)
	EnrollmentPath string // main enrollment snapshot
	ReceiptsPath   string // receipts NDJSON

	Transition *producerpolicy.Transition
	Authorized []string // external producer allowlist, as main() configures it

	// Blocks is the restored chain (S4); its last block is the tip N.
	Blocks []*chain.Block
	// Live is the live state applier, restored and root-checked at N by S4.
	// It is only read (cloned).
	Live chain.ChainReplayApplier
	// Receipts is the admin receipt store loaded by S4. The regenerated
	// receipts are stored into it after they are durable.
	Receipts *chain.ReceiptStore

	FS hl1FS
	// Append writes the receipts of rs at height to path (the H5 writer);
	// nil means rs.AppendBlockNDJSON.
	Append func(rs *chain.ReceiptStore, path string, height uint64) (int, error)
	Now    func() time.Time
	Log    *logging.Logger

	// mutatePost, when set (tests), runs on the replayed applier after the
	// sweep and before the post-state comparison.
	mutatePost func(*chain.EnrollmentAwareApplier)
	// scratchHeight, when set (tests), replaces the scratch applier's pinned
	// height N; tip is the scratch producer's tip height.
	scratchHeight func(n uint64, tip func() uint64) uint64
}

// hl1RepairResult reports an S4r run.
type hl1RepairResult struct {
	Gate        bool // tip = W+1: S5 rule 6 is about to advance W over the tip
	Height      uint64
	Hash        string
	Regenerated []string // tx IDs whose receipts were appended, in block order
	Written     int      // receipts lines appended
}

func hl1S4rErr(route, format string, a ...any) error {
	return fmt.Errorf("%s; follow %s", fmt.Sprintf(format, a...), route)
}

// hl1BlocksAt returns the block at a height of a contiguous restored chain.
func hl1BlocksAt(blocks []*chain.Block) func(uint64) (*chain.Block, bool) {
	return func(h uint64) (*chain.Block, bool) {
		if len(blocks) == 0 || blocks[0] == nil || h < blocks[0].Height {
			return nil, false
		}
		i := h - blocks[0].Height
		if i >= uint64(len(blocks)) || blocks[i] == nil || blocks[i].Height != h {
			return nil, false
		}
		return blocks[i], true
	}
}

// hl1RepairTipReceipts is S4r. It returns an error only for a refusal or an
// I/O error (exit 78). Whenever S5 would refuse, or tip = W, it does nothing.
func hl1RepairTipReceipts(o hl1RepairOptions) (res hl1RepairResult, err error) {
	if !o.ProducerRole || o.Receipts == nil || o.ReceiptsPath == "" || len(o.Blocks) == 0 {
		return res, nil
	}
	log := o.Log
	if log == nil {
		log = hl1Logger()
	}
	now := o.Now
	if now == nil {
		now = time.Now
	}
	fsys := o.FS
	if fsys == nil {
		fsys = hl1OSFS{}
	}
	start := now()

	// Gate: S5 rule 6 is about to write W := tip. Anything S5 refuses, and
	// tip = W (H5 runs before H7), is left to S5 unchanged.
	w, werr := hl1ReadWatermark(o.StateDir)
	if werr != nil {
		return res, nil
	}
	blocks := o.Blocks
	blkN := blocks[len(blocks)-1]
	if advance, derr := hl1S5Decide(w, blkN, hl1BlocksAt(blocks)); derr != nil || !advance {
		return res, nil
	}
	n := blkN.Height
	res.Gate, res.Height, res.Hash = true, n, blkN.Hash
	if len(blocks) < 2 || blocks[len(blocks)-2] == nil || blocks[len(blocks)-2].Height != w.Height || blocks[len(blocks)-2].Hash != w.Hash {
		return res, hl1S4rErr(hl1S4rRouteRX, "block %d: the restored chain has no block W=%d right before the tip", n, w.Height)
	}
	blkW := blocks[len(blocks)-2]

	// a. Invariants.
	if err := hl1S4rCheckInvariants(o, fsys, log, w, blkW, blkN); err != nil {
		return res, err
	}

	// b. The txs of N without a receipt at height N. A receipt at another
	// height (an earlier failed attempt of the same tx) counts as missing.
	present := make(map[string]*chain.TxReceipt)
	for _, r := range o.Receipts.GetByBlock(n) {
		if r != nil {
			present[r.TxID] = r
		}
	}
	var missing []string
	for _, tx := range blkN.Transactions {
		if tx != nil && present[tx.ID] == nil {
			missing = append(missing, tx.ID)
		}
	}
	if len(missing) == 0 {
		// Nothing to write, but the lines may be in the page cache only: a
		// kill between an append and its fsync (inside H5, or at
		// boot:S4r:after-append) leaves them complete and not durable, and S5
		// is about to write W := N (§3.2: fsync(receipts) before W). The bytes
		// do not change.
		_, serr := fsys.Lstat(o.ReceiptsPath)
		switch {
		case serr == nil:
			serr = hl1S4rSyncReceipts(o, fsys, n, true)
		case errors.Is(serr, fs.ErrNotExist):
			serr = hl1S4rSyncReceipts(o, fsys, n, false)
		default:
			serr = hl1S4rErr(hl1S4rRouteC6IO, "block %d: stat %s: %v", n, o.ReceiptsPath, serr)
		}
		if serr != nil {
			return res, serr
		}
		log.Info("hl1 S4r: the tip block above W has all its receipts; made them durable, nothing to write",
			"height", n, "hash", blkN.Hash, "watermark_height", w.Height, "receipts", len(present))
		return res, nil
	}
	log.Warn("hl1 S4r: the tip block above W lacks receipts; regenerating them from the .h<N-1> generation",
		"height", n, "hash", blkN.Hash, "watermark_height", w.Height, "watermark_source", w.Source,
		"missing", len(missing), "present", len(present))

	// c-e. Replay N onto the N-1 generation.
	pre, rs, err := hl1S4rReplay(o, fsys, blocks, blkW, blkN)
	if err != nil {
		return res, err
	}

	// f. The replayed post-state must be the durable N state.
	regen, err := hl1S4rCompare(o, log, pre, rs, blkN, present)
	if err != nil {
		return res, err
	}

	// g. Append only the missing receipts (the H5 writer), make them durable,
	// then store them in memory.
	out := chain.NewReceiptStore()
	for _, tx := range blkN.Transactions {
		if tx == nil || present[tx.ID] != nil {
			continue
		}
		cp := *regen[tx.ID]
		// Every receipt path stamps a node-local time.Now(), which is in no
		// hash, root or block; the block's own timestamp is deterministic and
		// within milliseconds of the lost original.
		cp.Timestamp = blkN.Timestamp
		out.Store(&cp)
		res.Regenerated = append(res.Regenerated, tx.ID)
	}
	if err := hl1Fault("boot:S4r:append"); err != nil {
		return res, hl1S4rErr(hl1S4rRouteC6IO, "block %d: append receipts: %v", n, err)
	}
	appendFn := o.Append
	if appendFn == nil {
		appendFn = func(rs *chain.ReceiptStore, path string, height uint64) (int, error) {
			return rs.AppendBlockNDJSON(path, height)
		}
	}
	res.Written, err = appendFn(out, o.ReceiptsPath, n)
	if err != nil {
		return res, hl1S4rErr(hl1S4rRouteC6IO, "block %d: append %d receipt(s) (%d written): %v", n, len(res.Regenerated), res.Written, err)
	}
	hl1Crashpoint("boot:S4r:after-append")
	if err := hl1S4rSyncReceipts(o, fsys, n, true); err != nil {
		return res, err
	}
	for _, r := range out.GetByBlock(n) {
		o.Receipts.Store(r)
	}
	log.Info("hl1 S4r: receipts regenerated",
		"height", n, "hash", blkN.Hash, "tx_ids", strings.Join(res.Regenerated, ","),
		"written", res.Written, "kept", len(present), "elapsed_ms", now().Sub(start).Milliseconds())
	return res, nil
}

// hl1S4rSyncReceipts makes N's receipts lines durable before S5 writes W:
// fsync(receipts) (file: the receipts file exists), then fsync(stateDir),
// which also covers a newly created file (§3.2).
func hl1S4rSyncReceipts(o hl1RepairOptions, fsys hl1FS, n uint64, file bool) error {
	if err := hl1Fault("boot:S4r:fsync"); err != nil {
		return hl1S4rErr(hl1S4rRouteC6IO, "block %d: fsync receipts: %v", n, err)
	}
	if file {
		if err := fsys.SyncFile(o.ReceiptsPath); err != nil {
			return hl1S4rErr(hl1S4rRouteC6IO, "block %d: fsync receipts: %v", n, err)
		}
	}
	if err := fsys.SyncDir(o.StateDir); err != nil {
		return hl1S4rErr(hl1S4rRouteC6IO, "block %d: fsync state directory: %v", n, err)
	}
	return nil
}

// hl1S4rCheckInvariants is S4r step a.
func hl1S4rCheckInvariants(o hl1RepairOptions, fsys hl1FS, log *logging.Logger, w legacymining.Watermark, blkW, blkN *chain.Block) error {
	n := blkN.Height
	h, ok, err := hl1LastReceiptHeight(fsys, o.ReceiptsPath)
	if err != nil {
		return err
	}
	if ok && h > n {
		return hl1S4rErr(hl1S4rRouteRX, "block %d: the last receipts line is at height %d, above the restored tip", n, h)
	}
	inBlock := make(map[string]bool, len(blkN.Transactions))
	for _, tx := range blkN.Transactions {
		if tx != nil {
			inBlock[tx.ID] = true
		}
	}
	for _, r := range o.Receipts.GetByBlock(n) {
		if r == nil {
			continue
		}
		if r.BlockHash != blkN.Hash {
			return hl1S4rErr(hl1S4rRouteRX, "block %d: the receipt of tx %s carries block hash %s, the restored block is %s", n, r.TxID, r.BlockHash, blkN.Hash)
		}
		if !inBlock[r.TxID] && !(r.Status == chain.ReceiptFailed && r.IndexInBlock >= len(blkN.Transactions)) {
			return hl1S4rErr(hl1S4rRouteRX, "block %d: the receipt of tx %s is for a tx that is not in the block and is not a local failed-drop (status %d, index %d)", n, r.TxID, r.Status, r.IndexInBlock)
		}
	}
	atW := make(map[string]bool)
	for _, r := range o.Receipts.GetByBlock(blkW.Height) {
		if r != nil && r.BlockHash == blkW.Hash {
			atW[r.TxID] = true
		}
	}
	var lack []string
	for _, tx := range blkW.Transactions {
		if tx != nil && !atW[tx.ID] {
			lack = append(lack, tx.ID)
		}
	}
	if len(lack) == 0 {
		return nil
	}
	switch w.Source {
	case legacymining.WatermarkSourceSeal, legacymining.WatermarkSourceReplay:
		return hl1S4rErr(hl1S4rRouteRX, "block W=%d lacks receipts for %d of its txs (first %s) although W was advanced by an HL1 seal or replay (source %s): more than one block lacks receipts",
			blkW.Height, len(lack), lack[0], w.Source)
	default:
		log.Warn("hl1 S4r: block W lacks receipts; W was seeded or advanced at boot (R0 history or a pre-fix binary), not repaired",
			"watermark_height", blkW.Height, "watermark_source", w.Source, "missing", len(lack), "first_tx_id", lack[0])
		return nil
	}
}

// hl1LastReceiptHeight returns the block height of the last complete
// receipts line, and false for an absent or empty file.
func hl1LastReceiptHeight(fsys hl1FS, path string) (uint64, bool, error) {
	for _, window := range []int64{64 << 10, 8 << 20} {
		tail, size, err := fsys.ReadTail(path, window)
		if errors.Is(err, fs.ErrNotExist) {
			return 0, false, nil
		}
		if err != nil {
			return 0, false, hl1S4rErr(hl1S4rRouteC6IO, "read the receipts tail: %v", err)
		}
		if size == 0 {
			return 0, false, nil
		}
		if tail[len(tail)-1] != '\n' {
			return 0, false, hl1S4rErr("R-C3", "the receipts file does not end with a newline")
		}
		body := bytes.TrimRight(tail, "\n")
		i := bytes.LastIndexByte(body, '\n')
		if i < 0 && int64(len(tail)) < size {
			continue // the last line is longer than the window
		}
		if len(body) == 0 {
			return 0, false, nil
		}
		var r struct {
			BlockHeight uint64 `json:"block_height"`
		}
		if err := json.Unmarshal(body[i+1:], &r); err != nil {
			return 0, false, hl1S4rErr(hl1S4rRouteRX, "the last receipts line does not parse: %v", err)
		}
		return r.BlockHeight, true, nil
	}
	return 0, false, hl1S4rErr(hl1S4rRouteRX, "the last receipts line is longer than 8 MiB")
}

// hl1S4rReplay is S4r steps c-e: it returns the scratch applier after block
// N (before the sweep) and the receipt store holding N's regenerated
// receipts.
func hl1S4rReplay(o hl1RepairOptions, fsys hl1FS, blocks []*chain.Block, blkW, blkN *chain.Block) (*chain.EnrollmentAwareApplier, *chain.ReceiptStore, error) {
	n, prev := blkN.Height, blkW.Height
	// The 3-index slice keeps the scratch producer's append from writing into
	// the backing array that the admin producer holds.
	prefix := blocks[: len(blocks)-1 : len(blocks)-1]
	if o.Live == nil {
		return nil, nil, hl1S4rErr(hl1S4rRouteC6RX, "block %d: the live state applier cannot be cloned for replay", n)
	}
	if o.EnrollmentPath == "" {
		return nil, nil, hl1S4rErr(hl1S4rRouteC6RX, "block %d: there is no enrollment snapshot path", n)
	}

	// c. The generation-(N-1) pair. The main files are never a fallback:
	// they are at N, or mixed.
	accLink, enrLink := hl1GenerationLink(o.AccountsPath, prev), hl1GenerationLink(o.EnrollmentPath, prev)
	for _, p := range []string{accLink, enrLink} {
		fi, err := fsys.Lstat(p)
		switch {
		case err == nil && fi.Mode().IsRegular():
		case err == nil:
			return nil, nil, hl1S4rErr(hl1S4rRouteC6RX, "block %d: generation link %s is not a regular file", n, p)
		case errors.Is(err, fs.ErrNotExist):
			return nil, nil, hl1S4rErr(hl1S4rRouteC6RX, "block %d: generation link %s is missing, so the N-1 state is unavailable", n, p)
		default:
			return nil, nil, hl1S4rErr(hl1S4rRouteC6IO, "block %d: stat %s: %v", n, p, err)
		}
	}
	acc := chain.NewAccountStore()
	enr := enrollment.NewInMemoryState()
	restored, err := hl1EvaluatePair(acc, enr, o.Transition, prefix, accLink, enrLink)
	if err != nil {
		return nil, nil, hl1S4rErr(hl1S4rRouteC6RX, "block %d: the .h%d pair: %v", n, prev, err)
	}
	if o.Transition == nil {
		if _, err := enr.Load(enrLink); err != nil {
			return nil, nil, hl1S4rErr(hl1S4rRouteC6RX, "block %d: enrollment snapshot %s: %v", n, enrLink, err)
		}
	}

	// d. The scratch applier: the live applier's clone (production slasher
	// wiring, no GovApplier) restored to the N-1 state.
	x := chain.NewEnrollmentAwareApplier(acc, chain.NewEnrollmentApplier(acc, enr))
	x.SetTaskStateStore(restored.taskState)
	x.SetStreamStateStore(restored.streamState)
	x.SetRecoveryCapsuleStateStore(restored.recoveryState)
	x.SetStateRootHeight(transitionStateRootHeight(o.Transition, blkW))
	pre, ok := o.Live.ChainReplayClone().(*chain.EnrollmentAwareApplier)
	if !ok || pre == nil {
		return nil, nil, hl1S4rErr(hl1S4rRouteC6RX, "block %d: the live state applier %T is not an EnrollmentAwareApplier", n, o.Live)
	}
	if err := pre.RestoreFromChainReplay(x); err != nil {
		return nil, nil, hl1S4rErr(hl1S4rRouteC6RX, "block %d: restore the scratch applier to the .h%d pair: %v", n, prev, err)
	}
	if root := pre.StateRoot(); root != blkW.StateRoot {
		return nil, nil, hl1S4rErr(hl1S4rRouteC6RX, "block %d: the scratch applier restored from the .h%d pair has state root %s, block %d has %s", n, prev, root, prev, blkW.StateRoot)
	}

	// e. Replay N on a scratch producer: the follower and R-C4 path
	// (authorization, hash, signature, spec and live roots), whose
	// storeExternalAppendReceipts regenerates N's receipts. No hook.
	scratch := chain.NewBlockProducer(mempool.New(mempool.DefaultConfig()), pre, chain.DefaultProducerConfig())
	rs := chain.NewReceiptStore()
	scratch.SetAppendReceiptStore(rs)
	scratch.SetAuthorizedBlockProducers(o.Authorized)
	if err := scratch.SetProducerTransition(o.Transition); err != nil {
		return nil, nil, hl1S4rErr(hl1S4rRouteC6RX, "block %d: scratch producer transition: %v", n, err)
	}
	// The height is pinned to N, the one block replayed. The canonical
	// tip+1 closure would give the receipt passes N+1:
	// TryAppendExternalBlock stores the new tip before
	// storeExternalAppendReceipts re-applies the txs on the backup clone,
	// which shares this closure. The producer's spec, live and receipt
	// passes (storeProduceBlockReceipts) all saw N, and height-dependent txs
	// (enrollment, slash, task actions) can apply at N and fail at N+1.
	height := func() uint64 { return n }
	if o.scratchHeight != nil {
		height = func() uint64 { return o.scratchHeight(n, scratch.TipHeight) }
	}
	pre.SetHeightFn(height)
	if err := scratch.RestoreChain(prefix); err != nil {
		return nil, nil, hl1S4rErr(hl1S4rRouteC6RX, "block %d: scratch producer hydrate (%d blocks): %v", n, len(prefix), err)
	}
	hl1S4rScratchReplays.Add(1)
	if err := scratch.TryAppendExternalBlock(blkN); err != nil {
		return nil, nil, hl1S4rErr(hl1S4rRouteC6RX, "block %d: replay onto the .h%d pair failed: %v", n, prev, err)
	}
	if tip, ok := scratch.LatestBlock(); !ok || tip.Hash != blkN.Hash {
		return nil, nil, hl1S4rErr(hl1S4rRouteC6RX, "block %d was not appended by the replay", n)
	}
	return pre, rs, nil
}

// hl1S4rCompare is S4r step f. It returns N's regenerated receipts by tx ID.
func hl1S4rCompare(o hl1RepairOptions, log *logging.Logger, pre *chain.EnrollmentAwareApplier, rs *chain.ReceiptStore, blkN *chain.Block, present map[string]*chain.TxReceipt) (map[string]*chain.TxReceipt, error) {
	n := blkN.Height
	if root := pre.StateRoot(); root != blkN.StateRoot {
		return nil, hl1S4rErr(hl1S4rRouteC6C4, "block %d: the replayed state root %s is not the block's %s", n, root, blkN.StateRoot)
	}
	if root := o.Live.StateRoot(); root != blkN.StateRoot {
		return nil, hl1S4rErr(hl1S4rRouteC6C4, "block %d: the restored state root %s is not the block's %s", n, root, blkN.StateRoot)
	}
	// The state half of H0 (the v2wiring hook logs a sweep error and goes on).
	if _, err := pre.Sweep(n); err != nil {
		log.Warn("hl1 S4r: enrollment sweep of the replayed block failed", "height", n, "error_str", err.Error())
	}
	if o.mutatePost != nil {
		o.mutatePost(pre)
	}

	want, err := os.ReadFile(o.AccountsPath)
	if err != nil {
		return nil, hl1S4rErr(hl1S4rRouteC6IO, "block %d: read %s: %v", n, o.AccountsPath, err)
	}
	got, err := json.MarshalIndent(pre.Accounts().AllAccounts(), "", "  ")
	if err != nil {
		return nil, hl1S4rErr(hl1S4rRouteC6RX, "block %d: marshal the replayed accounts: %v", n, err)
	}
	if !bytes.Equal(got, want) {
		return nil, hl1S4rErr(hl1S4rRouteC6C4, "block %d: the accounts replayed from the .h%d pair differ from %s", n, n-1, o.AccountsPath)
	}

	liveEnr, err := hl1ApplierEnrollment(o.Live)
	if err != nil {
		return nil, hl1S4rErr(hl1S4rRouteC6RX, "block %d: restored enrollment state: %v", n, err)
	}
	preEnr, err := hl1ApplierEnrollment(pre)
	if err != nil {
		return nil, hl1S4rErr(hl1S4rRouteC6RX, "block %d: replayed enrollment state: %v", n, err)
	}
	if eq, err := hl1EnrollmentEqual(preEnr, liveEnr); err != nil {
		return nil, hl1S4rErr(hl1S4rRouteC6RX, "block %d: compare the enrollment state: %v", n, err)
	} else if !eq {
		return nil, hl1S4rErr(hl1S4rRouteC6C4, "block %d: the enrollment state replayed from the .h%d pair differs from the restored enrollment snapshot %s", n, n-1, o.EnrollmentPath)
	}

	regen := make(map[string]*chain.TxReceipt)
	for _, r := range rs.GetByBlock(n) {
		if r != nil {
			regen[r.TxID] = r
		}
	}
	for _, tx := range blkN.Transactions {
		if tx == nil {
			continue
		}
		g := regen[tx.ID]
		if g == nil {
			return nil, hl1S4rErr(hl1S4rRouteC6RX, "block %d: the replay produced no receipt for tx %s", n, tx.ID)
		}
		// Every tx of N applied (a local seal includes only txs that
		// applied, and the replay's spec and live passes just applied them
		// all), so each has a status-1 receipt. A failed one means a receipt
		// pass disagreed with them (for example at another height): never
		// write it.
		if g.Status != chain.ReceiptSuccess {
			return nil, hl1S4rErr(hl1S4rRouteC6RX, "block %d: the replay regenerated tx %s with status %d (%s), but every tx of the block applied", n, tx.ID, g.Status, g.Error)
		}
		p := present[tx.ID]
		if p == nil {
			continue
		}
		if eq, err := hl1ReceiptEqual(p, g); err != nil {
			return nil, hl1S4rErr(hl1S4rRouteC6RX, "block %d: compare the receipt of tx %s: %v", n, tx.ID, err)
		} else if !eq {
			return nil, hl1S4rErr(hl1S4rRouteC6C4, "block %d: the present receipt of tx %s differs from the regenerated one", n, tx.ID)
		}
	}
	return regen, nil
}

// hl1ApplierEnrollment returns the in-memory enrollment state of an applier.
func hl1ApplierEnrollment(a chain.ChainReplayApplier) (*enrollment.InMemoryState, error) {
	aware, ok := a.(*chain.EnrollmentAwareApplier)
	if !ok || aware == nil {
		return nil, fmt.Errorf("applier %T is not an EnrollmentAwareApplier", a)
	}
	ea := aware.EnrollmentApplier()
	if ea == nil {
		return nil, errors.New("no enrollment applier is wired")
	}
	st, ok := ea.State.(*enrollment.InMemoryState)
	if !ok || st == nil {
		return nil, fmt.Errorf("enrollment state %T is not an InMemoryState", ea.State)
	}
	return st, nil
}

// hl1EnrollmentEqual compares two enrollment states canonically: the
// records sorted by node_id (every field, as JSON), and the enrollment state
// root, which also commits to the sorted evidence set. Save iterates maps, so
// snapshot bytes are not comparable.
func hl1EnrollmentEqual(a, b *enrollment.InMemoryState) (bool, error) {
	ra, err := hl1EnrollmentRecordsJSON(a)
	if err != nil {
		return false, err
	}
	rb, err := hl1EnrollmentRecordsJSON(b)
	if err != nil {
		return false, err
	}
	return bytes.Equal(ra, rb) && a.Count() == b.Count() && a.StateRoot() == b.StateRoot(), nil
}

func hl1EnrollmentRecordsJSON(s *enrollment.InMemoryState) ([]byte, error) {
	var recs []enrollment.EnrollmentRecord
	cursor := ""
	for {
		page := s.List(enrollment.ListOptions{Cursor: cursor, Limit: enrollment.MaxListLimit})
		recs = append(recs, page.Records...)
		if !page.HasMore {
			break
		}
		cursor = page.NextCursor
	}
	return json.Marshal(recs)
}

// hl1ReceiptEqual compares two receipts as their NDJSON lines with the
// node-local timestamp zeroed.
func hl1ReceiptEqual(a, b *chain.TxReceipt) (bool, error) {
	ca, cb := *a, *b
	ca.Timestamp, cb.Timestamp = time.Time{}, time.Time{}
	ja, err := json.Marshal(&ca)
	if err != nil {
		return false, err
	}
	jb, err := json.Marshal(&cb)
	if err != nil {
		return false, err
	}
	return bytes.Equal(ja, jb), nil
}
