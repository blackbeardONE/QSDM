package legacymining

// HL2 WP-D: the Ledger with a version 2 config. Three addresses, per-owner
// outstanding counts and their guard hooks, I6 against the row's miner_addr,
// the owner epoch cap, unenroll between accept and seal, and the R4 pro-rata
// split. The HL1 (v1) Ledger tests in ledger_test.go are unchanged.

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/mining"
	"github.com/blackbeardONE/QSDM/pkg/mining/enrollment"
)

// ltV2Config is a public-mode v2 config for the Ledger tests: no allowlist.
func ltV2Config() Config {
	return Config{
		Version: ConfigVersion2, MaxProofsPerMin: 600, MaxProofsPerMinPerOwner: 60, MaxProofsTotal: 3000,
		MaxPending: 600, MaxPendingPerOwner: 200, OwnerEpochCapCell: 100, DifficultyBits: 16,
		RequireOperatorSig: true, RequireFullyBonded: true, BondedSlotCap: 1, BudgetCell: 1291, ExpiresUnix: 1,
	}
}

// ledgerFakeOwnerGuard is a ledgerFakeGuard with the OwnerGuard methods. It
// records the Ledger's outstanding reports.
type ledgerFakeOwnerGuard struct {
	*ledgerFakeGuard
	omu    sync.Mutex
	counts map[string]int
	resets int
	sets   int
}

func (g *ledgerFakeOwnerGuard) TakeOwnerRate(Candidate) error          { return nil }
func (g *ledgerFakeOwnerGuard) ObserveOwnerRejection(Candidate, error) {}
func (g *ledgerFakeOwnerGuard) CheckOwnerPending(string) error         { return nil }
func (g *ledgerFakeOwnerGuard) SetOwnerOutstanding(owner string, n int) {
	g.omu.Lock()
	defer g.omu.Unlock()
	g.sets++
	if n <= 0 {
		delete(g.counts, owner)
	} else {
		g.counts[owner] = n
	}
}
func (g *ledgerFakeOwnerGuard) ResetOwnerOutstanding(m map[string]int) {
	g.omu.Lock()
	defer g.omu.Unlock()
	g.resets++
	g.counts = make(map[string]int)
	for k, n := range m {
		if n > 0 {
			g.counts[k] = n
		}
	}
}
func (g *ledgerFakeOwnerGuard) reported() map[string]int {
	g.omu.Lock()
	defer g.omu.Unlock()
	out := make(map[string]int, len(g.counts))
	for k, n := range g.counts {
		out[k] = n
	}
	return out
}

var _ OwnerGuard = (*ledgerFakeOwnerGuard)(nil)

// newLedgerHarnessV2 is newLedgerHarnessRaw with a v2 config and an
// OwnerGuard. init runs Init with the store's pending rows.
func newLedgerHarnessV2(t *testing.T, init bool) (*ledgerHarness, *ledgerFakeOwnerGuard) {
	t.Helper()
	h := newLedgerHarnessRaw(t)
	h.g.set(func(g *ledgerFakeGuard) { g.cfg = ltV2Config() })
	og := &ledgerFakeOwnerGuard{ledgerFakeGuard: h.g, counts: map[string]int{}}
	l, err := NewLedger(LedgerConfig{Store: h.s, Guard: og, Accounts: h.accts, RewardCell: h.rewardCell})
	if err != nil {
		t.Fatalf("NewLedger(v2): %v", err)
	}
	h.l = l
	if init {
		if err := h.l.Init(h.initState(Totals{ConfigSHA256: ltHash})); err != nil {
			t.Fatalf("Init: %v", err)
		}
	}
	return h, og
}

func sameCounts(t *testing.T, what string, got, want map[string]int) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
	for k, n := range want {
		if got[k] != n {
			t.Fatalf("%s = %v, want %v", what, got, want)
		}
	}
}

