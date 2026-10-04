package main

// hl1_persist.go (HL1 WP9): the persistence hook H0-H10 (design rev 4 §4.4),
// the served watermark W (§3.1, S5, H7), generation links, the H8 family
// audit, the durable tip and the surfaces it clamps (W5), and the HL1
// metrics. File-system operations go through hl1FS so tests can fault every
// step.
//
// Hook order (OnSealedBlock runs with sealLifecycleMu held and bp.mu
// released; W4: nothing becomes visible before H7 is durable):
//
//	H0   prior v2wiring hook (enrollment sweep, gov promotion)
//	H1   normal mode only: link accounts/enrollment -> .h<N-1>, fsync(dir)
//	H2   journal append + fsync; replay: assert line J, fsync the journal
//	H3   accounts snapshot (temp, fsync, rename)
//	H4   enrollment snapshot
//	H5   receipts append, then fsync the receipts file
//	H6   fsync(stateDir): the H3/H4 renames become durable
//	H7   producer role or replay: D1 W := N; prune .h<k> for k < N-1
//	---  any H1-H7 error: failStop(86, "persist:H<k>:<err>")
//	H8   normal mode: (a) I7 family audit for local seals;
//	     (b) canary: Ledger.OnDurableBlock, Guard.ObserveSeal
//	H9   normal mode: durable tip := N
//	H10  normal mode: POL publish, block broadcast

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/blackbeardONE/QSDM/internal/legacymining"
	"github.com/blackbeardONE/QSDM/internal/logging"
	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/monitoring"
)

// hl1Logger returns the process logger, or a silent one before it exists
// (tests).
func hl1Logger() *logging.Logger {
	if logger != nil {
		return logger
	}
	return logging.NewSilentLogger()
}

// -----------------------------------------------------------------------------
// Durable tip and the clamped surfaces (§2 (c), W5)
// -----------------------------------------------------------------------------

// hl1DurableTip is the highest height whose state is durable: set by S5 and
// advanced by H9. Until it is set, every clamped surface reports no tip.
type hl1DurableTip struct {
	v atomic.Uint64 // height+1; 0 means unset
}

// Store sets the durable tip to h.
func (d *hl1DurableTip) Store(h uint64) { d.v.Store(h + 1) }

// Load returns the durable tip and whether it is set. A nil tip is unset.
func (d *hl1DurableTip) Load() (uint64, bool) {
	if d == nil {
		return 0, false
	}
	v := d.v.Load()
	if v == 0 {
		return 0, false
	}
	return v - 1, true
}

// hl1ClampRange limits [from, to] to the durable tip. ok is false when
// nothing in the range may be served.
func hl1ClampRange(d *hl1DurableTip, from, to uint64) (uint64, uint64, bool) {
	tip, ok := d.Load()
	if !ok {
		return 0, 0, false
	}
	if to > tip {
		to = tip
	}
	if from > to {
		return 0, 0, false
	}
	return from, to, true
}

// durableChainView is the miningsvc ChainView (§2 (c)): WorkAt, HeaderHashAt
// and the accept height never see a block above the durable tip.
type durableChainView struct {
	tip      *hl1DurableTip
	producer *chain.BlockProducer
}

var _ legacymining.ChainView = (*durableChainView)(nil)

func (v *durableChainView) HasTip() bool {
	_, ok := v.tip.Load()
	return ok
}

func (v *durableChainView) TipHeight() uint64 {
	h, _ := v.tip.Load()
	return h
}

func (v *durableChainView) GetBlock(height uint64) (*chain.Block, bool) {
	tip, ok := v.tip.Load()
	if !ok || height > tip || v.producer == nil {
		return nil, false
	}
	return v.producer.GetBlock(height)
}

