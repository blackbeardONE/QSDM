package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/internal/legacymining"
	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

// -----------------------------------------------------------------------------
// Recording file system and collaborators
// -----------------------------------------------------------------------------

var errHL1Injected = errors.New("injected I/O error")

type hl1DirEntry string

func (e hl1DirEntry) Name() string               { return string(e) }
func (e hl1DirEntry) IsDir() bool                { return false }
func (e hl1DirEntry) Type() fs.FileMode          { return 0 }
func (e hl1DirEntry) Info() (fs.FileInfo, error) { return nil, errors.New("not implemented") }

// hl1Recorder records every hook operation as "<step>:<op>". When failStep is
// set, the first operation of that step returns errHL1Injected.
type hl1Recorder struct {
	mu       sync.Mutex
	step     string
	ops      []string
	failStep string
	failed   bool
	listing  []string // ReadDir result
	tail     []byte   // ReadTail result
}

func (r *hl1Recorder) trace(step string) {
	r.mu.Lock()
	r.step = step
	r.mu.Unlock()
}

func (r *hl1Recorder) rec(op string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ops = append(r.ops, r.step+":"+op)
	if r.failStep != "" && r.step == r.failStep && !r.failed {
		r.failed = true
		return errHL1Injected
	}
	return nil
}

func (r *hl1Recorder) Ops() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.ops...)
}

func (r *hl1Recorder) Remove(name string) error { return r.rec("remove " + filepath.Base(name)) }
func (r *hl1Recorder) Link(o, n string) error {
	return r.rec("link " + filepath.Base(o) + " " + filepath.Base(n))
}
func (r *hl1Recorder) Lstat(name string) (fs.FileInfo, error) {
	return nil, r.rec("lstat " + filepath.Base(name))
}
func (r *hl1Recorder) ReadDir(string) ([]fs.DirEntry, error) {
	if err := r.rec("readdir"); err != nil {
		return nil, err
	}
	var out []fs.DirEntry
	for _, n := range r.listing {
		out = append(out, hl1DirEntry(n))
	}
	return out, nil
}
func (r *hl1Recorder) SyncDir(string) error       { return r.rec("syncdir") }
func (r *hl1Recorder) SyncFile(name string) error { return r.rec("syncfile " + filepath.Base(name)) }
func (r *hl1Recorder) ReadTail(name string, n int64) ([]byte, int64, error) {
	if err := r.rec("readtail " + filepath.Base(name)); err != nil {
		return nil, 0, err
	}
	t := r.tail
	if int64(len(t)) > n {
		t = t[int64(len(t))-n:]
	}
	return t, int64(len(r.tail)), nil
}
func (r *hl1Recorder) WriteFileDurable(_, name string, _ []byte) error {
	return r.rec("d1 " + name)
}

// hl1FakeGuard records the H8 guard calls.
type hl1FakeGuard struct {
	legacymining.Guard
	rec     *hl1Recorder
	mu      sync.Mutex
	freezes []string
}

func (g *hl1FakeGuard) Freeze(cause string) {
	g.mu.Lock()
	g.freezes = append(g.freezes, cause)
	g.mu.Unlock()
	if g.rec != nil {
		_ = g.rec.rec("freeze")
	}
}

func (g *hl1FakeGuard) ObserveSeal(h uint64, local bool) {
	if g.rec != nil {
		_ = g.rec.rec(fmt.Sprintf("observe-seal %d local=%v", h, local))
	}
}

func (g *hl1FakeGuard) Freezes() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.freezes...)
}

type hl1FakeLedger struct {
	legacymining.Ledger
	rec *hl1Recorder
}

func (l *hl1FakeLedger) OnDurableBlock(blk *chain.Block, local bool) error {
	return l.rec.rec(fmt.Sprintf("ledger %d local=%v", blk.Height, local))
}

type hl1FailStopRecord struct {
	mu     sync.Mutex
	codes  []int
	causes []string
}

func (f *hl1FailStopRecord) Stop(code int, cause string) {
	f.mu.Lock()
	f.codes = append(f.codes, code)
	f.causes = append(f.causes, cause)
	f.mu.Unlock()
}

