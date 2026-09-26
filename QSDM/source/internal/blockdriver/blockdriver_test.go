package blockdriver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/internal/legacymining"
	"github.com/blackbeardONE/QSDM/internal/logging"
	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/producerpolicy"
)

// quietLogger is a Logger that writes to a temp file and is
// closed at test cleanup. The driver logs every block at
// Info level which would otherwise spam test output.
func quietLogger(t *testing.T) *logging.Logger {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "blockdriver-test.log")
	l := logging.NewLogger(path, true)
	t.Cleanup(func() {
		_ = l.Close()
		_ = os.Remove(path)
	})
	return l
}

// build returns a fresh BlockProducer + Mempool + Accounts
// triple suitable for driving a Driver. The producer has no
// BFT/POL gates set (mirrors the solo-mode boot path in
// cmd/qsdm/main.go) so ProduceBlock proceeds whenever the
// mempool has at least one tx.
func build(t *testing.T) (*chain.BlockProducer, *mempool.Mempool, *chain.AccountStore) {
	t.Helper()
	pool := mempool.New(mempool.DefaultConfig())
	accounts := chain.NewAccountStore()
	bp := chain.NewBlockProducer(pool, accounts, chain.DefaultProducerConfig())
	return bp, pool, accounts
}

// unexpectedFailStop fails the test if the driver fail-stops.
func unexpectedFailStop(t *testing.T) legacymining.FailStopFunc {
	return func(code int, cause string) {
		t.Errorf("unexpected FailStop(%d, %q)", code, cause)
	}
}

// validCfg returns the minimum-valid Stage A Config (no
// Ledger): every field populated, defaults filled in by New.
// Tests use FlatRewardPerBlock to keep arithmetic simple;
// schedule-driven behaviour is covered separately.
func validCfg(t *testing.T) Config {
	t.Helper()
	bp, pool, accounts := build(t)
	return Config{
		Producer:             bp,
		Pool:                 pool,
		Accounts:             accounts,
		Logger:               quietLogger(t),
		FailStop:             unexpectedFailStop(t),
		Period:               5 * time.Millisecond,
		FlatRewardPerBlock:   1.0,
		FunderInitialBalance: 1000.0,
	}
}

// ---- fakes -----------------------------------------------------------------

type failStopCall struct {
	code  int
	cause string
}

// failStopRecorder is an injected FailStop that returns, so the
// test can observe the driver after a POST-APPLY.
type failStopRecorder struct {
	mu    sync.Mutex
	calls []failStopCall
}

func (r *failStopRecorder) fn(code int, cause string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, failStopCall{code, cause})
}

func (r *failStopRecorder) snapshot() []failStopCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]failStopCall(nil), r.calls...)
}

// fakeGuard implements the two Guard methods the driver uses.
// Any other method panics through the nil embedded interface.
type fakeGuard struct {
	legacymining.Guard

	mu         sync.Mutex
	state      legacymining.State
	causes     []string
	stateCalls int
}

func newFakeGuard() *fakeGuard { return &fakeGuard{state: legacymining.StateOpen} }

func (g *fakeGuard) State() legacymining.State {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.stateCalls++
	return g.state
}

func (g *fakeGuard) Freeze(cause string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.causes = append(g.causes, cause)
	if g.state != legacymining.StateKilled {
		g.state = legacymining.StateFrozen
	}
}

func (g *fakeGuard) set(s legacymining.State) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.state = s
}

func (g *fakeGuard) freezes() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.causes...)
}

// fakeLedger is a contract-following in-memory Ledger. PreSeal
// runs the §6.3 ID, recipient, amount and sum checks; with
// permissive set it skips the Amount > 0 check so the driver's
// own backstop can be observed.
type fakeLedger struct {
	mu         sync.Mutex
	guard      *fakeGuard
	pending    map[legacymining.ProofID]string
	inflight   map[legacymining.ProofID]string
	paid       map[legacymining.ProofID]string
	bound      []*mempool.Tx
	nextID     uint64
	permissive bool
	onPreSeal  func()                                          // after a successful PreSeal, outside mu
	mutateTake func([]legacymining.Claim) []legacymining.Claim // test hook on Take's result

	takes, preSeals, requeues int
}

func newFakeLedger(g *fakeGuard) *fakeLedger {
	return &fakeLedger{
		guard:    g,
		pending:  map[legacymining.ProofID]string{},
		inflight: map[legacymining.ProofID]string{},
		paid:     map[legacymining.ProofID]string{},
	}
}

// record returns an unpaid record with a fresh, increasing ID.
func (l *fakeLedger) record(addr string) legacymining.Record {
	l.mu.Lock()
	l.nextID++
	n := l.nextID
	l.mu.Unlock()
	var id legacymining.ProofID
	binary.BigEndian.PutUint64(id[:8], n)
	id[31] = 0x5a
	return legacymining.Record{ProofID: id, MinerAddr: addr}
}

// add enqueues n fresh IDs for addr.
func (l *fakeLedger) add(t *testing.T, addr string, n int) []legacymining.ProofID {
	t.Helper()
	ids := make([]legacymining.ProofID, 0, n)
	for i := 0; i < n; i++ {
		rec := l.record(addr)
		if err := l.Enqueue(rec); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
		ids = append(ids, rec.ProofID)
	}
	return ids
}

func (l *fakeLedger) Outstanding() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.pending) + len(l.inflight)
}

func (l *fakeLedger) Enqueue(rec legacymining.Record) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	id := rec.ProofID
	if _, ok := l.pending[id]; ok {
		return errors.New("fake ledger: duplicate pending ID")
	}
	if _, ok := l.inflight[id]; ok {
		return errors.New("fake ledger: duplicate in-flight ID")
	}
	if _, ok := l.paid[id]; ok {
		return errors.New("fake ledger: duplicate paid ID")
	}
	l.pending[id] = rec.MinerAddr
	return nil
}

func (l *fakeLedger) Init(legacymining.LedgerInit) error { return nil }

func (l *fakeLedger) Totals() legacymining.Totals { return legacymining.Totals{} }

func (l *fakeLedger) Take() []legacymining.Claim {
	l.mu.Lock()
	l.takes++
	byAddr := map[string][]legacymining.ProofID{}
	for id, addr := range l.pending {
		byAddr[addr] = append(byAddr[addr], id)
		l.inflight[id] = addr
	}
	l.pending = map[legacymining.ProofID]string{}
	addrs := make([]string, 0, len(byAddr))
	for addr := range byAddr {
		addrs = append(addrs, addr)
	}
	sort.Strings(addrs)
	claims := make([]legacymining.Claim, 0, len(addrs))
	for _, addr := range addrs {
		ids := byAddr[addr]
		sort.Slice(ids, func(i, j int) bool { return bytes.Compare(ids[i][:], ids[j][:]) < 0 })
		claims = append(claims, legacymining.Claim{MinerAddr: addr, IDs: ids})
	}
	mutate := l.mutateTake
	l.mu.Unlock()
	if mutate != nil {
		claims = mutate(claims)
	}
	return claims
}

func (l *fakeLedger) PreSeal(height uint64, rewardCell float64, txs []*mempool.Tx) error {
	l.mu.Lock()
	l.preSeals++
	err := l.preSealCheckLocked(rewardCell, txs)
	if err != nil {
		l.requeueLocked()
		l.mu.Unlock()
		l.guard.Freeze(legacymining.CausePreSeal)
		return fmt.Errorf("%w: %v", legacymining.ErrPreSeal, err)
	}
	l.bound = txs
	hook := l.onPreSeal
	l.mu.Unlock()
	if hook != nil {
		hook()
	}
	return nil
}

func (l *fakeLedger) preSealCheckLocked(rewardCell float64, txs []*mempool.Tx) error {
	seen := map[legacymining.ProofID]bool{}
	sum := 0.0
	for _, tx := range txs {
		ids, err := decodeLMP1(tx.Payload)
		if err != nil {
			return err
		}
		for _, id := range ids {
			addr, ok := l.inflight[id]
			if !ok || seen[id] {
				return fmt.Errorf("ID %x not in flight or repeated", id[:8])
			}
			seen[id] = true
			if addr != tx.Recipient {
				return fmt.Errorf("recipient %s != miner %s", tx.Recipient, addr)
			}
		}
		if !l.permissive && !(tx.Amount > 0) {
			return fmt.Errorf("amount %v <= 0", tx.Amount)
		}
		sum += tx.Amount
	}
	if len(seen) != len(l.inflight) {
		return errors.New("in-flight ID missing from payloads")
	}
	if sum > rewardCell*(1+legacymining.RewardSumSlack) {
		return errors.New("sum above reward")
	}
	return nil
}

func (l *fakeLedger) Requeue() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.requeues++
	l.requeueLocked()
}

func (l *fakeLedger) requeueLocked() {
	for id, addr := range l.inflight {
		l.pending[id] = addr
	}
	l.inflight = map[legacymining.ProofID]string{}
	l.bound = nil
}

// OnDurableBlock is the H8(b) stand-in: the block's LMP1 IDs
// become paid.
func (l *fakeLedger) OnDurableBlock(blk *chain.Block, local bool) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, tx := range blk.Transactions {
		if tx == nil || tx.ContractID != chain.MiningRewardContractID {
			continue
		}
		ids, err := decodeLMP1(tx.Payload)
		if err != nil {
			return err
		}
		for _, id := range ids {
			delete(l.inflight, id)
			l.paid[id] = tx.Recipient
		}
	}
	return nil
}

type ledgerCounts struct{ pending, inflight, paid int }

func (l *fakeLedger) counts() ledgerCounts {
	l.mu.Lock()
	defer l.mu.Unlock()
	return ledgerCounts{len(l.pending), len(l.inflight), len(l.paid)}
}