// hl1BlockProvider is the P2P catch-up block provider, clamped to the durable
// tip.
func hl1BlockProvider(p *chain.BlockProducer, d *hl1DurableTip) func(from, to uint64, limit int) []*chain.Block {
	return func(from, to uint64, limit int) []*chain.Block {
		if limit <= 0 {
			limit = 64
		}
		out := make([]*chain.Block, 0, limit)
		from, to, ok := hl1ClampRange(d, from, to)
		if !ok || p == nil {
			return out
		}
		for h := from; h <= to && len(out) < limit; h++ {
			blk, ok := p.GetBlock(h)
			if !ok {
				break
			}
			out = append(out, blk)
			if h == ^uint64(0) {
				break
			}
		}
		return out
	}
}

// -----------------------------------------------------------------------------
// File-system operations
// -----------------------------------------------------------------------------

// hl1FS is the file-system surface of the hook and S5.
type hl1FS interface {
	Remove(name string) error
	Link(oldname, newname string) error
	Lstat(name string) (fs.FileInfo, error)
	ReadDir(dir string) ([]fs.DirEntry, error)
	// SyncDir is fsync(dir) (§3.2).
	SyncDir(dir string) error
	// SyncFile opens name write-only (no create, no truncate), fsyncs and
	// closes it.
	SyncFile(name string) error
	// ReadTail returns the last n bytes of name (all of it if shorter) and
	// the file size.
	ReadTail(name string, n int64) ([]byte, int64, error)
	// WriteFileDurable is D1 (legacymining.WriteFileDurable).
	WriteFileDurable(dir, name string, data []byte) error
}

type hl1OSFS struct{}

func (hl1OSFS) Remove(name string) error                  { return os.Remove(name) }
func (hl1OSFS) Link(oldname, newname string) error        { return os.Link(oldname, newname) }
func (hl1OSFS) Lstat(name string) (fs.FileInfo, error)    { return os.Lstat(name) }
func (hl1OSFS) ReadDir(dir string) ([]fs.DirEntry, error) { return os.ReadDir(dir) }
func (hl1OSFS) SyncDir(dir string) error {
	hl1FSTrace("SyncDir", dir)
	return legacymining.SyncDir(dir)
}

func (hl1OSFS) WriteFileDurable(d, n string, b []byte) error {
	hl1FSTrace("WriteFileDurable", n)
	return legacymining.WriteFileDurable(d, n, b)
}

func (hl1OSFS) SyncFile(name string) error {
	hl1FSTrace("SyncFile", name)
	f, err := os.OpenFile(name, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	serr := f.Sync()
	if cerr := f.Close(); serr == nil {
		serr = cerr
	}
	return serr
}

func (hl1OSFS) ReadTail(name string, n int64) ([]byte, int64, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, 0, err
	}
	size := fi.Size()
	if n > size {
		n = size
	}
	buf := make([]byte, n)
	if _, err := f.ReadAt(buf, size-n); err != nil && !errors.Is(err, io.EOF) {
		return nil, 0, err
	}
	return buf, size, nil
}

// -----------------------------------------------------------------------------
// Metrics (§2 (g))
// -----------------------------------------------------------------------------

type hl1MetricSet struct {
	hookLastNS atomic.Int64
	hookMaxNS  atomic.Int64
	hookSumNS  atomic.Int64
	hookCount  atomic.Uint64
	served     hl1DurableTip // hl1_served_watermark; unset when no W
	unexpected atomic.Uint64 // hl1_unexpected_tx_family_total
}

var hl1Metrics hl1MetricSet

func (m *hl1MetricSet) observeHook(d time.Duration) {
	ns := int64(d)
	m.hookLastNS.Store(ns)
	m.hookSumNS.Add(ns)
	m.hookCount.Add(1)
	for {
		cur := m.hookMaxNS.Load()
		if ns <= cur || m.hookMaxNS.CompareAndSwap(cur, ns) {
			return
		}
	}
}

func hl1HeightGauge(d *hl1DurableTip) float64 {
	h, ok := d.Load()
	if !ok {
		return -1
	}
	return float64(h)
}

