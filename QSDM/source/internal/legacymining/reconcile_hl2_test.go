package legacymining

// HL2 WP-D: Reconcile (S7-S14) and the crash classes with a version 2 config
// and three miners: multi-address LMP1 blocks, per-owner counts and owner
// epoch amounts derived at S12, and the v1 -> v2 upgrade of an HL1 DB. The
// HL1 tests in reconcile_test.go are unchanged.

import (
	"crypto/sha256"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

var (
	rcV2Hash  = ConfigHash(sha256.Sum256([]byte("mining-public.json phase 1")))
	rcV2Hash2 = ConfigHash(sha256.Sum256([]byte("mining-public.json phase 2")))
	rcMiners  = []string{otHonest, otVictim, otFlood} // A, B, C
)

// rcV2Config is otPublicConfig on the rcWorld clock.
func rcV2Config() Config {
	c := otPublicConfig()
	c.ExpiresUnix = rcNow.Unix() + 3600
	return c
}

// bootV2 is rcWorld.boot for a version 2 config in public mode: a version 2
// Store (which migrates an HL1 DB at S7), the v2 Guard over an enrollment
// view with the three miners, and the Ledger.
func (w *rcWorld) bootV2(hash ConfigHash, mut func(*Config)) *rcBoot {
	w.t.Helper()
	w.shutdown()
	cfg := rcV2Config()
	if mut != nil {
		mut(&cfg)
	}
	now := func() time.Time { return rcNow }
	b := &rcBoot{w: w, hash: hash, store: NewSQLiteStoreV2()}
	b.store.now = now
	w.cur = b
	view := newOTView()
	for i, m := range rcMiners {
		view.add(fmt.Sprintf("node-%d", i), m, true)
	}
	g, err := NewGuard(GuardOptions{
		Dir: w.dir, Config: cfg, ConfigHash: hash, Release: "hl2-test", Store: b.store,
		FailStop: func(code int, cause string) { b.stops = append(b.stops, fmt.Sprintf("%d:%s", code, cause)) },
		Now:      now, Logf: w.t.Logf,
		Mode: ModePublic, Enrollments: view, OwnerAuth: otAcceptAll,
	})
	if err != nil {
		w.t.Fatalf("NewGuard(v2): %v", err)
	}
	b.guard = g
	if b.ledger, err = NewLedger(LedgerConfig{Store: b.store, Guard: g, Accounts: w.accts, RewardCell: rcCellAt}); err != nil {
		w.t.Fatalf("NewLedger(v2): %v", err)
	}
	b.rep, b.err = Reconcile(ReconcileConfig{
		Store: b.store, DBPath: stPath(w.dir), Guard: g, Ledger: b.ledger, Accounts: w.accts,
		Blocks: w.blocks, Release: "hl2-test", RewardCell: rcCellAt, Now: now,
	})
	return b
}

// acceptFor is §4.1 steps 8-9 for miner.
func (b *rcBoot) acceptFor(miner string) Record {
	b.w.t.Helper()
	rec := b.newRecord(miner)
	if err := b.ledger.Enqueue(rec); err != nil {
		b.w.t.Fatalf("Enqueue: %v", err)
	}
	return rec
}

// acceptThree accepts A x2, B x1, C x3.
func (b *rcBoot) acceptThree() []Record {
	var recs []Record
	for i, n := range []int{2, 1, 3} {
		for range n {
			recs = append(recs, b.acceptFor(rcMiners[i]))
		}
	}
	return recs
}

// prepare is §4.2 steps 2-3 at tip+1: Take, the d7 formula, PreSeal.
func (b *rcBoot) prepare() []*mempool.Tx {
	w := b.w
	w.t.Helper()
	claims := b.ledger.Take()
	if len(claims) == 0 {
		w.t.Fatal("nothing to take")
	}
	txs := rcBuild(claims, w.funderNonce())
	if err := b.ledger.PreSeal(w.tip()+1, rcCell, txs); err != nil {
		w.t.Fatalf("PreSeal: %v", err)
	}
	return txs
}

// mustOwners checks the Ledger's per-owner outstanding counts.
func (b *rcBoot) mustOwners(want map[string]int) {
	b.w.t.Helper()
	for _, m := range rcMiners {
		if got := b.ledger.OutstandingFor(m); got != want[m] {
			b.w.t.Fatalf("OutstandingFor(%s) = %d, want %d", m[:4], got, want[m])
		}
	}
}

// chainPayments counts every LMP1 payment of each ID on the chain.
func (w *rcWorld) chainPayments() map[ProofID]int {
	out := make(map[ProofID]int)
	for _, blk := range w.blocks {
		for _, tx := range blk.Transactions {
			if tx.ContractID != chain.MiningRewardContractID || !reconcileTagged(tx) {
				continue
			}
			ids, err := DecodePayload(tx.Payload)
			if err != nil {
				w.t.Fatal(err)
			}
			for _, id := range ids {
				out[id]++
			}
		}
	}
	return out
}

// mustPaidOnce requires every record to be paid exactly once on the chain,
// to its own miner, and marked so in the DB.
func (b *rcBoot) mustPaidOnce(recs []Record) {
	w := b.w
	w.t.Helper()
	pays := w.chainPayments()
	rows, err := b.store.Lookup(rcIDs(recs...))
	if err != nil {
		w.t.Fatal(err)
	}
	for _, r := range recs {
		if pays[r.ProofID] != 1 {
			w.t.Fatalf("proof %x of %s paid %d times on the chain", r.ProofID[:4], r.MinerAddr[:4], pays[r.ProofID])
		}
		row := rows[r.ProofID]
		if row.PaidTxID == "" || !strings.Contains(row.PaidTxID, r.MinerAddr) {
			w.t.Fatalf("proof %x row %+v", r.ProofID[:4], row)
		}
	}
}

// A block pays three miners (one LMP1 tx each); the restart derives the
// payments, the per-owner rows and the owner epoch amounts from the chain.
func TestReconcileV2MultiAddressPayloads(t *testing.T) {
	w := newRCWorld(t, 9)
	b1 := w.bootV2(rcV2Hash, nil)
	b1.mustClean()
	if v := b1.store.SchemaVersion(); v != StoreUserVersionOperatorKeys {
		t.Fatalf("schema version %d", v)
	}
	recs := b1.acceptThree()
	b1.mustOwners(map[string]int{otHonest: 2, otVictim: 1, otFlood: 3})
	blk := b1.tick()
	if len(blk.Transactions) != 3 {
		t.Fatalf("block %d has %d txs, want one per miner", blk.Height, len(blk.Transactions))
	}
	r4 := b1.acceptFor(otVictim)

	b2 := w.bootV2(rcV2Hash, nil)
	b2.mustClean()
	r := b2.rep
	if r.Paid != 6 || r.Pending != 1 || r.Counts != (ReconcileCounts{}) || r.Totals.Proofs != 7 {
		t.Fatalf("report %+v", r)
	}
	wantOwners := map[string]OwnerCount{otHonest: {2, 2}, otVictim: {2, 1}, otFlood: {3, 3}}
	if !reflect.DeepEqual(r.Owners, wantOwners) {
		t.Fatalf("owners %+v, want %+v", r.Owners, wantOwners)
	}
	if r.OwnerEpoch != 0 || len(r.OwnerEmitted) != 3 {
		t.Fatalf("owner epoch %d emitted %v", r.OwnerEpoch, r.OwnerEmitted)
	}
	for i, tx := range blk.Transactions {
		rcSameFloat(t, "owner emitted "+rcMiners[i][:4], r.OwnerEmitted[tx.Recipient], tx.Amount)
		if _, c := b2.ledger.OwnerEpochEmitted(tx.Recipient); math.Float64bits(c) != math.Float64bits(tx.Amount) {
			t.Fatalf("ledger epoch emitted %v, want %v", c, tx.Amount)
		}
	}
	b2.mustOwners(map[string]int{otVictim: 1})
	b2.tick()
	b2.mustOwners(nil)
	b3 := w.bootV2(rcV2Hash, nil)
	b3.mustClean()
	b3.mustPaidOnce(append(recs, r4))
}

// The crash classes of rev 4 §4.6 with three miners pending: the restart pays
// every proof exactly once, to its own miner, whatever the crash point.
func TestReconcileV2CrashClassesThreeMiners(t *testing.T) {
	type crash struct {
		name string
		// run leaves the world in the crash state after the six proofs
		// are accepted; it returns the expected S11 counts and pending.
		run         func(b *rcBoot) (ReconcileCounts, int)
		paidByCrash bool
	}
	cases := []crash{
		{"C1/C2: in flight, the block is never sealed", func(b *rcBoot) (ReconcileCounts, int) {
			b.prepare()
			return ReconcileCounts{}, 6
		}, false},
		{"C4-C7: the block is durable, H8 never ran", func(b *rcBoot) (ReconcileCounts, int) {
			b.w.seal(b.prepare()...)
			return ReconcileCounts{Updated: 6}, 0
		}, true},
		{"C8: H8 and MarkPaid done", func(b *rcBoot) (ReconcileCounts, int) {
			b.tick()
			return ReconcileCounts{}, 0
		}, true},
		{"truncated tail: the paying block is lost", func(b *rcBoot) (ReconcileCounts, int) {
			b.tick()
			b.w.trim(b.w.tip() - 1)
			return ReconcileCounts{Reset: 6}, 6
		}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := newRCWorld(t, 9)
			b1 := w.bootV2(rcV2Hash, func(cfg *Config) { cfg.MaxPendingPerOwner = 3 })
			b1.mustClean()
			recs := b1.acceptThree()
			wantCounts, wantPending := c.run(b1)

			b2 := w.bootV2(rcV2Hash, func(cfg *Config) { cfg.MaxPendingPerOwner = 3 })
			b2.mustClean()
			if b2.rep.Counts != wantCounts || b2.rep.Pending != wantPending {
				t.Fatalf("report %+v; want counts %+v pending %d", b2.rep, wantCounts, wantPending)
			}
			if wantPending == 6 {
				b2.mustOwners(map[string]int{otHonest: 2, otVictim: 1, otFlood: 3})
				// S13 told the guard: C is at max_pending_per_owner (3).
				wantKind(t, "C after S13", b2.guard.CheckOwnerPending(otFlood), KindOwnerPendingFull)
				if err := b2.guard.CheckOwnerPending(otHonest); err != nil {
					t.Fatalf("A after S13: %v", err)
				}
				blk := b2.tick()
				if len(blk.Transactions) != 3 {
					t.Fatalf("the reloaded proofs were not paid in one 3-miner block: %+v", blk.Transactions)
				}
			} else if len(b2.rep.OwnerEmitted) != 3 {
				t.Fatalf("owner epoch amounts after the crash: %v", b2.rep.OwnerEmitted)
			}
			b2.mustOwners(nil)
			if err := b2.guard.CheckOwnerPending(otFlood); err != nil {
				t.Fatalf("C after payment: %v", err)
			}
			b3 := w.bootV2(rcV2Hash, nil)
			b3.mustClean()
			if b3.rep.Paid != 6 || b3.rep.Pending != 0 || b3.rep.Counts != (ReconcileCounts{}) {
				t.Fatalf("final report %+v", b3.rep)
			}
			b3.mustPaidOnce(recs)
			var sum float64
			for _, rw := range b3.rep.OwnerEmitted {
				sum += rw
			}
			if math.Abs(sum-rcCell) > rcCell*0x1p-50 {
				t.Fatalf("one block of three miners emitted %v, want %v", sum, rcCell)
			}
		})
	}
}