func (l *fakeLedger) calls() (takes, preSeals, requeues int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.takes, l.preSeals, l.requeues
}

func (l *fakeLedger) boundTxs() []*mempool.Tx {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]*mempool.Tx(nil), l.bound...)
}

var _ legacymining.Ledger = (*fakeLedger)(nil)

// decodeLMP1 is an independent §3.4 decoder.
func decodeLMP1(p []byte) ([]legacymining.ProofID, error) {
	tag := legacymining.PayloadTag
	if len(p) < len(tag)+2 || string(p[:len(tag)]) != tag {
		return nil, errors.New("bad LMP1 tag")
	}
	n := int(binary.BigEndian.Uint16(p[len(tag):]))
	body := p[len(tag)+2:]
	if n < 1 || n > legacymining.MaxPayloadIDs || len(body) != 32*n {
		return nil, fmt.Errorf("bad LMP1 count %d for %d bytes", n, len(body))
	}
	ids := make([]legacymining.ProofID, n)
	for i := range ids {
		copy(ids[i][:], body[32*i:])
		if i > 0 && bytes.Compare(ids[i][:], ids[i-1][:]) <= 0 {
			return nil, errors.New("LMP1 IDs not strictly ascending")
		}
	}
	return ids, nil
}

// failingSigner is a block signer whose Sign always fails.
// ProduceBlock reaches SignBlock only after the live apply.
type failingSigner struct{}

func (failingSigner) Sign([]byte) ([]byte, error) { return nil, errors.New("hsm unavailable") }
func (failingSigner) GetPublicKey() []byte        { return []byte("blockdriver-test-public-key") }

// harness is a Driver on a real BlockProducer whose
// OnSealedBlock stands in for the cmd/qsdm hook: it reads the
// shared local-seal flag and runs H8(b).
type harness struct {
	d         *Driver
	bp        *chain.BlockProducer
	pool      *mempool.Mempool
	accounts  *chain.AccountStore
	ledger    *fakeLedger // nil in Stage A
	guard     *fakeGuard  // nil in Stage A
	failStop  *failStopRecorder
	localSeal *atomic.Bool

	mu          sync.Mutex
	sealedLocal []bool
}

