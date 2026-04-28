package mining

// Tests for the FORK_V2_TC_HEIGHT gate (MINING_PROTOCOL_V2 §4) — the
// height at which the mix-digest algorithm switches from the v1 SHA3
// walk (pkg/mining.ComputeMixDigest) to the byte-exact Tensor-Core
// mixin (pkg/mining/pow/v2.ComputeMixDigestV2). The two algorithms
// produce different 32-byte digests for identical inputs, so a proof
// mined under the wrong algorithm fails Step 10 of the verifier with
// a mix_digest mismatch — the soft-tightening fork behaviour §4
// promises.
//
// The four invariants exercised here:
//
//   1. Default off: with ForkV2TCHeight == math.MaxUint64 (the
//      package init() default), the verifier and the reference
//      solver behave exactly like v1 — every existing
//      verifier_test.go case keeps passing untouched.
//
//   2. Post-TC happy path: with a finite ForkV2TCHeight set and
//      a proof at a height >= that value, both Solve and Verify
//      route through the powv2 mixin; the resulting proof
//      validates end-to-end.
//
//   3. Algorithm-mismatch rejection at post-TC height: a proof
//      mined under v1 (TC disabled) fails verification at a
//      post-TC height (TC enabled at <= proof's height) with
//      ReasonWork / "mix_digest mismatch".
//
//   4. Algorithm-mismatch rejection at pre-TC height: a proof
//      mined under v2 (TC enabled at <= proof's height) fails
//      verification at a pre-TC height (TC disabled) with
//      ReasonWork / "mix_digest mismatch".
//
// Tests that mutate ForkV2TCHeight install a t.Cleanup() that
// restores the math.MaxUint64 default, so the package-level atomic
// does not leak across the parallel test runner.

import (
	"context"
	"errors"
	"math"
	"math/big"
	"strings"
	"testing"
	"time"
)

// resetForkV2TC is the cleanup every TC-fork test installs so the
// atomic returns to the safe "TC disabled" default.
func resetForkV2TC(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { SetForkV2TCHeight(math.MaxUint64) })
}

