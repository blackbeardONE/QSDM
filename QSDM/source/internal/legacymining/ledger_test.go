package legacymining

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/mining/enrollment"
)

const (
	ltFunder = chain.MiningRewardFunderAddress
	// ltStartBalance is blockdriver.DefaultFunderBalance. The float64 ULP
	// at 1e15 is 0.125, so every funder debit lands on a 0.125 grid.
	ltStartBalance = 1e15
)

var (
	ltMinerA = strings.Repeat("a", 64)
	ltMinerB = strings.Repeat("b", 64)
	ltMinerC = strings.Repeat("c", 64)
	ltHash   = ConfigHash{0x51, 0x4c, 0x4d}
)

// ledgerFakeGuard is a Guard that records trips and totals. Freeze latches
// FROZEN; killed and stopped model KILL and ADMISSION_STOPPED.
type ledgerFakeGuard struct {
	mu      sync.Mutex
	cfg     Config
	killed  bool
	frozen  bool
	stopped bool
	freezes []string
	totals  []Totals
	cells   []float64
}

func (g *ledgerFakeGuard) Config() Config {
	g.mu.Lock()
	defer g.mu.Unlock()
	c := g.cfg
	c.Allowed = append([]AllowEntry(nil), g.cfg.Allowed...)
	return c
}
func (g *ledgerFakeGuard) ConfigHash() ConfigHash { return ltHash }
func (g *ledgerFakeGuard) State() State {
	g.mu.Lock()
	defer g.mu.Unlock()
	switch {
	case g.killed:
		return StateKilled
	case g.frozen:
		return StateFrozen
	case g.stopped:
		return StateAdmissionStopped
	}
	return StateOpen
}
func (g *ledgerFakeGuard) AdmissionOpen() bool { return false }
func (g *ledgerFakeGuard) PreArm() error       { return nil }
func (g *ledgerFakeGuard) Activate(bool)       {}
func (g *ledgerFakeGuard) Admit() error        { return nil }
func (g *ledgerFakeGuard) Precheck([]byte) (Candidate, error) {
	return Candidate{}, errors.New("unused")
}
func (g *ledgerFakeGuard) TakeRate() error                      { return nil }
func (g *ledgerFakeGuard) ObserveRejection(error)               {}
func (g *ledgerFakeGuard) ObserveSeal(uint64, bool)             {}
func (g *ledgerFakeGuard) StopAdmission(string)                 {}
func (g *ledgerFakeGuard) ObserveTotals(t Totals, cell float64) { g.record(t, cell) }
func (g *ledgerFakeGuard) record(t Totals, cell float64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.totals = append(g.totals, t)
	g.cells = append(g.cells, cell)
}
func (g *ledgerFakeGuard) Freeze(cause string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.freezes = append(g.freezes, cause)
	g.frozen = true
}
func (g *ledgerFakeGuard) freezeCauses() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.freezes...)
}
func (g *ledgerFakeGuard) lastObserved() (Totals, float64, int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.totals) == 0 {
		return Totals{}, 0, 0
	}
	return g.totals[len(g.totals)-1], g.cells[len(g.cells)-1], len(g.totals)
}
func (g *ledgerFakeGuard) setAllowed(addrs ...string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.cfg.Allowed = nil
	for _, a := range addrs {
		g.cfg.Allowed = append(g.cfg.Allowed, AllowEntry{MinerAddr: a, NodeID: "node-" + a[:4]})
	}
}
func (g *ledgerFakeGuard) set(f func(g *ledgerFakeGuard)) {
	g.mu.Lock()
	defer g.mu.Unlock()
	f(g)
}

// ledgerFakeStore keeps rows in memory. MarkPaid is all-or-nothing like the
// real store: an absent or paid row, or another miner, is ErrPaidConflict.
type ledgerFakeStore struct {
	mu    sync.Mutex
	rows  map[ProofID]*Record
	calls [][]Payment
	err   error
}

func (s *ledgerFakeStore) Open(string, *Meta) error { return nil }
func (s *ledgerFakeStore) Close() error             { return nil }
func (s *ledgerFakeStore) Meta() (Meta, error)      { return Meta{}, nil }
func (s *ledgerFakeStore) Accept(rec Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rows[rec.ProofID] != nil {
		return &Rejection{Kind: KindDuplicate}
	}
	r := rec
	s.rows[rec.ProofID] = &r
	return nil
}
func (s *ledgerFakeStore) Pending() ([]Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Record
	for _, r := range s.rows {
		if r.PaidTxID == "" {
			out = append(out, *r)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].AcceptedNS != out[j].AcceptedNS {
			return out[i].AcceptedNS < out[j].AcceptedNS
		}
		return bytes.Compare(out[i].ProofID[:], out[j].ProofID[:]) < 0
	})
	return out, nil
}
func (s *ledgerFakeStore) Lookup([]ProofID) (map[ProofID]Record, error) {
	return nil, errors.New("unused")
}
func (s *ledgerFakeStore) MarkPaid(ps []Payment) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, append([]Payment(nil), ps...))
	if s.err != nil {
		return s.err
	}
	for _, p := range ps {
		if r := s.rows[p.ProofID]; r == nil || r.PaidTxID != "" || r.MinerAddr != p.MinerAddr {
			return fmt.Errorf("%w: %x", ErrPaidConflict, p.ProofID)
		}
	}
	for _, p := range ps {
		s.rows[p.ProofID].PaidHeight, s.rows[p.ProofID].PaidTxID = p.Height, p.TxID
	}
	return nil
}
func (s *ledgerFakeStore) ApplyReconcile(Reconciliation) (ReconcileCounts, error) {
	return ReconcileCounts{}, errors.New("unused")
}
func (s *ledgerFakeStore) Counters(ConfigHash) (Counters, error) {
	return Counters{}, errors.New("unused")
}
func (s *ledgerFakeStore) Event(Event) error { return nil }
func (s *ledgerFakeStore) markPaidCalls() [][]Payment {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([][]Payment(nil), s.calls...)
}

// ledgerHarness drives a PayoutLedger against a real chain.AccountStore
// funded with 1e15 CELL, in the §4.2 tick order.
type ledgerHarness struct {
	t        *testing.T
	g        *ledgerFakeGuard
	s        *ledgerFakeStore
	accts    *chain.AccountStore
	l        *PayoutLedger
	height   uint64  // last sealed height
	cell     float64 // RewardCell at every height; 0 means DefaultRewardCell
	n        int     // records created
	beforeH8 func()  // runs once between the block's apply and H8
}

func newLedgerHarnessRaw(t *testing.T, allowed ...string) *ledgerHarness {
	t.Helper()
	if len(allowed) == 0 {
		allowed = []string{ltMinerA}
	}
	g := &ledgerFakeGuard{cfg: Config{Version: ConfigVersion, MaxProofsPerMin: 60, MaxProofsTotal: 3000,
		MaxPending: 600, BudgetCell: 1291, ExpiresUnix: 1}}
	g.setAllowed(allowed...)
	accts := chain.NewAccountStore()
	accts.Credit(ltFunder, ltStartBalance)
	h := &ledgerHarness{t: t, g: g, s: &ledgerFakeStore{rows: map[ProofID]*Record{}}, accts: accts, height: 1000}
	h.l = h.newLedger()
	return h
}

func newLedgerHarness(t *testing.T, allowed ...string) *ledgerHarness {
	t.Helper()
	h := newLedgerHarnessRaw(t, allowed...)
	if err := h.l.Init(h.initState(Totals{ConfigSHA256: ltHash})); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return h
}

func (h *ledgerHarness) newLedger() *PayoutLedger {
	h.t.Helper()
	l, err := NewLedger(LedgerConfig{Store: h.s, Guard: h.g, Accounts: h.accts, RewardCell: h.rewardCell})
	if err != nil {
		h.t.Fatalf("NewLedger: %v", err)
	}
	return l
}

// initState is S13: pending from the store, the funder from the chain.
func (h *ledgerHarness) initState(tot Totals) LedgerInit {
	h.t.Helper()
	pending, _ := h.s.Pending()
	acc := h.funder()
	return LedgerInit{Pending: pending, Totals: tot, FunderBalance: acc.Balance, FunderNonce: acc.Nonce}
}

