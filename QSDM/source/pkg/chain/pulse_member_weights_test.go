package chain

import (
	"encoding/hex"
	"errors"
	"testing"

	"github.com/blackbeardONE/QSDM/internal/pulse"
)

// 2026-11-18 report: a pool could give the weight of its members' escrowed rewards to an address that never joined.
// Every weight owner must now be a member of the settling pool; the batch is refused otherwise and nothing moves.
func TestPulseSettlementRefusesNonMemberWeights(t *testing.T) {
	f := newPulseFix(t, activeParams())
	pool := string(f.pool.ID())
	m1, accomplice := string(f.m1.ID()), string(f.m2.ID())
	f.must(f.pulseTx(f.m1, PulseAction{Op: "join", Pool: pool}))
	f.rewardOK(PulseEscrowAddress(pool, m1), 3, "v1", "v2")
	proofs := []string{proofHex("v1"), proofHex("v2")}
	skewed := []pulse.Weight{{Owner: f.m1.ID(), Weight: 1}, {Owner: f.m2.ID(), Weight: 99}}

	root := f.aware.StateRoot()
	body, sig := f.batch(1, 100, proofs, skewed)
	err := f.apply(f.pulseTx(f.pool, PulseAction{Op: "settle", Batch: body, BatchSig: sig}))
	if !errors.Is(err, ErrPulseMismatch) {
		t.Fatalf("a weight for an address that never joined: %v, want ErrPulseMismatch", err)
	}
	if f.aware.StateRoot() != root || f.bal(accomplice) != 0 || f.bal(m1) != 0 ||
		!near(f.bal(PulseEscrowAddress(pool, m1)), 3) {
		t.Fatalf("the refused settlement moved value: accomplice %v member %v escrow %v",
			f.bal(accomplice), f.bal(m1), f.bal(PulseEscrowAddress(pool, m1)))
	}

	// A member of ANOTHER pool is not a member of this one.
	other, _ := pulse.NewKeyPair()
	ps := PulseParamsSnapshot()
	ps.Pools = append(ps.Pools, pulse.PoolEntry{ID: other.ID(), PublicKey: hex.EncodeToString(other.Pub.Bytes())})
	if err := SetPulseParams(ps); err != nil {
		t.Fatal(err)
	}
	f.must(f.pulseTx(f.m2, PulseAction{Op: "join", Pool: string(other.ID())}))
	if err := f.apply(f.pulseTx(f.pool, PulseAction{Op: "settle", Batch: body, BatchSig: sig})); !errors.Is(err, ErrPulseMismatch) {
		t.Fatalf("a weight for a member of another pool: %v, want ErrPulseMismatch", err)
	}

	// Control: once the second address leaves the other pool and joins this one, the same weights settle.
	f.must(f.pulseTx(f.m2, PulseAction{Op: "leave"}))
	f.must(f.pulseTx(f.m2, PulseAction{Op: "join", Pool: pool}))
	f.must(f.pulseTx(f.pool, PulseAction{Op: "settle", Batch: body, BatchSig: sig}))
	// 3 CELL pooled, 1 % fee 0.03; m1's 1/100 share is under the floor and carries forward as owed.
	if !near(f.bal(f.fee), 0.03) || !near(f.bal(accomplice), 2.9403) || f.bal(m1) != 0 {
		t.Fatalf("members m1 %v m2 %v fee %v", f.bal(m1), f.bal(accomplice), f.bal(f.fee))
	}

	// A member who left is no longer a valid weight owner.
	f.rewardOK(PulseEscrowAddress(pool, m1), 1, "v3")
	f.must(f.pulseTx(f.m2, PulseAction{Op: "leave"}))
	b2, s2 := f.batch(2, 100, []string{proofHex("v3")}, skewed)
	if err := f.apply(f.pulseTx(f.pool, PulseAction{Op: "settle", Batch: b2, BatchSig: s2})); !errors.Is(err, ErrPulseMismatch) {
		t.Fatalf("a weight for a member who left: %v, want ErrPulseMismatch", err)
	}
}