// hl1TestHook wires a hook to the recorder. The domain steps (H0, H2-H5,
// H10) record through it too.
func hl1TestHook(rec *hl1Recorder, fsr *hl1FailStopRecord, durable *hl1DurableTip, canary bool) (*hl1PersistHook, *hl1FakeGuard) {
	dir := filepath.Join("state")
	local := new(atomic.Bool)
	local.Store(true)
	guard := &hl1FakeGuard{rec: rec}
	h := &hl1PersistHook{
		Mode:           hl1HookNormal,
		ProducerRole:   true,
		StateDir:       dir,
		JournalPath:    filepath.Join(dir, "qsdm_chain.ndjson"),
		AccountsPath:   filepath.Join(dir, "qsdm_accounts.json"),
		EnrollmentPath: filepath.Join(dir, "qsdm_enrollment.json"),
		ReceiptsPath:   filepath.Join(dir, "qsdm_receipts.ndjson"),
		FS:             rec,
		Prior:          func(*chain.Block) { _ = rec.rec("prior") },
		AppendJournal:  func(*chain.Block) error { return rec.rec("journal") },
		SaveAccounts:   func(string) error { return rec.rec("accounts") },
		SaveEnrollment: func(string) error { return rec.rec("enrollment") },
		AppendReceipts: func(string, uint64) (int, error) { return 1, rec.rec("receipts") },
		LocalSeal:      local,
		Durable:        durable,
		PublishPol: func(blk *chain.Block) {
			tip, ok := durable.Load()
			_ = rec.rec(fmt.Sprintf("pol durable=%d/%v", tip, ok))
		},
		Broadcast:        func(*chain.Block) { _ = rec.rec("broadcast") },
		FailStop:         fsr.Stop,
		OnFailStopReturn: func(error) { _ = rec.rec("failstop-returned") },
		Metrics:          &hl1MetricSet{},
		trace:            rec.trace,
	}
	if canary {
		h.Canary = true
		h.Guard = guard
		h.Ledger = &hl1FakeLedger{rec: rec}
	}
	return h, guard
}

func hl1Heartbeat(nonce uint64) *mempool.Tx {
	f := chain.MiningRewardFunderAddress
	return &mempool.Tx{ID: fmt.Sprintf("solo-heartbeat-%d", nonce), Sender: f, Recipient: f, Nonce: nonce}
}

func hl1TestBlock(height uint64, txs ...*mempool.Tx) *chain.Block {
	blk := recoveryBlock(height, fmt.Sprintf("%064x", height), "root")
	blk.Transactions = txs
	blk.Hash = chain.ComputeBlockHash(blk)
	return blk
}

// -----------------------------------------------------------------------------
// H0-H10 order
// -----------------------------------------------------------------------------

func TestHL1HookOrderNormal(t *testing.T) {
	rec := &hl1Recorder{listing: []string{
		"qsdm_accounts.json", "qsdm_accounts.json.h1", "qsdm_accounts.json.h3", "qsdm_accounts.json.h4",
		"qsdm_enrollment.json.h3", "qsdm_enrollment.json.h4", "qsdm_accounts.json.h3.bak", "other.h1",
	}}
	fsr := &hl1FailStopRecord{}
	durable := &hl1DurableTip{}
	durable.Store(4)
	h, _ := hl1TestHook(rec, fsr, durable, true)
	h.OnSealedBlock(hl1TestBlock(5, hl1Heartbeat(9)))

	want := []string{
		"H0:prior",
		"H1:lstat qsdm_accounts.json",
		"H1:remove qsdm_accounts.json.h4",
		"H1:link qsdm_accounts.json qsdm_accounts.json.h4",
		"H1:lstat qsdm_enrollment.json",
		"H1:remove qsdm_enrollment.json.h4",
		"H1:link qsdm_enrollment.json qsdm_enrollment.json.h4",
		"H1:syncdir",
		"H2:journal",
		"H3:accounts",
		"H4:enrollment",
		"H5:receipts",
		"H5:syncfile qsdm_receipts.ndjson",
		"H6:syncdir",
		"H7:d1 hl1-served-watermark.json",
		"H7:readdir",
		"H7:remove qsdm_accounts.json.h1",
		"H7:remove qsdm_accounts.json.h3",
		"H7:remove qsdm_enrollment.json.h3",
		"H8:ledger 5 local=true",
		"H8:observe-seal 5 local=true",
		"H10:pol durable=5/true",
		"H10:broadcast",
	}
	if got := rec.Ops(); !reflect.DeepEqual(got, want) {
		t.Fatalf("hook ops:\n got %q\nwant %q", got, want)
	}
	if len(fsr.codes) != 0 {
		t.Fatalf("failStop called: %v %v", fsr.codes, fsr.causes)
	}
	if tip, ok := durable.Load(); !ok || tip != 5 {
		t.Fatalf("durable tip = %d/%v, want 5", tip, ok)
	}
	if w, ok := h.Metrics.served.Load(); !ok || w != 5 {
		t.Fatalf("served watermark metric = %d/%v, want 5", w, ok)
	}
}