// hl1MetricsCollector exports the HL1 metrics. An unset watermark or durable
// tip is -1.
func hl1MetricsCollector(durable *hl1DurableTip) monitoring.MetricCollector {
	return func() []monitoring.Metric {
		m := &hl1Metrics
		return []monitoring.Metric{
			{Name: "hl1_persist_hook_seconds", Help: "Duration of the last HL1 persistence hook (H0-H10)", Type: monitoring.MetricGauge, Value: time.Duration(m.hookLastNS.Load()).Seconds()},
			{Name: "hl1_persist_hook_seconds_max", Help: "Longest HL1 persistence hook since start", Type: monitoring.MetricGauge, Value: time.Duration(m.hookMaxNS.Load()).Seconds()},
			{Name: "hl1_persist_hook_seconds_sum", Help: "Total time spent in the HL1 persistence hook", Type: monitoring.MetricCounter, Value: time.Duration(m.hookSumNS.Load()).Seconds()},
			{Name: "hl1_persist_hook_seconds_count", Help: "HL1 persistence hook runs", Type: monitoring.MetricCounter, Value: float64(m.hookCount.Load())},
			{Name: "hl1_served_watermark", Help: "Served watermark W height (-1 when absent)", Type: monitoring.MetricGauge, Value: hl1HeightGauge(&m.served)},
			{Name: "hl1_durable_tip", Help: "Durable tip height; nothing above it is served (-1 when unset)", Type: monitoring.MetricGauge, Value: hl1HeightGauge(durable)},
			{Name: "hl1_tx_gossip_ingress_installed", Help: "1 if the tx gossip ingress is installed (follower role), 0 otherwise", Type: monitoring.MetricGauge, Value: float64(hl1TxGossipIngressInstalled.Load())},
			{Name: "hl1_tx_gossip_dropped_total", Help: "Tx-topic gossip messages dropped in the producer role", Type: monitoring.MetricCounter, Value: float64(hl1TxGossipDropped.Load())},
			{Name: "hl1_unexpected_tx_family_total", Help: "Txs in local blocks outside the I7 families", Type: monitoring.MetricCounter, Value: float64(m.unexpected.Load())},
		}
	}
}

// -----------------------------------------------------------------------------
// Generation links (§3.1)
// -----------------------------------------------------------------------------

// hl1GenerationLink is the generation-link path of snapshot path at height.
func hl1GenerationLink(path string, height uint64) string {
	return fmt.Sprintf(legacymining.GenerationLinkFormat, path, height)
}

// hl1ParseGeneration returns k if name is base+".h<k>".
func hl1ParseGeneration(name, base string) (uint64, bool) {
	rest, ok := strings.CutPrefix(name, base+".h")
	if !ok || rest == "" {
		return 0, false
	}
	for i := 0; i < len(rest); i++ {
		if rest[i] < '0' || rest[i] > '9' {
			return 0, false
		}
	}
	k, err := strconv.ParseUint(rest, 10, 64)
	return k, err == nil
}

// -----------------------------------------------------------------------------
// The persistence hook
// -----------------------------------------------------------------------------

type hl1HookMode uint8

const (
	hl1HookNormal hl1HookMode = iota
	// hl1HookReplay is the --hl1-tail-replay hook: no H1, H2 asserts and
	// fsyncs the journal, no H8-H10.
	hl1HookReplay
)