func TestLedgerV2RequiresOwnerGuard(t *testing.T) {
	h := newLedgerHarnessRaw(t)
	h.g.set(func(g *ledgerFakeGuard) { g.cfg = ltV2Config() })
	if _, err := NewLedger(LedgerConfig{Store: h.s, Guard: h.g, Accounts: h.accts}); err == nil || !strings.Contains(err.Error(), "OwnerGuard") {
		t.Fatalf("NewLedger(v2, plain Guard) = %v; want refusal", err)
	}
	// A v1 config never needs it, and CheckOwnerEpoch is a no-op.
	h1 := newLedgerHarness(t)
	h1.cell = 50
	h1.accept(ltMinerA)
	if _, err := h1.tick(); err != nil {
		t.Fatal(err)
	}
	if err := h1.l.CheckOwnerEpoch(ltMinerA); err != nil {
		t.Fatalf("v1 CheckOwnerEpoch: %v", err)
	}
	if e, c := h1.l.OwnerEpochEmitted(ltMinerA); e != 0 || c != 0 {
		t.Fatalf("v1 owner epoch %d emitted %v", e, c)
	}
	if h1.l.OutstandingFor(ltMinerA) != 0 {
		t.Fatal("v1 OutstandingFor after payment")
	}
}

// I1-I7 with three addresses (A: 2 IDs, B: 1, C: 3) and no allowlist.
func TestLedgerV2InvariantsThreeAddresses(t *testing.T) {
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
		first string
		all   []string
		not   []string
	}{
		{"clean", func(f *fix) {}, "", nil, nil},
		{"clean with an allowlist that lists only A (v2 I6 ignores it)", func(f *fix) { f.h.g.setAllowed(ltMinerA) }, "", nil, nil},
		{"I1 amount bits", func(f *fix) { f.txs[2].Amount = math.Nextafter(f.txs[2].Amount, 0) },
			"invariant:I1", []string{"differs from its in-flight record"}, []string{"I5", "I3", "I6"}},
		{"I1 and I6 recipient", func(f *fix) { f.txs[1].Recipient = ltMinerC },
			"invariant:I1", []string{"differs", "invariant:I6", "belongs to " + ltMinerB + ", not the recipient " + ltMinerC, "mark paid"}, []string{"I5"}},
		{"I2 repeated in the block", func(f *fix) {
			id := f.h.accept(ltMinerB)
			f.txs = append(f.txs, ltRewardTx(extraNonce(f), ltMinerB, 0.1, id), ltRewardTx(extraNonce(f)+1, ltMinerB, 0.1, id))
		}, "invariant:I1", []string{"invariant:I2", "repeated or already paid"}, nil},
		{"I3 schedule", func(f *fix) { f.h.cell = f.cell / 2 }, "invariant:I3", nil, []string{"I1", "I5", "I6"}},
		{"I4 nonce", func(f *fix) {
			f.h.beforeH8 = func() {
				if err := f.h.accts.ChargeAndBumpNonce(ltFunder, 0, f.h.funder().Nonce); err != nil {
					panic(err)
				}
			}
		}, "invariant:I4", nil, []string{"I5", "I1", "I6"}},
		{"I5 balance one ULP", func(f *fix) { f.h.beforeH8 = func() { f.h.accts.Credit(ltFunder, 0.125) } },
			"invariant:I5", []string{"bits"}, []string{"I4", "I1", "I6"}},
		{"I7 enrollment tx", func(f *fix) {
			f.txs = append(f.txs, &mempool.Tx{ID: "e", Sender: ltMinerC, Nonce: 0, ContractID: enrollment.ContractID})
		}, CauseTxFamily, []string{enrollment.ContractID}, []string{"invariant:I"}},
		{"MarkPaid I/O", func(f *fix) { f.h.s.err = errors.New("disk I/O error") },
			CauseMarkPaidIO + ":disk I/O error", []string{"mark paid"}, []string{"invariant:I"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, og := newLedgerHarnessV2(t, true)
			h.accept(ltMinerA)
			h.accept(ltMinerB)
			h.accept(ltMinerC)
			h.accept(ltMinerA)
			h.accept(ltMinerC)
			h.accept(ltMinerC)
			sameCounts(t, "reported before", og.reported(), map[string]int{ltMinerA: 2, ltMinerB: 1, ltMinerC: 3})
			claims, txs, err := h.prepare()
			if err != nil || len(claims) != 3 {
				t.Fatalf("PreSeal: %v, %d claims", err, len(claims))
			}
			f := &fix{h: h, claims: claims, txs: txs, cell: h.rewardCell(h.height + 1)}
			c.mut(f)
			blk, err := h.seal(true, f.txs...)
			if len(blk.Transactions) != len(f.txs) {
				t.Fatalf("fixture: %d of %d txs applied", len(blk.Transactions), len(f.txs))
			}
			// W3 in every case: the six IDs of the durable block are paid,
			// and the guard is told each owner's count (here 0 or the extra
			// I2 ID).
			for _, cl := range claims {
				for _, id := range cl.IDs {
					if _, ok := h.l.paid[id]; !ok {
						t.Fatalf("ID %x of %s not paid", id[:4], cl.MinerAddr[:4])
					}
				}
				if n := h.l.OutstandingFor(cl.MinerAddr); og.reported()[cl.MinerAddr] != n {
					t.Fatalf("%s: reported %d, ledger %d", cl.MinerAddr[:4], og.reported()[cl.MinerAddr], n)
				}
			}
			if c.first == "" {
				if err != nil {
					t.Fatalf("clean block: %v", err)
				}
				h.noFreeze()
				calls := h.s.markPaidCalls()
				if len(calls) != 1 || len(calls[0]) != 6 || h.l.Outstanding() != 0 || len(og.reported()) != 0 {
					t.Fatalf("MarkPaid calls %v, outstanding %d, reported %v", calls, h.l.Outstanding(), og.reported())
				}
				for _, p := range calls[0] {
					if h.s.rows[p.ProofID].MinerAddr != p.MinerAddr || p.Height != blk.Height {
						t.Errorf("payment %+v", p)
					}
				}
				sum := 0.0
				for i, tx := range txs {
					sum += tx.Amount
					if _, got := h.l.OwnerEpochEmitted(claims[i].MinerAddr); math.Float64bits(got) != math.Float64bits(tx.Amount) {
						t.Errorf("%s epoch emitted %v, want %v", claims[i].MinerAddr[:4], got, tx.Amount)
					}
				}
				rcSameFloat(t, "emitted", h.l.Totals().Emitted, sum)
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
		})
	}
}