// Owner epochs at S12: the epoch is that of tip+1, it counts only the blocks
// of that epoch in the config window, and the cap holds admission across a
// restart.
func TestReconcileV2OwnerEpochAcrossBoundary(t *testing.T) {
	w := newRCWorld(t, OwnerEpochBlocks-4) // tip 8636
	capped := func(cfg *Config) { cfg.OwnerEpochCapCell = 5 }
	b1 := w.bootV2(rcV2Hash, capped)
	b1.mustClean()
	b1.acceptFor(otHonest)
	b1.acceptFor(otVictim)
	b1.tick() // 8637: A and B half each
	b1.acceptFor(otHonest)
	b1.tick() // 8638: A whole
	wantA := rcCell/2 + rcCell

	b2 := w.bootV2(rcV2Hash, capped)
	b2.mustClean()
	if b2.rep.OwnerEpoch != 0 {
		t.Fatalf("epoch %d", b2.rep.OwnerEpoch)
	}
	rcSameFloat(t, "A", b2.rep.OwnerEmitted[otHonest], wantA)
	rcSameFloat(t, "B", b2.rep.OwnerEmitted[otVictim], rcCell/2)
	wantKind(t, "A over the cap after a restart", b2.ledger.CheckOwnerEpoch(otHonest), KindOwnerRateLimited)
	if err := b2.ledger.CheckOwnerEpoch(otVictim); err != nil {
		t.Fatal(err)
	}
	b2.tick() // 8639, the last block of epoch 0

	b3 := w.bootV2(rcV2Hash, capped)
	b3.mustClean()
	if b3.rep.OwnerEpoch != 1 || len(b3.rep.OwnerEmitted) != 0 {
		t.Fatalf("at tip 8639: epoch %d emitted %v", b3.rep.OwnerEpoch, b3.rep.OwnerEmitted)
	}
	if err := b3.ledger.CheckOwnerEpoch(otHonest); err != nil {
		t.Fatalf("A in epoch 1: %v", err)
	}
	b3.acceptFor(otFlood)
	b3.tick() // 8640

	// A new config window starting above the epoch's first height counts
	// only its own blocks.
	b4 := w.bootV2(rcV2Hash2, capped)
	b4.mustClean()
	if b4.rep.Window.FirstHeight != 8641 || len(b4.rep.OwnerEmitted) != 0 || len(b4.rep.Owners) != 0 {
		t.Fatalf("new window %+v emitted %v owners %v", b4.rep.Window, b4.rep.OwnerEmitted, b4.rep.Owners)
	}
	b5 := w.bootV2(rcV2Hash, capped)
	b5.mustClean()
	if b5.rep.OwnerEpoch != 1 || len(b5.rep.OwnerEmitted) != 1 {
		t.Fatalf("back on the first window: %d %v", b5.rep.OwnerEpoch, b5.rep.OwnerEmitted)
	}
	rcSameFloat(t, "C", b5.rep.OwnerEmitted[otFlood], rcCell)
}