// hl1PersistHook is the OnSealedBlock hook. Every step's collaborators are
// fields, so tests can record and fault each operation.
type hl1PersistHook struct {
	Mode         hl1HookMode
	ProducerRole bool // W is written only in the producer role (and replay)

	StateDir       string
	JournalPath    string
	AccountsPath   string
	EnrollmentPath string // "" when there is no enrollment snapshot
	ReceiptsPath   string // "" when there is no receipt store
	FS             hl1FS

	Prior          func(*chain.Block)                            // H0
	AppendJournal  func(*chain.Block) error                      // H2 (normal mode)
	SaveAccounts   func(path string) error                       // H3
	SaveEnrollment func(path string) error                       // H4; nil skips
	AppendReceipts func(path string, height uint64) (int, error) // H5; nil skips

	LocalSeal *atomic.Bool // set by the block driver around ProduceBlock
	Canary    bool
	Ledger    legacymining.Ledger // canary only
	Guard     legacymining.Guard  // canary only

	Durable    *hl1DurableTip
	PublishPol func(*chain.Block) // H10
	Broadcast  func(*chain.Block) // H10

	// FailStop is failStop. If it returns (tests), OnFailStopReturn gets the
	// error and the hook stops.
	FailStop         legacymining.FailStopFunc
	OnFailStopReturn func(error)

	Now     func() time.Time
	Log     *logging.Logger
	Metrics *hl1MetricSet

	// trace, when set (tests), is called with "H0".."H10" as each step starts.
	trace func(step string)
}

var hl1StepNames = [...]string{"H0", "H1", "H2", "H3", "H4", "H5", "H6", "H7", "H8", "H9", "H10"}

func (h *hl1PersistHook) log() *logging.Logger {
	if h.Log != nil {
		return h.Log
	}
	return hl1Logger()
}

func (h *hl1PersistHook) metrics() *hl1MetricSet {
	if h.Metrics != nil {
		return h.Metrics
	}
	return &hl1Metrics
}

func (h *hl1PersistHook) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

// begin starts step k. H1-H7 may carry an injected fault (hl_crashpoints
// builds only).
func (h *hl1PersistHook) begin(k int) error {
	if h.trace != nil {
		h.trace(hl1StepNames[k])
	}
	if k < 1 || k > 7 {
		return nil
	}
	return hl1Fault("persist:" + hl1StepNames[k])
}

func (h *hl1PersistHook) done(k int) { hl1Crashpoint("persist:after-" + hl1StepNames[k]) }

// fail is the H1-H7 error path: failStop(86). The durable tip and W do not
// advance.
func (h *hl1PersistHook) fail(k int, blk *chain.Block, err error) {
	cause := fmt.Sprintf("%s%s:%v", legacymining.CausePersistPrefix, hl1StepNames[k], err)
	h.log().Error("hl1: persistence failed; fail-stop",
		"step", hl1StepNames[k], "height", blk.Height, "hash", blk.Hash, "error_str", err.Error())
	h.FailStop(legacymining.ExitFailStop, cause)
	if h.OnFailStopReturn != nil {
		h.OnFailStopReturn(fmt.Errorf("%s at height %d: %w", hl1StepNames[k], blk.Height, err))
	}
}

// OnSealedBlock runs H0-H10 for blk.
func (h *hl1PersistHook) OnSealedBlock(blk *chain.Block) {
	start := time.Now()
	defer func() { h.metrics().observeHook(time.Since(start)) }()

	// H0 (in memory).
	_ = h.begin(0)
	if h.Prior != nil {
		h.Prior(blk)
	}
	if blk == nil {
		return
	}
	h.done(0)

	steps := [...]func(*chain.Block) error{1: h.h1, 2: h.h2, 3: h.h3, 4: h.h4, 5: h.h5, 6: h.h6, 7: h.h7}
	for k := 1; k <= 7; k++ {
		if k == 1 && h.Mode == hl1HookReplay {
			continue // replay skips H1: it would relink .h<J-1> from J-state files
		}
		err := h.begin(k)
		if err == nil {
			err = steps[k](blk)
		}
		if err != nil {
			h.fail(k, blk, err)
			return
		}
		h.done(k)
	}
	if h.Mode == hl1HookReplay {
		return // replay skips H8-H10
	}

	// H8: failures trip FREEZE and never exit.
	_ = h.begin(8)
	h.h8(blk)
	h.done(8)

	// H9: from here, N may be served.
	_ = h.begin(9)
	if h.Durable != nil {
		h.Durable.Store(blk.Height)
	}
	h.done(9)

	// H10.
	_ = h.begin(10)
	if h.PublishPol != nil {
		h.PublishPol(blk)
	}
	if h.Broadcast != nil {
		h.Broadcast(blk)
	}
	h.done(10)
}