func (h *ledgerHarness) rewardCell(height uint64) float64 {
	if h.cell != 0 {
		return h.cell
	}
	return DefaultRewardCell(height)
}

func (h *ledgerHarness) funder() *chain.Account {
	acc, ok := h.accts.Get(ltFunder)
	if !ok {
		panic("funder account missing")
	}
	return acc
}

func (h *ledgerHarness) freezes() []string { return h.g.freezeCauses() }

func (h *ledgerHarness) noFreeze() {
	h.t.Helper()
	if f := h.freezes(); len(f) != 0 {
		h.t.Fatalf("unexpected FREEZE: %q", f)
	}
}

func (h *ledgerHarness) firstFreeze(prefix string) {
	h.t.Helper()
	f := h.freezes()
	if len(f) == 0 || !strings.HasPrefix(f[0], prefix) {
		h.t.Fatalf("first FREEZE = %q, want prefix %q", f, prefix)
	}
}

func ltID(i int) ProofID { return ProofID(sha256.Sum256([]byte(fmt.Sprintf("proof-%d", i)))) }

func (h *ledgerHarness) record(addr string) Record {
	h.n++
	return Record{ProofID: ltID(h.n), MinerAddr: addr, NodeID: "node", AcceptedNS: int64(h.n), ConfigSHA256: ltHash}
}

// accept is §4.1 steps 8-9: commit the row, then Enqueue it.
func (h *ledgerHarness) accept(addr string) ProofID {
	h.t.Helper()
	rec := h.record(addr)
	if err := h.s.Accept(rec); err != nil {
		h.t.Fatalf("Accept: %v", err)
	}
	if err := h.l.Enqueue(rec); err != nil {
		h.t.Fatalf("Enqueue: %v", err)
	}
	return rec.ProofID
}

func ltPayload(ids ...ProofID) []byte {
	p := append([]byte(nil), PayloadTag...)
	p = binary.BigEndian.AppendUint16(p, uint16(len(ids)))
	for _, id := range ids {
		p = append(p, id[:]...)
	}
	return p
}

func ltSorted(ids ...ProofID) []ProofID {
	out := append([]ProofID(nil), ids...)
	sort.Slice(out, func(i, j int) bool { return bytes.Compare(out[i][:], out[j][:]) < 0 })
	return out
}

// ltRewardTx is the HL1 reward tx: d7's shape (d7:717-726) plus the LMP1
// payload and the RewardIDFormat ID, derived here independently.
func ltRewardTx(nonce uint64, addr string, amount float64, ids ...ProofID) *mempool.Tx {
	payload := ltPayload(ids...)
	sum := sha256.Sum256(payload)
	return &mempool.Tx{
		ID:         fmt.Sprintf("solo-reward-%d-%s-%s", nonce, addr, hex.EncodeToString(sum[:8])),
		Sender:     ltFunder,
		Recipient:  addr,
		Amount:     amount,
		Nonce:      nonce,
		Payload:    payload,
		ContractID: chain.MiningRewardContractID,
	}
}

func (h *ledgerHarness) heartbeat(nonce uint64) *mempool.Tx {
	return &mempool.Tx{ID: fmt.Sprintf("solo-heartbeat-%d-%d", nonce, h.height), Sender: ltFunder, Recipient: ltFunder, Nonce: nonce}
}

// build is the d7 float formula (d7:693-701) with one tx per claim.
func (h *ledgerHarness) build(claims []Claim, rewardCell float64) []*mempool.Tx {
	total := 0
	for _, c := range claims {
		total += len(c.IDs)
	}
	nonce := h.funder().Nonce
	txs := make([]*mempool.Tx, 0, len(claims))
	for i, c := range claims {
		share := rewardCell * float64(len(c.IDs)) / float64(total)
		txs = append(txs, ltRewardTx(nonce+uint64(i), c.MinerAddr, share, c.IDs...))
	}
	return txs
}

// seal mimics ProduceBlock then H8(b): txs are applied to the real account
// store (a tx that fails is left out of the block), then OnDurableBlock runs.
// Txs of other families are put in the block without being applied.
func (h *ledgerHarness) seal(local bool, txs ...*mempool.Tx) (*chain.Block, error) {
	var included []*mempool.Tx
	for _, tx := range txs {
		var err error
		switch tx.ContractID {
		case chain.MiningRewardContractID:
			err = h.accts.ApplyProtocolReward(tx, tx.Amount)
		case "", chain.WalletTransferContractID:
			err = h.accts.ApplyTx(tx)
		}
		if err == nil {
			included = append(included, tx)
		}
	}
	h.height++
	blk := &chain.Block{Height: h.height, Transactions: included}
	if f := h.beforeH8; f != nil {
		h.beforeH8 = nil
		f()
	}
	return blk, h.l.OnDurableBlock(blk, local)
}

// prepare is §4.2 steps 2-3: Take, build and PreSeal.
func (h *ledgerHarness) prepare() ([]Claim, []*mempool.Tx, error) {
	claims := h.l.Take()
	if len(claims) == 0 {
		return nil, nil, nil
	}
	cell := h.rewardCell(h.height + 1)
	txs := h.build(claims, cell)
	return claims, txs, h.l.PreSeal(h.height+1, cell, txs)
}

// tick is one §4.2 tick on the SUCCESS path.
func (h *ledgerHarness) tick() (*chain.Block, error) {
	_, txs, err := h.prepare()
	if err != nil || len(txs) == 0 {
		txs = []*mempool.Tx{h.heartbeat(h.funder().Nonce)}
	}
	blk, err := h.seal(true, txs...)
	h.l.Requeue()
	return blk, err
}

func (h *ledgerHarness) inFlightIDs() int {
	h.l.mu.Lock()
	defer h.l.mu.Unlock()
	n := 0
	for _, e := range h.l.entries {
		if e.inFlight {
			n++
		}
	}
	if n != h.l.inFlight {
		h.t.Fatalf("inFlight counter %d, entries in flight %d", h.l.inFlight, n)
	}
	return n
}

func TestLedgerDecodePayload(t *testing.T) {
	var many []ProofID
	for i := 0; i < MaxPayloadIDs+1; i++ {
		many = append(many, ltID(i))
	}
	many = ltSorted(many...)
	a, b := many[0], many[1]
	withCount := func(n int, ids ...ProofID) []byte {
		p := ltPayload(ids...)
		binary.BigEndian.PutUint16(p[len(PayloadTag):], uint16(n))
		return p
	}
	cases := []struct {
		name string
		p    []byte
		n    int // IDs on success; -1 means error
	}{
		{"one", ltPayload(a), 1},
		{"max", ltPayload(many[:MaxPayloadIDs]...), MaxPayloadIDs},
		{"empty", nil, -1},
		{"tag only", []byte(PayloadTag), -1},
		{"other tag", append([]byte("QSDM-LMP2"), ltPayload(a)[len(PayloadTag):]...), -1},
		{"count zero", withCount(0), -1},
		{"count above max", ltPayload(many...), -1},
		{"short", withCount(2, a), -1},
		{"long", append(ltPayload(a), 0), -1},
		{"descending", ltPayload(b, a), -1},
		{"repeated", ltPayload(a, a), -1},
	}
	for _, c := range cases {
		ids, err := ledgerDecodePayload(c.p)
		if c.n < 0 {
			if !errors.Is(err, ErrBadPayload) || ids != nil {
				t.Errorf("%s: got %d IDs, err %v; want ErrBadPayload", c.name, len(ids), err)
			}
			continue
		}
		if err != nil || len(ids) != c.n || ids[0] != a {
			t.Errorf("%s: got %d IDs, err %v", c.name, len(ids), err)
		}
	}
	if len(ltPayload(many[:MaxPayloadIDs]...)) != MaxPayloadBytes {
		t.Error("max payload size")
	}
}