// buildTCMiniSetup returns a Verifier wired with a real DAG,
// permissive sub-checks, and a successfully-mined Proof at a fixed
// height. Callers typically do
//
//	resetForkV2TC(t); SetForkV2TCHeight(<something>); buildTCMiniSetup(t)
//
// so the solver and the verifier agree on which mix algorithm to
// run.
func buildTCMiniSetup(t *testing.T, height uint64) (*Verifier, *Proof, []byte) {
	t.Helper()
	ws := makeWorkSet(t, 4)
	const epoch = uint64(0)
	const dagN = 64
	dag, err := NewInMemoryDAG(epoch, ws.Root(), dagN)
	if err != nil {
		t.Fatalf("dag: %v", err)
	}

	difficulty := big.NewInt(2)
	target, err := TargetFromDifficulty(difficulty)
	if err != nil {
		t.Fatalf("target: %v", err)
	}

	headerHash := [32]byte{0x01, 0x23, 0x45}
	batchRoot, err := ws.PrefixRoot(1)
	if err != nil {
		t.Fatalf("prefix root: %v", err)
	}

	params := SolverParams{
		Epoch:      epoch,
		Height:     height,
		HeaderHash: headerHash,
		MinerAddr:  "qsdm1tctest",
		BatchRoot:  batchRoot,
		BatchCount: 1,
		Target:     target,
		DAG:        dag,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := Solve(ctx, params, nil, nil)
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	if res.Proof == nil {
		t.Fatal("solver returned nil proof")
	}

	cfg := VerifierConfig{
		EpochParams:      NewEpochParams(),
		DifficultyParams: NewDifficultyAdjusterParams(),
		Chain: &fakeChain{
			tip:     height,
			headers: map[uint64][32]byte{height: headerHash},
		},
		Addresses:       permissiveAddr{},
		Batches:         goodBatches{},
		Dedup:           NewProofIDSet(1024),
		Quarantine:      NewQuarantineSet(),
		DAGProvider:     func(uint64) (DAG, error) { return dag, nil },
		WorkSetProvider: func(uint64) (WorkSet, error) { return ws, nil },
		DifficultyAt:    func(uint64) (*big.Int, error) { return difficulty, nil },
	}
	v, err := NewVerifier(cfg)
	if err != nil {
		t.Fatalf("new verifier: %v", err)
	}

	raw, err := res.Proof.CanonicalJSON()
	if err != nil {
		t.Fatalf("canonical JSON: %v", err)
	}
	return v, res.Proof, raw
}

// TestForkV2TCHeight_DefaultDisabled locks the safety default: out
// of the box, ForkV2TCHeight() is math.MaxUint64 and IsV2TC()
// returns false for every realistic height. A network operator
// MUST opt in by calling SetForkV2TCHeight before validators
// switch to the mixin.
func TestForkV2TCHeight_DefaultDisabled(t *testing.T) {
	if got := ForkV2TCHeight(); got != math.MaxUint64 {
		t.Errorf("ForkV2TCHeight() = %d; default must be math.MaxUint64", got)
	}
	if IsV2TC(0) {
		t.Errorf("IsV2TC(0) = true at default; want false")
	}
	if IsV2TC(math.MaxUint64 - 1) {
		t.Errorf("IsV2TC(MaxUint64-1) = true at default; want false")
	}
}

// TestForkV2TCHeight_BoundaryInclusive covers the off-by-one corner
// of the gate: when ForkV2TCHeight is set to H, IsV2TC must return
// false at H-1 and true at H exactly. Mixing this up would mean the
// pre-fork block at H-1 expects the v1 walk while the post-fork
// block at H expects the v2 mixin, with the boundary block running
// the wrong algorithm.
func TestForkV2TCHeight_BoundaryInclusive(t *testing.T) {
	resetForkV2TC(t)
	SetForkV2TCHeight(100)
	if IsV2TC(99) {
		t.Errorf("IsV2TC(99) = true with TC=100; want false")
	}
	if !IsV2TC(100) {
		t.Errorf("IsV2TC(100) = false with TC=100; want true")
	}
	if !IsV2TC(101) {
		t.Errorf("IsV2TC(101) = false with TC=100; want true")
	}
}

// TestVerify_TCFork_DefaultAcceptsV1Mix confirms the no-op safety
// behaviour: with the default TC fork height, the verifier still
// accepts proofs whose mix-digest was computed by the v1 walk.
// This is the "every existing test keeps passing" guarantee.
func TestVerify_TCFork_DefaultAcceptsV1Mix(t *testing.T) {
	resetForkV2TC(t)
	if ForkV2TCHeight() != math.MaxUint64 {
		t.Fatalf("precondition: TC must be disabled by default, got %d", ForkV2TCHeight())
	}
	v, _, raw := buildTCMiniSetup(t, 42)
	id, err := v.Verify(raw, 42)
	if err != nil {
		t.Fatalf("default TC=disabled rejected v1-mined proof: %v", err)
	}
	var zero [32]byte
	if id == zero {
		t.Fatal("verifier returned zero proof ID on success")
	}
}

// TestVerify_TCFork_PostForkAcceptsV2Mix is the post-TC happy path.
// With the TC fork active at height 0, both Solve and Verify route
// through powv2.ComputeMixDigestV2; the resulting proof validates
// end-to-end. This is the test that proves today's reference impl
// is actually load-bearing once the gate is flipped.
func TestVerify_TCFork_PostForkAcceptsV2Mix(t *testing.T) {
	resetForkV2TC(t)
	SetForkV2TCHeight(0)
	v, _, raw := buildTCMiniSetup(t, 42)
	id, err := v.Verify(raw, 42)
	if err != nil {
		t.Fatalf("TC=0 rejected v2-mined proof at height 42: %v", err)
	}
	var zero [32]byte
	if id == zero {
		t.Fatal("verifier returned zero proof ID on success")
	}
}

// TestVerify_TCFork_RejectsV1MixAtPostForkHeight is the
// soft-tightening guarantee: a proof mined while TC was disabled
// (v1 walk) is rejected by a verifier that has TC enabled at or
// below the proof's height. The rejection must come from Step 10
// (PoW) with ReasonWork / "mix_digest mismatch", proving the
// dispatch is on the verifier side.
func TestVerify_TCFork_RejectsV1MixAtPostForkHeight(t *testing.T) {
	// Stage 1: mine with TC disabled (default) -> v1 walk.
	resetForkV2TC(t)
	v, _, raw := buildTCMiniSetup(t, 42)
	// Stage 2: flip TC on at a height <= the proof's height. The
	// verifier should now compute a v2 mix and find it does NOT
	// match the v1 mix recorded in the proof.
	SetForkV2TCHeight(42)
	_, err := v.Verify(raw, 42)
	if err == nil {
		t.Fatal("v1-mined proof at post-TC height was accepted; expected mix_digest mismatch")
	}
	var rej *RejectError
	if !errors.As(err, &rej) {
		t.Fatalf("expected RejectError, got %T %v", err, err)
	}
	if rej.Reason != ReasonWork {
		t.Errorf("reject reason: got %s, want %s", rej.Reason, ReasonWork)
	}
	if !strings.Contains(rej.Error(), "mix_digest mismatch") {
		t.Errorf("expected 'mix_digest mismatch' in error, got: %v", rej.Error())
	}
}

// TestVerify_TCFork_RejectsV2MixAtPreForkHeight is the symmetric
// case: a proof mined while TC was active (v2 mixin) is rejected
// by a verifier that has TC disabled. The verifier computes a v1
// mix and finds it does not match the v2 mix in the proof. This
// scenario only arises if an operator misconfigures their node OR
// a malicious miner tries to push v2 proofs ahead of the activation
// height.
func TestVerify_TCFork_RejectsV2MixAtPreForkHeight(t *testing.T) {
	// Stage 1: mine with TC active -> v2 mixin.
	resetForkV2TC(t)
	SetForkV2TCHeight(0)
	v, _, raw := buildTCMiniSetup(t, 42)
	// Stage 2: flip TC off. The verifier should now compute a v1
	// mix and find it does NOT match the v2 mix recorded in the
	// proof.
	SetForkV2TCHeight(math.MaxUint64)
	_, err := v.Verify(raw, 42)
	if err == nil {
		t.Fatal("v2-mined proof at pre-TC height was accepted; expected mix_digest mismatch")
	}
	var rej *RejectError
	if !errors.As(err, &rej) {
		t.Fatalf("expected RejectError, got %T %v", err, err)
	}
	if rej.Reason != ReasonWork {
		t.Errorf("reject reason: got %s, want %s", rej.Reason, ReasonWork)
	}
	if !strings.Contains(rej.Error(), "mix_digest mismatch") {
		t.Errorf("expected 'mix_digest mismatch' in error, got: %v", rej.Error())
	}
}