// h1 links the generation-(N-1) snapshots to .h<N-1>, then fsyncs the state
// directory. The N-1 files are durable from block N-1's H6. A snapshot that
// does not exist yet has no generation to preserve and is skipped.
func (h *hl1PersistHook) h1(blk *chain.Block) error {
	if blk.Height == 0 {
		return nil // genesis has no previous generation
	}
	prev := blk.Height - 1
	for _, src := range []string{h.AccountsPath, h.EnrollmentPath} {
		if src == "" {
			continue
		}
		if _, err := h.FS.Lstat(src); errors.Is(err, fs.ErrNotExist) {
			h.log().Warn("hl1: H1: snapshot absent; no generation link", "path", src, "height", prev)
			continue
		} else if err != nil {
			return fmt.Errorf("stat %s: %w", filepath.Base(src), err)
		}
		dst := hl1GenerationLink(src, prev)
		if err := h.FS.Remove(dst); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove stale %s: %w", filepath.Base(dst), err)
		}
		if err := h.FS.Link(src, dst); err != nil {
			return fmt.Errorf("link %s: %w", filepath.Base(dst), err)
		}
	}
	if err := h.FS.SyncDir(h.StateDir); err != nil {
		return fmt.Errorf("fsync state dir: %w", err)
	}
	return nil
}

// h2 journals blk. In replay mode the line is already in the journal but may
// not be fsynced: assert that the last line equals json.Marshal(blk)+"\n",
// then fsync the journal.
func (h *hl1PersistHook) h2(blk *chain.Block) error {
	if h.Mode != hl1HookReplay {
		if err := h.AppendJournal(blk); err != nil {
			return fmt.Errorf("journal append height %d: %w", blk.Height, err)
		}
		return nil
	}
	if err := hl1AssertJournalTail(h.FS, h.JournalPath, blk); err != nil {
		return err
	}
	if err := h.FS.SyncFile(h.JournalPath); err != nil {
		return fmt.Errorf("fsync journal: %w", err)
	}
	return nil
}

// hl1AssertJournalTail checks that the journal ends with exactly the line
// json.Marshal(blk)+"\n".
func hl1AssertJournalTail(fsys hl1FS, path string, blk *chain.Block) error {
	want, err := json.Marshal(blk)
	if err != nil {
		return fmt.Errorf("marshal block %d: %w", blk.Height, err)
	}
	want = append(want, '\n')
	tail, size, err := fsys.ReadTail(path, int64(len(want))+1)
	if err != nil {
		return fmt.Errorf("read journal tail: %w", err)
	}
	if size < int64(len(want)) || !bytes.Equal(tail[len(tail)-len(want):], want) {
		return fmt.Errorf("journal line %d is not the replayed block", blk.Height)
	}
	if size > int64(len(want)) && tail[0] != '\n' {
		return fmt.Errorf("journal line %d does not start on a line boundary", blk.Height)
	}
	return nil
}

func (h *hl1PersistHook) h3(*chain.Block) error {
	if err := h.SaveAccounts(h.AccountsPath); err != nil {
		return fmt.Errorf("save accounts snapshot: %w", err)
	}
	return nil
}

func (h *hl1PersistHook) h4(*chain.Block) error {
	if h.SaveEnrollment == nil || h.EnrollmentPath == "" {
		return nil
	}
	if err := h.SaveEnrollment(h.EnrollmentPath); err != nil {
		return fmt.Errorf("save enrollment snapshot: %w", err)
	}
	return nil
}