func TestHL1HookOrderReplay(t *testing.T) {
	blk := hl1TestBlock(5, hl1Heartbeat(9))
	line, _ := json.Marshal(blk)
	rec := &hl1Recorder{tail: append([]byte("{\"prev\":1}\n"), append(line, '\n')...)}
	fsr := &hl1FailStopRecord{}
	durable := &hl1DurableTip{}
	h, _ := hl1TestHook(rec, fsr, durable, true)
	h.Mode = hl1HookReplay
	h.AppendJournal = func(*chain.Block) error { t.Fatal("replay must not append the journal"); return nil }
	h.OnSealedBlock(blk)

	want := []string{
		"H0:prior",
		"H2:readtail qsdm_chain.ndjson",
		"H2:syncfile qsdm_chain.ndjson",
		"H3:accounts",
		"H4:enrollment",
		"H5:receipts",
		"H5:syncfile qsdm_receipts.ndjson",
		"H6:syncdir",
		"H7:d1 hl1-served-watermark.json",
		"H7:readdir",
	}
	if got := rec.Ops(); !reflect.DeepEqual(got, want) {
		t.Fatalf("replay ops:\n got %q\nwant %q", got, want)
	}
	if len(fsr.codes) != 0 {
		t.Fatalf("failStop called: %v", fsr.causes)
	}
	if _, ok := durable.Load(); ok {
		t.Fatal("replay advanced the durable tip (H9 must be skipped)")
	}
}

