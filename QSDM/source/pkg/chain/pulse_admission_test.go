package chain

import (
	"encoding/hex"
	"errors"
	"testing"

	"github.com/blackbeardONE/QSDM/internal/pulse"
)

// The mempool refuses a settlement above the fee ceiling and a reclaim before
// its timeout, once the live state is attached; without it (or for a valid tx)
// admission passes as before, and block production keeps the final word.
func TestPulseAdmissionRefusesOverCapAndEarlyReclaim(t *testing.T) {
	f := newPulseFix(t, activeParams())
	t.Cleanup(func() { SetPulseAdmissionState(nil, nil) })
	pool := string(f.pool.ID())
	m1 := string(f.m1.ID())
	f.must(f.pulseTx(f.m1, PulseAction{Op: "join", Pool: pool}))
	f.rewardOK(PulseEscrowAddress(pool, m1), 2, "a1")
	admit := PulseAdmissionChecker(nil)
	w := []pulse.Weight{{Owner: f.m1.ID(), Weight: 1}}

	over := func() error {
		b, s := f.batch(1, 301, []string{proofHex("a1")}, w)
		return admit(f.pulseTx(f.pool, PulseAction{Op: "settle", Batch: b, BatchSig: s}))
	}
	atCap := func() error {
		b, s := f.batch(1, 300, []string{proofHex("a1")}, w)
		return admit(f.pulseTx(f.pool, PulseAction{Op: "settle", Batch: b, BatchSig: s}))
	}
	early := func() error {
		return admit(f.pulseTx(f.m1, PulseAction{Op: "reclaim", Proofs: []string{proofHex("a1")}}))
	}
	foreign := func() error { // someone else's escrow: left to block production
		return admit(f.pulseTx(f.m2, PulseAction{Op: "reclaim", Proofs: []string{proofHex("a1")}}))
	}

	// Without the live state: the old stateless behaviour.
	for name, fn := range map[string]func() error{"over cap": over, "early reclaim": early} {
		if err := fn(); err != nil {
			t.Fatalf("%s without state: %v", name, err)
		}
	}

	SetPulseAdmissionState(f.ps, func() uint64 { return f.height })
	if err := over(); !errors.Is(err, ErrPulseMismatch) {
		t.Fatalf("over-cap settlement admitted: %v", err)
	}
	if err := atCap(); err != nil {
		t.Fatalf("settlement at the ceiling refused: %v", err)
	}
	if err := early(); !errors.Is(err, ErrPulseNotDue) {
		t.Fatalf("early reclaim admitted: %v", err)
	}
	if err := foreign(); err != nil {
		t.Fatalf("reclaim of another owner's escrow should be left to the block: %v", err)
	}
	f.height += 99
	if err := early(); !errors.Is(err, ErrPulseNotDue) {
		t.Fatalf("reclaim one block early admitted: %v", err)
	}
	f.height++
	if err := early(); err != nil {
		t.Fatalf("due reclaim refused: %v", err)
	}
	// Block production agrees at the same height.
	f.must(f.pulseTx(f.m1, PulseAction{Op: "reclaim", Proofs: []string{proofHex("a1")}}))
}

// A pool entry with its own lower ceiling is honoured at admission too.
func TestPulseAdmissionHonoursAPoolsOwnCeiling(t *testing.T) {
	f := newPulseFix(t, nil)
	t.Cleanup(func() { SetPulseAdmissionState(nil, nil) })
	p := activeParams()
	p.Pools = []pulse.PoolEntry{{ID: f.pool.ID(), PublicKey: hex.EncodeToString(f.pool.Pub.Bytes()), MaxFeeBPS: 100}}
	if err := SetPulseParams(*p); err != nil {
		t.Fatal(err)
	}
	SetPulseAdmissionState(f.ps, func() uint64 { return f.height })
	admit := PulseAdmissionChecker(nil)
	w := []pulse.Weight{{Owner: f.m1.ID(), Weight: 1}}
	b, s := f.batch(1, 101, []string{proofHex("x")}, w)
	if err := admit(f.pulseTx(f.pool, PulseAction{Op: "settle", Batch: b, BatchSig: s})); !errors.Is(err, ErrPulseMismatch) {
		t.Fatalf("settlement above the pool's own 100 bps admitted: %v", err)
	}
	b, s = f.batch(1, 100, []string{proofHex("x")}, w)
	if err := admit(f.pulseTx(f.pool, PulseAction{Op: "settle", Batch: b, BatchSig: s})); err != nil {
		t.Fatalf("settlement at the pool's ceiling refused: %v", err)
	}
}