func (h *hl1PersistHook) h5(blk *chain.Block) error {
	if h.AppendReceipts == nil || h.ReceiptsPath == "" {
		return nil
	}
	n, err := h.AppendReceipts(h.ReceiptsPath, blk.Height)
	if err != nil {
		return fmt.Errorf("append receipts at height %d: %w", blk.Height, err)
	}
	if err := h.FS.SyncFile(h.ReceiptsPath); err != nil {
		if n == 0 && errors.Is(err, fs.ErrNotExist) {
			return nil // no receipts have ever been written
		}
		return fmt.Errorf("fsync receipts: %w", err)
	}
	return nil
}

func (h *hl1PersistHook) h6(*chain.Block) error {
	if err := h.FS.SyncDir(h.StateDir); err != nil {
		return fmt.Errorf("fsync state dir: %w", err)
	}
	return nil
}

// h7 is the durable boundary: D1 W := N in the producer role and in replay,
// then a best-effort prune of generation links older than N-1.
func (h *hl1PersistHook) h7(blk *chain.Block) error {
	if h.ProducerRole || h.Mode == hl1HookReplay {
		source := legacymining.WatermarkSourceSeal
		if h.Mode == hl1HookReplay {
			source = legacymining.WatermarkSourceReplay
		}
		if err := hl1WriteWatermark(h.FS, h.StateDir, legacymining.Watermark{
			Version:   legacymining.WatermarkVersion,
			Height:    blk.Height,
			Hash:      blk.Hash,
			Source:    source,
			WrittenNS: h.now().UnixNano(),
		}); err != nil {
			return err
		}
		h.metrics().served.Store(blk.Height)
	}
	if blk.Height >= 1 {
		h.pruneGenerations(blk.Height - 1)
	}
	return nil
}

// pruneGenerations removes every generation link .h<k> with k < keep. It is
// best effort: errors are logged.
func (h *hl1PersistHook) pruneGenerations(keep uint64) {
	if keep == 0 {
		return
	}
	entries, err := h.FS.ReadDir(h.StateDir)
	if err != nil {
		h.log().Warn("hl1: H7: generation prune skipped", "error_str", err.Error())
		return
	}
	for _, e := range entries {
		for _, src := range []string{h.AccountsPath, h.EnrollmentPath} {
			if src == "" || filepath.Dir(src) != filepath.Clean(h.StateDir) {
				continue
			}
			if k, ok := hl1ParseGeneration(e.Name(), filepath.Base(src)); ok && k < keep {
				if err := h.FS.Remove(filepath.Join(h.StateDir, e.Name())); err != nil && !errors.Is(err, fs.ErrNotExist) {
					h.log().Warn("hl1: H7: generation link not removed", "name", e.Name(), "error_str", err.Error())
				}
			}
		}
	}
}

// h8 is the family audit (I7) for local seals in every mode, then the canary
// ledger. Failures trip FREEZE and never exit.
func (h *hl1PersistHook) h8(blk *chain.Block) {
	local := h.LocalSeal != nil && h.LocalSeal.Load()
	if local {
		if v := legacymining.AuditTxFamilies(blk); len(v) > 0 {
			h.metrics().unexpected.Add(uint64(len(v)))
			for _, fv := range v {
				h.log().Error("hl1: I7: unexpected tx family in a local block",
					"height", blk.Height, "tx_id", fv.TxID, "contract_id", fv.ContractID)
			}
			if h.Canary && h.Guard != nil {
				h.Guard.Freeze(fmt.Sprintf("%s:height=%d tx=%s contract=%q", legacymining.CauseTxFamily, blk.Height, v[0].TxID, v[0].ContractID))
			}
		}
	}
	if !h.Canary {
		return
	}
	if h.Ledger != nil {
		if err := h.Ledger.OnDurableBlock(blk, local); err != nil {
			h.log().Error("hl1: H8: ledger", "height", blk.Height, "local", local, "error_str", err.Error())
		}
	}
	if h.Guard != nil {
		h.Guard.ObserveSeal(blk.Height, local)
	}
}