func TestHL1HookReplayRefusesDifferentJournalLine(t *testing.T) {
	blk := hl1TestBlock(5, hl1Heartbeat(9))
	other := hl1TestBlock(5, hl1Heartbeat(10))
	line, _ := json.Marshal(other)
	for name, tail := range map[string][]byte{
		"other block":     append(line, '\n'),
		"no newline":      mustMarshal(t, blk),
		"glued to prefix": append([]byte("x"), append(mustMarshal(t, blk), '\n')...),
	} {
		t.Run(name, func(t *testing.T) {
			rec := &hl1Recorder{tail: tail}
			fsr := &hl1FailStopRecord{}
			h, _ := hl1TestHook(rec, fsr, &hl1DurableTip{}, false)
			h.Mode = hl1HookReplay
			h.OnSealedBlock(blk)
			if len(fsr.causes) != 1 || !strings.HasPrefix(fsr.causes[0], "persist:H2:") {
				t.Fatalf("failStop causes = %q, want one persist:H2", fsr.causes)
			}
			for _, op := range rec.Ops() {
				if strings.HasPrefix(op, "H3:") || strings.Contains(op, "syncfile qsdm_chain") {
					t.Fatalf("hook continued after the H2 assertion failed: %q", rec.Ops())
				}
			}
		})
	}
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestHL1HookFaultAtEachStep(t *testing.T) {
	for k := 1; k <= 7; k++ {
		step := hl1StepNames[k]
		t.Run(step, func(t *testing.T) {
			rec := &hl1Recorder{failStep: step}
			fsr := &hl1FailStopRecord{}
			durable := &hl1DurableTip{}
			durable.Store(4)
			h, guard := hl1TestHook(rec, fsr, durable, true)
			h.Metrics.served.Store(4)
			h.OnSealedBlock(hl1TestBlock(5, hl1Heartbeat(9)))

			if len(fsr.codes) != 1 || fsr.codes[0] != legacymining.ExitFailStop {
				t.Fatalf("failStop codes = %v, want [86]", fsr.codes)
			}
			if want := legacymining.CausePersistPrefix + step + ":"; !strings.HasPrefix(fsr.causes[0], want) ||
				!strings.Contains(fsr.causes[0], errHL1Injected.Error()) {
				t.Fatalf("cause = %q, want prefix %q with the injected error", fsr.causes[0], want)
			}
			if tip, _ := durable.Load(); tip != 4 {
				t.Fatalf("durable tip advanced to %d", tip)
			}
			if w, _ := h.Metrics.served.Load(); w != 4 {
				t.Fatalf("served watermark advanced to %d", w)
			}
			ops := rec.Ops()
			for _, op := range ops {
				s, _, _ := strings.Cut(op, ":")
				if hl1StepIndex(s) > k {
					t.Fatalf("step %s ran after the %s fault: %q", s, step, ops)
				}
				if strings.Contains(op, "d1 hl1-served-watermark.json") && k != 7 {
					t.Fatalf("W written despite the %s fault: %q", step, ops)
				}
			}
			if ops[len(ops)-1] != step+":failstop-returned" {
				t.Fatalf("last op = %q, want %s:failstop-returned", ops[len(ops)-1], step)
			}
			if len(guard.Freezes()) != 0 {
				t.Fatalf("an H1-H7 fault must fail-stop, not freeze: %q", guard.Freezes())
			}
		})
	}
}

func hl1StepIndex(s string) int {
	for i, n := range hl1StepNames {
		if n == s {
			return i
		}
	}
	return -1
}

func TestHL1HookFollowerRoleDoesNotWriteWatermark(t *testing.T) {
	rec := &hl1Recorder{}
	fsr := &hl1FailStopRecord{}
	durable := &hl1DurableTip{}
	h, _ := hl1TestHook(rec, fsr, durable, false)
	h.ProducerRole = false
	h.LocalSeal.Store(false)
	h.OnSealedBlock(hl1TestBlock(5, hl1Heartbeat(9)))
	for _, op := range rec.Ops() {
		if strings.Contains(op, "hl1-served-watermark") {
			t.Fatalf("follower role wrote W: %q", rec.Ops())
		}
	}
	if tip, ok := durable.Load(); !ok || tip != 5 {
		t.Fatalf("follower durable tip = %d/%v, want 5", tip, ok)
	}
	if _, ok := h.Metrics.served.Load(); ok {
		t.Fatal("follower role set the served watermark metric")
	}
}

func TestHL1HookGenesisSkipsGenerationLinks(t *testing.T) {
	rec := &hl1Recorder{}
	h, _ := hl1TestHook(rec, &hl1FailStopRecord{}, &hl1DurableTip{}, false)
	h.OnSealedBlock(hl1TestBlock(0, hl1Heartbeat(0)))
	for _, op := range rec.Ops() {
		if strings.HasPrefix(op, "H1:") {
			t.Fatalf("genesis ran H1: %q", rec.Ops())
		}
	}
}

// -----------------------------------------------------------------------------
// H8: I7 family audit and the canary ledger
// -----------------------------------------------------------------------------

func TestHL1HookFamilyAuditMetric(t *testing.T) {
	funder := chain.MiningRewardFunderAddress
	stream := &mempool.Tx{ID: "stream-1", Sender: "a", Recipient: "b", ContractID: chain.StreamContractID}
	enroll := &mempool.Tx{ID: "enroll-1", Sender: "a", Recipient: "b", ContractID: "qsdm/enroll/v1"}
	transfer := &mempool.Tx{ID: "plain-1", Sender: "a", Recipient: "b", Amount: 1}
	ok := []*mempool.Tx{
		hl1Heartbeat(1),
		{ID: "solo-reward-2-x", Sender: funder, Recipient: "m", ContractID: chain.MiningRewardContractID, Amount: 1},
		{ID: "wt-1", Sender: "a", Recipient: "b", ContractID: chain.WalletTransferContractID, Amount: 1},
	}
	for _, tc := range []struct {
		name   string
		canary bool
		local  bool
		txs    []*mempool.Tx
		count  uint64
		freeze bool
	}{
		{"allowed families", true, true, ok, 0, false},
		{"stream, enrollment and plain transfer, canary", true, true, append(append([]*mempool.Tx{}, ok...), stream, enroll, transfer), 3, true},
		{"stream, Stage A", false, true, []*mempool.Tx{hl1Heartbeat(1), stream}, 1, false},
		{"non-local block is not audited", false, false, []*mempool.Tx{stream}, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := &hl1Recorder{}
			h, guard := hl1TestHook(rec, &hl1FailStopRecord{}, &hl1DurableTip{}, tc.canary)
			h.LocalSeal.Store(tc.local)
			h.OnSealedBlock(hl1TestBlock(5, tc.txs...))
			if got := h.Metrics.unexpected.Load(); got != tc.count {
				t.Fatalf("hl1_unexpected_tx_family_total = %d, want %d", got, tc.count)
			}
			fz := guard.Freezes()
			if tc.freeze != (len(fz) == 1) || (tc.freeze && !strings.HasPrefix(fz[0], legacymining.CauseTxFamily+":")) {
				t.Fatalf("freezes = %q, want freeze=%v", fz, tc.freeze)
			}
		})
	}
}