// S10 with several recipients per block.
func TestReconcileV2Anomalies(t *testing.T) {
	setup := func(t *testing.T) (*rcWorld, []Record, uint64) {
		w := newRCWorld(t, 9)
		b := w.bootV2(rcV2Hash, nil)
		b.mustClean()
		var recs []Record
		for _, m := range rcMiners {
			recs = append(recs, b.newRecord(m))
		}
		return w, recs, w.funderNonce()
	}
	t.Run("control", func(t *testing.T) {
		w, recs, n := setup(t)
		w.add(ltRewardTx(n, otHonest, rcCell/3, recs[0].ProofID), ltRewardTx(n+1, otVictim, rcCell/3, recs[1].ProofID),
			ltRewardTx(n+2, otFlood, rcCell/3, recs[2].ProofID))
		b := w.bootV2(rcV2Hash, nil)
		b.mustClean()
		if b.rep.Counts.Updated != 3 {
			t.Fatalf("report %+v", b.rep)
		}
	})
	t.Run("a payload paid to another miner", func(t *testing.T) {
		w, recs, n := setup(t)
		w.add(ltRewardTx(n, otHonest, rcCell/2, recs[0].ProofID), ltRewardTx(n+1, otVictim, rcCell/2, recs[2].ProofID))
		w.bootV2(rcV2Hash, nil).mustTrip("S10", CauseReconcile+":S10: height 10 tx \"solo-reward-")
		if b := w.cur; !strings.Contains(b.err.Error(), "belongs to "+otFlood+", paid to "+otVictim) {
			t.Fatalf("err %v", b.err)
		}
	})
	t.Run("two rewards to one recipient in a block", func(t *testing.T) {
		w, recs, n := setup(t)
		w.add(ltRewardTx(n, otHonest, rcCell/2, recs[0].ProofID), ltRewardTx(n+1, otHonest, rcCell/2, recs[1].ProofID))
		b := w.bootV2(rcV2Hash, nil)
		b.mustTrip("S10", CauseReconcile+":S10:")
		if !strings.Contains(b.err.Error(), "a second reward to "+otHonest+" in one block") {
			t.Fatalf("err %v", b.err)
		}
	})
}