func TestLedgerRewardIDAndCell(t *testing.T) {
	p := ltPayload(ltID(1))
	sum := sha256.Sum256(p)
	want := "solo-reward-42-" + ltMinerA + "-" + hex.EncodeToString(sum[:8])
	if got := ledgerRewardID(42, ltMinerA, p); got != want || len(hex.EncodeToString(sum[:8])) != 16 {
		t.Errorf("reward ID %q, want %q", got, want)
	}
	// §6.1: rewardCell = 3.56490987 in epoch 0; genesis carries no reward.
	if got := DefaultRewardCell(1); got != 3.56490987 {
		t.Errorf("DefaultRewardCell(1) = %v", got)
	}
	if DefaultRewardCell(0) != 0 {
		t.Error("DefaultRewardCell(0) != 0")
	}
	if _, err := NewLedger(LedgerConfig{Guard: &ledgerFakeGuard{}, Accounts: chain.NewAccountStore()}); err == nil {
		t.Error("NewLedger without a Store succeeded")
	}
}

// Acceptance: an uninitialised H8 trips FREEZE, and so does every other
// entry point that needs S13 state.
func TestLedgerUninitialized(t *testing.T) {
	blk := &chain.Block{Height: 7, Transactions: []*mempool.Tx{{ID: "hb", Sender: ltFunder, Recipient: ltFunder}}}
	cases := []struct {
		name   string
		call   func(h *ledgerHarness) error
		prefix string
		is     error
	}{
		{"H8", func(h *ledgerHarness) error { return h.l.OnDurableBlock(blk, true) }, CauseLedgerUninit + ":", ErrNotInitialized},
		{"H8 non-local", func(h *ledgerHarness) error { return h.l.OnDurableBlock(blk, false) }, CauseLedgerUninit + ":", ErrNotInitialized},
		{"Enqueue", func(h *ledgerHarness) error { return h.l.Enqueue(h.record(ltMinerA)) }, CauseEnqueue + ":", ErrNotInitialized},
		{"PreSeal", func(h *ledgerHarness) error {
			return h.l.PreSeal(1, 1, []*mempool.Tx{ltRewardTx(0, ltMinerA, 1, ltID(1))})
		}, CausePreSeal + ":ledger not initialized", ErrPreSeal},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newLedgerHarnessRaw(t)
			if err := c.call(h); !errors.Is(err, c.is) {
				t.Fatalf("err = %v, want %v", err, c.is)
			}
			h.firstFreeze(c.prefix)
			if h.l.Take() != nil || h.l.Outstanding() != 0 {
				t.Error("uninitialised ledger took or holds IDs")
			}
		})
	}
	// Init after an uninitialised H8 still works, but the ledger stays
	// tripped: no payout is ever built in this process.
	h := newLedgerHarnessRaw(t)
	_ = h.l.OnDurableBlock(blk, true)
	if err := h.l.Init(h.initState(Totals{ConfigSHA256: ltHash})); err != nil {
		t.Fatal(err)
	}
	h.accept(ltMinerA)
	if h.l.Take() != nil {
		t.Error("Take after a ledger trip returned claims")
	}
}

// Acceptance: a non-local block at H8 trips FREEZE. Its IDs still move to
// paid (W3), and nothing is marked in the DB (S10-S11 do that at boot).
func TestLedgerNonLocalBlockFreezes(t *testing.T) {
	h := newLedgerHarness(t)
	a := h.accept(ltMinerA)
	h.accept(ltMinerA)
	tx := ltRewardTx(h.funder().Nonce, ltMinerA, 1.5, a)
	blk, err := h.seal(false, tx)
	if !errors.Is(err, ErrInvariant) || len(blk.Transactions) != 1 {
		t.Fatalf("err = %v, txs %d", err, len(blk.Transactions))
	}
	h.firstFreeze(fmt.Sprintf("%s:height %d", CauseNonLocalBlock, blk.Height))
	if len(h.freezes()) != 1 {
		t.Errorf("freezes = %q", h.freezes())
	}
	if h.l.Outstanding() != 1 || len(h.s.markPaidCalls()) != 0 {
		t.Errorf("outstanding %d, MarkPaid calls %d", h.l.Outstanding(), len(h.s.markPaidCalls()))
	}
	if tot, cell, _ := h.g.lastObserved(); tot.Emitted != 1.5 || tot.Proofs != 2 || cell != DefaultRewardCell(blk.Height+1) {
		t.Errorf("observed %+v cell %v", tot, cell)
	}
	if h.l.Take() != nil {
		t.Error("Take after FREEZE returned claims")
	}
	// The funder expectations were re-anchored, so a later local heartbeat
	// passes I4 and I5.
	if _, err := h.seal(true, h.heartbeat(h.funder().Nonce)); err != nil {
		t.Errorf("heartbeat after non-local block: %v", err)
	}
}

// Acceptance: the count is pending plus in-flight, and only Enqueue grows it.
func TestLedgerOutstandingCountsPendingAndInFlight(t *testing.T) {
	h := newLedgerHarness(t)
	h.g.set(func(g *ledgerFakeGuard) { g.cfg.MaxPending = 4 })
	for i := 0; i < 3; i++ {
		h.accept(ltMinerA)
	}
	if h.l.Outstanding() != 3 {
		t.Fatalf("outstanding %d", h.l.Outstanding())
	}
	// SAFE path: Take, PreSeal, then Requeue. The count never moves.
	claims, _, err := h.prepare()
	if err != nil || len(claims) != 1 || len(claims[0].IDs) != 3 || h.inFlightIDs() != 3 || h.l.Outstanding() != 3 {
		t.Fatalf("prepare: %v %+v", err, claims)
	}
	h.l.Requeue()
	if h.inFlightIDs() != 0 || h.l.Outstanding() != 3 || h.l.bound != nil {
		t.Fatal("Requeue did not return the IDs to pending")
	}
	// SUCCESS path with an Enqueue while the IDs are in flight.
	claims, txs, err := h.prepare()
	if err != nil || len(claims[0].IDs) != 3 {
		t.Fatalf("prepare: %v", err)
	}
	d := h.accept(ltMinerA)
	if h.l.Outstanding() != 4 || h.inFlightIDs() != 3 {
		t.Fatalf("outstanding %d in flight %d", h.l.Outstanding(), h.inFlightIDs())
	}
	// At max_pending (miningsvc checks first, under submitMu), Enqueue is an
	// invariant violation and trips FREEZE; the count does not grow.
	over := h.record(ltMinerA)
	if err := h.l.Enqueue(over); !errors.Is(err, ErrInvariant) || h.l.Outstanding() != 4 {
		t.Fatalf("Enqueue over the cap: %v, outstanding %d", err, h.l.Outstanding())
	}
	h.firstFreeze(fmt.Sprintf("%s:%x: pending plus in-flight 4 is at max_pending", CauseEnqueue, over.ProofID))
	if _, err := h.seal(true, txs...); err != nil {
		t.Fatalf("seal: %v", err)
	}
	if h.l.Outstanding() != 1 || h.inFlightIDs() != 0 {
		t.Fatalf("after H8: outstanding %d in flight %d", h.l.Outstanding(), h.inFlightIDs())
	}
	h.l.Requeue()
	if _, ok := h.l.entries[d]; !ok || h.l.Outstanding() != 1 || h.l.Totals().Proofs != 4 {
		t.Errorf("after Requeue: outstanding %d, proofs %d", h.l.Outstanding(), h.l.Totals().Proofs)
	}
}

// A reward tx that the block leaves out (SUCCESS, not included) keeps its IDs
// in flight until Requeue; they are paid by a later tick.
func TestLedgerPartialInclusion(t *testing.T) {
	h := newLedgerHarness(t, ltMinerA, ltMinerB)
	h.accept(ltMinerA)
	b := h.accept(ltMinerB)
	_, txs, err := h.prepare()
	if err != nil || len(txs) != 2 {
		t.Fatalf("prepare: %v", err)
	}
	if _, err := h.seal(true, txs[0]); err != nil {
		t.Fatalf("seal: %v", err)
	}
	h.noFreeze()
	if h.l.Outstanding() != 1 || h.inFlightIDs() != 1 || h.l.entries[b].missed != 1 {
		t.Fatalf("outstanding %d in flight %d", h.l.Outstanding(), h.inFlightIDs())
	}
	h.l.Requeue()
	if _, err := h.tick(); err != nil || h.l.Outstanding() != 0 {
		t.Fatalf("second tick: %v, outstanding %d", err, h.l.Outstanding())
	}
	h.noFreeze()
	for id, r := range h.s.rows {
		if r.PaidTxID == "" {
			t.Errorf("%x unpaid", id)
		}
	}
}