func TestHL1HookCanaryLedgerSeesLocalFlag(t *testing.T) {
	rec := &hl1Recorder{}
	h, _ := hl1TestHook(rec, &hl1FailStopRecord{}, &hl1DurableTip{}, true)
	h.LocalSeal.Store(false)
	h.OnSealedBlock(hl1TestBlock(7, hl1Heartbeat(1)))
	got := strings.Join(rec.Ops(), "|")
	if !strings.Contains(got, "H8:ledger 7 local=false") || !strings.Contains(got, "H8:observe-seal 7 local=false") {
		t.Fatalf("ledger and guard did not see local=false: %s", got)
	}
}

// -----------------------------------------------------------------------------
// Real files: links, W, prune
// -----------------------------------------------------------------------------

func hl1WriteFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

// hl1ReplaceFile mimics the snapshot saves: temp, then rename (a new inode).
func hl1ReplaceFile(path, data string) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(data), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func TestHL1HookRealFilesPreservesGenerationAndWritesW(t *testing.T) {
	dir := t.TempDir()
	acc := filepath.Join(dir, "qsdm_accounts.json")
	enr := filepath.Join(dir, "qsdm_enrollment.json")
	journal := filepath.Join(dir, "qsdm_chain.ndjson")
	receipts := filepath.Join(dir, "qsdm_receipts.ndjson")
	hl1WriteFile(t, acc, "accounts@4")
	hl1WriteFile(t, enr, "enrollment@4")
	hl1WriteFile(t, receipts, "r4\n")
	for _, old := range []string{"qsdm_accounts.json.h2", "qsdm_accounts.json.h3", "qsdm_enrollment.json.h3", "qsdm_accounts.json.h4"} {
		hl1WriteFile(t, filepath.Join(dir, old), "stale")
	}
	cj, err := chain.OpenChainJournal(journal, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cj.Close()
	durable := &hl1DurableTip{}
	durable.Store(4)
	local := new(atomic.Bool)
	fsr := &hl1FailStopRecord{}
	h := &hl1PersistHook{
		ProducerRole:   true,
		StateDir:       dir,
		JournalPath:    journal,
		AccountsPath:   acc,
		EnrollmentPath: enr,
		ReceiptsPath:   receipts,
		FS:             hl1OSFS{},
		AppendJournal: func(b *chain.Block) error {
			line, _ := json.Marshal(b)
			f, err := os.OpenFile(journal, os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				return err
			}
			defer f.Close()
			_, err = f.Write(append(line, '\n'))
			return err
		},
		SaveAccounts:   func(p string) error { return hl1ReplaceFile(p, "accounts@5") },
		SaveEnrollment: func(p string) error { return hl1ReplaceFile(p, "enrollment@5") },
		AppendReceipts: func(p string, _ uint64) (int, error) {
			f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				return 0, err
			}
			defer f.Close()
			_, err = f.WriteString("r5\n")
			return 1, err
		},
		LocalSeal: local,
		Durable:   durable,
		FailStop:  fsr.Stop,
		Metrics:   &hl1MetricSet{},
		Now:       func() time.Time { return time.Unix(1700000000, 0) },
	}
	blk := hl1TestBlock(5, hl1Heartbeat(9))
	h.OnSealedBlock(blk)
	if len(fsr.causes) != 0 {
		t.Fatalf("failStop: %q", fsr.causes)
	}
	for path, want := range map[string]string{
		acc:         "accounts@5",
		enr:         "enrollment@5",
		acc + ".h4": "accounts@4",
		enr + ".h4": "enrollment@4",
		receipts:    "r4\nr5\n",
		filepath.Join(dir, "qsdm_accounts.json.h2"): "",
	} {
		got, err := os.ReadFile(path)
		if want == "" {
			if !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("%s was not pruned (err=%v)", filepath.Base(path), err)
			}
			continue
		}
		if err != nil || string(got) != want {
			t.Fatalf("%s = %q (%v), want %q", filepath.Base(path), got, err, want)
		}
	}
	for _, gone := range []string{"qsdm_accounts.json.h3", "qsdm_enrollment.json.h3"} {
		if _, err := os.Lstat(filepath.Join(dir, gone)); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("%s was not pruned", gone)
		}
	}
	w, err := hl1ReadWatermark(dir)
	if err != nil {
		t.Fatal(err)
	}
	if w.Height != 5 || w.Hash != blk.Hash || w.Source != legacymining.WatermarkSourceSeal || w.WrittenNS != 1700000000*int64(time.Second) {
		t.Fatalf("W = %+v", w)
	}
	if tip, _ := durable.Load(); tip != 5 {
		t.Fatalf("durable tip = %d", tip)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, legacymining.TempPrefix+"*"))
	if len(matches) != 0 {
		t.Fatalf("D1 left temps: %v", matches)
	}

	// Replay of the same block on the same files: the journal line is the
	// last line, so H2 asserts and fsyncs; W becomes a replay W.
	h.Mode = hl1HookReplay
	h.OnSealedBlock(blk)
	if len(fsr.causes) != 0 {
		t.Fatalf("replay failStop: %q", fsr.causes)
	}
	if w, _ := hl1ReadWatermark(dir); w.Source != legacymining.WatermarkSourceReplay || w.Height != 5 {
		t.Fatalf("replay W = %+v", w)
	}
	if got, _ := os.ReadFile(acc + ".h4"); string(got) != "accounts@4" {
		t.Fatalf("replay changed the .h4 generation: %q", got)
	}
}