func newHarness(t *testing.T, payouts bool, opts ...func(*Config)) *harness {
	t.Helper()
	pool := mempool.New(mempool.DefaultConfig())
	accounts := chain.NewAccountStore()
	h := &harness{failStop: &failStopRecorder{}, localSeal: new(atomic.Bool)}
	cfg := Config{
		Producer:             chain.NewBlockProducer(pool, accounts, chain.DefaultProducerConfig()),
		Pool:                 pool,
		Accounts:             accounts,
		Logger:               quietLogger(t),
		FailStop:             h.failStop.fn,
		LocalSeal:            h.localSeal,
		Period:               5 * time.Millisecond,
		FlatRewardPerBlock:   1.0,
		FunderInitialBalance: 1000.0,
	}
	if payouts {
		h.guard = newFakeGuard()
		h.ledger = newFakeLedger(h.guard)
		cfg.Ledger = h.ledger
		cfg.Guard = h.guard
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	h.bp, h.pool, h.accounts = cfg.Producer, cfg.Pool, cfg.Accounts
	h.bp.OnSealedBlock = func(blk *chain.Block) {
		local := h.localSeal.Load()
		h.mu.Lock()
		h.sealedLocal = append(h.sealedLocal, local)
		h.mu.Unlock()
		if h.ledger != nil {
			if err := h.ledger.OnDurableBlock(blk, local); err != nil {
				t.Errorf("OnDurableBlock: %v", err)
			}
		}
	}
	d, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h.d = d
	return h
}

func (h *harness) tip(t *testing.T) *chain.Block {
	t.Helper()
	blk, ok := h.bp.LatestBlock()
	if !ok || blk == nil {
		t.Fatal("no tip")
	}
	return blk
}

func (h *harness) funder(t *testing.T) *chain.Account {
	t.Helper()
	acc, ok := h.accounts.Get(FunderAddress)
	if !ok || acc == nil {
		t.Fatal("funder account missing")
	}
	return acc
}

func (h *harness) noFailStop(t *testing.T) {
	t.Helper()
	if calls := h.failStop.snapshot(); len(calls) != 0 {
		t.Fatalf("unexpected FailStop calls: %+v", calls)
	}
}

func (h *harness) noFreeze(t *testing.T) {
	t.Helper()
	if h.guard != nil {
		if causes := h.guard.freezes(); len(causes) != 0 {
			t.Fatalf("unexpected Freeze causes: %q", causes)
		}
	}
}

func assertHeartbeatOnly(t *testing.T, blk *chain.Block) {
	t.Helper()
	if len(blk.Transactions) != 1 {
		t.Fatalf("block %d has %d txs, want exactly one heartbeat", blk.Height, len(blk.Transactions))
	}
	hb := blk.Transactions[0]
	if hb.Sender != FunderAddress || hb.Recipient != FunderAddress || hb.Amount != 0 ||
		hb.ContractID != "" || hb.Payload != nil || !strings.HasPrefix(hb.ID, "solo-heartbeat-") {
		t.Fatalf("not a heartbeat: %+v", hb)
	}
}

func assertCounts(t *testing.T, l *fakeLedger, want ledgerCounts) {
	t.Helper()
	if got := l.counts(); got != want {
		t.Fatalf("ledger counts = %+v, want %+v", got, want)
	}
}

func balanceOf(h *harness, addr string) float64 {
	if acc, ok := h.accounts.Get(addr); ok && acc != nil {
		return acc.Balance
	}
	return 0
}

// ---- New: validation -----------------------------------------------------

func TestNew_RejectsMissingProducer(t *testing.T) {
	cfg := validCfg(t)
	cfg.Producer = nil
	if _, err := New(cfg); err == nil {
		t.Fatal("expected error when Producer is nil")
	}
}

func TestNew_RejectsMissingPool(t *testing.T) {
	cfg := validCfg(t)
	cfg.Pool = nil
	if _, err := New(cfg); err == nil {
		t.Fatal("expected error when Pool is nil")
	}
}

func TestNew_RejectsMissingAccounts(t *testing.T) {
	cfg := validCfg(t)
	cfg.Accounts = nil
	if _, err := New(cfg); err == nil {
		t.Fatal("expected error when Accounts is nil")
	}
}

func TestNew_RejectsMissingLogger(t *testing.T) {
	cfg := validCfg(t)
	cfg.Logger = nil
	if _, err := New(cfg); err == nil {
		t.Fatal("expected error when Logger is nil")
	}
}

// POST-APPLY must fail-stop in every mode, so the hook is
// required even without a Ledger.
func TestNew_RejectsMissingFailStop(t *testing.T) {
	cfg := validCfg(t)
	cfg.FailStop = nil
	if _, err := New(cfg); err == nil {
		t.Fatal("expected error when FailStop is nil")
	}
}

func TestNew_RejectsLedgerWithoutGuard(t *testing.T) {
	g := newFakeGuard()
	cfg := validCfg(t)
	cfg.Ledger = newFakeLedger(g)
	if _, err := New(cfg); err == nil {
		t.Fatal("expected error for a Ledger without a Guard")
	}
	cfg = validCfg(t)
	cfg.Guard = g
	if _, err := New(cfg); err == nil {
		t.Fatal("expected error for a Guard without a Ledger")
	}
}

func TestNew_FillsDefaults(t *testing.T) {
	cfg := Config{
		Producer: nilSafeBuild(t),
		Pool:     mempool.New(mempool.DefaultConfig()),
		Accounts: chain.NewAccountStore(),
		Logger:   quietLogger(t),
		FailStop: unexpectedFailStop(t),
	}
	d, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if d.cfg.Period != DefaultPeriod {
		t.Errorf("Period: got %v want %v", d.cfg.Period, DefaultPeriod)
	}
	if d.cfg.FunderInitialBalance != DefaultFunderBalance {
		t.Errorf("FunderInitialBalance: got %v want %v", d.cfg.FunderInitialBalance, DefaultFunderBalance)
	}
	if d.localSeal == nil {
		t.Error("a private local-seal flag should be allocated")
	}
	// Default schedule should match chain.DefaultEmissionSchedule().
	want := chain.DefaultEmissionSchedule()
	if d.schedule.MiningCapDust != want.MiningCapDust {
		t.Errorf("schedule.MiningCapDust: got %d want %d", d.schedule.MiningCapDust, want.MiningCapDust)
	}
	if d.schedule.BlocksPerEpoch != want.BlocksPerEpoch {
		t.Errorf("schedule.BlocksPerEpoch: got %d want %d", d.schedule.BlocksPerEpoch, want.BlocksPerEpoch)
	}
}

// TestNew_RejectsZeroValueSchedule guards against the
// classic "passed a zero EmissionSchedule by mistake" bug
// which would silently produce 0-reward forever.
func TestNew_RejectsZeroValueSchedule(t *testing.T) {
	cfg := validCfg(t)
	zero := chain.EmissionSchedule{}
	cfg.EmissionSchedule = &zero
	if _, err := New(cfg); err == nil {
		t.Fatal("expected error when EmissionSchedule has BlocksPerEpoch == 0")
	}
}

// nilSafeBuild builds a producer wired to a fresh mempool +
// account store, used by tests that only care about the
// producer reference.
func nilSafeBuild(t *testing.T) *chain.BlockProducer {
	t.Helper()
	pool := mempool.New(mempool.DefaultConfig())
	accounts := chain.NewAccountStore()
	return chain.NewBlockProducer(pool, accounts, chain.DefaultProducerConfig())
}

// ---- tick: heartbeat path ------------------------------------------------

// TestTick_HeartbeatSealsEmptyBlock confirms that an idle
// driver (Stage A, no Ledger) still seals a block per tick so
// the chain advances and metrics keep flowing. A heartbeat
// tx (funder→funder, amount=0) is the minimum payload.
func TestTick_HeartbeatSealsEmptyBlock(t *testing.T) {
	cfg := validCfg(t)
	d, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	d.tick()
	if got := d.Stats().BlocksSealed; got != 1 {
		t.Fatalf("BlocksSealed: got %d want 1 (heartbeat path)", got)
	}
	if !cfg.Producer.HasTip() {
		t.Fatal("producer should have a tip after heartbeat seal")
	}
	tip, _ := cfg.Producer.LatestBlock()
	if tip == nil {
		t.Fatal("LatestBlock returned nil")
	}
	assertHeartbeatOnly(t, tip)
}

func TestTick_NoPendingIDsSealsHeartbeat(t *testing.T) {
	h := newHarness(t, true)
	h.d.tick()
	assertHeartbeatOnly(t, h.tip(t))
	if takes, preSeals, _ := h.ledger.calls(); takes != 1 || preSeals != 0 {
		t.Fatalf("takes=%d preSeals=%d, want 1/0", takes, preSeals)
	}
	h.noFreeze(t)
	h.noFailStop(t)
}

// ---- tick: payout path ---------------------------------------------------

// TestTick_PayoutCreditsMiners is the headline test: pending
// IDs for two miners result in a sealed block whose state
// credits each miner proportional to their ID count.
func TestTick_PayoutCreditsMiners(t *testing.T) {
	h := newHarness(t, true, func(c *Config) {
		c.FlatRewardPerBlock = 4.0 // exact split: alice=3 of 4, bob=1 of 4.
	})
	h.ledger.add(t, "qsdm1alice", 3)
	h.ledger.add(t, "qsdm1bob", 1)
	h.d.tick()

	if got := h.d.Stats().BlocksSealed; got != 1 {
		t.Fatalf("BlocksSealed: got %d want 1", got)
	}
	if got := h.d.Stats().ProofsPaid; got != 4 {
		t.Fatalf("ProofsPaid: got %d want 4", got)
	}
	assertCounts(t, h.ledger, ledgerCounts{pending: 0, inflight: 0, paid: 4})
	if alice := balanceOf(h, "qsdm1alice"); alice != 3.0 {
		t.Errorf("alice balance: got %.6f want 3.0", alice)
	}
	if bob := balanceOf(h, "qsdm1bob"); bob != 1.0 {
		t.Errorf("bob balance: got %.6f want 1.0", bob)
	}
	// Funder balance dropped by exactly the reward total.
	if got, want := h.funder(t).Balance, 1000.0-4.0; got != want {
		t.Errorf("funder balance: got %.6f want %.6f", got, want)
	}
	if _, _, requeues := h.ledger.calls(); requeues != 1 {
		t.Errorf("requeues = %d, want 1 (release after SUCCESS)", requeues)
	}
	h.noFreeze(t)
	h.noFailStop(t)
}

// TestTick_QueueDrainedAfterTick ensures nothing is left
// pending or in flight after a successful tick.
func TestTick_QueueDrainedAfterTick(t *testing.T) {
	h := newHarness(t, true)
	h.ledger.add(t, "qsdm1alice", 1)
	h.d.tick()
	if got := h.d.Stats().QueueDepth; got != 0 {
		t.Fatalf("queue should be drained, got depth %d", got)
	}
}

// One tx per address per tick, carrying all of that address's
// pending IDs in an LMP1 payload, under the HL1 reward ID.
func TestTick_OneRewardTxPerAddressCarriesAllIDs(t *testing.T) {
	h := newHarness(t, true)
	alice := h.ledger.add(t, "qsdm1alice", 3)
	bob := h.ledger.add(t, "qsdm1bob", 2)
	startNonce := h.funder(t).Nonce
	h.d.tick()
	h.noFailStop(t)

	blk := h.tip(t)
	if len(blk.Transactions) != 2 {
		t.Fatalf("block has %d txs, want 2 (one per address)", len(blk.Transactions))
	}
	byRecipient := map[string]*mempool.Tx{}
	for _, tx := range blk.Transactions {
		byRecipient[tx.Recipient] = tx
	}
	for i, c := range []struct {
		addr string
		ids  []legacymining.ProofID
	}{{"qsdm1alice", alice}, {"qsdm1bob", bob}} {
		tx := byRecipient[c.addr]
		if tx == nil {
			t.Fatalf("no reward tx for %s", c.addr)
		}
		nonce := startNonce + uint64(i) // claims are address-sorted
		if tx.Nonce != nonce || tx.Sender != FunderAddress || tx.ContractID != chain.MiningRewardContractID || tx.Fee != 0 {
			t.Errorf("%s: tx fields %+v", c.addr, tx)
		}
		got, err := decodeLMP1(tx.Payload)
		if err != nil {
			t.Fatalf("%s: payload: %v", c.addr, err)
		}
		if !reflect.DeepEqual(got, c.ids) {
			t.Errorf("%s: payload IDs = %x, want %x", c.addr, got, c.ids)
		}
		if want := len(legacymining.PayloadTag) + 2 + 32*len(c.ids); len(tx.Payload) != want {
			t.Errorf("%s: payload length %d, want %d", c.addr, len(tx.Payload), want)
		}
		sum := sha256.Sum256(tx.Payload)
		wantID := fmt.Sprintf("solo-reward-%d-%s-%s", nonce, c.addr, hex.EncodeToString(sum[:8]))
		if tx.ID != wantID {
			t.Errorf("%s: ID %q, want %q", c.addr, tx.ID, wantID)
		}
	}
	assertCounts(t, h.ledger, ledgerCounts{paid: 5})
}

// ---- tick: nonce monotonicity --------------------------------------------

// TestTick_FunderNonceMonotonic ensures the funder's nonce
// advances strictly across ticks even when individual blocks
// have multiple reward txs. A double-use of the same nonce
// trips ApplyTx and produces a stuck chain.
func TestTick_FunderNonceMonotonic(t *testing.T) {
	h := newHarness(t, true)
	h.ledger.add(t, "qsdm1alice", 1)
	h.ledger.add(t, "qsdm1bob", 1)
	h.d.tick()
	// Two reward txs were issued; funder nonce should have
	// moved by 2 from its starting value of 0.
	if got := h.funder(t).Nonce; got != 2 {
		t.Errorf("funder nonce after 2 reward txs: got %d want 2", got)
	}
	// One more tick — heartbeat path — should still bump
	// the nonce by exactly 1.
	h.d.tick()
	if got := h.funder(t).Nonce; got != 3 {
		t.Errorf("funder nonce after heartbeat: got %d want 3", got)
	}
}

// ---- multi-block end-to-end ---------------------------------------------

// TestE2E_SealsManyBlocksAccumulatesBalance confirms the
// driver can advance the chain across multiple ticks with
// payouts each time, and balances accumulate.
func TestE2E_SealsManyBlocksAccumulatesBalance(t *testing.T) {
	h := newHarness(t, true, func(c *Config) { c.FlatRewardPerBlock = 0.5 })
	for i := 0; i < 5; i++ {
		h.ledger.add(t, "qsdm1charlie", 1)
		h.d.tick()
	}
	if got := h.d.Stats().BlocksSealed; got != 5 {
		t.Fatalf("BlocksSealed: got %d want 5", got)
	}
	if got := h.bp.TipHeight(); got != 4 {
		// 5 blocks at heights 0..4 → tip = 4.
		t.Fatalf("TipHeight: got %d want 4", got)
	}
	if got, want := balanceOf(h, "qsdm1charlie"), 5*0.5; got != want {
		t.Errorf("charlie balance after 5 blocks: got %.6f want %.6f", got, want)
	}
	assertCounts(t, h.ledger, ledgerCounts{paid: 5})
}

// ---- Start / Stop --------------------------------------------------------

// TestStart_TicksUntilStop spins up the goroutine, lets it
// run a few ticks, then Stops it. Confirms the goroutine
// exits cleanly and we observed at least one block sealed.
func TestStart_TicksUntilStop(t *testing.T) {
	cfg := validCfg(t)
	cfg.Period = 2 * time.Millisecond
	d, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d.Start(ctx)

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if d.Stats().BlocksSealed >= 2 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if got := d.Stats().BlocksSealed; got < 2 {
		t.Fatalf("BlocksSealed: got %d want >= 2 in 500ms", got)
	}
	d.Stop()
	// Stop should be idempotent.
	d.Stop()
}

// ---- emission schedule ---------------------------------------------------

// TestTick_UsesEmissionScheduleWhenFlatRewardZero verifies
// that the production code path (FlatRewardPerBlock = 0)
// pulls the per-block reward from the schedule. We use a
// small custom schedule (cap = 8 dust, BlocksPerEpoch = 1)
// to make the arithmetic obvious: epoch 0 allocates cap/2 = 4
// dust per block, so the miner gets 4 dust = 4e-8 CELL.
func TestTick_UsesEmissionScheduleWhenFlatRewardZero(t *testing.T) {
	// Tiny schedule: cap=8 dust, 10 s blocks, 10 s epochs ⇒
	// BlocksPerEpoch=1 ⇒ epoch 0 alloc = 4 dust = 4 dust per
	// block (only block in the epoch).
	sched, err := chain.NewEmissionSchedule(8, 10, 10)
	if err != nil {
		t.Fatalf("NewEmissionSchedule: %v", err)
	}
	h := newHarness(t, true, func(c *Config) {
		c.FlatRewardPerBlock = 0
		c.EmissionSchedule = &sched
	})
	// Seal genesis (height 0; 0 reward by spec) so the
	// next tick targets height 1 and the schedule pays out.
	h.d.tick()
	if !h.bp.HasTip() {
		t.Fatal("genesis tick did not seal a block")
	}
	h.ledger.add(t, "qsdm1solo", 1)
	h.d.tick()
	want := 4.0 / float64(chain.DustPerCell) // 4 dust as CELL
	if got := balanceOf(h, "qsdm1solo"); got != want {
		t.Errorf("schedule reward: got %.12f want %.12f", got, want)
	}
	if e := h.d.Stats().EmittedDust; e != 4 {
		t.Errorf("EmittedDust: got %d want 4", e)
	}
	if h.d.Stats().FlatReward {
		t.Error("FlatReward should be false when schedule is in use")
	}
	h.noFreeze(t)
}

// TestTick_ZeroScheduleRewardIsPreSealFailure: once the
// emission curve allocates 0 dust per block, a pending ID
// would get a share of 0. d7 sealed a heartbeat and silently
// dropped the proof; HL1 treats the share <= 0 as a pre-seal
// failure (§6.3): FREEZE, the ID stays pending, and a
// heartbeat still advances the chain.
func TestTick_ZeroScheduleRewardIsPreSealFailure(t *testing.T) {
	sched, err := chain.NewEmissionSchedule(1, 10, 10) // alloc = 0
	if err != nil {
		t.Fatalf("NewEmissionSchedule: %v", err)
	}
	h := newHarness(t, true, func(c *Config) {
		c.FlatRewardPerBlock = 0
		c.EmissionSchedule = &sched
	})
	// Seal genesis first so the next tick is at height 1.
	h.d.tick()
	h.ledger.add(t, "qsdm1cap", 1)
	h.d.tick()
	if got := balanceOf(h, "qsdm1cap"); got != 0 {
		t.Errorf("expected no credit when schedule emits 0, got %.12f", got)
	}
	if e := h.d.Stats().EmittedDust; e != 0 {
		t.Errorf("EmittedDust: got %d want 0", e)
	}
	if got := h.bp.TipHeight(); got != 1 {
		t.Fatalf("TipHeight %d, want 1 (heartbeat sealed)", got)
	}
	assertHeartbeatOnly(t, h.tip(t))
	if causes := h.guard.freezes(); len(causes) == 0 || !strings.HasPrefix(causes[0], legacymining.CausePreSeal) {
		t.Fatalf("freeze causes %q, want a pre-seal FREEZE", causes)
	}
	assertCounts(t, h.ledger, ledgerCounts{pending: 1})
	h.noFailStop(t)
}

// ---- SyncFunderNonce -----------------------------------------------------

// TestSyncFunderNonce_AbsorbsOutOfBandTx mirrors the
// production boot sequence: an out-of-band tx (the genesis-
// seal heartbeat in cmd/qsdm/main.go) consumes nonce=0 from
// the funder before the driver gets to issue its first tx.
// The driver re-reads the AccountStore at every tick, so it
// automatically absorbs the out-of-band nonce. SyncFunderNonce
// remains an explicit boot-time/operator probe and is idempotent.
func TestSyncFunderNonce_AbsorbsOutOfBandTx(t *testing.T) {
	h := newHarness(t, true)
	// Simulate an out-of-band tx that consumed funder.Nonce=0.
	// We mutate the AccountStore directly (the genesis-seal
	// path goes through ApplyTx, which has the same effect).
	h.accounts.Credit("genesis-anchor", 1.0)
	if err := h.accounts.ApplyTx(&mempool.Tx{
		ID: "oob", Sender: FunderAddress, Recipient: "genesis-anchor",
		Amount: 1.0, Nonce: 0,
	}); err != nil {
		t.Fatalf("oob tx setup: %v", err)
	}
	// The tick must pick up nonce=1 from AccountStore without a
	// manual sync and seal successfully.
	h.ledger.add(t, "qsdm1early", 1)
	h.d.tick()
	if got := h.d.Stats().BlocksFailed; got != 0 {
		t.Fatalf("automatic nonce resync: blocks failed got %d want 0", got)
	}
	if got := h.d.Stats().BlocksSealed; got != 1 {
		t.Fatalf("automatic nonce resync: blocks sealed got %d want 1", got)
	}

	// Explicit sync remains safe and reflects the post-seal nonce.
	h.d.SyncFunderNonce()
	if got, want := h.d.Stats().FunderNonce, uint64(2); got != want {
		t.Fatalf("after sync: nonce got %d want %d", got, want)
	}
	// And the next tick should continue the same nonce stream.
	h.ledger.add(t, "qsdm1latee", 1)
	h.d.tick()
	if got := h.d.Stats().BlocksSealed; got != 2 {
		t.Fatalf("post-sync tick: blocks sealed got %d want 2", got)
	}
	assertCounts(t, h.ledger, ledgerCounts{paid: 2})
}

func TestTick_RetainsProofsAndNonceAfterSealFailure(t *testing.T) {
	h := newHarness(t, true)
	h.bp.SetSealGuard(func() error { return errors.New("persistence unavailable") })
	h.ledger.add(t, "qsdm1retry", 1)
	h.d.tick()
	stats := h.d.Stats()
	if stats.BlocksFailed != 1 || stats.QueueDepth != 1 || stats.FunderNonce != 0 {
		t.Fatalf("failed tick stats = %+v, want failed=1 queue=1 nonce=0", stats)
	}
	if got := h.pool.Size(); got != 0 {
		t.Fatalf("driver transaction leaked into pool after failure: size=%d", got)
	}

	h.bp.SetSealGuard(nil)
	h.d.tick()
	stats = h.d.Stats()
	if stats.BlocksSealed != 1 || stats.QueueDepth != 0 || stats.ProofsPaid != 1 {
		t.Fatalf("retry stats = %+v, want sealed=1 queue=0 paid=1", stats)
	}
	if balanceOf(h, "qsdm1retry") <= 0 {
		t.Fatal("retained proof was not paid")
	}
	h.noFreeze(t)
	h.noFailStop(t)
}

// ---- §4.3 classification -------------------------------------------------

func TestClassifyProduce(t *testing.T) {
	fp := fingerprint{stateRoot: "root", funderExists: true, funderNonce: 7, funderBalance: math.Float64bits(10)}
	with := func(mut func(*fingerprint)) func() fingerprint {
		return func() fingerprint {
			f := fp
			mut(&f)
			return f
		}
	}
	same := with(func(*fingerprint) {})
	notCalled := func() fingerprint {
		t.Error("fingerprint re-read on SUCCESS")
		return fp
	}
	boom := errors.New("boom")
	cases := []struct {
		name  string
		blk   *chain.Block
		err   error
		after func() fingerprint
		want  produceClass
	}{
		{"success", &chain.Block{}, nil, notCalled, produceSuccess},
		{"nil block and nil error", nil, nil, notCalled, producePostApply},
		{"error, unchanged", nil, boom, same, produceSafe},
		{"known error, unchanged", nil, chain.ErrSealGuardBlocked, same, produceSafe},
		{"error, state root changed", nil, boom, with(func(f *fingerprint) { f.stateRoot = "other" }), producePostApply},
		{"error, funder nonce changed", nil, boom, with(func(f *fingerprint) { f.funderNonce++ }), producePostApply},
		{"error, funder balance bits changed", nil, boom, with(func(f *fingerprint) { f.funderBalance = math.Float64bits(math.Nextafter(10, 11)) }), producePostApply},
		{"error, funder vanished", nil, boom, with(func(f *fingerprint) { *f = fingerprint{stateRoot: f.stateRoot} }), producePostApply},
		{"known error, changed", nil, chain.ErrSealGuardBlocked, with(func(f *fingerprint) { f.funderNonce++ }), producePostApply},
	}
	for _, c := range cases {
		if got := classifyProduce(c.blk, c.err, fp, c.after); got != c.want {
			t.Errorf("%s: class %d, want %d", c.name, got, c.want)
		}
	}
}

// The pre-dust state root renders balances with %f, so a
// sub-micro change to the funder balance leaves it unchanged;
// the balance bits in the fingerprint still see it.
func TestFingerprint_SeesEveryFunderChange(t *testing.T) {
	h := newHarness(t, false)
	fp0 := h.d.fingerprint()
	if !fp0.funderExists || fp0.stateRoot == "" {
		t.Fatalf("fingerprint %+v", fp0)
	}
	h.accounts.Credit(FunderAddress, 1e-9)
	fp1 := h.d.fingerprint()
	if fp1.stateRoot != fp0.stateRoot {
		t.Logf("state root also changed (%s -> %s)", fp0.stateRoot, fp1.stateRoot)
	}
	if fp1 == fp0 || fp1.funderBalance == fp0.funderBalance {
		t.Fatalf("fingerprint missed a funder balance change: %+v -> %+v", fp0, fp1)
	}
	if err := h.accounts.ApplyTx(&mempool.Tx{ID: "n", Sender: FunderAddress, Recipient: FunderAddress, Nonce: fp1.funderNonce}); err != nil {
		t.Fatal(err)
	}
	if fp2 := h.d.fingerprint(); fp2 == fp1 || fp2.funderNonce != fp1.funderNonce+1 {
		t.Fatalf("fingerprint missed a funder nonce change: %+v -> %+v", fp1, fp2)
	}
}

func TestIsKnownSafeError(t *testing.T) {
	for _, err := range knownSafeErrors {
		if !isKnownSafeError(fmt.Errorf("%w: detail", err)) {
			t.Errorf("wrapped %v not known", err)
		}
	}
	for msg := range knownSafeMessages {
		if !isKnownSafeError(errors.New(msg)) {
			t.Errorf("%q not known", msg)
		}
		if isKnownSafeError(fmt.Errorf("wrapped: %s", msg)) {
			t.Errorf("a wrapped %q must not match: the strings are exact", msg)
		}
	}
	if isKnownSafeError(errors.New("chain: sign produced block: x")) {
		t.Error("a signing error is not a known SAFE error")
	}
}

// The string-matched SAFE errors are fmt.Errorf literals in
// pkg/chain. Pin them to the source so a reword there cannot
// silently turn them into unknown errors.
func TestKnownSafeMessagesMatchChainSource(t *testing.T) {
	var src []byte
	for _, name := range []string{"block.go", "producer_transition.go"} {
		b, err := os.ReadFile(filepath.Join("..", "..", "pkg", "chain", name))
		if err != nil {
			t.Fatalf("read pkg/chain/%s: %v", name, err)
		}
		src = append(src, b...)
	}
	for msg := range knownSafeMessages {
		if !bytes.Contains(src, []byte(`"`+msg+`"`)) {
			t.Errorf("pkg/chain no longer returns %q", msg)
		}
	}
}

// TestTick_FailingSignerIsPostApply: SignBlock fails after the
// live apply, so the fingerprint moved. The driver must call
// FailStop(86), leave its txs in the pool and its IDs in flight,
// not resync the nonce, and never tick again.
func TestTick_FailingSignerIsPostApply(t *testing.T) {
	for _, payouts := range []bool{true, false} {
		t.Run(fmt.Sprintf("payouts=%v", payouts), func(t *testing.T) {
			h := newHarness(t, payouts)
			h.bp.SetBlockSigner(failingSigner{})
			if payouts {
				h.ledger.add(t, "qsdm1alice", 2)
			}
			nonceBefore := h.funder(t).Nonce
			h.d.tick()

			calls := h.failStop.snapshot()
			if len(calls) != 1 {
				t.Fatalf("FailStop calls = %+v, want exactly 1", calls)
			}
			if calls[0].code != 86 {
				t.Errorf("FailStop code %d, want 86", calls[0].code)
			}
			if !strings.HasPrefix(calls[0].cause, legacymining.CausePostApplyPrefix) || !strings.Contains(calls[0].cause, "hsm unavailable") {
				t.Errorf("FailStop cause %q", calls[0].cause)
			}
			// The live state moved (that is what makes it POST-APPLY) ...
			if got := h.funder(t).Nonce; got != nonceBefore+1 {
				t.Fatalf("funder nonce %d, want %d after the live apply", got, nonceBefore+1)
			}
			// ... but the driver neither resynced nor sealed.
			if got := h.d.Stats().FunderNonce; got != nonceBefore {
				t.Errorf("driver nonce %d, want %d (no SyncFunderNonce after POST-APPLY)", got, nonceBefore)
			}
			if h.bp.HasTip() {
				t.Error("a block was appended")
			}
			// No Pool.Remove: ProduceBlock restored the batch and it stays.
			if got := h.pool.Size(); got != 1 {
				t.Errorf("pool size %d, want 1 (own tx not removed)", got)
			}
			if payouts {
				bound := h.ledger.boundTxs()
				if len(bound) != 1 {
					t.Fatalf("bound txs %d, want 1", len(bound))
				}
				if _, ok := h.pool.Get(bound[0].ID); !ok {
					t.Error("reward tx was removed from the pool")
				}
				assertCounts(t, h.ledger, ledgerCounts{inflight: 2})
				if _, _, requeues := h.ledger.calls(); requeues != 0 {
					t.Errorf("requeues = %d, want 0 (no release after POST-APPLY)", requeues)
				}
				h.noFreeze(t)
			}
			if h.localSeal.Load() {
				t.Error("localSeal left set")
			}
			if !h.d.Stats().Halted {
				t.Error("driver not halted")
			}

			// No further tick: nothing is taken, added or sealed.
			h.d.tick()
			if got := len(h.failStop.snapshot()); got != 1 {
				t.Errorf("FailStop calls %d after a further tick, want 1", got)
			}
			if got := h.pool.Size(); got != 1 {
				t.Errorf("pool size %d after a further tick, want 1", got)
			}
			if payouts {
				if takes, _, _ := h.ledger.calls(); takes != 1 {
					t.Errorf("takes = %d after a further tick, want 1", takes)
				}
			}
		})
	}
}

// After POST-APPLY the run loop exits by itself.
func TestRun_PostApplyStopsLoop(t *testing.T) {
	h := newHarness(t, true, func(c *Config) { c.Period = 2 * time.Millisecond })
	h.bp.SetBlockSigner(failingSigner{})
	h.ledger.add(t, "qsdm1alice", 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.d.Start(ctx)
	select {
	case <-h.d.doneCh:
	case <-time.After(10 * time.Second):
		h.d.Stop()
		t.Fatal("run loop still ticking after POST-APPLY")
	}
	h.d.Stop()
	if got := len(h.failStop.snapshot()); got != 1 {
		t.Fatalf("FailStop calls %d, want 1", got)
	}
	if takes, _, _ := h.ledger.calls(); takes != 1 {
		t.Fatalf("takes = %d, want 1", takes)
	}
}

// Known SAFE errors: the own txs leave the pool, the IDs return
// to pending, the nonce is resynced, and nothing freezes or
// fail-stops. The seal guard, a bad nonce and an empty pool are
// the §7 cases; the transition refusal is another known error.
func TestTick_SafeErrorsReleaseWithoutFreeze(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, h *harness)
		clear func(h *harness) // nil: no retry
		errIs string
	}{
		{
			name: "seal guard",
			setup: func(t *testing.T, h *harness) {
				h.bp.SetSealGuard(func() error { return errors.New("persistence unavailable") })
			},
			clear: func(h *harness) { h.bp.SetSealGuard(nil) },
		},
		{
			// An out-of-band funder tx lands after the build and
			// before fp0, so the reward nonce is stale and every
			// tx fails state application.
			name: "bad nonce",
			setup: func(t *testing.T, h *harness) {
				h.accounts.Credit("qsdm1sink", 0)
				h.ledger.onPreSeal = func() {
					acc, _ := h.accounts.Get(FunderAddress)
					if err := h.accounts.ApplyTx(&mempool.Tx{ID: "oob", Sender: FunderAddress, Recipient: "qsdm1sink", Amount: 1, Nonce: acc.Nonce}); err != nil {
						t.Errorf("oob tx: %v", err)
					}
				}
			},
			clear: func(h *harness) { h.ledger.onPreSeal = nil },
		},
		{
			name: "empty pool",
			setup: func(t *testing.T, h *harness) {
				h.bp.SetSealGuard(func() error {
					h.pool.Drain(1 << 20)
					return nil
				})
			},
			clear: func(h *harness) { h.bp.SetSealGuard(nil) },
		},
		{
			name: "transition checkpoint",
			setup: func(t *testing.T, h *harness) {
				hx := func(b string) string { return strings.Repeat(b, 32) }
				if err := h.bp.SetProducerTransition(&producerpolicy.Transition{
					Version: 1, CheckpointHeight: 2, CheckpointHash: hx("ab"), HistoricalSignatureHeight: 1,
					HistoricalProducer: hx("11"), ReplacementProducer: hx("22"), EffectiveHeight: 3,
					HistoricalPrefixBytes: 1, HistoricalPrefixSHA256: hx("cd"),
				}); err != nil {
					t.Fatalf("SetProducerTransition: %v", err)
				}
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t, true)
			h.ledger.add(t, "qsdm1alice", 2)
			c.setup(t, h)
			h.d.tick()

			h.noFailStop(t)
			h.noFreeze(t)
			if h.bp.HasTip() {
				t.Fatal("a block was sealed")
			}
			if got := h.pool.Size(); got != 0 {
				t.Fatalf("pool size %d, want 0 (own txs removed)", got)
			}
			assertCounts(t, h.ledger, ledgerCounts{pending: 2})
			if _, _, requeues := h.ledger.calls(); requeues != 1 {
				t.Fatalf("requeues = %d, want 1", requeues)
			}
			if got, want := h.d.Stats().FunderNonce, h.funder(t).Nonce; got != want {
				t.Fatalf("driver nonce %d, want %d (resynced)", got, want)
			}
			if got := h.d.Stats().BlocksFailed; got != 1 {
				t.Fatalf("BlocksFailed %d, want 1", got)
			}
			if c.clear == nil {
				return
			}
			c.clear(h)
			h.d.tick()
			assertCounts(t, h.ledger, ledgerCounts{paid: 2})
			if balanceOf(h, "qsdm1alice") != 1.0 {
				t.Fatalf("retry did not pay alice: %v", balanceOf(h, "qsdm1alice"))
			}
			h.noFailStop(t)
			h.noFreeze(t)
		})
	}
}