// -----------------------------------------------------------------------------
// Served watermark W (§3.1) and startup S4/S5
// -----------------------------------------------------------------------------

// hl1MaxWatermarkBytes bounds the watermark file.
const hl1MaxWatermarkBytes = 4096

// hl1WriteWatermark D1-writes W into dir.
func hl1WriteWatermark(fsys hl1FS, dir string, w legacymining.Watermark) error {
	data, err := json.Marshal(w)
	if err != nil {
		return fmt.Errorf("encode watermark: %w", err)
	}
	hl1CrashInsideD1(dir, legacymining.WatermarkFile, "persist:H7:d1")
	if err := fsys.WriteFileDurable(dir, legacymining.WatermarkFile, append(data, '\n')); err != nil {
		return fmt.Errorf("write watermark %d: %w", w.Height, err)
	}
	return nil
}

// errHL1WatermarkMissing is S5 rule 1.
var errHL1WatermarkMissing = errors.New("watermark missing: run R-H (or R-B)")

// hl1ReadWatermark reads and validates <dir>/hl1-served-watermark.json. An
// absent file returns errHL1WatermarkMissing; anything unreadable or invalid
// is another error (S5 rule 2, R-X).
func hl1ReadWatermark(dir string) (legacymining.Watermark, error) {
	p := filepath.Join(dir, legacymining.WatermarkFile)
	fi, err := os.Lstat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return legacymining.Watermark{}, errHL1WatermarkMissing
	}
	if err != nil {
		return legacymining.Watermark{}, fmt.Errorf("watermark unreadable: %w", err)
	}
	if !fi.Mode().IsRegular() {
		return legacymining.Watermark{}, fmt.Errorf("watermark %s is not a regular file", p)
	}
	if fi.Size() > hl1MaxWatermarkBytes {
		return legacymining.Watermark{}, fmt.Errorf("watermark %s is %d bytes", p, fi.Size())
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return legacymining.Watermark{}, fmt.Errorf("watermark unreadable: %w", err)
	}
	return hl1ParseWatermark(data)
}

// hl1ParseWatermark decodes W strictly and validates it.
func hl1ParseWatermark(data []byte) (legacymining.Watermark, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var w legacymining.Watermark
	if err := dec.Decode(&w); err != nil {
		return legacymining.Watermark{}, fmt.Errorf("watermark invalid: %w", err)
	}
	if err := dec.Decode(new(json.RawMessage)); err != io.EOF {
		return legacymining.Watermark{}, errors.New("watermark invalid: trailing data")
	}
	if w.Version != legacymining.WatermarkVersion {
		return legacymining.Watermark{}, fmt.Errorf("watermark invalid: version %d", w.Version)
	}
	if !hl1IsLowerHex(w.Hash, 64) {
		return legacymining.Watermark{}, fmt.Errorf("watermark invalid: hash %q", w.Hash)
	}
	switch w.Source {
	case legacymining.WatermarkSourceSeal, legacymining.WatermarkSourceBoot,
		legacymining.WatermarkSourceReplay, legacymining.WatermarkSourceSeed:
	default:
		return legacymining.Watermark{}, fmt.Errorf("watermark invalid: source %q", w.Source)
	}
	return w, nil
}

func hl1IsLowerHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// hl1S5Result is the outcome of S5.
type hl1S5Result struct {
	DurableTip    uint64
	DurableTipSet bool
	Watermark     legacymining.Watermark // producer role only
	Wrote         bool                   // S5 rule 6 wrote W := tip
}