// -----------------------------------------------------------------------------
// S4 newline checks and the S5 matrix
// -----------------------------------------------------------------------------

func TestHL1RequireFinalNewline(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		name, data string
		exists, ok bool
	}{
		{"absent", "", false, true},
		{"empty", "", true, true},
		{"terminated", "{\"a\":1}\n{\"b\":2}\n", true, true},
		{"complete but unterminated", "{\"a\":1}\n{\"b\":2}", true, false},
		{"fragment", "{\"a\":1}\n{\"b\"", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(dir, strings.ReplaceAll(tc.name, " ", "_")+".ndjson")
			if tc.exists {
				hl1WriteFile(t, p, tc.data)
			}
			err := hl1RequireFinalNewline(hl1OSFS{}, p)
			if (err == nil) != tc.ok {
				t.Fatalf("err = %v, want ok=%v", err, tc.ok)
			}
		})
	}
}

func hl1Chain(n int) []*chain.Block {
	var out []*chain.Block
	prev := ""
	for i := 0; i < n; i++ {
		b := recoveryBlock(uint64(i), prev, "root")
		out = append(out, b)
		prev = b.Hash
	}
	return out
}

func hl1BlockAtFn(blocks []*chain.Block) func(uint64) (*chain.Block, bool) {
	return func(h uint64) (*chain.Block, bool) {
		if h < uint64(len(blocks)) {
			return blocks[h], true
		}
		return nil, false
	}
}

func hl1SeedW(t *testing.T, dir string, w legacymining.Watermark) []byte {
	t.Helper()
	data, _ := json.Marshal(w)
	data = append(data, '\n')
	hl1WriteFile(t, filepath.Join(dir, legacymining.WatermarkFile), string(data))
	return data
}