func TestLedgerTake(t *testing.T) {
	h := newLedgerHarnessRaw(t, ltMinerA, ltMinerB)
	// Pending from S13, interleaved: claims come sorted by address, each with
	// strictly ascending IDs.
	for i := 0; i < 40; i++ {
		rec := h.record(ltMinerB)
		if i%2 == 0 {
			rec.MinerAddr = ltMinerA
		}
		_ = h.s.Accept(rec)
	}
	if err := h.l.Init(h.initState(Totals{ConfigSHA256: ltHash})); err != nil {
		t.Fatal(err)
	}
	claims := h.l.Take()
	if len(claims) != 2 || claims[0].MinerAddr != ltMinerA || claims[1].MinerAddr != ltMinerB ||
		len(claims[0].IDs) != 20 || len(claims[1].IDs) != 20 {
		t.Fatalf("claims %d", len(claims))
	}
	for _, c := range claims {
		for i := 1; i < len(c.IDs); i++ {
			if bytes.Compare(c.IDs[i-1][:], c.IDs[i][:]) >= 0 {
				t.Fatalf("%s IDs not strictly ascending", c.MinerAddr[:4])
			}
		}
		for _, id := range c.IDs {
			if h.s.rows[id].MinerAddr != c.MinerAddr {
				t.Fatal("ID in the wrong claim")
			}
		}
	}
	if h.l.Outstanding() != 40 || h.inFlightIDs() != 40 {
		t.Fatalf("outstanding %d", h.l.Outstanding())
	}

	// Cap per address: one address with MaxPayloadIDs+6 pending.
	h = newLedgerHarnessRaw(t)
	var recs []Record
	for i := 0; i < MaxPayloadIDs+6; i++ {
		recs = append(recs, h.record(ltMinerA))
	}
	st := h.initState(Totals{ConfigSHA256: ltHash})
	st.Pending = recs
	if err := h.l.Init(st); err != nil {
		t.Fatal(err)
	}
	claims = h.l.Take()
	if len(claims) != 1 || len(claims[0].IDs) != MaxPayloadIDs || h.inFlightIDs() != MaxPayloadIDs {
		t.Fatalf("capped claim: %d claims", len(claims))
	}
	for _, r := range recs[MaxPayloadIDs:] {
		if h.l.entries[r.ProofID].inFlight {
			t.Fatal("a newest ID was taken")
		}
	}

	// Payouts disabled: nothing moves, nothing trips.
	h = newLedgerHarness(t)
	h.accept(ltMinerA)
	for _, set := range []func(g *ledgerFakeGuard){
		func(g *ledgerFakeGuard) { g.killed = true },
		func(g *ledgerFakeGuard) { g.killed, g.frozen = false, true },
	} {
		h.g.set(set)
		if h.l.Take() != nil || h.inFlightIDs() != 0 {
			t.Fatalf("Take with payouts disabled (%v)", h.g.State())
		}
	}
	h.noFreeze()
	h.g.set(func(g *ledgerFakeGuard) { g.frozen, g.stopped = false, true })
	if len(h.l.Take()) != 1 {
		t.Fatal("ADMISSION_STOPPED must still pay")
	}

	// A second Take without Requeue trips FREEZE.
	if h.l.Take() != nil {
		t.Fatal("second Take returned claims")
	}
	h.firstFreeze(CausePreSeal + ":take: IDs are already in flight")
}

// §6.3: every failure trips FREEZE, returns the IDs to pending and wraps
// ErrPreSeal.
func TestLedgerPreSeal(t *testing.T) {
	type fix struct {
		h      *ledgerHarness
		claims []Claim // A (2 IDs), then B (1 ID)
		height uint64
		cell   float64
		txs    []*mempool.Tx
	}
	rebuild := func(f *fix, i int, addr string, amount float64, ids ...ProofID) {
		f.txs[i] = ltRewardTx(f.txs[i].Nonce, addr, amount, ltSorted(ids...)...)
	}
	cases := []struct {
		name string
		mut  func(f *fix)
		want string // "" means success
	}{
		{"ok", func(f *fix) {}, ""},
		{"share zero (penalty multiplier 0)", func(f *fix) { f.txs[0].Amount = 0 }, "share 0 is not positive"},
		{"share negative", func(f *fix) { f.txs[1].Amount = -1 }, "share -1 is not positive"},
		{"share NaN", func(f *fix) { f.txs[0].Amount = math.NaN() }, "share NaN is not positive"},
		{"share Inf", func(f *fix) { f.txs[0].Amount = math.Inf(1) }, "share +Inf is not positive"},
		{"sum above the reward cell", func(f *fix) { f.txs[0].Amount *= 1.0001 }, "sum of amounts"},
		{"budget", func(f *fix) { f.h.g.set(func(g *ledgerFakeGuard) { g.cfg.BudgetCell = 3 }) }, "exceeds the budget 3"},
		{"budget with emitted", func(f *fix) { f.h.l.totals.Emitted = 1291 - 3 }, "exceeds the budget 1291"},
		{"recipient not the row's miner", func(f *fix) { rebuild(f, 1, ltMinerC, f.txs[1].Amount, f.claims[1].IDs...) },
			"belongs to " + ltMinerB + ", not the recipient"},
		{"recipient not allowlisted", func(f *fix) { f.h.g.setAllowed(ltMinerA, ltMinerC) }, "recipient " + ltMinerB + " is not allowlisted"},
		{"unknown ID", func(f *fix) { rebuild(f, 0, ltMinerA, f.txs[0].Amount, append(f.claims[0].IDs, ltID(999))...) }, "is not in flight"},
		{"pending ID not taken", func(f *fix) {
			id := f.h.accept(ltMinerA)
			rebuild(f, 0, ltMinerA, f.txs[0].Amount, append(f.claims[0].IDs, id)...)
		}, "is not in flight"},
		{"in-flight ID left out", func(f *fix) { rebuild(f, 0, ltMinerA, f.txs[0].Amount, f.claims[0].IDs[0]) },
			"3 IDs are in flight but 2 are in payloads"},
		{"ID in two payloads", func(f *fix) {
			rebuild(f, 1, ltMinerB, f.txs[1].Amount, append(f.claims[1].IDs, f.claims[0].IDs[0])...)
		}, "is in more than one payload"},
		{"ID already paid", func(f *fix) { f.h.l.paid[f.claims[0].IDs[1]] = 1 }, "is already paid"},
		{"no txs", func(f *fix) { f.txs = nil }, "3 IDs are in flight but 0 are in payloads"},
		{"nil tx", func(f *fix) { f.txs[1] = nil }, "tx 1 is nil"},
		{"not the reward contract", func(f *fix) { f.txs[0].ContractID = "" }, "is not the reward contract"},
		{"sender not the funder", func(f *fix) { f.txs[0].Sender = ltMinerA }, "is not the funder"},
		{"fee", func(f *fix) { f.txs[0].Fee = 0.01 }, "fee 0.01 is not zero"},
		{"negative zero fee", func(f *fix) { f.txs[0].Fee = math.Copysign(0, -1) }, "is not zero"},
		{"nonce gap", func(f *fix) { f.txs[1].Nonce++ }, "nonce 2, want 1"},
		{"nonce not the funder's", func(f *fix) {
			for i, tx := range f.txs {
				f.txs[i] = ltRewardTx(tx.Nonce+5, tx.Recipient, tx.Amount, f.claims[i].IDs...)
			}
		}, "nonce 5, want 0"},
		{"d7 tx ID", func(f *fix) { f.txs[0].ID = fmt.Sprintf("solo-reward-%d-%s", f.txs[0].Nonce, ltMinerA) }, "tx ID"},
		{"bad payload", func(f *fix) { f.txs[0].Payload = []byte("junk") }, "missing QSDM-LMP1 tag"},
		{"two txs for one recipient", func(f *fix) {
			n := f.txs[0].Nonce
			a := f.claims[0].IDs
			f.txs = []*mempool.Tx{ltRewardTx(n, ltMinerA, 1, a[0]), ltRewardTx(n+1, ltMinerA, 1, a[1]),
				ltRewardTx(n+2, ltMinerB, 1, f.claims[1].IDs...)}
		}, "has more than one tx"},
		{"reward cell above the schedule", func(f *fix) { f.cell *= 2 }, "is not within the schedule"},
		{"reward cell zero", func(f *fix) { f.cell = 0 }, "is not within the schedule"},
		{"bound twice", func(f *fix) {
			if err := f.h.l.PreSeal(f.height, f.cell, f.txs); err != nil {
				panic(err)
			}
		}, "already bound"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newLedgerHarness(t, ltMinerA, ltMinerB, ltMinerC)
			h.accept(ltMinerA)
			h.accept(ltMinerB)
			h.accept(ltMinerA)
			f := &fix{h: h, claims: h.l.Take(), height: h.height + 1, cell: h.rewardCell(h.height + 1)}
			if len(f.claims) != 2 || f.claims[0].MinerAddr != ltMinerA {
				t.Fatalf("claims %+v", f.claims)
			}
			f.txs = h.build(f.claims, f.cell)
			c.mut(f)
			err := h.l.PreSeal(f.height, f.cell, f.txs)
			if c.want == "" {
				if err != nil {
					t.Fatalf("PreSeal: %v", err)
				}
				h.noFreeze()
				if len(h.l.bound) != 2 || h.inFlightIDs() != 3 {
					t.Fatalf("bound %d in flight %d", len(h.l.bound), h.inFlightIDs())
				}
				for _, e := range h.l.entries {
					if e.boundTx == "" {
						t.Fatal("in-flight ID not bound")
					}
				}
				return
			}
			if !errors.Is(err, ErrPreSeal) || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
			h.firstFreeze(CausePreSeal + ":")
			if !strings.Contains(h.freezes()[0], c.want) {
				t.Errorf("cause %q lacks %q", h.freezes()[0], c.want)
			}
			if h.inFlightIDs() != 0 || h.l.bound != nil || h.l.Outstanding() < 3 {
				t.Errorf("IDs not back to pending: in flight %d, outstanding %d", h.inFlightIDs(), h.l.Outstanding())
			}
			for _, e := range h.l.entries {
				if e.boundTx != "" {
					t.Error("binding left behind")
				}
			}
			if h.l.Take() != nil {
				t.Error("Take after a pre-seal FREEZE returned claims")
			}
		})
	}
}