// PreSeal with no allowlist: the recipients are checked against the rows
// only.
func TestLedgerV2PreSealRecipients(t *testing.T) {
	h, _ := newLedgerHarnessV2(t, true)
	a, b := h.accept(ltMinerA), h.accept(ltMinerB)
	claims := h.l.Take()
	cell := h.rewardCell(h.height + 1)
	txs := h.build(claims, cell)
	if err := h.l.PreSeal(h.height+1, cell, txs); err != nil {
		t.Fatalf("PreSeal with an empty allowlist: %v", err)
	}
	h.noFreeze()
	h.l.Requeue()

	// B's ID in a tx to C: refused before sealing.
	claims = h.l.Take()
	txs = h.build(claims, cell)
	txs[1] = ltRewardTx(txs[1].Nonce, ltMinerC, txs[1].Amount, b)
	err := h.l.PreSeal(h.height+1, cell, txs)
	if !errors.Is(err, ErrPreSeal) || !strings.Contains(err.Error(), "belongs to "+ltMinerB+", not the recipient") {
		t.Fatalf("PreSeal = %v", err)
	}
	h.firstFreeze(CausePreSeal + ":")
	if h.l.OutstandingFor(ltMinerA) != 1 || h.l.OutstandingFor(ltMinerB) != 1 || a == b {
		t.Fatal("IDs not back to pending")
	}
}

