package blockdriver

// HL2 WP-D: reward rule R4 with N miners. The driver keeps d7's pro-rata
// split by admitted proofs; with a version 2 legacy-mining config it refuses
// the Tier-3 reward penalty, so emission is the full split of rewardCell per
// non-empty block and no multiplier can produce a zero share.

import (
	"math"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/internal/legacymining"
	"github.com/blackbeardONE/QSDM/pkg/chain"
)

func TestNew_RejectsTier3PenaltyWithV2Guard(t *testing.T) {
	g := newFakeGuard()
	g.cfg.Version = legacymining.ConfigVersion2
	cfg := validCfg(t)
	cfg.Guard, cfg.Ledger = g, newFakeLedger(g)
	cfg.RewardPenalty = &fakeRewardPenalty{multipliers: map[string]float64{"qsdm1alice": 0}}
	if d, err := New(cfg); err == nil || d != nil || !strings.Contains(err.Error(), "R4") {
		t.Fatalf("New(v2, RewardPenalty) = %v, %v; want refusal", d, err)
	}
	cfg.RewardPenalty = nil
	if _, err := New(cfg); err != nil {
		t.Fatalf("New(v2, no penalty): %v", err)
	}
	// A v1 config keeps Tier-3 (unchanged HL1 behaviour).
	g1 := newFakeGuard()
	cfg = validCfg(t)
	cfg.Guard, cfg.Ledger = g1, newFakeLedger(g1)
	cfg.RewardPenalty = &fakeRewardPenalty{multipliers: map[string]float64{}}
	if _, err := New(cfg); err != nil {
		t.Fatalf("New(v1, RewardPenalty): %v", err)
	}
}

// The split: every share has the d7 float bits rewardCell*count/total, and
// the shares sum to rewardCell within a few ULPs, inside the PreSeal and I3
// bound rewardCell*(1+RewardSumSlack).
func TestBuildTxs_ProRataSplitR4(t *testing.T) {
	d := &Driver{rewardPenalty: noopRewardPenalty{}, now: time.Now}
	rng := rand.New(rand.NewSource(7))
	cells := []float64{legacymining.DefaultRewardCell(1), 3.56490987, 1, 1e-8}
	for round := 0; round < 2000; round++ {
		cell := cells[round%len(cells)]
		n := 1 + rng.Intn(5)
		claims := make([]legacymining.Claim, n)
		total := 0
		for i := range claims {
			k := 1 + rng.Intn(legacymining.MaxPayloadIDs)
			if rng.Intn(4) == 0 {
				k = 1
			}
			ids := make([]legacymining.ProofID, k)
			for j := range ids {
				ids[j][0], ids[j][1], ids[j][2] = byte(j>>8), byte(j), 1
			}
			claims[i] = legacymining.Claim{MinerAddr: string(rune('a'+i)) + strings.Repeat("0", 63), IDs: ids}
			total += k
		}
		txs, err := d.buildTxs(claims, cell, 0)
		if err != nil {
			t.Fatal(err)
		}
		var sum float64
		for i, tx := range txs {
			want := cell * float64(len(claims[i].IDs)) / float64(total)
			if math.Float64bits(tx.Amount) != math.Float64bits(want) || !(tx.Amount > 0) {
				t.Fatalf("round %d: share %v, want %v", round, tx.Amount, want)
			}
			sum += tx.Amount
		}
		if math.Abs(sum-cell) > cell*float64(3*n)*0x1p-53 || !(sum <= cell*(1+legacymining.RewardSumSlack)) {
			t.Fatalf("round %d: %d claims sum to %v, rewardCell %v", round, n, sum, cell)
		}
	}
}

// A tick with three miners on the real producer: one reward tx each, credited
// exactly, and the funder pays the whole split; a tick with nothing pending
// emits nothing.
func TestTick_ThreeMinersEmitRewardCell(t *testing.T) {
	const cell = 3.0
	h := newHarness(t, true, func(c *Config) { c.FlatRewardPerBlock = cell })
	h.ledger.add(t, "qsdm1alice", 5)
	h.ledger.add(t, "qsdm1bob", 3)
	h.ledger.add(t, "qsdm1carol", 1)
	before := h.funder(t).Balance
	h.d.tick()
	h.noFreeze(t)
	h.noFailStop(t)
	blk := h.tip(t)
	if len(blk.Transactions) != 3 {
		t.Fatalf("%d txs, want 3", len(blk.Transactions))
	}
	var sum float64
	for _, tx := range blk.Transactions {
		if tx.ContractID != chain.MiningRewardContractID || balanceOf(h, tx.Recipient) != tx.Amount {
			t.Fatalf("tx %+v, balance %v", tx, balanceOf(h, tx.Recipient))
		}
		sum += tx.Amount
	}
	for addr, k := range map[string]float64{"qsdm1alice": 5, "qsdm1bob": 3, "qsdm1carol": 1} {
		if got, want := balanceOf(h, addr), cell*k/9; got != want {
			t.Fatalf("%s: %v, want %v", addr, got, want)
		}
	}
	if math.Abs(sum-cell) > cell*0x1p-50 || math.Abs((before-h.funder(t).Balance)-sum) > 1e-12 {
		t.Fatalf("emitted %v (funder paid %v), want %v", sum, before-h.funder(t).Balance, cell)
	}
	before = h.funder(t).Balance
	h.d.tick()
	assertHeartbeatOnly(t, h.tip(t))
	if h.funder(t).Balance != before {
		t.Fatal("an empty block emitted")
	}
}