// An unknown error with an unchanged fingerprint is SAFE (the
// IDs are released, no fail-stop) but trips FREEZE.
func TestTick_UnknownSafeErrorFreezes(t *testing.T) {
	for _, payouts := range []bool{true, false} {
		t.Run(fmt.Sprintf("payouts=%v", payouts), func(t *testing.T) {
			h := newHarness(t, payouts)
			// The pre-seal round runs on a clone, before the live apply.
			h.bp.SetPreSealBFTRound(func(*chain.Block) error { return errors.New("synthetic round exploded") })
			if payouts {
				h.ledger.add(t, "qsdm1alice", 2)
			}
			h.d.tick()
			h.noFailStop(t)
			if h.bp.HasTip() {
				t.Fatal("a block was sealed")
			}
			if got := h.pool.Size(); got != 0 {
				t.Fatalf("pool size %d, want 0", got)
			}
			if got := h.d.Stats().BlocksFailed; got != 1 {
				t.Fatalf("BlocksFailed %d, want 1", got)
			}
			if !payouts {
				return
			}
			causes := h.guard.freezes()
			if len(causes) != 1 || !strings.HasPrefix(causes[0], legacymining.CauseUnknownSafeError+":") ||
				!strings.Contains(causes[0], "synthetic round exploded") {
				t.Fatalf("freeze causes %q, want one unknown-safe-error", causes)
			}
			assertCounts(t, h.ledger, ledgerCounts{pending: 2})
		})
	}
}