// I1-I7 table. Each case prepares a tick for A (2 IDs) and B (1 ID), may
// change the block or the state between the block's apply and H8, and seals.
func TestLedgerInvariants(t *testing.T) {
	type fix struct {
		h      *ledgerHarness
		claims []Claim
		txs    []*mempool.Tx
		cell   float64
	}
	extraNonce := func(f *fix) uint64 { return f.txs[len(f.txs)-1].Nonce + 1 }
	cases := []struct {
		name  string
		mut   func(f *fix)
		first string   // expected first FREEZE cause prefix; "" means clean
		all   []string // labels that must appear in the error
		not   []string // labels that must not
	}{
		{"clean", func(f *fix) {}, "", nil, nil},
		{"I1 amount bits", func(f *fix) { f.txs[0].Amount = math.Nextafter(f.txs[0].Amount, 0) },
			"invariant:I1", []string{"differs from its in-flight record"}, []string{"I5", "I3"}},
		{"I1 payload", func(f *fix) { f.txs[0].Payload = ltPayload(f.claims[0].IDs[0]) },
			"invariant:I1", []string{"differs"}, []string{"I5"}},
		{"I1 recipient", func(f *fix) { f.txs[1].Recipient = ltMinerC },
			"invariant:I1", []string{"differs", "mark paid"}, []string{"I6", "I5"}},
		{"I1 unbound reward", func(f *fix) {
			id := f.h.accept(ltMinerA)
			f.txs = append(f.txs, ltRewardTx(extraNonce(f), ltMinerA, 0.5, id))
		}, "invariant:I1", []string{"is not bound", "is not in flight for that tx", "I3"}, []string{"I4", "I5"}},
		{"I1 height", func(f *fix) { f.h.height++ }, "invariant:I1", []string{"bound for"}, nil},
		{"I1 LMP1 outside the reward contract", func(f *fix) {
			hb := f.h.heartbeat(extraNonce(f))
			hb.Payload = ltPayload(f.claims[0].IDs[0])
			f.txs = append(f.txs, hb)
		}, "invariant:I1", []string{"outside the reward contract"}, []string{"I4", "I5", CauseTxFamily}},
		{"I2 repeated in the block", func(f *fix) {
			id := f.h.accept(ltMinerA)
			f.txs = append(f.txs, ltRewardTx(extraNonce(f), ltMinerA, 0.1, id), ltRewardTx(extraNonce(f)+1, ltMinerA, 0.1, id))
		}, "invariant:I1", []string{"invariant:I2", "repeated or already paid"}, nil},
		{"I3 schedule", func(f *fix) { f.h.cell = f.cell / 2 }, "invariant:I3", nil, []string{"I1", "I5"}},
		{"I4 nonce", func(f *fix) {
			f.h.beforeH8 = func() {
				if err := f.h.accts.ChargeAndBumpNonce(ltFunder, 0, f.h.funder().Nonce); err != nil {
					panic(err)
				}
			}
		}, "invariant:I4", nil, []string{"I5", "I1"}},
		{"I5 balance one ULP", func(f *fix) { f.h.beforeH8 = func() { f.h.accts.Credit(ltFunder, 0.125) } },
			"invariant:I5", []string{"bits"}, []string{"I4", "I1"}},
		{"I5 unknown funder tx", func(f *fix) {
			f.txs = append(f.txs, &mempool.Tx{ID: "stream-1", Sender: ltFunder, Recipient: ltMinerA, Amount: 1,
				Nonce: extraNonce(f), ContractID: chain.StreamContractID})
		}, "invariant:I4", []string{"invariant:I5", "unknown accounting", CauseTxFamily}, nil},
		{"I5 reward to the funder", func(f *fix) {
			f.txs = append(f.txs, &mempool.Tx{ID: "r", Sender: ltFunder, Recipient: ltFunder, Amount: 1,
				Nonce: extraNonce(f), ContractID: chain.MiningRewardContractID, Payload: ltPayload(ltID(777))})
		}, "invariant:I1", []string{"invariant:I5", "invariant:I6"}, []string{"I4"}},
		{"I6 allowlist", func(f *fix) { f.h.g.setAllowed(ltMinerA, ltMinerC) }, "invariant:I6", nil, []string{"I1", "I5"}},
		{"I7 non-funder transfer", func(f *fix) {
			f.txs = append(f.txs, &mempool.Tx{ID: "t", Sender: ltMinerA, Recipient: ltMinerC, Amount: 0.1, Nonce: 0})
		}, CauseTxFamily, nil, []string{"invariant:I"}},
		{"I7 enrollment tx", func(f *fix) {
			f.txs = append(f.txs, &mempool.Tx{ID: "e", Sender: ltMinerC, Nonce: 0, ContractID: enrollment.ContractID})
		}, CauseTxFamily, []string{enrollment.ContractID}, []string{"invariant:I"}},
		{"MarkPaid I/O", func(f *fix) { f.h.s.err = errors.New("disk I/O error") },
			CauseMarkPaidIO + ":disk I/O error", []string{"mark paid"}, []string{"invariant:I"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newLedgerHarness(t, ltMinerA, ltMinerB, ltMinerC)
			h.accept(ltMinerA)
			h.accept(ltMinerB)
			h.accept(ltMinerA)
			claims, txs, err := h.prepare()
			if err != nil {
				t.Fatalf("PreSeal: %v", err)
			}
			f := &fix{h: h, claims: claims, txs: txs, cell: h.rewardCell(h.height + 1)}
			c.mut(f)
			held := h.l.Outstanding()
			blk, err := h.seal(true, f.txs...)
			if len(blk.Transactions) != len(f.txs) {
				t.Fatalf("fixture: %d of %d txs applied", len(blk.Transactions), len(f.txs))
			}
			paidHere := 0
			for _, r := range []ProofID{claims[0].IDs[0], claims[0].IDs[1], claims[1].IDs[0]} {
				if _, ok := h.l.paid[r]; ok {
					paidHere++
				}
			}
			if c.first == "" {
				if err != nil {
					t.Fatalf("clean block: %v", err)
				}
				h.noFreeze()
				calls := h.s.markPaidCalls()
				if len(calls) != 1 || len(calls[0]) != 3 || h.l.Outstanding() != 0 {
					t.Fatalf("MarkPaid calls %v, outstanding %d", calls, h.l.Outstanding())
				}
				for _, p := range calls[0] {
					if p.Height != blk.Height || !strings.HasPrefix(p.TxID, RewardIDPrefix) || h.s.rows[p.ProofID].MinerAddr != p.MinerAddr {
						t.Errorf("payment %+v", p)
					}
				}
				want := h.l.Totals()
				if tot, cell, _ := h.g.lastObserved(); tot != want || tot.Emitted != txs[0].Amount+txs[1].Amount ||
					cell != DefaultRewardCell(blk.Height+1) {
					t.Errorf("observed %+v %v", tot, cell)
				}
				return
			}
			if err == nil {
				t.Fatal("violation not reported")
			}
			h.firstFreeze(c.first)
			for _, s := range c.all {
				if !strings.Contains(err.Error(), s) {
					t.Errorf("error lacks %q: %v", s, err)
				}
			}
			for _, s := range c.not {
				if strings.Contains(err.Error(), s) {
					t.Errorf("error has %q: %v", s, err)
				}
			}
			if strings.HasPrefix(c.first, CauseInvariant) && !errors.Is(err, ErrInvariant) {
				t.Errorf("err does not wrap ErrInvariant: %v", err)
			}
			// W3: whatever the checks say, IDs in the durable block are paid.
			if paidHere == 0 || h.l.Outstanding() > held {
				t.Errorf("paid %d, outstanding %d of %d", paidHere, h.l.Outstanding(), held)
			}
			if h.l.Take() != nil {
				t.Error("Take after FREEZE returned claims")
			}
		})
	}
}