// Per-owner outstanding: Init resets the guard's map, Enqueue and H8 update
// it, partial inclusion keeps the unpaid owner's count, and Enqueue enforces
// max_pending_per_owner like max_pending.
func TestLedgerV2OwnerOutstanding(t *testing.T) {
	h, og := newLedgerHarnessV2(t, false)
	for _, a := range []string{ltMinerA, ltMinerB, ltMinerA, ltMinerC, ltMinerA} {
		_ = h.s.Accept(h.record(a))
	}
	og.ResetOwnerOutstanding(map[string]int{strings.Repeat("f", 64): 9}) // stale state from nowhere
	if err := h.l.Init(h.initState(Totals{ConfigSHA256: ltHash})); err != nil {
		t.Fatal(err)
	}
	want := map[string]int{ltMinerA: 3, ltMinerB: 1, ltMinerC: 1}
	sameCounts(t, "after Init", og.reported(), want)
	if og.resets != 2 {
		t.Fatalf("resets %d", og.resets)
	}
	for a, n := range want {
		if h.l.OutstandingFor(a) != n {
			t.Fatalf("OutstandingFor(%s) = %d", a[:4], h.l.OutstandingFor(a))
		}
	}

	// Partial inclusion: only A's tx is sealed.
	_, txs, err := h.prepare()
	if err != nil || len(txs) != 3 {
		t.Fatalf("prepare: %v", err)
	}
	d := h.accept(ltMinerB) // while the others are in flight
	sameCounts(t, "after Enqueue", og.reported(), map[string]int{ltMinerA: 3, ltMinerB: 2, ltMinerC: 1})
	if _, err := h.seal(true, txs[0]); err != nil {
		t.Fatal(err)
	}
	h.l.Requeue()
	h.noFreeze()
	sameCounts(t, "after A is paid", og.reported(), map[string]int{ltMinerB: 2, ltMinerC: 1})
	if _, err := h.tick(); err != nil {
		t.Fatal(err)
	}
	h.noFreeze()
	if _, ok := h.l.paid[d]; !ok || len(og.reported()) != 0 || h.l.Outstanding() != 0 {
		t.Fatalf("after the second tick: reported %v outstanding %d", og.reported(), h.l.Outstanding())
	}

	// max_pending_per_owner at Enqueue: an invariant violation (miningsvc
	// checks the guard's copy first, under submitMu).
	h.g.set(func(g *ledgerFakeGuard) { g.cfg.MaxPendingPerOwner = 2 })
	h.accept(ltMinerC)
	h.accept(ltMinerC)
	h.accept(ltMinerA) // other owners are not affected
	over := h.record(ltMinerC)
	if err := h.l.Enqueue(over); !errors.Is(err, ErrInvariant) || h.l.OutstandingFor(ltMinerC) != 2 {
		t.Fatalf("Enqueue over the owner cap: %v, outstanding %d", err, h.l.OutstandingFor(ltMinerC))
	}
	h.firstFreeze(fmt.Sprintf("%s:%x: owner %s pending plus in-flight 2 is at max_pending_per_owner", CauseEnqueue, over.ProofID, ltMinerC))
}

// The real v2 guard sees the Ledger's counts: CheckOwnerPending fires at
// max_pending_per_owner and clears once the owner is paid.
func TestLedgerV2OwnerPendingThroughGuard(t *testing.T) {
	cfg := otPublicConfig()
	cfg.MaxPendingPerOwner = 2
	view := newOTView().add("na", otHonest, true).add("nb", otVictim, true)
	e := otNew(t, gtDir(t), cfg, view, otOptions{})
	h := newLedgerHarnessRaw(t)
	l, err := NewLedger(LedgerConfig{Store: h.s, Guard: e.g, Accounts: h.accts, RewardCell: h.rewardCell})
	if err != nil {
		t.Fatal(err)
	}
	h.l = l
	hash := e.g.ConfigHash()
	pre := ltV2Record(h, otHonest, hash)
	_ = h.s.Accept(pre)
	if err := h.l.Init(LedgerInit{Pending: []Record{pre}, Totals: Totals{ConfigSHA256: hash},
		FunderBalance: h.funder().Balance, FunderNonce: h.funder().Nonce}); err != nil {
		t.Fatal(err)
	}
	if err := e.g.CheckOwnerPending(otHonest); err != nil {
		t.Fatalf("1 of 2: %v", err)
	}
	ltV2Accept(t, h, otHonest, hash)
	wantKind(t, "owner at max_pending_per_owner", e.g.CheckOwnerPending(otHonest), KindOwnerPendingFull)
	if err := e.g.CheckOwnerPending(otVictim); err != nil {
		t.Fatalf("another owner: %v", err)
	}
	if _, err := h.tick(); err != nil {
		t.Fatal(err)
	}
	if err := e.g.CheckOwnerPending(otHonest); err != nil {
		t.Fatalf("after payment: %v", err)
	}
	e.requireNoLatch0(t)
}

// requireNoLatch0 is requireNoLatch without the admission predicate (the
// guard was never activated).
func (e *gtEnv) requireNoLatch0(t *testing.T) {
	t.Helper()
	if s := e.g.State(); s != StateOpen {
		t.Fatalf("state %v, want OPEN", s)
	}
	if gtExists(t, e.dir+"/"+TrippedFile) || gtExists(t, e.dir+"/"+AdmissionStoppedFile) {
		t.Fatal("latch marker present")
	}
}