// ---- pre-seal (§6.3) -----------------------------------------------------

// A penalty multiplier of 0 gives a share of 0. That is a
// pre-seal failure: FREEZE, every ID stays pending, no reward
// tx is sent, and a heartbeat is sealed instead. The second
// case uses a ledger whose PreSeal does not check amounts, so
// the driver's own backstop is what trips FREEZE.
func TestTick_ZeroShareTripsFreeze(t *testing.T) {
	for _, permissive := range []bool{false, true} {
		t.Run(fmt.Sprintf("permissiveLedger=%v", permissive), func(t *testing.T) {
			h := newHarness(t, true, func(c *Config) {
				c.RewardPenalty = &fakeRewardPenalty{multipliers: map[string]float64{"qsdm1alice": 0}}
			})
			h.ledger.permissive = permissive
			h.ledger.add(t, "qsdm1alice", 2)
			h.ledger.add(t, "qsdm1bob", 1)
			h.d.tick()

			causes := h.guard.freezes()
			if len(causes) == 0 || !strings.HasPrefix(causes[0], legacymining.CausePreSeal) {
				t.Fatalf("freeze causes %q, want pre-seal first", causes)
			}
			assertCounts(t, h.ledger, ledgerCounts{pending: 3})
			assertHeartbeatOnly(t, h.tip(t))
			if got := h.pool.Size(); got != 0 {
				t.Fatalf("pool size %d, want 0", got)
			}
			if balanceOf(h, "qsdm1bob") != 0 || balanceOf(h, "qsdm1alice") != 0 {
				t.Fatal("a miner was paid despite the pre-seal failure")
			}
			if got := h.d.Stats().PenalisedPayouts; got != 0 {
				t.Errorf("PenalisedPayouts %d, want 0", got)
			}
			h.noFailStop(t)

			// FROZEN now: heartbeat only, nothing taken.
			takes, _, _ := h.ledger.calls()
			h.d.tick()
			if again, _, _ := h.ledger.calls(); again != takes {
				t.Fatalf("takes %d -> %d while FROZEN", takes, again)
			}
			assertHeartbeatOnly(t, h.tip(t))
			assertCounts(t, h.ledger, ledgerCounts{pending: 3})
		})
	}
}