// I2 across blocks: an ID paid by one block and paid again by the next.
func TestLedgerI2AlreadyPaid(t *testing.T) {
	h := newLedgerHarness(t)
	a := h.accept(ltMinerA)
	if _, err := h.tick(); err != nil {
		t.Fatal(err)
	}
	h.noFreeze()
	_, err := h.seal(true, ltRewardTx(h.funder().Nonce, ltMinerA, 0.5, a))
	h.firstFreeze("invariant:I1")
	if err == nil || !strings.Contains(err.Error(), "invariant:I2") {
		t.Fatalf("err = %v", err)
	}
	if calls := h.s.markPaidCalls(); len(calls) != 1 {
		t.Errorf("MarkPaid called for an already-paid ID: %v", calls)
	}
}

// I5 near 1e15: the replay must perform pkg/chain's float operations one tx at
// a time, in block order. At 1e15 the ULP is 0.125, so an aggregated or
// integer replay gives different bits.
func TestLedgerI5FloatReplayNear1e15(t *testing.T) {
	h := newLedgerHarness(t, ltMinerA, ltMinerB, ltMinerC)
	h.g.set(func(g *ledgerFakeGuard) { g.cfg.BudgetCell = 1 << 40 })
	for _, a := range []string{ltMinerA, ltMinerB, ltMinerC} {
		h.accept(a)
	}
	blk, err := h.tick()
	if err != nil || len(blk.Transactions) != 3 {
		t.Fatalf("tick: %v", err)
	}
	h.noFreeze()
	amt := []float64{blk.Transactions[0].Amount, blk.Transactions[1].Amount, blk.Transactions[2].Amount}
	got := h.funder().Balance
	seq := ltStartBalance
	for _, a := range amt {
		seq -= a
	}
	aggregated := ltStartBalance - (amt[0] + amt[1] + amt[2])
	if math.Float64bits(seq) != math.Float64bits(got) {
		t.Fatalf("harness: sequential %v, chain %v", seq, got)
	}
	if math.Float64bits(aggregated) == math.Float64bits(got) {
		t.Fatalf("fixture does not distinguish sequential from aggregated replay (%v)", got)
	}
	if debit := ltStartBalance - got; debit == amt[0]+amt[1]+amt[2] || math.Mod(debit, 0.125) != 0 {
		t.Fatalf("debit %v not quantised to the 0.125 grid (credit %v)", debit, amt[0]+amt[1]+amt[2])
	}

	// Many blocks: varying shares, heartbeats and wallet transfers into the
	// funder (credited on the same grid) all replay bit-exactly.
	h.accts.Credit(ltMinerC, 100)
	cNonce := uint64(0)
	for i := 0; i < 300; i++ {
		for j := 0; j < i%5; j++ {
			h.accept([]string{ltMinerA, ltMinerB, ltMinerC}[(i+j)%3])
		}
		if i%7 == 3 {
			_, txs, err := h.prepare()
			if err != nil {
				t.Fatal(err)
			}
			if len(txs) == 0 {
				txs = []*mempool.Tx{h.heartbeat(h.funder().Nonce)}
			}
			in := &mempool.Tx{ID: fmt.Sprintf("wt-%d", i), Sender: ltMinerC, Recipient: ltFunder, Amount: 0.3 + float64(i)/1000,
				Nonce: cNonce, ContractID: chain.WalletTransferContractID}
			cNonce++
			if _, err := h.seal(true, append(txs, in)...); err != nil {
				t.Fatalf("block %d: %v", i, err)
			}
			h.l.Requeue()
			continue
		}
		if _, err := h.tick(); err != nil {
			t.Fatalf("block %d: %v", i, err)
		}
	}
	h.noFreeze()
	if h.l.funderBal != h.funder().Balance || h.l.Outstanding() != 0 {
		t.Fatalf("expectation %v, funder %v, outstanding %d", h.l.funderBal, h.funder().Balance, h.l.Outstanding())
	}
	if h.funder().Balance >= ltStartBalance || h.funder().Balance < ltStartBalance-2000 {
		t.Fatalf("funder balance %v not near 1e15", h.funder().Balance)
	}

	// One ULP off between blocks: the next block trips I5, no tolerance.
	h.accts.Credit(ltFunder, 0.125)
	if _, err := h.tick(); err == nil || !strings.Contains(err.Error(), "invariant:I5") {
		t.Fatalf("one-ULP drift: %v", err)
	}
	h.firstFreeze("invariant:I5")
}