func TestHL1StartupWatermarkMatrix(t *testing.T) {
	blocks := hl1Chain(6) // tip 5
	tip := blocks[5]
	now := time.Unix(1700000000, 0)
	w := func(h uint64, hash string) legacymining.Watermark {
		return legacymining.Watermark{Version: 1, Height: h, Hash: hash, Source: "seed", WrittenNS: 1}
	}
	for _, tc := range []struct {
		name     string
		producer bool
		wfile    *legacymining.Watermark
		raw      string
		tip      *chain.Block
		wantErr  string
		wantTip  uint64
		wantSet  bool
		rewrites bool
	}{
		{name: "rule 1: W absent", producer: true, tip: tip, wantErr: "watermark missing"},
		{name: "rule 2: W invalid JSON", producer: true, raw: "{not json", tip: tip, wantErr: "watermark invalid"},
		{name: "rule 2: W unknown field", producer: true, raw: `{"version":1,"height":5,"hash":"` + tip.Hash + `","source":"seal","written_ns":1,"x":1}`, tip: tip, wantErr: "watermark invalid"},
		{name: "rule 2: W bad version", producer: true, wfile: &legacymining.Watermark{Version: 2, Height: 5, Hash: tip.Hash, Source: "seal"}, tip: tip, wantErr: "version"},
		{name: "rule 2: W bad source", producer: true, wfile: &legacymining.Watermark{Version: 1, Height: 5, Hash: tip.Hash, Source: "guess"}, tip: tip, wantErr: "source"},
		{name: "rule 3: W above tip", producer: true, wfile: ptrW(w(6, tip.Hash)), tip: tip, wantErr: "above the restored tip"},
		{name: "rule 3: W with empty chain", producer: true, wfile: ptrW(w(0, tip.Hash)), tip: nil, wantErr: "above the restored tip"},
		{name: "rule 4: hash mismatch at W", producer: true, wfile: ptrW(w(4, blocks[3].Hash)), tip: tip, wantErr: "follow R-W"},
		{name: "rule 5: tip above W+1", producer: true, wfile: ptrW(w(3, blocks[3].Hash)), tip: tip, wantErr: "follow R-H"},
		{name: "rule 6: tip = W+1 advances W", producer: true, wfile: ptrW(w(4, blocks[4].Hash)), tip: tip, wantTip: 5, wantSet: true, rewrites: true},
		{name: "tip = W", producer: true, wfile: ptrW(w(5, tip.Hash)), tip: tip, wantTip: 5, wantSet: true},
		{name: "follower ignores an invalid W", producer: false, raw: "garbage", tip: tip, wantTip: 5, wantSet: true},
		{name: "follower ignores a W above tip", producer: false, wfile: ptrW(w(9, tip.Hash)), tip: tip, wantTip: 5, wantSet: true},
		{name: "follower with empty chain", producer: false, tip: nil, wantSet: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			var before []byte
			switch {
			case tc.wfile != nil:
				before = hl1SeedW(t, dir, *tc.wfile)
			case tc.raw != "":
				before = []byte(tc.raw)
				hl1WriteFile(t, filepath.Join(dir, legacymining.WatermarkFile), tc.raw)
			}
			res, err := hl1StartupWatermark(hl1OSFS{}, dir, tc.producer, tc.tip, hl1BlockAtFn(blocks), now)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if res.DurableTipSet != tc.wantSet || res.DurableTip != tc.wantTip {
				t.Fatalf("durable tip = %d/%v, want %d/%v", res.DurableTip, res.DurableTipSet, tc.wantTip, tc.wantSet)
			}
			after, _ := os.ReadFile(filepath.Join(dir, legacymining.WatermarkFile))
			if tc.rewrites {
				got, err := hl1ParseWatermark(after)
				if err != nil || got.Height != tip.Height || got.Hash != tip.Hash || got.Source != legacymining.WatermarkSourceBoot || got.WrittenNS != now.UnixNano() || !res.Wrote {
					t.Fatalf("rule 6 W = %+v (%v), wrote=%v", got, err, res.Wrote)
				}
			} else if string(after) != string(before) {
				t.Fatalf("W changed: %q -> %q", before, after)
			}
		})
	}
}

func ptrW(w legacymining.Watermark) *legacymining.Watermark { return &w }

// -----------------------------------------------------------------------------
// Durable-tip clamp on all four surfaces (§2 (c))
// -----------------------------------------------------------------------------