// A malformed Take result (here: two claims for one address)
// is a pre-seal failure without ever reaching PreSeal.
func TestTick_MalformedClaimsFailPreSeal(t *testing.T) {
	h := newHarness(t, true)
	h.ledger.add(t, "qsdm1alice", 2)
	h.ledger.mutateTake = func(c []legacymining.Claim) []legacymining.Claim {
		return []legacymining.Claim{{MinerAddr: "qsdm1alice", IDs: c[0].IDs[:1]}, {MinerAddr: "qsdm1alice", IDs: c[0].IDs[1:]}}
	}
	h.d.tick()
	if _, preSeals, _ := h.ledger.calls(); preSeals != 0 {
		t.Fatalf("preSeals = %d, want 0", preSeals)
	}
	if causes := h.guard.freezes(); len(causes) != 1 || !strings.HasPrefix(causes[0], legacymining.CausePreSeal+":") {
		t.Fatalf("freeze causes %q", causes)
	}
	assertCounts(t, h.ledger, ledgerCounts{pending: 2})
	assertHeartbeatOnly(t, h.tip(t))
	h.noFailStop(t)
}

func TestBuildTxs_RejectsMalformedClaims(t *testing.T) {
	h := newHarness(t, true)
	id := func(b byte) legacymining.ProofID { return legacymining.ProofID{b} }
	many := make([]legacymining.ProofID, legacymining.MaxPayloadIDs+1)
	for i := range many {
		binary.BigEndian.PutUint16(many[i][:], uint16(i+1))
	}
	cases := []struct {
		name    string
		claims  []legacymining.Claim
		payload bool
	}{
		{"empty address", []legacymining.Claim{{MinerAddr: "", IDs: []legacymining.ProofID{id(1)}}}, false},
		{"duplicate address", []legacymining.Claim{{MinerAddr: "a", IDs: []legacymining.ProofID{id(1)}}, {MinerAddr: "a", IDs: []legacymining.ProofID{id(2)}}}, false},
		{"unsorted addresses", []legacymining.Claim{{MinerAddr: "b", IDs: []legacymining.ProofID{id(1)}}, {MinerAddr: "a", IDs: []legacymining.ProofID{id(2)}}}, false},
		{"no IDs", []legacymining.Claim{{MinerAddr: "a"}}, true},
		{"too many IDs", []legacymining.Claim{{MinerAddr: "a", IDs: many}}, true},
		{"unsorted IDs", []legacymining.Claim{{MinerAddr: "a", IDs: []legacymining.ProofID{id(2), id(1)}}}, true},
		{"duplicate IDs", []legacymining.Claim{{MinerAddr: "a", IDs: []legacymining.ProofID{id(1), id(1)}}}, true},
	}
	for _, c := range cases {
		txs, err := h.d.buildTxs(c.claims, 1.0, 0)
		if err == nil || txs != nil {
			t.Errorf("%s: got %d txs, err %v; want an error", c.name, len(txs), err)
			continue
		}
		if !errors.Is(err, legacymining.ErrPreSeal) || errors.Is(err, legacymining.ErrBadPayload) != c.payload {
			t.Errorf("%s: error %v has the wrong class", c.name, err)
		}
	}
}

func TestEncodePayload(t *testing.T) {
	ids := make([]legacymining.ProofID, legacymining.MaxPayloadIDs)
	for i := range ids {
		binary.BigEndian.PutUint16(ids[i][:], uint16(i+1))
		ids[i][31] = byte(i)
	}
	for _, n := range []int{1, 2, legacymining.MaxPayloadIDs} {
		p, err := encodePayload(ids[:n])
		if err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		want := append([]byte(legacymining.PayloadTag), byte(n>>8), byte(n))
		for _, id := range ids[:n] {
			want = append(want, id[:]...)
		}
		if !bytes.Equal(p, want) {
			t.Fatalf("n=%d: payload bytes differ from the §3.4 layout", n)
		}
		if got, err := decodeLMP1(p); err != nil || !reflect.DeepEqual(got, ids[:n]) {
			t.Fatalf("n=%d: round trip: %v", n, err)
		}
	}
	if p, _ := encodePayload(ids); len(p) != legacymining.MaxPayloadBytes {
		t.Fatalf("max payload %d bytes, want %d", len(p), legacymining.MaxPayloadBytes)
	}
	bad := [][]legacymining.ProofID{
		nil,
		append(append([]legacymining.ProofID(nil), ids...), legacymining.ProofID{0xff}),
		{ids[1], ids[0]},
		{ids[0], ids[0]},
	}
	for i, b := range bad {
		if _, err := encodePayload(b); !errors.Is(err, legacymining.ErrBadPayload) {
			t.Errorf("bad[%d]: error %v, want ErrBadPayload", i, err)
		}
	}
}

// ---- guard states (§6.5) -------------------------------------------------

func TestTick_FrozenOrKilledSealsHeartbeatOnly(t *testing.T) {
	for _, st := range []legacymining.State{legacymining.StateFrozen, legacymining.StateKilled} {
		t.Run(st.String(), func(t *testing.T) {
			h := newHarness(t, true)
			h.ledger.add(t, "qsdm1alice", 2)
			h.guard.set(st)
			h.d.tick()
			h.d.tick()
			if takes, preSeals, _ := h.ledger.calls(); takes != 0 || preSeals != 0 {
				t.Fatalf("takes=%d preSeals=%d while %s", takes, preSeals, st)
			}
			if got := h.d.Stats().BlocksSealed; got != 2 {
				t.Fatalf("BlocksSealed %d, want 2", got)
			}
			assertHeartbeatOnly(t, h.tip(t))
			assertCounts(t, h.ledger, ledgerCounts{pending: 2})
			if balanceOf(h, "qsdm1alice") != 0 {
				t.Fatal("paid while payouts are disabled")
			}
			h.noFailStop(t)
		})
	}
}

func TestTick_AdmissionStoppedStillPays(t *testing.T) {
	h := newHarness(t, true)
	h.guard.set(legacymining.StateAdmissionStopped)
	h.ledger.add(t, "qsdm1alice", 2)
	h.d.tick()
	assertCounts(t, h.ledger, ledgerCounts{paid: 2})
	if balanceOf(h, "qsdm1alice") != 1.0 {
		t.Fatal("alice not paid while ADMISSION_STOPPED")
	}
}

// ---- pool ------------------------------------------------------------------