// hl1StartupWatermark is S5. It runs after the whole restore block (S4) and
// before OpenChainJournal, so an S4 refusal leaves W untouched. tip is the
// restored tip (nil for an empty chain) and blockAt returns the restored
// block at a height.
//
// Producer role:
//  1. W absent -> refuse (R-H or R-B).
//  2. W unreadable or invalid -> refuse (R-X).
//  3. W.height > tip -> refuse (R-W).
//  4. restored block at W.height has a different hash -> refuse (R-W).
//  5. tip > W.height+1 -> refuse (R-H): blocks above W+1 were not sealed by
//     HL1.
//  6. tip > W.height -> D1 W := (tip, tip.hash, boot). The durable tip is
//     the restored tip.
//
// Rules 3-5 and the rule 6 decision are hl1S5Decide. In the producer role
// main() runs S4r (hl1RepairTipReceipts) before S5, so rule 6 advances W
// only over a block whose receipts are complete (errata E7).
//
// Follower role: W is neither read nor written; the durable tip is the
// restored tip.
func hl1StartupWatermark(fsys hl1FS, stateDir string, producerRole bool, tip *chain.Block, blockAt func(uint64) (*chain.Block, bool), now time.Time) (hl1S5Result, error) {
	var res hl1S5Result
	if !producerRole {
		if tip != nil {
			res.DurableTip, res.DurableTipSet = tip.Height, true
		}
		return res, nil
	}
	w, err := hl1ReadWatermark(stateDir)
	if err != nil {
		return res, err
	}
	res.Watermark = w
	advance, err := hl1S5Decide(w, tip, blockAt)
	if err != nil {
		return res, err
	}
	if advance {
		nw := legacymining.Watermark{
			Version:   legacymining.WatermarkVersion,
			Height:    tip.Height,
			Hash:      tip.Hash,
			Source:    legacymining.WatermarkSourceBoot,
			WrittenNS: now.UnixNano(),
		}
		if err := hl1WriteWatermark(fsys, stateDir, nw); err != nil {
			return res, err
		}
		res.Watermark, res.Wrote = nw, true
	}
	res.DurableTip, res.DurableTipSet = tip.Height, true
	return res, nil
}

// hl1S5Decide is S5 rules 3-5 for a valid W against the restored chain, and
// the rule 6 decision: advance reports tip = W.height+1. It is pure: S5 and
// the boot step S4r (hl1RepairTipReceipts) both call it, so S4r runs exactly
// when S5 rule 6 is about to write W := tip. tip is nil for an empty chain.
func hl1S5Decide(w legacymining.Watermark, tip *chain.Block, blockAt func(uint64) (*chain.Block, bool)) (advance bool, err error) {
	if tip == nil {
		return false, fmt.Errorf("watermark height %d is above the restored tip (empty chain); follow R-W", w.Height)
	}
	if w.Height > tip.Height {
		return false, fmt.Errorf("watermark height %d is above the restored tip %d; follow R-W", w.Height, tip.Height)
	}
	at, ok := blockAt(w.Height)
	if !ok || at == nil {
		return false, fmt.Errorf("restored chain has no block at watermark height %d; follow R-W", w.Height)
	}
	if at.Hash != w.Hash {
		return false, fmt.Errorf("restored block %d has hash %s, watermark has %s; follow R-W", w.Height, at.Hash, w.Hash)
	}
	if tip.Height > w.Height+1 {
		return false, fmt.Errorf("restored tip %d is more than one block above watermark %d: blocks were sealed without HL1; follow R-H", tip.Height, w.Height)
	}
	return tip.Height > w.Height, nil
}

// hl1RequireFinalNewline is the S4 check for the journal and the receipts
// NDJSON: a non-empty file must end with "\n" (R-C3). An absent file passes.
func hl1RequireFinalNewline(fsys hl1FS, path string) error {
	tail, size, err := fsys.ReadTail(path, 1)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if size > 0 && (len(tail) != 1 || tail[0] != '\n') {
		return fmt.Errorf("%s does not end with a newline: torn last line; follow R-C3", path)
	}
	return nil
}