func hl1ProducerWithChain(t *testing.T, n int) (*chain.BlockProducer, []*chain.Block) {
	t.Helper()
	blocks := hl1Chain(n)
	bp := chain.NewBlockProducer(mempool.New(mempool.DefaultConfig()), chain.NewAccountStore(), chain.DefaultProducerConfig())
	if err := bp.RestoreChain(append([]*chain.Block(nil), blocks...)); err != nil {
		t.Fatal(err)
	}
	return bp, blocks
}

func TestHL1DurableTipClampsAllSurfaces(t *testing.T) {
	bp, _ := hl1ProducerWithChain(t, 6) // tip 5
	rs := chain.NewReceiptStore()
	for h := uint64(0); h <= 5; h++ {
		rs.Store(&chain.TxReceipt{TxID: fmt.Sprintf("tx-%d", h), BlockHeight: h, Timestamp: time.Unix(1, 0)})
	}
	durable := &hl1DurableTip{}
	blocksProbe := blocksProbeFromProducer(bp, durable)
	provider := hl1BlockProvider(bp, durable)
	receipts := receiptsListProbeFromStore(rs, bp, durable)
	view := &durableChainView{tip: durable, producer: bp}

	// Before S5 sets the durable tip, nothing is served.
	if blocksProbe.Tip() != 0 || len(blocksProbe.HeadersInRange(0, 10)) != 0 || len(blocksProbe.BlocksInRange(0, 10)) != 0 {
		t.Fatal("blocks probe served before the durable tip was set")
	}
	if got := provider(0, 10, 64); len(got) != 0 {
		t.Fatalf("P2P provider served %d blocks before the durable tip was set", len(got))
	}
	if receipts.Tip() != 0 || len(receipts.ListByHeightRange(0, 10, 100)) != 0 {
		t.Fatal("receipts list served before the durable tip was set")
	}
	if view.HasTip() || view.TipHeight() != 0 {
		t.Fatal("durable chain view reports a tip before S5")
	}
	if _, ok := view.GetBlock(0); ok {
		t.Fatal("durable chain view served block 0 before S5")
	}

	durable.Store(3)
	maxHeight := func(hs []uint64) uint64 {
		var m uint64
		for _, h := range hs {
			if h > m {
				m = h
			}
		}
		return m
	}
	var hs []uint64
	for _, hdr := range blocksProbe.HeadersInRange(0, 10) {
		hs = append(hs, hdr.Height)
	}
	if blocksProbe.Tip() != 3 || len(hs) != 4 || maxHeight(hs) != 3 {
		t.Fatalf("blocks probe tip=%d headers=%v", blocksProbe.Tip(), hs)
	}
	raw := blocksProbe.BlocksInRange(2, 10)
	if len(raw) != 2 {
		t.Fatalf("/chain/blocks returned %d blocks for [2,10], want 2 (2..3)", len(raw))
	}
	if len(blocksProbe.BlocksInRange(4, 10)) != 0 {
		t.Fatal("/chain/blocks served a block above the durable tip")
	}
	hs = nil
	for _, b := range provider(0, 10, 64) {
		hs = append(hs, b.Height)
	}
	if len(hs) != 4 || maxHeight(hs) != 3 {
		t.Fatalf("P2P provider heights = %v", hs)
	}
	if len(provider(4, 10, 64)) != 0 {
		t.Fatal("P2P provider served above the durable tip")
	}
	hs = nil
	for _, r := range receipts.ListByHeightRange(0, 10, 100) {
		hs = append(hs, r.BlockHeight)
	}
	if receipts.Tip() != 3 || len(hs) != 4 || maxHeight(hs) != 3 {
		t.Fatalf("receipts list tip=%d heights=%v", receipts.Tip(), hs)
	}
	if !view.HasTip() || view.TipHeight() != 3 {
		t.Fatalf("durable chain view tip = %d/%v", view.TipHeight(), view.HasTip())
	}
	if _, ok := view.GetBlock(4); ok {
		t.Fatal("durable chain view served block 4 above the durable tip")
	}
	if b, ok := view.GetBlock(3); !ok || b.Height != 3 {
		t.Fatal("durable chain view did not serve block 3")
	}
	if bp.TipHeight() != 5 {
		t.Fatalf("producer tip = %d, fixture broken", bp.TipHeight())
	}
}