// The replay against a real AccountStore, one tx family at a time, and the
// unknown kinds that must fail.
func TestLedgerReplayFunder(t *testing.T) {
	x := ltMinerA
	cases := []struct {
		name string
		tx   *mempool.Tx
		fail string
	}{
		{"heartbeat", &mempool.Tx{Sender: ltFunder, Recipient: ltFunder}, ""},
		{"reward", &mempool.Tx{Sender: ltFunder, Recipient: x, Amount: 1.18830329, ContractID: chain.MiningRewardContractID}, ""},
		{"self transfer with fee", &mempool.Tx{Sender: ltFunder, Recipient: ltFunder, Amount: 0.7, Fee: 0.3}, ""},
		{"plain transfer out", &mempool.Tx{Sender: ltFunder, Recipient: x, Amount: 0.06, Fee: 0.01}, ""},
		{"wallet transfer in", &mempool.Tx{Sender: x, Recipient: ltFunder, Amount: 0.3, ContractID: chain.WalletTransferContractID}, ""},
		{"wallet transfer out", &mempool.Tx{Sender: ltFunder, Recipient: x, Amount: 0.3, Fee: 0.05, ContractID: chain.WalletTransferContractID}, ""},
		{"not the funder", &mempool.Tx{Sender: x, Recipient: ltMinerB, Amount: 5, ContractID: chain.StreamContractID}, ""},
		{"reward to the funder", &mempool.Tx{Sender: ltFunder, Recipient: ltFunder, Amount: 1, ContractID: chain.MiningRewardContractID}, "unknown accounting"},
		{"stream from the funder", &mempool.Tx{Sender: ltFunder, Recipient: x, Amount: 1, ContractID: chain.StreamContractID}, "unknown accounting"},
		{"enrollment from the funder", &mempool.Tx{Sender: ltFunder, ContractID: enrollment.SignedContractID}, "unknown accounting"},
		{"unknown contract to the funder", &mempool.Tx{Sender: x, Recipient: ltFunder, Amount: 1, ContractID: "qsdm/other/v1"}, "unknown accounting"},
		{"task to the funder", &mempool.Tx{Sender: x, Recipient: ltFunder, Amount: 1, ContractID: chain.TaskContractID}, "unknown accounting"},
	}
	for _, c := range cases {
		as := chain.NewAccountStore()
		as.Credit(ltFunder, ltStartBalance-0.375)
		as.Credit(x, 10)
		before, _ := as.Get(ltFunder)
		got, err := ledgerReplayFunder(before.Balance, 5, []*mempool.Tx{nil, c.tx})
		if c.fail != "" {
			if err == nil || !strings.Contains(err.Error(), c.fail) {
				t.Errorf("%s: err = %v", c.name, err)
			}
			continue
		}
		var applyErr error
		if c.tx.ContractID == chain.MiningRewardContractID {
			applyErr = as.ApplyProtocolReward(c.tx, c.tx.Amount)
		} else if c.tx.Sender == ltFunder || c.tx.Recipient == ltFunder {
			applyErr = as.ApplyTx(c.tx)
		}
		after, _ := as.Get(ltFunder)
		if err != nil || applyErr != nil || math.Float64bits(got) != math.Float64bits(after.Balance) {
			t.Errorf("%s: replay %v (%v), chain %v (%v)", c.name, got, err, after.Balance, applyErr)
		}
	}
	// Integer-dust accounting would make the float replay meaningless.
	prev := chain.ForkDustHeight()
	chain.SetForkDustHeight(5)
	defer chain.SetForkDustHeight(prev)
	if _, err := ledgerReplayFunder(1, 5, []*mempool.Tx{{Sender: ltFunder, Recipient: ltFunder}}); err == nil {
		t.Error("replay under dust accounting succeeded")
	}
	if _, err := ledgerReplayFunder(1, 4, []*mempool.Tx{{Sender: ltFunder, Recipient: ltFunder}}); err != nil {
		t.Errorf("replay below the dust fork: %v", err)
	}
}

// I7 (§6.4) in every mode: only funder heartbeats, funder rewards and signed
// wallet transfers may appear in a local block.
func TestLedgerAuditTxFamilies(t *testing.T) {
	ok := []*mempool.Tx{
		{ID: "hb", Sender: ltFunder, Recipient: ltFunder, Nonce: 3},
		{ID: "rw", Sender: ltFunder, Recipient: ltMinerA, Amount: 1, ContractID: chain.MiningRewardContractID},
		{ID: "wt", Sender: ltMinerA, Recipient: ltMinerB, Amount: 1, ContractID: chain.WalletTransferContractID},
	}
	bad := []*mempool.Tx{
		{ID: "stream", Sender: ltMinerA, Recipient: ltMinerB, ContractID: chain.StreamContractID},
		{ID: "enroll", Sender: ltMinerA, ContractID: enrollment.ContractID},
		{ID: "enroll2", Sender: ltMinerA, ContractID: enrollment.SignedContractID},
		{ID: "task", Sender: ltMinerA, ContractID: chain.TaskContractID},
		{ID: "plain", Sender: ltMinerA, Recipient: ltMinerB, Amount: 1},
		{ID: "to-funder", Sender: ltMinerA, Recipient: ltFunder},
		{ID: "funder-out", Sender: ltFunder, Recipient: ltMinerA},
		{ID: "hb-amount", Sender: ltFunder, Recipient: ltFunder, Amount: 1},
		{ID: "foreign-reward", Sender: ltMinerA, Recipient: ltMinerB, Amount: 1, ContractID: chain.MiningRewardContractID},
		nil,
	}
	if v := AuditTxFamilies(&chain.Block{Transactions: ok}); len(v) != 0 {
		t.Errorf("clean block: %+v", v)
	}
	if AuditTxFamilies(nil) != nil {
		t.Error("nil block")
	}
	var mixed []*mempool.Tx
	for i := range bad {
		mixed = append(mixed, ok[i%len(ok)], bad[i])
	}
	v := AuditTxFamilies(&chain.Block{Transactions: mixed})
	if len(v) != len(bad) {
		t.Fatalf("violations %+v", v)
	}
	for i, tx := range bad {
		want := FamilyViolation{}
		if tx != nil {
			want = FamilyViolation{TxID: tx.ID, ContractID: tx.ContractID}
		}
		if v[i] != want {
			t.Errorf("violation %d = %+v, want %+v", i, v[i], want)
		}
	}
}

// Stall (§6.6): a pending ID that misses StallSeals consecutive local
// durable seals while payouts are enabled trips FREEZE. The count pauses
// while KILLED or FROZEN and restarts at 0 on each boot.
func TestLedgerStall(t *testing.T) {
	h := newLedgerHarness(t)
	id := h.accept(ltMinerA)
	hb := func() {
		t.Helper()
		if _, err := h.seal(true, h.heartbeat(h.funder().Nonce)); err != nil {
			t.Fatalf("heartbeat: %v", err)
		}
	}
	for i := 1; i < StallSeals-5; i++ {
		hb()
	}
	h.g.set(func(g *ledgerFakeGuard) { g.stopped = true }) // payouts still enabled
	for i := 0; i < 5; i++ {
		hb()
	}
	h.g.set(func(g *ledgerFakeGuard) { g.killed = true })
	for i := 0; i < 10; i++ {
		hb()
	}
	h.noFreeze()
	if m := h.l.entries[id].missed; m != StallSeals-1 {
		t.Fatalf("missed %d", m)
	}

	// A new boot (S13) restarts every count at 0.
	h.g.set(func(g *ledgerFakeGuard) { g.killed = false })
	h.l = h.newLedger()
	if err := h.l.Init(h.initState(Totals{ConfigSHA256: ltHash, Proofs: 1})); err != nil {
		t.Fatal(err)
	}
	for i := 1; i < StallSeals; i++ {
		hb()
	}
	h.noFreeze()
	hb()
	h.firstFreeze(fmt.Sprintf("%s:%x missed %d consecutive local durable seals", CauseStall, id, StallSeals))
	if len(h.freezes()) != 1 {
		t.Errorf("freezes %q", h.freezes())
	}
	hb()
	if len(h.freezes()) != 1 {
		t.Error("stall counted while FROZEN")
	}

	// The normal flow never stalls: an ID enqueued while its tick is sealing
	// misses one seal and is paid by the next tick.
	h = newLedgerHarness(t)
	h.g.set(func(g *ledgerFakeGuard) { g.cfg.BudgetCell = 1 << 40 })
	for i := 0; i < 3*StallSeals; i++ {
		_, txs, err := h.prepare()
		if err != nil {
			t.Fatal(err)
		}
		h.accept(ltMinerA)
		if len(txs) == 0 {
			txs = []*mempool.Tx{h.heartbeat(h.funder().Nonce)}
		}
		if _, err := h.seal(true, txs...); err != nil {
			t.Fatal(err)
		}
		h.l.Requeue()
		for _, e := range h.l.entries {
			if e.missed > 1 {
				t.Fatalf("missed %d in the normal flow", e.missed)
			}
		}
	}
	h.noFreeze()
}

func TestLedgerInit(t *testing.T) {
	h := newLedgerHarness(t)
	h.accept(ltMinerA)
	if err := h.l.Init(h.initState(Totals{ConfigSHA256: ltHash})); !errors.Is(err, ErrInvariant) || h.l.Outstanding() != 1 {
		t.Fatalf("second Init: %v", err)
	}
	h.noFreeze()

	bad := []struct {
		name string
		mut  func(s *LedgerInit)
		want string
	}{
		{"config hash", func(s *LedgerInit) { s.Totals.ConfigSHA256 = ConfigHash{9} }, "totals are for config"},
		{"NaN balance", func(s *LedgerInit) { s.FunderBalance = math.NaN() }, "not a finite amount"},
		{"negative emitted", func(s *LedgerInit) { s.Totals.Emitted = -1 }, "not a finite amount"},
		{"paid record", func(s *LedgerInit) { s.Pending[0].PaidTxID = "solo-reward-x" }, "is paid by"},
		{"no miner", func(s *LedgerInit) { s.Pending[1].MinerAddr = "" }, "has no miner address"},
		{"repeated", func(s *LedgerInit) { s.Pending = append(s.Pending, s.Pending[0]) }, "is repeated"},
	}
	for _, c := range bad {
		h := newLedgerHarnessRaw(t)
		for i := 0; i < 2; i++ {
			_ = h.s.Accept(h.record(ltMinerA))
		}
		s := h.initState(Totals{ConfigSHA256: ltHash})
		c.mut(&s)
		if err := h.l.Init(s); !errors.Is(err, ErrReconcile) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", c.name, err)
		}
		h.firstFreeze(CauseReconcile + ":ledger-init: ")
		if !strings.Contains(h.freezes()[0], c.want) {
			t.Errorf("%s: cause %q", c.name, h.freezes()[0])
		}
		if h.l.Outstanding() != 0 || h.l.Take() != nil || h.l.initialized {
			t.Errorf("%s: ledger initialised after a failed Init", c.name)
		}
	}
}