// A SUCCESS block that does not include an own reward tx: the
// tx must leave the pool before its IDs are released, or it
// could be sealed later next to its replacement (d7 requeued
// without Pool.Remove).
func TestTick_NonIncludedOwnTxRemovedAndReleased(t *testing.T) {
	h := newHarness(t, true, func(c *Config) {
		c.Producer = chain.NewBlockProducer(c.Pool, c.Accounts, chain.ProducerConfig{MaxTxPerBlock: 1, ProducerID: "node-0"})
	})
	h.accounts.Credit("qsdm1payer", 10)
	if err := h.pool.Add(&mempool.Tx{ID: "foreign-1", Sender: "qsdm1payer", Recipient: "qsdm1payee", Amount: 1, Fee: 0.5}); err != nil {
		t.Fatalf("foreign tx: %v", err)
	}
	h.ledger.add(t, "qsdm1alice", 2)
	h.d.tick()

	blk := h.tip(t)
	if len(blk.Transactions) != 1 || blk.Transactions[0].ID != "foreign-1" {
		t.Fatalf("block txs = %+v, want only the higher-fee foreign tx", blk.Transactions)
	}
	if got := h.pool.Size(); got != 0 {
		t.Fatalf("pool size %d, want 0: the non-included reward tx must be removed", got)
	}
	assertCounts(t, h.ledger, ledgerCounts{pending: 2})
	if balanceOf(h, "qsdm1alice") != 0 {
		t.Fatal("alice paid by a block that did not include her tx")
	}
	if s := h.d.Stats(); s.BlocksSealed != 1 || s.ProofsPaid != 0 || s.EmittedDust != 0 {
		t.Fatalf("stats %+v", s)
	}
	h.noFreeze(t)
	h.noFailStop(t)

	// The released IDs are paid by the next tick, exactly once.
	h.d.tick()
	assertCounts(t, h.ledger, ledgerCounts{paid: 2})
	if balanceOf(h, "qsdm1alice") != 1.0 {
		t.Fatalf("alice balance %v, want 1.0", balanceOf(h, "qsdm1alice"))
	}
	if got := h.pool.Size(); got != 0 {
		t.Fatalf("pool size %d, want 0", got)
	}
}

func TestTick_PoolAddFailureReleases(t *testing.T) {
	h := newHarness(t, true)
	h.pool.SetAdmissionChecker(func(tx *mempool.Tx) error {
		if tx.Recipient == "qsdm1bob" {
			return errors.New("admission refused")
		}
		return nil
	})
	h.ledger.add(t, "qsdm1alice", 1)
	h.ledger.add(t, "qsdm1bob", 1)
	h.d.tick()
	if h.bp.HasTip() {
		t.Fatal("sealed after a Pool.Add failure")
	}
	if got := h.pool.Size(); got != 0 {
		t.Fatalf("pool size %d, want 0 (alice's admitted tx removed)", got)
	}
	assertCounts(t, h.ledger, ledgerCounts{pending: 2})
	if _, _, requeues := h.ledger.calls(); requeues != 1 {
		t.Fatalf("requeues = %d, want 1", requeues)
	}
	if got := h.d.Stats().BlocksFailed; got != 1 {
		t.Fatalf("BlocksFailed %d, want 1", got)
	}
	h.noFreeze(t)
	h.noFailStop(t)
}

// ---- bit identity with d7 --------------------------------------------------

// d7BuildTxs is d7 internal/blockdriver/blockdriver.go:670-742
// (buildTxs), verbatim except that the receiver's penalty and
// metric counters became a parameter and return values and
// time.Now() became a parameter.
func d7BuildTxs(penalty RewardPenalty, queue map[string]int, total int, rewardCell float64, startNonce uint64, now time.Time) (txs []*mempool.Tx, penalised, withheldDust uint64) {
	nextNonce := startNonce
	if total == 0 || len(queue) == 0 || rewardCell <= 0 {
		return []*mempool.Tx{{
			ID:        fmt.Sprintf("solo-heartbeat-%d-%d", nextNonce, now.UnixNano()),
			Sender:    FunderAddress,
			Recipient: FunderAddress,
			Amount:    0,
			Fee:       0,
			Nonce:     nextNonce,
			AddedAt:   now,
		}}, 0, 0
	}

	out := make([]*mempool.Tx, 0, len(queue))
	addresses := make([]string, 0, len(queue))
	for addr := range queue {
		addresses = append(addresses, addr)
	}
	sort.Strings(addresses)
	for _, addr := range addresses {
		count := queue[addr]
		baseShare := rewardCell * float64(count) / float64(total)
		mult := penalty.MultiplierFor(addr)
		if !(mult >= 0 && mult <= 1) {
			mult = 1.0
		}
		share := baseShare * mult
		if share <= 0 {
			continue
		}
		if mult < 1.0 {
			penalised++
			withheld := uint64((baseShare - share) * float64(chain.DustPerCell))
			if withheld > 0 {
				withheldDust += withheld
			}
		}
		out = append(out, &mempool.Tx{
			ID:         fmt.Sprintf("solo-reward-%d-%s", nextNonce, addr),
			Sender:     FunderAddress,
			Recipient:  addr,
			Amount:     share,
			Fee:        0,
			Nonce:      nextNonce,
			ContractID: chain.MiningRewardContractID,
			AddedAt:    now,
		})
		nextNonce++
	}
	if len(out) == 0 {
		out = append(out, &mempool.Tx{
			ID:        fmt.Sprintf("solo-heartbeat-%d-%d", nextNonce, now.UnixNano()),
			Sender:    FunderAddress,
			Recipient: FunderAddress,
			Amount:    0,
			Fee:       0,
			Nonce:     nextNonce,
			AddedAt:   now,
		})
	}
	return out, penalised, withheldDust
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Amounts (Float64bits), every d7 field and the Tier-3 metrics
// are identical to d7 across a matrix of reward cells, ID
// counts and penalties. HL1 only adds the payload and the
// payload-hash suffix of the ID.
func TestBuildTxs_BitIdenticalToD7(t *testing.T) {
	fixed := time.Unix(1790000000, 123456789).UTC()
	a, b, c := strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64)

	sched := chain.DefaultEmissionSchedule()
	cells := []float64{3.56490987, 1.0, 4.0, 0.5, 0.1, 1.0 / 3, 1e-8, 123456.789}
	for _, height := range []uint64{1, 659977, sched.BlocksPerEpoch, sched.BlocksPerEpoch + 1, 5*sched.BlocksPerEpoch + 7} {
		cells = append(cells, float64(sched.BlockRewardDust(height))/float64(chain.DustPerCell))
	}
	queues := []map[string]int{
		{a: 1},
		{a: 3, b: 1},
		{a: 2, b: 2},
		{a: 7, b: 5, c: 3},
		{a: 1023, b: 1},
		{a: 1024},
		{a: 600},
		{a: 1, b: 1, c: 1},
	}
	penalties := []RewardPenalty{
		noopRewardPenalty{},
		&fakeRewardPenalty{multipliers: map[string]float64{a: 0.5}},
		&fakeRewardPenalty{multipliers: map[string]float64{b: 0.3}},
		&fakeRewardPenalty{multipliers: map[string]float64{a: 1.0 / 3, c: 0.999}},
		&degenerateMultiplier{value: nanFloat()},
		&degenerateMultiplier{value: -1},
		&degenerateMultiplier{value: 2},
	}

	cases := 0
	for _, cell := range cells {
		for qi, queue := range queues {
			total := 0
			addrs := make([]string, 0, len(queue))
			for addr, n := range queue {
				total += n
				addrs = append(addrs, addr)
			}
			sort.Strings(addrs)
			claims := make([]legacymining.Claim, 0, len(addrs))
			for ai, addr := range addrs {
				ids := make([]legacymining.ProofID, queue[addr])
				for i := range ids {
					ids[i][0] = byte(ai)
					binary.BigEndian.PutUint16(ids[i][30:], uint16(i))
				}
				claims = append(claims, legacymining.Claim{MinerAddr: addr, IDs: ids})
			}
			for pi, pen := range penalties {
				name := fmt.Sprintf("cell=%v/queue=%d/penalty=%d", cell, qi, pi)
				const startNonce = 41
				want, wantPen, wantWithheld := d7BuildTxs(pen, queue, total, cell, startNonce, fixed)

				d := &Driver{rewardPenalty: pen, now: func() time.Time { return fixed }}
				got, err := d.buildTxs(claims, cell, startNonce)
				if err != nil {
					t.Fatalf("%s: %v", name, err)
				}
				if len(got) != len(want) {
					t.Fatalf("%s: %d txs, d7 %d", name, len(got), len(want))
				}
				for i := range got {
					g, w := got[i], want[i]
					if math.Float64bits(g.Amount) != math.Float64bits(w.Amount) {
						t.Fatalf("%s tx %d: amount %v (%#x), d7 %v (%#x)", name, i, g.Amount, math.Float64bits(g.Amount), w.Amount, math.Float64bits(w.Amount))
					}
					sum := sha256.Sum256(g.Payload)
					if g.ID != w.ID+"-"+hex.EncodeToString(sum[:8]) {
						t.Fatalf("%s tx %d: ID %q, d7 %q", name, i, g.ID, w.ID)
					}
					ids, err := decodeLMP1(g.Payload)
					if err != nil || !reflect.DeepEqual(ids, claims[i].IDs) {
						t.Fatalf("%s tx %d: payload does not carry the claim: %v", name, i, err)
					}
					// Every other byte of the tx is d7's.
					shape := *g
					shape.ID, shape.Payload = w.ID, nil
					if !reflect.DeepEqual(&shape, w) || !bytes.Equal(mustJSON(t, &shape), mustJSON(t, w)) {
						t.Fatalf("%s tx %d: shape differs from d7:\n got %+v\nd7 %+v", name, i, shape, *w)
					}
				}
				if d.penalisedPayouts.Load() != wantPen || d.withheldDust.Load() != wantWithheld {
					t.Fatalf("%s: metrics %d/%d, d7 %d/%d", name, d.penalisedPayouts.Load(), d.withheldDust.Load(), wantPen, wantWithheld)
				}
				cases++
			}
		}
	}
	if cases < 100 {
		t.Fatalf("only %d cases ran", cases)
	}
}