// An HL1 DB under a v1 config, then a v2 boot: S7 migrates it, the HL1
// window's pending rows are paid to their own miner (v2 I6 checks rows, not
// an allowlist), and the new window's owner counts start empty.
func TestReconcileV1ToV2Upgrade(t *testing.T) {
	w := newRCWorld(t, 9)
	b1 := w.bootHash(rcHash1)
	b1.mustClean()
	b1.accept()
	b1.tick()
	pend := b1.accept()
	if v := b1.store.SchemaVersion(); v != StoreUserVersion {
		t.Fatalf("v1 boot schema %d", v)
	}

	b2 := w.bootV2(rcV2Hash, nil)
	b2.mustClean()
	if v := b2.store.SchemaVersion(); v != StoreUserVersionOperatorKeys {
		t.Fatalf("v2 boot did not migrate: %d", v)
	}
	if b2.rep.Pending != 1 || b2.rep.Paid != 1 || len(b2.rep.Owners) != 0 || b2.rep.Totals.Proofs != 0 {
		t.Fatalf("report %+v", b2.rep)
	}
	b2.mustOwners(nil)
	if b2.ledger.OutstandingFor(stMiner) != 1 {
		t.Fatal("the HL1 pending row is not outstanding")
	}
	a := b2.acceptFor(otHonest)
	blk := b2.tick()
	if len(blk.Transactions) != 2 {
		t.Fatalf("block %+v", blk.Transactions)
	}
	b3 := w.bootV2(rcV2Hash, nil)
	b3.mustClean()
	b3.mustPaidOnce([]Record{pend, a})
	if !reflect.DeepEqual(b3.rep.Owners, map[string]OwnerCount{otHonest: {1, 1}}) {
		t.Fatalf("owners %+v", b3.rep.Owners)
	}
}