func TestLedgerEnqueueRefusals(t *testing.T) {
	cases := []struct {
		name string
		rec  func(h *ledgerHarness) Record
		want string
	}{
		{"paid record", func(h *ledgerHarness) Record { r := h.record(ltMinerA); r.PaidTxID = "t"; return r }, "record is paid"},
		{"no miner", func(h *ledgerHarness) Record { r := h.record(ltMinerA); r.MinerAddr = ""; return r }, "no miner address"},
		{"pending ID", func(h *ledgerHarness) Record {
			r := h.record(ltMinerA)
			if err := h.l.Enqueue(r); err != nil {
				panic(err)
			}
			return r
		}, "already pending or in flight"},
		{"in-flight ID", func(h *ledgerHarness) Record {
			r := h.record(ltMinerA)
			if err := h.l.Enqueue(r); err != nil || len(h.l.Take()) != 1 {
				panic("setup")
			}
			return r
		}, "already pending or in flight"},
		{"paid ID", func(h *ledgerHarness) Record {
			r := h.record(ltMinerA)
			_ = h.s.Accept(r)
			if err := h.l.Enqueue(r); err != nil {
				panic(err)
			}
			if _, err := h.tick(); err != nil {
				panic(err)
			}
			return r
		}, "already paid"},
		{"other config", func(h *ledgerHarness) Record { r := h.record(ltMinerA); r.ConfigSHA256 = ConfigHash{7}; return r }, "is not the active window"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newLedgerHarness(t)
			rec := c.rec(h)
			held, proofs := h.l.Outstanding(), h.l.Totals().Proofs
			err := h.l.Enqueue(rec)
			if !errors.Is(err, ErrInvariant) || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v", err)
			}
			h.firstFreeze(fmt.Sprintf("%s:%x: ", CauseEnqueue, rec.ProofID))
			if !strings.Contains(h.freezes()[0], c.want) {
				t.Errorf("cause %q lacks %q", h.freezes()[0], c.want)
			}
			if h.l.Outstanding() != held || h.l.Totals().Proofs != proofs {
				t.Error("refused Enqueue changed the ledger")
			}
		})
	}
}

func TestLedgerTotals(t *testing.T) {
	h := newLedgerHarnessRaw(t)
	if err := h.l.Init(h.initState(Totals{ConfigSHA256: ltHash, Proofs: 5, Emitted: 10})); err != nil {
		t.Fatal(err)
	}
	h.accept(ltMinerA)
	tot, cell, n := h.g.lastObserved()
	if n != 1 || tot.Proofs != 6 || tot.Emitted != 10 || cell != DefaultRewardCell(1) {
		t.Fatalf("after Enqueue: %+v cell %v n %d", tot, cell, n)
	}
	blk, err := h.tick()
	if err != nil {
		t.Fatal(err)
	}
	tot, cell, _ = h.g.lastObserved()
	if tot.Emitted != 10+blk.Transactions[0].Amount || tot.Proofs != 6 || cell != DefaultRewardCell(blk.Height+1) || tot != h.l.Totals() {
		t.Fatalf("after H8: %+v cell %v", tot, cell)
	}
	h.cell = 2.5
	h.accept(ltMinerA)
	if tot, cell, _ = h.g.lastObserved(); tot.Proofs != 7 || cell != DefaultRewardCell(blk.Height+1) {
		t.Fatalf("Enqueue after H8 uses the next height's cell: %+v %v", tot, cell)
	}
}

// A restart reloads the unpaid rows (S13); in-flight state is not durable.
func TestLedgerRestart(t *testing.T) {
	h := newLedgerHarness(t)
	for i := 0; i < 3; i++ {
		h.accept(ltMinerA)
	}
	if _, err := h.tick(); err != nil {
		t.Fatal(err)
	}
	h.accept(ltMinerA)
	h.accept(ltMinerA)
	if _, _, err := h.prepare(); err != nil { // crash with IDs in flight
		t.Fatal(err)
	}
	h.l = h.newLedger()
	if err := h.l.Init(h.initState(Totals{ConfigSHA256: ltHash, Proofs: 5})); err != nil {
		t.Fatal(err)
	}
	if h.l.Outstanding() != 2 {
		t.Fatalf("outstanding %d after restart", h.l.Outstanding())
	}
	if _, err := h.tick(); err != nil {
		t.Fatal(err)
	}
	h.noFreeze()
	paid := map[ProofID]int{}
	for _, call := range h.s.markPaidCalls() {
		for _, p := range call {
			paid[p.ProofID]++
		}
	}
	if len(paid) != 5 {
		t.Fatalf("paid %d IDs", len(paid))
	}
	for id, n := range paid {
		if n != 1 || h.s.rows[id].PaidTxID == "" {
			t.Errorf("%x paid %d times", id, n)
		}
	}
}

// Race (§7): concurrent submits under submitMu against the tick. The cap is
// never exceeded and every accepted proof is paid exactly once.
func TestLedgerRaceSubmitAndTick(t *testing.T) {
	h := newLedgerHarness(t)
	const maxPending = 50
	h.g.set(func(g *ledgerFakeGuard) { g.cfg.MaxPending, g.cfg.BudgetCell = maxPending, 1<<40 })
	var (
		submitMu       sync.Mutex
		accepted, full int
		maxSeen        int
		wg             sync.WaitGroup
		tickErr        error
	)
	done := make(chan struct{})
	tickDone := make(chan struct{})
	go func() {
		defer close(tickDone)
		for {
			select {
			case <-done:
				return
			default:
			}
			if _, err := h.tick(); err != nil && tickErr == nil {
				tickErr = err
			}
		}
	}()
	for g := 0; g < 200; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 3; i++ {
				submitMu.Lock()
				if h.l.Outstanding() >= maxPending { // §4.1 step 6
					full++
					submitMu.Unlock()
					continue
				}
				rec := h.record(ltMinerA)
				_ = h.s.Accept(rec)
				if err := h.l.Enqueue(rec); err != nil {
					t.Errorf("Enqueue: %v", err)
				}
				accepted++
				maxSeen = max(maxSeen, h.l.Outstanding())
				submitMu.Unlock()
			}
		}()
	}
	wg.Wait()
	close(done)
	<-tickDone
	for i := 0; i < 5 && h.l.Outstanding() > 0; i++ {
		if _, err := h.tick(); err != nil {
			t.Fatal(err)
		}
	}
	if tickErr != nil {
		t.Fatal(tickErr)
	}
	h.noFreeze()
	if accepted+full != 600 || maxSeen > maxPending || h.l.Outstanding() != 0 {
		t.Fatalf("accepted %d full %d max %d outstanding %d", accepted, full, maxSeen, h.l.Outstanding())
	}
	paid := map[ProofID]int{}
	for _, call := range h.s.markPaidCalls() {
		for _, p := range call {
			paid[p.ProofID]++
		}
	}
	if len(paid) != accepted || len(h.s.rows) != accepted {
		t.Fatalf("paid %d of %d", len(paid), accepted)
	}
	for id, n := range paid {
		if n != 1 {
			t.Errorf("%x paid %d times", id, n)
		}
	}
	if tot := h.l.Totals(); tot.Proofs != uint64(accepted) {
		t.Errorf("proofs %d", tot.Proofs)
	}
}