func ltV2Record(h *ledgerHarness, owner string, hash ConfigHash) Record {
	rec := h.record(owner)
	rec.ConfigSHA256 = hash
	return rec
}

func ltV2Accept(t *testing.T, h *ledgerHarness, owner string, hash ConfigHash) ProofID {
	t.Helper()
	rec := ltV2Record(h, owner, hash)
	if err := h.s.Accept(rec); err != nil {
		t.Fatal(err)
	}
	if err := h.l.Enqueue(rec); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	return rec.ProofID
}

// owner_epoch_cap_cell: the cap holds admission for the owner that reached
// it (503, owner-rate-limited, "owner epoch cap"), never payouts, and lifts at
// the first height of the next owner epoch.
func TestLedgerV2OwnerEpochCap(t *testing.T) {
	h, _ := newLedgerHarnessV2(t, true)
	h.g.set(func(g *ledgerFakeGuard) { g.cfg.OwnerEpochCapCell = 2 })
	h.cell = 1.5
	h.accept(ltMinerA)
	h.accept(ltMinerB)
	if _, err := h.tick(); err != nil { // A 0.75, B 0.75
		t.Fatal(err)
	}
	for _, a := range []string{ltMinerA, ltMinerB, ltMinerC} {
		if err := h.l.CheckOwnerEpoch(a); err != nil {
			t.Fatalf("below the cap: %s: %v", a[:4], err)
		}
	}
	// Accepted before the cap is reached: paid in full, past the cap.
	h.accept(ltMinerA)
	h.accept(ltMinerA)
	if _, err := h.tick(); err != nil { // A alone: 1.5 -> 2.25
		t.Fatal(err)
	}
	h.noFreeze()
	epoch, got := h.l.OwnerEpochEmitted(ltMinerA)
	if epoch != OwnerEpochOf(h.height+1) || got != 2.25 {
		t.Fatalf("A: epoch %d emitted %v", epoch, got)
	}
	err := h.l.CheckOwnerEpoch(ltMinerA)
	wantKind(t, "A at the cap", err, KindOwnerRateLimited)
	if !errors.Is(err, ErrUnavailable) || KindOwnerRateLimited.HTTPStatus() != http.StatusServiceUnavailable ||
		!strings.Contains(err.Error(), "owner epoch cap: 2.25 of 2 CELL emitted in owner epoch 0 (heights 0..8639); admission resumes at height 8640") {
		t.Fatalf("cap rejection %v", err)
	}
	if err := h.l.CheckOwnerEpoch(ltMinerB); err != nil {
		t.Fatalf("B: %v", err)
	}
	// Proofs A had pending are still paid while capped.
	h.accept(ltMinerA)
	if _, err := h.tick(); err != nil || h.l.Outstanding() != 0 {
		t.Fatalf("payout while capped: %v, outstanding %d", err, h.l.Outstanding())
	}
	h.noFreeze()

	// The last block of epoch 0 keeps the cap; the next block opens epoch 1.
	h.height = OwnerEpochBlocks - 2
	if _, err := h.tick(); err != nil { // heartbeat at 8639
		t.Fatal(err)
	}
	if e, _ := h.l.OwnerEpochEmitted(ltMinerA); e != 1 {
		t.Fatalf("after height %d the epoch is %d, want 1 (the next block's)", h.height, e)
	}
	if err := h.l.CheckOwnerEpoch(ltMinerA); err != nil {
		t.Fatalf("new epoch: %v", err)
	}
	h.accept(ltMinerA)
	if _, err := h.tick(); err != nil { // 8640: A 1.5 in epoch 1
		t.Fatal(err)
	}
	if e, c := h.l.OwnerEpochEmitted(ltMinerA); e != 1 || c != 1.5 {
		t.Fatalf("epoch 1: %d %v", e, c)
	}
	h.noFreeze()
}

