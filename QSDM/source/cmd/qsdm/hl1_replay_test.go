package main

// WP10 tests: qsdm --hl1-tail-replay (R-C4) and the recovery runbooks on real
// state directories. A core-like producer (the replay stack, which is main()'s
// wiring, plus a signer, the journal and the normal-mode hook H0-H10) seals
// blocks, a fault in the hook leaves a C2/C3, C4, C5 or C6 crash state, and
// the recovery is run as the runbooks do: replay, hl1-tail, and the next
// boot's S4 restore and S5 (hl1rBoot).

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/internal/hl1tail"
	"github.com/blackbeardONE/QSDM/internal/legacymining"
	"github.com/blackbeardONE/QSDM/internal/logging"
	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/producerpolicy"
)

var (
	errHL1rFault = errors.New("injected I/O fault")
	hl1rNow      = time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	hl1rSenders  = []string{"hl1r-sender-a", "hl1r-sender-b"}
)

// -----------------------------------------------------------------------------
// Fixture: a core-like producer on a real state directory
// -----------------------------------------------------------------------------

// hl1rFaultFS fails the n-th SyncDir call after arming (H1 is call 1 of a
// block with a previous generation, H6 is call 2).
type hl1rFaultFS struct {
	hl1OSFS
	failSyncDirCall int
	syncDirCalls    int
}

func (f *hl1rFaultFS) SyncDir(dir string) error {
	f.syncDirCalls++
	if f.failSyncDirCall != 0 && f.syncDirCalls == f.failSyncDirCall {
		return errHL1rFault
	}
	return f.hl1OSFS.SyncDir(dir)
}

type hl1rCore struct {
	t       *testing.T
	dir     string
	st      *hl1ReplayStack
	journal *chain.ChainJournal
	fsys    *hl1rFaultFS
	causes  []string

	failJournal, failAccounts, failEnroll bool
	// failReceipts makes H5 write receiptsPrefix complete lines of the
	// block's receipts, then fail (an EIO after a partial append).
	failReceipts   bool
	receiptsPrefix int

	// plan, when set, returns the txs a sender submits to the block at
	// height h in place of its self-transfer (nil keeps the self-transfer).
	plan hl1rPlan
}

// hl1rPlan chooses a sender's txs for the block at height h; nonce is the
// sender's account nonce before the block.
type hl1rPlan func(h uint64, sender string, nonce uint64) []*mempool.Tx