// Heartbeat bytes are identical to d7 (blockdriver.go:674-682
// and :731-739), both as built and as sealed.
func TestHeartbeat_BytesIdenticalToD7(t *testing.T) {
	fixed := time.Unix(1790000000, 987654321).UTC()
	for _, nonce := range []uint64{0, 1, 659976, math.MaxUint64 - 1} {
		want, _, _ := d7BuildTxs(noopRewardPenalty{}, nil, 0, 1.0, nonce, fixed)
		d := &Driver{rewardPenalty: noopRewardPenalty{}, now: func() time.Time { return fixed }}
		for _, got := range []*mempool.Tx{d.heartbeatTx(nonce), mustBuild(t, d, nonce)} {
			if !reflect.DeepEqual(got, want[0]) || !bytes.Equal(mustJSON(t, got), mustJSON(t, want[0])) {
				t.Fatalf("nonce %d: heartbeat %+v, d7 %+v", nonce, *got, *want[0])
			}
		}
	}

	// Sealed through a real tick, in Stage A and with payouts
	// wired but nothing pending.
	for _, payouts := range []bool{false, true} {
		h := newHarness(t, payouts)
		h.d.now = func() time.Time { return fixed }
		h.d.tick()
		blk := h.tip(t)
		want, _, _ := d7BuildTxs(noopRewardPenalty{}, nil, 0, 1.0, 0, fixed)
		if len(blk.Transactions) != 1 || !bytes.Equal(mustJSON(t, blk.Transactions[0]), mustJSON(t, want[0])) {
			t.Fatalf("payouts=%v: sealed heartbeat %s, d7 %s", payouts, mustJSON(t, blk.Transactions), mustJSON(t, want[0]))
		}
	}
}

func mustBuild(t *testing.T, d *Driver, nonce uint64) *mempool.Tx {
	t.Helper()
	txs, err := d.buildTxs(nil, 1.0, nonce)
	if err != nil || len(txs) != 1 {
		t.Fatalf("buildTxs(nil): %d txs, %v", len(txs), err)
	}
	return txs[0]
}

// ---- locks (§4.5 L1) -------------------------------------------------------

// No lock is held across Pool.Add or ProduceBlock: while the
// tick is inside each, a submit (Sink.Enqueue), a metrics
// scrape (Stats) and a guard read complete on another
// goroutine. The local-seal flag is set exactly for the
// duration of ProduceBlock, and an ID enqueued mid-tick is
// neither lost nor paid early.
func TestTick_NoLockHeldAcrossPoolAddOrProduceBlock(t *testing.T) {
	h := newHarness(t, true)
	h.ledger.add(t, "qsdm1alice", 2)
	var probes atomic.Int32
	probe := func(stage string) {
		done := make(chan error, 1)
		go func() {
			if err := h.ledger.Enqueue(h.ledger.record("qsdm1bob")); err != nil {
				done <- err
				return
			}
			_ = h.d.Stats()
			_ = h.guard.State()
			done <- nil
		}()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("%s: %v", stage, err)
			}
		case <-time.After(10 * time.Second):
			t.Errorf("%s: a concurrent caller blocked; a lock is held across the call", stage)
		}
		probes.Add(1)
	}
	h.pool.SetAdmissionChecker(func(*mempool.Tx) error {
		if h.localSeal.Load() {
			t.Error("localSeal set during Pool.Add")
		}
		probe("Pool.Add")
		return nil
	})
	h.bp.SetSealGuard(func() error {
		if !h.localSeal.Load() {
			t.Error("localSeal not set inside ProduceBlock")
		}
		probe("ProduceBlock")
		return nil
	})
	h.d.tick()

	if got := probes.Load(); got != 2 {
		t.Fatalf("probes ran %d times, want 2", got)
	}
	if h.localSeal.Load() {
		t.Error("localSeal still set after ProduceBlock")
	}
	h.mu.Lock()
	sealed := append([]bool(nil), h.sealedLocal...)
	h.mu.Unlock()
	if !reflect.DeepEqual(sealed, []bool{true}) {
		t.Errorf("hook saw local=%v, want [true]", sealed)
	}
	// alice paid; bob's two mid-tick IDs still pending.
	assertCounts(t, h.ledger, ledgerCounts{pending: 2, paid: 2})
	h.noFreeze(t)
	h.noFailStop(t)
}

// Submits and metrics scrapes race the running tick loop; run
// with -race. Every enqueued ID is paid exactly once.
func TestConcurrentSubmitsAndTicks_NoRace(t *testing.T) {
	h := newHarness(t, true, func(c *Config) {
		c.Period = time.Millisecond
		c.FlatRewardPerBlock = 0.25
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.d.Start(ctx)
	const submitters, each = 4, 50
	var wg sync.WaitGroup
	for i := 0; i < submitters; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			addr := fmt.Sprintf("qsdm1miner%d", i)
			for j := 0; j < each; j++ {
				if err := h.ledger.Enqueue(h.ledger.record(addr)); err != nil {
					t.Errorf("Enqueue: %v", err)
					return
				}
				_ = h.d.Stats()
				time.Sleep(100 * time.Microsecond)
			}
		}(i)
	}
	wg.Wait()
	deadline := time.Now().Add(10 * time.Second)
	for h.ledger.counts().paid < submitters*each && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	h.d.Stop()
	assertCounts(t, h.ledger, ledgerCounts{paid: submitters * each})
	if got := h.d.Stats().ProofsPaid; got != submitters*each {
		t.Fatalf("ProofsPaid %d, want %d", got, submitters*each)
	}
	h.noFreeze(t)
	h.noFailStop(t)
}

// ---- Tier-3 reward downgrade --------------------------------------------

// fakeRewardPenalty is a deterministic RewardPenalty
// implementation used by the tests below. Returns the
// configured multiplier for matching addresses; 1.0 for
// every other address.
type fakeRewardPenalty struct {
	multipliers map[string]float64
}

func (f *fakeRewardPenalty) MultiplierFor(addr string) float64 {
	if m, ok := f.multipliers[addr]; ok {
		return m
	}
	return 1.0
}

// TestTier3_PenaltyAppliesToFlaggedMiner verifies the
// happy path: a miner over-threshold gets a fraction of
// their full reward, while honest miners get their full
// share. Uses FlatRewardPerBlock to keep arithmetic
// trivially auditable.
func TestTier3_PenaltyAppliesToFlaggedMiner(t *testing.T) {
	h := newHarness(t, true, func(c *Config) {
		c.FlatRewardPerBlock = 4.0
		c.RewardPenalty = &fakeRewardPenalty{
			multipliers: map[string]float64{
				"qsdm1bad": 0.5,
			},
		}
	})
	h.ledger.add(t, "qsdm1bad", 2)
	h.ledger.add(t, "qsdm1good", 2)
	h.d.tick()

	// 4 proofs, 2 each → base share = 2.0 each.
	// Bad gets 2.0 * 0.5 = 1.0, Good gets 2.0.
	if got := balanceOf(h, "qsdm1bad"); got != 1.0 {
		t.Errorf("bad balance: got %.6f want 1.0 (2.0 * 0.5 multiplier)", got)
	}
	if got := balanceOf(h, "qsdm1good"); got != 2.0 {
		t.Errorf("good balance: got %.6f want 2.0 (no penalty)", got)
	}
	stats := h.d.Stats()
	if stats.PenalisedPayouts != 1 {
		t.Errorf("PenalisedPayouts: got %d want 1", stats.PenalisedPayouts)
	}
	if stats.WithheldDust == 0 {
		t.Errorf("WithheldDust: got 0 want > 0 (1.0 CELL withheld)")
	}
	if !stats.PenaltyActive {
		t.Errorf("PenaltyActive should be true")
	}
	h.noFreeze(t)
}

// TestTier3_NilPenaltyKeepsLegacyBehaviour confirms the
// pre-Tier-3 posture (no RewardPenalty wired): full per-proof
// share, no withheld dust, PenaltyActive=false.
func TestTier3_NilPenaltyKeepsLegacyBehaviour(t *testing.T) {
	h := newHarness(t, true, func(c *Config) { c.FlatRewardPerBlock = 2.0 })
	h.ledger.add(t, "qsdm1solo", 2)
	h.d.tick()

	if got := balanceOf(h, "qsdm1solo"); got != 2.0 {
		t.Errorf("solo balance: got %v want 2.0 (full reward)", got)
	}
	stats := h.d.Stats()
	if stats.PenalisedPayouts != 0 {
		t.Errorf("PenalisedPayouts: got %d want 0", stats.PenalisedPayouts)
	}
	if stats.WithheldDust != 0 {
		t.Errorf("WithheldDust: got %d want 0", stats.WithheldDust)
	}
	if stats.PenaltyActive {
		t.Errorf("PenaltyActive should be false when no penalty wired")
	}
}

// TestTier3_NaNAndOutOfRangeMultiplier_ClampsToFullReward
// exercises the defensive clamp inside buildTxs: a buggy
// MismatchPenalty that returns NaN, +Inf, negative, or >1
// must NOT cause a phantom mint or a negative tx amount.
// Falls back to 1.0 (no penalty) so the system is fail-safe.
type degenerateMultiplier struct{ value float64 }

func (d *degenerateMultiplier) MultiplierFor(string) float64 { return d.value }

func TestTier3_DefensiveClamp_OnDegenerateMultipliers(t *testing.T) {
	cases := []struct {
		name  string
		value float64
	}{
		{"NaN", nanFloat()},
		{"PositiveInfinity", infFloat()},
		{"Negative", -0.25},
		{"GreaterThanOne", 1.5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, true, func(c *Config) {
				c.FlatRewardPerBlock = 1.0
				c.RewardPenalty = &degenerateMultiplier{value: tc.value}
			})
			h.ledger.add(t, "qsdm1clamp", 1)
			h.d.tick()
			if got := balanceOf(h, "qsdm1clamp"); got != 1.0 {
				t.Errorf("balance under degenerate multiplier %v: got %v want 1.0",
					tc.value, got)
			}
			if h.d.Stats().PenalisedPayouts != 0 {
				t.Errorf("degenerate multiplier should not register as penalty firing")
			}
			h.noFreeze(t)
		})
	}
}

func nanFloat() float64 { var z float64; return z / z }
func infFloat() float64 { var z float64; z = 1; return z / 0 }