// Init takes the S12 per-owner epoch amounts for the epoch of tip+1.
func TestLedgerV2InitOwnerEmitted(t *testing.T) {
	h, _ := newLedgerHarnessV2(t, false)
	h.g.set(func(g *ledgerFakeGuard) { g.cfg.OwnerEpochCapCell = 5 })
	h.height = 2*OwnerEpochBlocks - 3
	st := h.initState(Totals{ConfigSHA256: ltHash})
	st.Tip, st.OwnerEmitted = h.height, map[string]float64{ltMinerA: 5, ltMinerB: 4.5, ltMinerC: 0}
	if err := h.l.Init(st); err != nil {
		t.Fatal(err)
	}
	wantKind(t, "A from S12", h.l.CheckOwnerEpoch(ltMinerA), KindOwnerRateLimited)
	if err := h.l.CheckOwnerEpoch(ltMinerB); err != nil {
		t.Fatal(err)
	}
	if e, c := h.l.OwnerEpochEmitted(ltMinerB); e != 1 || c != 4.5 {
		t.Fatalf("B: %d %v", e, c)
	}
	if _, err := h.tick(); err != nil { // 2*8640-2, still epoch 1
		t.Fatal(err)
	}
	wantKind(t, "A in the same epoch", h.l.CheckOwnerEpoch(ltMinerA), KindOwnerRateLimited)
	if _, err := h.tick(); err != nil { // 2*8640-1: the next block is in epoch 2
		t.Fatal(err)
	}
	if err := h.l.CheckOwnerEpoch(ltMinerA); err != nil {
		t.Fatalf("epoch 2: %v", err)
	}

	for name, bad := range map[string]float64{"NaN": math.NaN(), "negative": -1, "Inf": math.Inf(1)} {
		t.Run(name, func(t *testing.T) {
			h, _ := newLedgerHarnessV2(t, false)
			st := h.initState(Totals{ConfigSHA256: ltHash})
			st.OwnerEmitted = map[string]float64{ltMinerA: bad}
			if err := h.l.Init(st); !errors.Is(err, ErrReconcile) {
				t.Fatalf("Init = %v", err)
			}
			h.firstFreeze(CauseReconcile + ":ledger-init: owner " + ltMinerA)
		})
	}
	// A v1 Ledger ignores the v2 fields.
	h1 := newLedgerHarnessRaw(t)
	st1 := h1.initState(Totals{ConfigSHA256: ltHash})
	st1.OwnerEmitted = map[string]float64{ltMinerA: math.NaN()}
	if err := h1.l.Init(st1); err != nil {
		t.Fatalf("v1 Init: %v", err)
	}
}

// An unenroll between accept and seal (HL2 §1 Q4): the proofs Precheck
// attributed to the owner are paid to it, and nothing trips, whether the node
// is revoked while the ID is pending or while it is in flight.
func TestLedgerV2UnenrollBetweenAcceptAndSeal(t *testing.T) {
	view := newOTView().add("node-a", otHonest, true).add("node-b", otVictim, true).add("node-c", otFlood, true)
	e := otNew(t, gtDir(t), otPublicConfig(), view, otOptions{})
	h := newLedgerHarnessRaw(t)
	l, err := NewLedger(LedgerConfig{Store: h.s, Guard: e.g, Accounts: h.accts, RewardCell: h.rewardCell})
	if err != nil {
		t.Fatal(err)
	}
	h.l = l
	hash := e.g.ConfigHash()
	if err := h.l.Init(LedgerInit{Totals: Totals{ConfigSHA256: hash}, FunderBalance: h.funder().Balance, FunderNonce: h.funder().Nonce}); err != nil {
		t.Fatal(err)
	}
	// §4.1 steps 4 and 8-9 with the real v2 Precheck.
	admit := func(owner, node string) ProofID {
		t.Helper()
		raw, _, _ := gtProof(t, e.clock.Now(), owner, node, mining.AttestationTypeHMAC)
		c, err := e.g.Precheck(raw)
		if err != nil || c.Owner != owner {
			t.Fatalf("Precheck(%s): %+v, %v", node, c, err)
		}
		rec := ltV2Record(h, c.Owner, hash)
		rec.NodeID = c.NodeID
		if err := h.s.Accept(rec); err != nil {
			t.Fatal(err)
		}
		if err := h.l.Enqueue(rec); err != nil {
			t.Fatal(err)
		}
		return rec.ProofID
	}
	a := admit(otHonest, "node-a")
	b := admit(otVictim, "node-b")
	view.revoke("node-a") // pending
	if _, err := e.g.Precheck(mustProof(t, e, otHonest, "node-a")); RejectKindOf(err) != KindNotEnrolled {
		t.Fatalf("revoked node still admitted: %v", err)
	}
	if _, err := h.tick(); err != nil {
		t.Fatalf("tick: %v", err)
	}
	c := admit(otFlood, "node-c")
	claims, txs, err := h.prepare()
	if err != nil || len(claims) != 1 {
		t.Fatalf("prepare: %v", err)
	}
	view.revoke("node-c") // in flight
	if _, err := h.seal(true, txs...); err != nil {
		t.Fatalf("seal: %v", err)
	}
	h.l.Requeue()
	for _, id := range []ProofID{a, b, c} {
		if _, ok := h.l.paid[id]; !ok {
			t.Fatalf("%x not paid", id[:4])
		}
		if r := h.s.rows[id]; r.PaidTxID == "" {
			t.Fatalf("%x not marked paid", id[:4])
		}
	}
	e.requireNoLatch0(t)
	if e.g.frozenCause != "" {
		t.Fatalf("frozen: %s", e.g.frozenCause)
	}
}