func hl1rSigner(t *testing.T) *chain.PersistentBFTSigner {
	t.Helper()
	s, _, err := chain.LoadOrCreateBFTSigner(filepath.Join(t.TempDir(), "signer.json"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// hl1rStartCore starts a core on dir: a fresh chain (funded senders) when
// there is no journal, otherwise the S4/S5 boot of the existing state.
func hl1rStartCore(t *testing.T, dir string, policy *producerpolicy.Transition, signer chain.BFTSigner) *hl1rCore {
	t.Helper()
	var st *hl1ReplayStack
	var tip *chain.Block
	if _, err := os.Lstat(filepath.Join(dir, hl1JournalName)); err == nil {
		booted, _, err := hl1rBoot(t, dir, policy)
		if err != nil {
			t.Fatalf("boot: %v", err)
		}
		st = booted
		tip, _ = st.producer.LatestBlock()
	} else {
		fresh, err := hl1NewReplayStack(dir, nil, nil, policy, logging.NewSilentLogger())
		if err != nil {
			t.Fatal(err)
		}
		st = fresh
		for _, a := range hl1rSenders {
			st.accounts.Credit(a, 1000)
		}
	}
	st.producer.SetBlockSigner(signer)
	j, err := chain.OpenChainJournal(filepath.Join(dir, hl1JournalName), tip)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = j.Close() })
	c := &hl1rCore{t: t, dir: dir, st: st, journal: j, fsys: &hl1rFaultFS{}}
	hook := &hl1PersistHook{
		Mode:           hl1HookNormal,
		ProducerRole:   true,
		StateDir:       dir,
		JournalPath:    filepath.Join(dir, hl1JournalName),
		AccountsPath:   filepath.Join(dir, hl1AccountsName),
		EnrollmentPath: filepath.Join(dir, hl1EnrollmentName),
		ReceiptsPath:   filepath.Join(dir, hl1ReceiptsName),
		FS:             c.fsys,
		Prior:          st.v2.SealedBlockHook,
		AppendJournal: func(b *chain.Block) error {
			if c.failJournal {
				return errHL1rFault
			}
			return j.Append(b)
		},
		SaveAccounts: func(p string) error {
			if c.failAccounts {
				return errHL1rFault
			}
			return st.accounts.Save(p)
		},
		SaveEnrollment: func(p string) error {
			if c.failEnroll {
				return errHL1rFault
			}
			return st.v2.EnrollmentState.Save(p)
		},
		AppendReceipts: func(p string, h uint64) (int, error) {
			if c.failReceipts {
				return hl1rAppendPrefix(t, st.receipts, p, h, c.receiptsPrefix)
			}
			return st.receipts.AppendBlockNDJSON(p, h)
		},
		FailStop:         func(_ int, cause string) { c.causes = append(c.causes, cause) },
		OnFailStopReturn: func(error) {},
		Log:              logging.NewSilentLogger(),
		Metrics:          &hl1MetricSet{},
	}
	st.producer.OnSealedBlock = hook.OnSealedBlock
	return c
}

// seal produces one block with a self-transfer from every sender (or the
// txs of the plan).
func (c *hl1rCore) seal() *chain.Block {
	c.t.Helper()
	var h uint64
	if c.st.producer.HasTip() {
		h = c.st.producer.TipHeight() + 1
	}
	want := 0
	for _, a := range hl1rSenders {
		acc, ok := c.st.accounts.Get(a)
		if !ok {
			c.t.Fatalf("sender %s missing", a)
		}
		var txs []*mempool.Tx
		if c.plan != nil {
			txs = c.plan(h, a, acc.Nonce)
		}
		if txs == nil {
			txs = []*mempool.Tx{{ID: fmt.Sprintf("hl1r-%s-%d", a, acc.Nonce), Sender: a, Recipient: a, Nonce: acc.Nonce}}
		}
		for _, tx := range txs {
			if err := c.st.pool.Add(tx); err != nil {
				c.t.Fatal(err)
			}
		}
		want += len(txs)
	}
	blk, err := c.st.producer.ProduceBlock()
	if err != nil {
		c.t.Fatalf("ProduceBlock: %v", err)
	}
	dropped := 0 // local failed-drop receipts: a planned tx that did not apply
	for _, r := range c.st.receipts.GetByBlock(blk.Height) {
		if r.Status == chain.ReceiptFailed {
			dropped++
		}
	}
	if len(blk.Transactions)+dropped != want {
		c.t.Fatalf("block %d has %d txs and %d failed-drop receipts, want %d txs in all", blk.Height, len(blk.Transactions), dropped, want)
	}
	return blk
}

// crash ends the core as a SIGKILL would: nothing else runs.
func (c *hl1rCore) crash() { _ = c.journal.Close() }

// hl1rTransitionPrefix seals the historical chain 0..checkpoint and returns
// the transition policy pinning it, and the replacement signer.
func hl1rTransitionPrefix(t *testing.T, dir string, checkpoint uint64) (*producerpolicy.Transition, *chain.PersistentBFTSigner) {
	t.Helper()
	hist, repl := hl1rSigner(t), hl1rSigner(t)
	c := hl1rStartCore(t, dir, nil, hist)
	for !c.st.producer.HasTip() || c.st.producer.TipHeight() < checkpoint {
		c.seal()
	}
	c.crash()
	data, err := os.ReadFile(filepath.Join(dir, hl1JournalName))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	cp, _ := c.st.producer.GetBlock(checkpoint)
	return &producerpolicy.Transition{
		Version: 1, CheckpointHeight: checkpoint, CheckpointHash: cp.Hash, HistoricalSignatureHeight: 1,
		HistoricalProducer: hist.Address(), ReplacementProducer: repl.Address(), EffectiveHeight: checkpoint + 1,
		HistoricalPrefixBytes: int64(len(data)), HistoricalPrefixSHA256: hex.EncodeToString(sum[:]),
	}, repl
}

// hl1rAppendPrefix appends the first k receipts of height h to path as H5
// would, then fails: an I/O error after k complete lines.
func hl1rAppendPrefix(t *testing.T, rs *chain.ReceiptStore, path string, h uint64, k int) (int, error) {
	t.Helper()
	recs := rs.GetByBlock(h)
	if k > len(recs) {
		t.Fatalf("height %d has %d receipts, cannot write %d", h, len(recs), k)
	}
	var buf bytes.Buffer
	for _, r := range recs[:k] {
		data, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		buf.Write(append(data, '\n'))
	}
	fh, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer fh.Close()
	if _, err := fh.Write(buf.Bytes()); err != nil {
		t.Fatal(err)
	}
	return k, fmt.Errorf("%w after %d line(s)", errHL1rFault, k)
}

// hl1rFixture is a crash state: blocks 0..j-1 sealed normally, then block j
// sealed with the hook failing at step fault: H2, H3 (C4), H4 (C5), H5 (H5
// fails before writing: the C6a/F1 state), H5p (H5 writes one complete line,
// then fails) or H6.
type hl1rFixture struct {
	dir    string
	policy *producerpolicy.Transition
	signer chain.BFTSigner
	core   *hl1rCore
	blkJ   *chain.Block
}

func hl1rCrash(t *testing.T, transition bool, j uint64, fault string) *hl1rFixture {
	t.Helper()
	return hl1rCrashPlan(t, transition, j, fault, nil)
}

// hl1rCrashPlan is hl1rCrash with a tx plan for the blocks after the
// historical prefix (see hl1rCore.plan).
func hl1rCrashPlan(t *testing.T, transition bool, j uint64, fault string, plan hl1rPlan) *hl1rFixture {
	t.Helper()
	f := &hl1rFixture{dir: t.TempDir()}
	if transition {
		policy, repl := hl1rTransitionPrefix(t, f.dir, 2)
		f.policy, f.signer = policy, repl
	} else {
		f.signer = hl1rSigner(t)
	}
	c := hl1rStartCore(t, f.dir, f.policy, f.signer)
	c.plan = plan
	for !c.st.producer.HasTip() || c.st.producer.TipHeight() < j-1 {
		c.seal()
	}
	switch fault {
	case "H2":
		c.failJournal = true
	case "H3":
		c.failAccounts = true
	case "H4":
		c.failEnroll = true
	case "H5":
		c.failReceipts = true
	case "H5p":
		c.failReceipts, c.receiptsPrefix = true, 1
	case "H6":
		c.fsys.syncDirCalls, c.fsys.failSyncDirCall = 0, 2
	default:
		t.Fatalf("unknown fault %s", fault)
	}
	f.blkJ = c.seal()
	c.crash()
	step := strings.TrimSuffix(fault, "p")
	if len(c.causes) != 1 || !strings.HasPrefix(c.causes[0], legacymining.CausePersistPrefix+step+":") {
		t.Fatalf("fail-stop causes = %q, want one persist:%s", c.causes, step)
	}
	f.core = c
	if f.blkJ.Height != j {
		t.Fatalf("faulted block is %d, want %d", f.blkJ.Height, j)
	}
	return f
}

func (f *hl1rFixture) path(name string) string { return filepath.Join(f.dir, name) }

// hl1rBoot is main()'s S4 restore block, S4r and S5 in the producer role,
// in process: the journal must end with "\n" and load; the main snapshot
// pair must reproduce the tip's state root (the non-transition path also runs
// main()'s reconcilePersistedStateTail, which must keep the tip); the
// receipts must end with "\n" and load; then hl1RepairTipReceipts and
// hl1StartupWatermark. A non-nil error is a boot refusal (exit 78).
func hl1rBoot(t *testing.T, dir string, policy *producerpolicy.Transition) (*hl1ReplayStack, hl1S5Result, error) {
	t.Helper()
	st, _, s5, err := hl1rBootWith(t, dir, policy, hl1OSFS{}, nil)
	return st, s5, err
}

// hl1rRestore is main()'s S4 restore block (see hl1rBoot). It returns the
// restored stack and blocks.
func hl1rRestore(t *testing.T, dir string, policy *producerpolicy.Transition) (*hl1ReplayStack, []*chain.Block, error) {
	t.Helper()
	journal := filepath.Join(dir, hl1JournalName)
	if err := hl1RequireFinalNewline(hl1OSFS{}, journal); err != nil {
		return nil, nil, err
	}
	blocks, err := chain.LoadChainNDJSON(journal)
	if err != nil {
		return nil, nil, err
	}
	if len(blocks) == 0 {
		return nil, nil, errors.New("empty journal")
	}
	st, err := hl1NewReplayStack(dir, nil, nil, policy, logging.NewSilentLogger())
	if err != nil {
		return nil, nil, err
	}
	if err := st.producer.ValidateProducerTransitionChain(blocks); err != nil {
		return nil, nil, err
	}
	if _, dropped := canonicalPersistedChain(blocks); dropped != 0 {
		return nil, nil, errors.New("forked journal")
	}
	accounts := filepath.Join(dir, hl1AccountsName)
	if err := st.restore(blocks, accounts, filepath.Join(dir, hl1EnrollmentName), filepath.Join(dir, hl1ReceiptsName)); err != nil {
		return nil, nil, err
	}
	if policy == nil {
		acc := chain.NewAccountStore()
		if _, err := acc.Load(accounts); err != nil {
			return nil, nil, err
		}
		r, err := reconcilePersistedStateTail(journal, acc, blocks, time.Now())
		if err != nil || r.recovered {
			return nil, nil, fmt.Errorf("reconcilePersistedStateTail: recovered=%v err=%v", r.recovered, err)
		}
	}
	return st, blocks, nil
}

// hl1rRepairOptions are main()'s S4r options for a restored stack.
func hl1rRepairOptions(dir string, policy *producerpolicy.Transition, st *hl1ReplayStack, blocks []*chain.Block, fsys hl1FS) hl1RepairOptions {
	return hl1RepairOptions{
		ProducerRole:   true,
		StateDir:       dir,
		AccountsPath:   filepath.Join(dir, hl1AccountsName),
		EnrollmentPath: filepath.Join(dir, hl1EnrollmentName),
		ReceiptsPath:   filepath.Join(dir, hl1ReceiptsName),
		Transition:     policy,
		Blocks:         blocks,
		Live:           st.v2.StateApplier.(chain.ChainReplayApplier),
		Receipts:       st.receipts,
		FS:             fsys,
		Now:            func() time.Time { return hl1rNow },
		Log:            logging.NewSilentLogger(),
	}
}

// hl1rBootWith is hl1rBoot with the file-system surface of S4r and S5, and
// a hook that edits the S4r options.
func hl1rBootWith(t *testing.T, dir string, policy *producerpolicy.Transition, fsys hl1FS, mod func(*hl1RepairOptions)) (*hl1ReplayStack, hl1RepairResult, hl1S5Result, error) {
	t.Helper()
	st, blocks, err := hl1rRestore(t, dir, policy)
	if err != nil {
		return nil, hl1RepairResult{}, hl1S5Result{}, err
	}
	o := hl1rRepairOptions(dir, policy, st, blocks, fsys)
	if mod != nil {
		mod(&o)
	}
	s4r, err := hl1RepairTipReceipts(o)
	if err != nil {
		return nil, s4r, hl1S5Result{}, err
	}
	tip, _ := st.producer.LatestBlock()
	s5, err := hl1StartupWatermark(fsys, dir, true, tip, st.producer.GetBlock, hl1rNow)
	return st, s4r, s5, err
}

type hl1rExit struct{ code int }

// hl1rReplay runs replay mode in process with a test fail-stopper, and
// releases the state lock before returning.
func hl1rReplay(t *testing.T, dir string, policy *producerpolicy.Transition) (hl1ReplayResult, error, *hl1rExit) {
	t.Helper()
	ex := &hl1rExit{code: -1}
	stopper := &hl1FailStopper{exit: func(code int) { ex.code = code }, logf: func(string, ...any) {}}
	res, err := hl1RunReplay(hl1ReplayOptions{
		StateDir: dir, Transition: policy, Release: "test",
		Getenv: func(string) string { return "" }, Stopper: stopper, FailStop: stopper.Stop,
		Log: logging.NewSilentLogger(), Now: func() time.Time { return hl1rNow },
	})
	if res.Lock != nil {
		if cerr := res.Lock.Close(); cerr != nil {
			t.Fatal(cerr)
		}
	}
	return res, err, ex
}

// hl1rTail runs hl1-tail on dir.
func hl1rTail(t *testing.T, dir string, args ...string) (int, string) {
	t.Helper()
	var out bytes.Buffer
	code := hl1tail.Run(args, hl1tail.Env{
		StateDir: func() (string, error) { return dir, nil }, Getenv: func(string) string { return "" },
		Stdout: &out, Stderr: &out, Now: func() time.Time { return hl1rNow },
	})
	return code, out.String()
}

func hl1rRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func hl1rStat(t *testing.T, path string) fs.FileInfo {
	t.Helper()
	fi, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi
}

func hl1rW(t *testing.T, dir string) legacymining.Watermark {
	t.Helper()
	w, err := hl1ReadWatermark(dir)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// hl1rChainFiles hashes the journal, the snapshots, their generation links,
// W and the receipts.
func hl1rChainFiles(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, e := range entries {
		n := e.Name()
		if strings.HasPrefix(n, hl1JournalName) || strings.HasPrefix(n, hl1AccountsName) || strings.HasPrefix(n, hl1EnrollmentName) ||
			strings.HasPrefix(n, hl1ReceiptsName) || strings.HasPrefix(n, legacymining.WatermarkFile) {
			sum := sha256.Sum256(hl1rRead(t, filepath.Join(dir, n)))
			lines = append(lines, n+" "+hex.EncodeToString(sum[:]))
		}
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// hl1rTreeHash is hl1TreeHash without the state lock file, which every
// taker creates and rewrites with its PID.
func hl1rTreeHash(t *testing.T, dir string) string {
	t.Helper()
	var lines []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Name() == legacymining.StateLockFile {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		line := rel
		if !d.IsDir() {
			sum := sha256.Sum256(hl1rRead(t, p))
			line += " " + hex.EncodeToString(sum[:])
		}
		lines = append(lines, line)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// hl1rReceiptHeights counts the complete receipts lines per height and
// reports whether the file ends with "\n".
func hl1rReceiptHeights(t *testing.T, path string) (map[uint64]int, bool) {
	t.Helper()
	data := hl1rRead(t, path)
	out := map[uint64]int{}
	lines := bytes.Split(data, []byte("\n"))
	for _, l := range lines[:len(lines)-1] { // the last element is the fragment, if any
		if len(l) == 0 {
			continue
		}
		var r chain.TxReceipt
		if err := json.Unmarshal(l, &r); err != nil {
			t.Fatalf("receipts line %q: %v", l, err)
		}
		out[r.BlockHeight]++
	}
	return out, len(data) == 0 || data[len(data)-1] == '\n'
}

// hl1rTearReceipts appends block j's receipts (as the crashed core generated
// them) and half of one more line: a receipts tail that a host crash can leave
// next to a C4 or C5 snapshot state.
func (f *hl1rFixture) hl1rTearReceipts(t *testing.T) {
	t.Helper()
	var buf bytes.Buffer
	var last []byte
	for _, r := range f.core.st.receipts.GetByBlock(f.blkJ.Height) {
		data, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		buf.Write(append(data, '\n'))
		last = data
	}
	if buf.Len() == 0 {
		t.Fatal("block J has no receipts")
	}
	buf.Write(last[:len(last)/2])
	fh, err := os.OpenFile(f.path(hl1ReceiptsName), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fh.Write(buf.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := fh.Close(); err != nil {
		t.Fatal(err)
	}
}

// -----------------------------------------------------------------------------
// R-C4: replay recovers C4 and C5
// -----------------------------------------------------------------------------

func TestHL1ReplayRecoversC4AndC5(t *testing.T) {
	if testing.Short() {
		t.Skip("state-directory recovery test")
	}
	const j = 5
	for _, tc := range []struct {
		name         string
		transition   bool
		fault        string // H3: C4, H4: C5
		tornReceipts bool
	}{
		{"C4", false, "H3", false},
		{"C4 with J receipts and a fragment", false, "H3", true},
		{"C5 with J receipts and a fragment", false, "H4", true},
		{"C4 under the producer transition", true, "H3", true},
		{"C5 under the producer transition", true, "H4", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := hl1rCrash(t, tc.transition, j, tc.fault)
			accMain, enrMain := f.path(hl1AccountsName), f.path(hl1EnrollmentName)
			accLink, enrLink := hl1GenerationLink(accMain, j-1), hl1GenerationLink(enrMain, j-1)

			// The crash state: journal J, W = J-1, the .h<J-1> pair, and the
			// C4 (accounts main is still the J-1 inode) or C5 (accounts main is
			// the newer J inode) shape.
			if w := hl1rW(t, f.dir); w.Height != j-1 {
				t.Fatalf("crash state W = %d", w.Height)
			}
			accLinkFI, enrLinkFI := hl1rStat(t, accLink), hl1rStat(t, enrLink)
			accLinkBytes, enrLinkBytes := hl1rRead(t, accLink), hl1rRead(t, enrLink)
			sameAcc := os.SameFile(hl1rStat(t, accMain), accLinkFI)
			if (tc.fault == "H3") != sameAcc || !os.SameFile(hl1rStat(t, enrMain), enrLinkFI) {
				t.Fatalf("not a %s shape: accounts main same inode as .h%d = %v", tc.fault, j-1, sameAcc)
			}
			if tc.tornReceipts {
				f.hl1rTearReceipts(t)
			}
			// C4 (accounts behind the journal) never boots. In C5 only the
			// enrollment snapshot is behind, and the boot's root check sees
			// that only when block J changed root-covered enrollment state;
			// these blocks do not, so the C5 boot is not refused: S4r
			// completes J's receipts and S5 advances W (TestHL1S4r*). Replay
			// runs here on the raw C5 crash state.
			if tc.fault == "H3" {
				if _, _, err := hl1rBoot(t, f.dir, f.policy); err == nil {
					t.Fatal("the C4 crash state boots although the journal is ahead of the snapshots")
				}
			}
			journalBefore := hl1rRead(t, f.path(hl1JournalName))

			res, err, ex := hl1rReplay(t, f.dir, f.policy)
			if err != nil {
				t.Fatalf("replay: %v", err)
			}
			if ex.code != -1 {
				t.Fatalf("replay exited %d", ex.code)
			}
			if res.PairAccounts != accLink || res.PairEnroll != enrLink {
				t.Fatalf("J-1 pair = %s, %s; want the .h%d links", res.PairAccounts, res.PairEnroll, j-1)
			}
			if tc.tornReceipts != (res.PreTrim.Archive != "") {
				t.Fatalf("receipts pre-trim = %+v", res.PreTrim)
			}

			// W := J from replay; the journal is untouched.
			if w := hl1rW(t, f.dir); w.Height != j || w.Hash != f.blkJ.Hash || w.Source != legacymining.WatermarkSourceReplay {
				t.Fatalf("W after replay = %+v", w)
			}
			if !bytes.Equal(hl1rRead(t, f.path(hl1JournalName)), journalBefore) {
				t.Fatal("replay changed the journal")
			}
			// Replay skips H1: the .h<J-1> generation keeps its inodes and bytes.
			if !os.SameFile(hl1rStat(t, accLink), accLinkFI) || !os.SameFile(hl1rStat(t, enrLink), enrLinkFI) ||
				!bytes.Equal(hl1rRead(t, accLink), accLinkBytes) || !bytes.Equal(hl1rRead(t, enrLink), enrLinkBytes) {
				t.Fatalf(".h%d generation changed during replay", j-1)
			}
			// Exactly one copy of J's receipts, on a clean line boundary.
			heights, terminated := hl1rReceiptHeights(t, f.path(hl1ReceiptsName))
			if !terminated || heights[j] != len(f.blkJ.Transactions) || heights[j-1] != len(hl1rSenders) {
				t.Fatalf("receipts per height = %v (terminated %v)", heights, terminated)
			}

			// The next boot passes with tip = W = J, and the chain continues.
			_, s5, err := hl1rBoot(t, f.dir, f.policy)
			if err != nil {
				t.Fatalf("boot after replay: %v", err)
			}
			if !s5.DurableTipSet || s5.DurableTip != j || s5.Wrote {
				t.Fatalf("S5 after replay = %+v", s5)
			}
			next := hl1rStartCore(t, f.dir, f.policy, f.signer).seal()
			if w := hl1rW(t, f.dir); next.Height != j+1 || w.Height != j+1 || w.Source != legacymining.WatermarkSourceSeal {
				t.Fatalf("after replay the next seal is %d, W = %+v", next.Height, w)
			}
		})
	}
}

// -----------------------------------------------------------------------------
// R-C4 replay failure: trim --above J-1 with the .h<J-1> inode check
// -----------------------------------------------------------------------------

func TestHL1ReplayFailureFallbackTrimAbove(t *testing.T) {
	if testing.Short() {
		t.Skip("state-directory recovery test")
	}
	const j = 4
	t.Run("C4: allowed; the next boot passes at J-1", func(t *testing.T) {
		f := hl1rCrash(t, false, j, "H3")
		code, out := hl1rTail(t, f.dir, "trim", "--above", fmt.Sprint(j-1))
		if code != legacymining.TailExitOK || !strings.Contains(out, "same-inode") {
			t.Fatalf("trim --above: exit %d\n%s", code, out)
		}
		_, s5, err := hl1rBoot(t, f.dir, nil)
		if err != nil || s5.DurableTip != j-1 || s5.Wrote {
			t.Fatalf("boot after trim: %+v %v", s5, err)
		}
		if next := hl1rStartCore(t, f.dir, nil, f.signer).seal(); next.Height != j || next.Hash == f.blkJ.Hash {
			t.Fatalf("the re-sealed height %d reuses the trimmed block", next.Height)
		}
	})
	t.Run("C5: refused with nothing changed; replay still recovers", func(t *testing.T) {
		f := hl1rCrash(t, true, j, "H4")
		before := hl1rTreeHash(t, f.dir)
		code, out := hl1rTail(t, f.dir, "trim", "--above", fmt.Sprint(j-1))
		if code != legacymining.TailExitRefused || !strings.Contains(out, "C5 shape") {
			t.Fatalf("trim --above: exit %d\n%s", code, out)
		}
		if hl1rTreeHash(t, f.dir) != before {
			t.Fatal("a refused trim changed the state directory")
		}
		if _, err, _ := hl1rReplay(t, f.dir, f.policy); err != nil {
			t.Fatalf("replay: %v", err)
		}
		if _, _, err := hl1rBoot(t, f.dir, f.policy); err != nil {
			t.Fatalf("boot after replay: %v", err)
		}
	})
	t.Run("C5 without links: replay refuses, trim proceeds, the next boot's root check is the backstop", func(t *testing.T) {
		f := hl1rCrash(t, false, j, "H4")
		for _, name := range []string{hl1AccountsName, hl1EnrollmentName} {
			if err := os.Remove(hl1GenerationLink(f.path(name), j-1)); err != nil {
				t.Fatal(err)
			}
		}
		before := hl1rChainFiles(t, f.dir)
		_, err, _ := hl1rReplay(t, f.dir, nil)
		if err == nil || !strings.Contains(err.Error(), "does not reproduce") {
			t.Fatalf("replay err = %v", err)
		}
		if hl1rChainFiles(t, f.dir) != before {
			t.Fatal("a refused replay changed the chain files")
		}
		if code, out := hl1rTail(t, f.dir, "trim", "--above", fmt.Sprint(j-1)); code != 0 || !strings.Contains(out, "backstop") {
			t.Fatalf("trim --above: exit %d\n%s", code, out)
		}
		if _, _, err := hl1rBoot(t, f.dir, nil); err == nil {
			t.Fatal("the next boot passed although the accounts snapshot is at J and the journal at J-1")
		}
	})
}

// -----------------------------------------------------------------------------
// Replay refusals (exit 78): lock busy, W preconditions, torn journal
// -----------------------------------------------------------------------------

func TestHL1ReplayRefusals(t *testing.T) {
	if testing.Short() {
		t.Skip("state-directory recovery test")
	}
	const j = 4
	f := hl1rCrash(t, false, j, "H3")
	f.hl1rTearReceipts(t) // a pre-trim would change the receipts
	blocks, err := chain.LoadChainNDJSON(f.path(hl1JournalName))
	if err != nil {
		t.Fatal(err)
	}
	wOf := func(h uint64, hash string) []byte {
		data, _ := json.Marshal(legacymining.Watermark{Version: 1, Height: h, Hash: hash, Source: "seal", WrittenNS: 1})
		return append(data, '\n')
	}
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, dir string)
		msg   string
	}{
		{"W absent", func(t *testing.T, dir string) { _ = os.Remove(filepath.Join(dir, legacymining.WatermarkFile)) }, "watermark missing"},
		{"W below J-1", func(t *testing.T, dir string) {
			hl1WriteFile(t, filepath.Join(dir, legacymining.WatermarkFile), string(wOf(j-2, blocks[j-2].Hash)))
		}, "outside [J-1, J]"},
		{"W hash mismatch", func(t *testing.T, dir string) {
			hl1WriteFile(t, filepath.Join(dir, legacymining.WatermarkFile), string(wOf(j-1, blocks[j-2].Hash)))
		}, "W has"},
		{"W invalid", func(t *testing.T, dir string) {
			hl1WriteFile(t, filepath.Join(dir, legacymining.WatermarkFile), `{"version":1}`)
		}, "watermark invalid"},
		{"torn journal", func(t *testing.T, dir string) {
			fh, _ := os.OpenFile(filepath.Join(dir, hl1JournalName), os.O_APPEND|os.O_WRONLY, 0)
			_, _ = fh.WriteString(`{"height":5`)
			_ = fh.Close()
		}, "R-C3"},
		{"partial legacy-mining environment (S2)", nil, "hl1 S2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			hl1rCopyDir(t, f.dir, dir)
			getenv := func(string) string { return "" }
			if tc.setup != nil {
				tc.setup(t, dir)
			} else {
				getenv = func(k string) string {
					if k == legacymining.EnvMode {
						return "canary"
					}
					return ""
				}
			}
			before := hl1rChainFiles(t, dir)
			stopper := &hl1FailStopper{exit: func(int) { t.Fatal("fail-stop during a refused replay") }, logf: func(string, ...any) {}}
			res, err := hl1RunReplay(hl1ReplayOptions{StateDir: dir, Getenv: getenv, Stopper: stopper, FailStop: stopper.Stop, Log: logging.NewSilentLogger()})
			if res.Lock != nil {
				_ = res.Lock.Close()
			}
			if err == nil || !strings.Contains(err.Error(), tc.msg) {
				t.Fatalf("err = %v, want %q", err, tc.msg)
			}
			if hl1rChainFiles(t, dir) != before {
				t.Fatal("a refused replay changed the journal, snapshots, W or receipts")
			}
		})
	}
	t.Run("state lock busy: nothing touched", func(t *testing.T) {
		dir := t.TempDir()
		hl1rCopyDir(t, f.dir, dir)
		stale := filepath.Join(dir, legacymining.TempPrefix+legacymining.WatermarkFile+"-1-0011223344556677")
		hl1WriteFile(t, stale, "partial")
		held, err := chain.AcquireStateLock(filepath.Join(dir, legacymining.StateLockFile))
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		before := hl1TreeHash(t, dir)
		res, err := hl1RunReplay(hl1ReplayOptions{StateDir: dir, Log: logging.NewSilentLogger()})
		if res.Lock != nil {
			t.Fatal("replay returned a lock it could not hold")
		}
		if err == nil || !strings.Contains(err.Error(), "validator state lock") {
			t.Fatalf("err = %v", err)
		}
		if hl1TreeHash(t, dir) != before {
			t.Fatal("replay changed the state directory although the lock was busy")
		}
	})
}

// hl1rCopyDir copies the regular files of src into dst (hard links become
// separate files, which is fine for the refusal cases).
func hl1rCopyDir(t *testing.T, src, dst string) {
	t.Helper()
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !e.Type().IsRegular() || e.Name() == legacymining.StateLockFile {
			continue
		}
		hl1WriteFile(t, filepath.Join(dst, e.Name()), string(hl1rRead(t, filepath.Join(src, e.Name()))))
	}
}

// -----------------------------------------------------------------------------
// Replay fail-stop: 86 and FAILSTOP.json kept
// -----------------------------------------------------------------------------

func TestHL1ReplayFailStopKeepsFailStopLatch(t *testing.T) {
	if testing.Short() {
		t.Skip("state-directory recovery test")
	}
	const j = 4
	for _, latched := range []bool{true, false} {
		t.Run(fmt.Sprintf("FAILSTOP.json present=%v", latched), func(t *testing.T) {
			f := hl1rCrash(t, false, j, "H3")
			failstop := f.path(legacymining.FailStopFile)
			if latched {
				hl1WriteFile(t, failstop, `{"release":"latched"}`)
			}
			// Re-encode line J with a space: the same block, but not the bytes
			// json.Marshal produces, so H2's byte assertion fails after the
			// block was applied in memory.
			journal := hl1rRead(t, f.path(hl1JournalName))
			start := bytes.LastIndexByte(journal[:len(journal)-1], '\n') + 1
			edited := append(append(append([]byte{}, journal[:start]...), "{ "...), journal[start+1:]...)
			hl1WriteFile(t, f.path(hl1JournalName), string(edited))
			wBefore := hl1rRead(t, f.path(legacymining.WatermarkFile))
			accBefore := hl1rRead(t, f.path(hl1AccountsName))

			res, err, ex := hl1rReplay(t, f.dir, nil)
			if !errors.Is(err, errHL1ReplayFailStop) || !strings.Contains(err.Error(), "H2") {
				t.Fatalf("err = %v, want the H2 fail-stop", err)
			}
			if ex.code != legacymining.ExitFailStop {
				t.Fatalf("exit = %d, want 86", ex.code)
			}
			if res.FailStopLatch != latched {
				t.Fatalf("FailStopLatch = %v", res.FailStopLatch)
			}
			got, err := os.ReadFile(failstop)
			if err != nil {
				t.Fatalf("FAILSTOP.json after the replay fail-stop: %v", err)
			}
			if latched && string(got) != `{"release":"latched"}` {
				t.Fatalf("the latched FAILSTOP.json was replaced: %s", got)
			}
			if _, err := os.Lstat(f.path(legacymining.FailStopArmedFile)); !errors.Is(err, fs.ErrNotExist) {
				t.Fatal("FAILSTOP.armed left behind")
			}
			if !bytes.Equal(hl1rRead(t, f.path(legacymining.WatermarkFile)), wBefore) || !bytes.Equal(hl1rRead(t, f.path(hl1AccountsName)), accBefore) {
				t.Fatal("W or the accounts snapshot changed although H2 failed")
			}
		})
	}
}

// -----------------------------------------------------------------------------
// R-C3: torn journal (W-gated) and torn receipts with W = N-1 (journal-gated)
// -----------------------------------------------------------------------------

func TestHL1TornJournalRecoveredByRC3(t *testing.T) {
	if testing.Short() {
		t.Skip("state-directory recovery test")
	}
	const n = 4
	f := hl1rCrash(t, false, n, "H2") // nothing of N persisted
	line, err := json.Marshal(f.blkJ)
	if err != nil {
		t.Fatal(err)
	}
	fh, err := os.OpenFile(f.path(hl1JournalName), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fh.Write(line[:len(line)/2]); err != nil {
		t.Fatal(err)
	}
	_ = fh.Close()
	wBefore := hl1rRead(t, f.path(legacymining.WatermarkFile))
	if _, _, err := hl1rBoot(t, f.dir, nil); err == nil || !strings.Contains(err.Error(), "R-C3") {
		t.Fatalf("boot on a torn journal: %v", err)
	}
	if code, out := hl1rTail(t, f.dir, "trim-fragment", "--file", "journal"); code != 0 {
		t.Fatalf("R-C3 journal: exit %d\n%s", code, out)
	}
	_, s5, err := hl1rBoot(t, f.dir, nil)
	if err != nil || s5.DurableTip != n-1 || s5.Wrote || !bytes.Equal(hl1rRead(t, f.path(legacymining.WatermarkFile)), wBefore) {
		t.Fatalf("boot after R-C3: %+v %v", s5, err)
	}
}

// Design rev 4 §7 "S5 placement": snapshots at N, a torn receipts tail with
// complete lines of N plus a fragment, and W = N-1. The boot refuses at the
// receipts load with W byte-identical; R-C3 on receipts succeeds (journal
// gated); the next boot passes and writes W := N.
func TestHL1TornReceiptsAtWMinus1(t *testing.T) {
	if testing.Short() {
		t.Skip("state-directory recovery test")
	}
	const n = 4
	f := hl1rCrash(t, false, n, "H6")
	if w := hl1rW(t, f.dir); w.Height != n-1 {
		t.Fatalf("W = %d, want N-1", w.Height)
	}
	receipts := f.path(hl1ReceiptsName)
	data := hl1rRead(t, receipts)
	lastStart := bytes.LastIndexByte(data[:len(data)-1], '\n') + 1
	torn := data[:lastStart+(len(data)-lastStart)/2]
	hl1WriteFile(t, receipts, string(torn))
	if heights, _ := hl1rReceiptHeights(t, receipts); heights[n] == 0 {
		t.Fatal("the torn receipts hold no complete line of N")
	}
	wBefore := hl1rRead(t, f.path(legacymining.WatermarkFile))

	_, _, err := hl1rBoot(t, f.dir, nil)
	if err == nil || !strings.Contains(err.Error(), "R-C3") {
		t.Fatalf("boot on torn receipts: %v", err)
	}
	if !bytes.Equal(hl1rRead(t, f.path(legacymining.WatermarkFile)), wBefore) {
		t.Fatal("the refused boot changed W")
	}
	if code, out := hl1rTail(t, f.dir, "trim-fragment", "--file", "receipts"); code != 0 {
		t.Fatalf("R-C3 receipts: exit %d\n%s", code, out)
	}
	trimmed := hl1rRead(t, receipts)
	_, s5, err := hl1rBoot(t, f.dir, nil)
	if err != nil || !s5.Wrote || s5.DurableTip != n {
		t.Fatalf("boot after R-C3: %+v %v", s5, err)
	}
	if w := hl1rW(t, f.dir); w.Height != n || w.Hash != f.blkJ.Hash || w.Source != legacymining.WatermarkSourceBoot {
		t.Fatalf("W after the boot = %+v, want N (boot)", w)
	}
	// Errata E7: the trim left N's receipts incomplete; S4r appended the rest
	// after the kept bytes before S5 advanced W.
	if after := hl1rRead(t, receipts); !bytes.HasPrefix(after, trimmed) || len(after) == len(trimmed) {
		t.Fatal("S4r did not append to the receipts bytes kept by R-C3")
	}
	heights, terminated := hl1rReceiptHeights(t, receipts)
	if !terminated || heights[n] != len(f.blkJ.Transactions) || heights[n-1] != len(hl1rSenders) {
		t.Fatalf("receipts per height after the boot = %v (terminated %v), want every tx of N once", heights, terminated)
	}
}

// -----------------------------------------------------------------------------
// Process level: qsdm --hl1-tail-replay through main()
// -----------------------------------------------------------------------------

const (
	hl1rReplayChildEnv = "HL1_TEST_RUN_REPLAY_MAIN"
	hl1rReplayArgsEnv  = "HL1_TEST_REPLAY_ARGS"
)

// TestHL1ReplayMainChild runs main() with the arguments in
// HL1_TEST_REPLAY_ARGS. Replay always exits; exit 97 means main() returned.
func TestHL1ReplayMainChild(t *testing.T) {
	if os.Getenv(hl1rReplayChildEnv) != "1" {
		t.Skip("helper process for the HL1 replay tests")
	}
	os.Args = append([]string{os.Args[0]}, strings.Split(os.Getenv(hl1rReplayArgsEnv), "\x1f")...)
	main()
	os.Exit(97)
}

func hl1rRunMain(t *testing.T, stateDir string, args ...string) (int, string) {
	t.Helper()
	scratch := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestHL1ReplayMainChild$", "-test.count=1")
	cmd.Dir = scratch
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		switch {
		case strings.HasPrefix(k, "QSDM_"), strings.HasPrefix(k, "WEBVIEWER_"), strings.HasPrefix(k, "HL1_TEST_"),
			k == "CONFIG_FILE", k == "SQLITE_PATH", k == "LOG_FILE", k == "PROPOSAL_FILE":
			continue
		}
		env = append(env, kv)
	}
	cmd.Env = append(env,
		hl1rReplayChildEnv+"=1",
		hl1rReplayArgsEnv+"="+strings.Join(args, "\x1f"),
		"CONFIG_FILE="+filepath.Join(scratch, "absent.toml"),
		"SQLITE_PATH="+filepath.Join(stateDir, "qsdm.db"),
		"LOG_FILE="+filepath.Join(scratch, "qsdm.log"),
		"PROPOSAL_FILE="+filepath.Join(scratch, "proposals.json"),
		"DISABLE_CLI=1",
	)
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("child did not exit: %v\n%s", ctx.Err(), out)
	}
	var ee *exec.ExitError
	switch {
	case err == nil:
		return 0, string(out)
	case errors.As(err, &ee):
		return ee.ExitCode(), string(out)
	default:
		t.Fatalf("run child: %v", err)
		return -1, ""
	}
}

func TestHL1ReplayProcess(t *testing.T) {
	if testing.Short() {
		t.Skip("process-level replay test")
	}
	const j = 4
	f := hl1rCrash(t, false, j, "H4") // C5
	f.hl1rTearReceipts(t)

	t.Run("lock held: exit 78 before the receipts pre-trim, nothing changed", func(t *testing.T) {
		held, err := chain.AcquireStateLock(f.path(legacymining.StateLockFile))
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		before := hl1TreeHash(t, f.dir)
		code, out := hl1rRunMain(t, f.dir, hl1ReplayFlag)
		if code != legacymining.ExitFatalRestore || !strings.Contains(out, "validator state lock") {
			t.Fatalf("exit %d\n%s", code, out)
		}
		if hl1TreeHash(t, f.dir) != before {
			t.Fatal("the state directory changed although the lock was busy")
		}
	})
	for _, args := range [][]string{{hl1ReplayFlag, "extra"}, {hl1ReplayFlag + "=1"}, {"--version", hl1ReplayFlag}} {
		t.Run(fmt.Sprintf("malformed %q: exit 78, no node start", args), func(t *testing.T) {
			before := hl1rTreeHash(t, f.dir)
			code, out := hl1rRunMain(t, f.dir, args...)
			if code != legacymining.ExitFatalRestore || !strings.Contains(out, "usage") {
				t.Fatalf("exit %d\n%s", code, out)
			}
			if hl1rTreeHash(t, f.dir) != before {
				t.Fatal("a malformed replay invocation changed the state directory")
			}
		})
	}
	t.Run("replay: exit 0, W := J", func(t *testing.T) {
		code, out := hl1rRunMain(t, f.dir, hl1ReplayFlag)
		if code != 0 {
			t.Fatalf("exit %d\n%s", code, out)
		}
		if w := hl1rW(t, f.dir); w.Height != j || w.Hash != f.blkJ.Hash || w.Source != legacymining.WatermarkSourceReplay {
			t.Fatalf("W = %+v", w)
		}
		heights, terminated := hl1rReceiptHeights(t, f.path(hl1ReceiptsName))
		if !terminated || heights[j] != len(f.blkJ.Transactions) {
			t.Fatalf("receipts per height = %v", heights)
		}
		if _, _, err := hl1rBoot(t, f.dir, nil); err != nil {
			t.Fatalf("boot after replay: %v", err)
		}
	})
}

// -----------------------------------------------------------------------------
// hl1-tail and core agree on W; replay and hl1-tail use main()'s file names
// -----------------------------------------------------------------------------

func TestHL1TailWatermarkMatchesCore(t *testing.T) {
	h := strings.Repeat("0a", 32)
	s, fh := uint64(3), uint64(2)
	seed, _ := json.Marshal(legacymining.Watermark{Version: 1, Height: 3, Hash: h, Source: "seed", WrittenNS: 1, ServedTip: &s, FollowerHeight: &fh, FollowerHash: h})
	corpus := []string{
		string(seed),
		`{"version":1,"height":3,"hash":"` + h + `","source":"seal","written_ns":1}` + "\n",
		`{"version":1,"height":3,"hash":"` + h + `","source":"boot","written_ns":1}`,
		`{"version":1,"height":3,"hash":"` + h + `","source":"replay","written_ns":1}`,
		`{"version":1,"height":3,"hash":"` + h + `","source":"guess","written_ns":1}`,
		`{"version":2,"height":3,"hash":"` + h + `","source":"seal","written_ns":1}`,
		`{"version":1,"height":3,"hash":"` + strings.ToUpper(h) + `","source":"seal","written_ns":1}`,
		`{"version":1,"height":3,"hash":"` + h[:62] + `","source":"seal","written_ns":1}`,
		`{"version":1,"height":3,"hash":"` + h + `","source":"seal","written_ns":1,"extra":true}`,
		`{"version":1,"height":3,"hash":"` + h + `","source":"seal","written_ns":1}{}`,
		`{"version":1,"height":-3,"hash":"` + h + `","source":"seal","written_ns":1}`,
		`{}`, ``, `null`, `[]`,
	}
	for _, in := range corpus {
		wc, errc := hl1ParseWatermark([]byte(in))
		wt, errt := hl1tail.ParseWatermark([]byte(in))
		if (errc == nil) != (errt == nil) {
			t.Fatalf("%q: core err %v, hl1-tail err %v", in, errc, errt)
		}
		if errc == nil {
			a, _ := json.Marshal(wc)
			b, _ := json.Marshal(wt)
			if !bytes.Equal(a, b) {
				t.Fatalf("%q: core %s, hl1-tail %s", in, a, b)
			}
		}
	}

	// A W seeded by hl1-tail passes the core's S5 with tip = W.
	dir := t.TempDir()
	blocks := hl1Chain(6)
	for _, b := range blocks {
		if err := chain.AppendBlockToFile(filepath.Join(dir, hl1JournalName), b); err != nil {
			t.Fatal(err)
		}
	}
	code, out := hl1rTail(t, dir, "watermark", "seed", "--served-tip", "5", "--follower-height", "4", "--follower-hash", blocks[4].Hash)
	if code != 0 {
		t.Fatalf("seed: exit %d\n%s", code, out)
	}
	res, err := hl1StartupWatermark(hl1OSFS{}, dir, true, blocks[5], hl1BlockAtFn(blocks), hl1rNow)
	if err != nil || res.Wrote || res.Watermark.Source != legacymining.WatermarkSourceSeed || *res.Watermark.ServedTip != 5 {
		t.Fatalf("S5 on a seeded W: %+v %v", res, err)
	}
	// After retire, the core's S5 refuses (rule 1).
	if code, out := hl1rTail(t, dir, "watermark", "retire"); code != 0 {
		t.Fatalf("retire: exit %d\n%s", code, out)
	}
	if _, err := hl1StartupWatermark(hl1OSFS{}, dir, true, blocks[5], hl1BlockAtFn(blocks), hl1rNow); !errors.Is(err, errHL1WatermarkMissing) {
		t.Fatalf("S5 after retire: %v", err)
	}
}

func TestHL1ReplayFileNamesMatchMain(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{
		{hl1JournalName, hl1tail.JournalFile},
		{hl1AccountsName, hl1tail.AccountsFile},
		{hl1EnrollmentName, hl1tail.EnrollmentFile},
		{hl1ReceiptsName, hl1tail.ReceiptsFile},
		{hl1ReceiptsLegacyName, hl1ReceiptsLegacyName},
	} {
		if pair[0] != pair[1] {
			t.Fatalf("replay %q != hl1-tail %q", pair[0], pair[1])
		}
		if !bytes.Contains(src, []byte(`filepath.Join(stateDir, "`+pair[0]+`")`)) {
			t.Fatalf("main.go does not use %q under the state directory", pair[0])
		}
	}
}

func TestHL1ReplayRequested(t *testing.T) {
	for args, want := range map[string]bool{
		"": false, "--version": false, "--hl1-tail-replay": true, "-hl1-tail-replay": true,
		"--hl1-tail-replay=1": true, "x\x1f--hl1-tail-replay": true, "--hl1-tail-replayx": true,
	} {
		var a []string
		if args != "" {
			a = strings.Split(args, "\x1f")
		}
		if got := hl1ReplayRequested(a); got != want {
			t.Errorf("%q: %v", a, got)
		}
	}
}

// -----------------------------------------------------------------------------
// Source-level pins: replay reproduces main()'s state-affecting wiring
// -----------------------------------------------------------------------------

type hl1rSource struct {
	fset  *token.FileSet
	funcs map[string]*ast.FuncDecl
}

func hl1rParse(t *testing.T, files ...string) *hl1rSource {
	t.Helper()
	s := &hl1rSource{fset: token.NewFileSet(), funcs: map[string]*ast.FuncDecl{}}
	for _, name := range files {
		f, err := parser.ParseFile(s.fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv == nil {
				s.funcs[fd.Name.Name] = fd
			}
		}
	}
	return s
}

func (s *hl1rSource) fn(t *testing.T, name string) *ast.FuncDecl {
	t.Helper()
	fd, ok := s.funcs[name]
	if !ok {
		t.Fatalf("func %s not found", name)
	}
	return fd
}

func (s *hl1rSource) print(n ast.Node) string {
	var buf bytes.Buffer
	_ = printer.Fprint(&buf, s.fset, n)
	return buf.String()
}

// calls returns the printed calls in node whose name matches re.
func (s *hl1rSource) calls(node ast.Node, re *regexp.Regexp) map[string]bool {
	out := map[string]bool{}
	ast.Inspect(node, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok && re.MatchString(hl1CallName(c)) {
			out[s.print(c)] = true
		}
		return true
	})
	return out
}

func hl1rKeys(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// The package-level consensus settings replay applies are exactly the ones
// main() applies, with the same arguments.
func TestHL1ReplayConsensusSettingsMatchMain(t *testing.T) {
	s := hl1rParse(t, "main.go", "hl1_replay.go")
	re := regexp.MustCompile(`^(chain|mining|enrollment)\.Set`)
	got := hl1rKeys(s.calls(s.fn(t, "hl1ReplayConsensusSettings"), re))
	want := hl1rKeys(s.calls(s.fn(t, "main"), re))
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("replay applies\n  %s\nmain applies\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
	if len(want) < 10 {
		t.Fatalf("only %d settings found in main()", len(want))
	}
}

// Replay wires v2wiring.Config with main()'s keys and state file names, and
// sets every producer option that TryAppendExternalBlock consults.
func TestHL1ReplayStackMatchesMainWiring(t *testing.T) {
	s := hl1rParse(t, "main.go", "hl1_replay.go")
	keys := func(fd *ast.FuncDecl) []string {
		var out []string
		ast.Inspect(fd, func(n ast.Node) bool {
			if cl, ok := n.(*ast.CompositeLit); ok && s.print(cl.Type) == "v2wiring.Config" {
				for _, e := range cl.Elts {
					out = append(out, s.print(e.(*ast.KeyValueExpr).Key))
				}
			}
			return true
		})
		sort.Strings(out)
		return out
	}
	mainFn, stackFn := s.fn(t, "main"), s.fn(t, "hl1NewReplayStack")
	if a, b := strings.Join(keys(stackFn), ","), strings.Join(keys(mainFn), ","); a != b || a == "" {
		t.Fatalf("v2wiring.Config keys: replay %s, main %s", a, b)
	}
	for _, lit := range []string{`"qsdm_governance.json"`, `"qsdm_slash_receipts.ndjson"`} {
		for _, fd := range []*ast.FuncDecl{mainFn, stackFn} {
			if !strings.Contains(s.print(fd), lit) {
				t.Fatalf("%s does not use %s", fd.Name.Name, lit)
			}
		}
	}
	// main()'s producer options: the external-append ones are replayed;
	// the others only affect local sealing, which replay never does.
	mainSet := s.calls(mainFn, regexp.MustCompile(`^adminProducer\.Set`))
	var names []string
	for c := range mainSet {
		names = append(names, c[:strings.Index(c, "(")])
	}
	sort.Strings(names)
	names = hl1rUniq(names)
	wantMain := "adminProducer.SetAppendReceiptStore,adminProducer.SetAuthorizedBlockProducers,adminProducer.SetBFTSealGate,adminProducer.SetBlockSigner,adminProducer.SetPolFollower,adminProducer.SetPreSealBFTRound,adminProducer.SetProducerTransition,adminProducer.SetSealGuard"
	if got := strings.Join(names, ","); got != wantMain {
		t.Fatalf("main() producer options changed; review replay (hl1NewReplayStack):\n  %s", got)
	}
	replaySet := hl1rKeys(s.calls(stackFn, regexp.MustCompile(`^s\.producer\.Set`)))
	if got := strings.Join(replaySet, ","); got != "s.producer.SetAppendReceiptStore(s.receipts),s.producer.SetAuthorizedBlockProducers(authorized),s.producer.SetProducerTransition(transition)" {
		t.Fatalf("replay producer options: %s", got)
	}
}

func hl1rUniq(in []string) []string {
	var out []string
	for i, v := range in {
		if i == 0 || v != in[i-1] {
			out = append(out, v)
		}
	}
	return out
}

// The replay dispatch runs right after the config load, before S0 and before
// anything else reads the state directory.
func TestHL1ReplayDispatchPlacement(t *testing.T) {
	s := hl1ParseMain(t)
	s.before(t, "config.LoadConfig", "hl1ReplayRequested")
	s.before(t, "hl1ReplayRequested", "hl1ReplayMain")
	s.before(t, "hl1ReplayMain", "cfg.ProducerTransition.ValidateJournalPrefix")
	s.before(t, "hl1ReplayMain", "hl1AcquireStateLock")
	src := hl1rParse(t, "hl1_replay.go")
	run := src.fn(t, "hl1RunReplay")
	var order []string
	ast.Inspect(run, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok {
			switch name := hl1CallName(c); name {
			case "chain.AcquireStateLock", "hl1ArmFailStop", "hl1ReadWatermark", "hl1tail.PreTrimReceipts", "st.restore", "st.producer.TryAppendExternalBlock":
				order = append(order, name)
			}
		}
		return true
	})
	want := "chain.AcquireStateLock,hl1ArmFailStop,hl1ReadWatermark,hl1tail.PreTrimReceipts,st.restore,st.producer.TryAppendExternalBlock,hl1ReadWatermark"
	if got := strings.Join(order, ","); got != want {
		t.Fatalf("replay step order:\n  %s\nwant\n  %s", got, want)
	}
}