func mustProof(t *testing.T, e *gtEnv, owner, node string) []byte {
	t.Helper()
	raw, _, _ := gtProof(t, e.clock.Now(), owner, node, mining.AttestationTypeHMAC)
	return raw
}

// R4: each non-empty block pays the d7 pro-rata split of rewardCell by
// admitted proofs; each share has the exact d7 float bits, the shares sum to
// rewardCell up to float rounding (far inside PreSeal's and I3's
// rewardCell*(1+RewardSumSlack)), and a block with nothing pending emits 0.
func TestLedgerV2ProRataEmission(t *testing.T) {
	h, _ := newLedgerHarnessV2(t, true)
	h.g.set(func(g *ledgerFakeGuard) {
		g.cfg.BudgetCell, g.cfg.OwnerEpochCapCell = 1<<30, 1<<30
		g.cfg.MaxPending, g.cfg.MaxPendingPerOwner = MaxPendingLimit, MaxPendingLimit
	})
	h.cell = 3.56490987
	addrs := []string{ltMinerA, ltMinerB, ltMinerC}
	rounds := [][3]int{{5, 3, 1}, {1, 1, 1}, {7, 0, 2}, {0, 0, 0}, {1000, 23, 1}, {2, 0, 0}, {13, 17, 19}}
	emitted := map[string]float64{}
	var total float64
	for i, counts := range rounds {
		n := 0
		for j, k := range counts {
			for range k {
				h.accept(addrs[j])
			}
			n += k
		}
		before := h.funder().Balance
		blk, err := h.tick()
		if err != nil {
			t.Fatalf("round %d: %v", i, err)
		}
		h.noFreeze()
		var sum float64
		paid := 0
		for _, tx := range blk.Transactions {
			if tx.ContractID != chain.MiningRewardContractID {
				continue
			}
			k := counts[strings.Index("abc", tx.Recipient[:1])]
			want := h.cell * float64(k) / float64(n)
			if math.Float64bits(tx.Amount) != math.Float64bits(want) {
				t.Fatalf("round %d: %s share %v, want %v", i, tx.Recipient[:4], tx.Amount, want)
			}
			sum += tx.Amount
			total += tx.Amount // block and tx order, as Totals.Emitted
			emitted[tx.Recipient] += tx.Amount
			paid++
		}
		if n == 0 {
			if paid != 0 || h.funder().Balance != before {
				t.Fatalf("round %d: an empty block emitted", i)
			}
			continue
		}
		if rel := math.Abs(sum-h.cell) / h.cell; rel > 0x1p-50 {
			t.Fatalf("round %d: sum %v differs from rewardCell %v by %g relative", i, sum, h.cell, rel)
		}
		if !(sum <= h.cell*(1+RewardSumSlack)) {
			t.Fatalf("round %d: sum %v above the PreSeal bound", i, sum)
		}
	}
	rcSameFloat(t, "Totals.Emitted", h.l.Totals().Emitted, total)
	for _, a := range addrs {
		if _, got := h.l.OwnerEpochEmitted(a); math.Float64bits(got) != math.Float64bits(emitted[a]) {
			t.Fatalf("%s epoch emitted %v, want %v", a[:4], got, emitted[a])
		}
	}
}
