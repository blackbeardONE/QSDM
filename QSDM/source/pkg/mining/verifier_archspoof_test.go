package mining

// verifier_archspoof_test.go: outer-Verifier coverage for the
// MINING_PROTOCOL_V2 §4.6 / §3.3 step-8 arch-spoof rejection.
// Companion to pkg/mining/attest/hmac/verifier_archcheck_test.go
// (which covers the same logic at the per-type-verifier layer)
// and pkg/mining/attest/archcheck/archcheck_test.go (which
// covers the policy primitives in isolation).
//
// This file's job is the WIRING: the outer Verifier rejects an
// unknown / out-of-allowlist gpu_arch BEFORE dispatching to the
// per-type Attestation verifier, so a malformed proof never
// pays the cost of (potentially expensive) crypto work.

import (
	"errors"
	"math"
	"math/big"
	"testing"
	"time"
)

// TestV2Gate_RejectsUnknownArchBeforeDispatcher proves the cheap-
// reject ordering: with a custom AttestationVerifier that fails
// loudly if it's ever called, an out-of-allowlist gpu_arch must
// reject WITHOUT the dispatcher being invoked. If the dispatcher
// fires this test fails.
func TestV2Gate_RejectsUnknownArchBeforeDispatcher(t *testing.T) {
	resetForkV2(t)
	SetForkV2Height(50)

	dispatcherCalled := false
	stub := &recordingVerifier{
		onVerify: func(_ Proof, _ time.Time) error {
			dispatcherCalled = true
			return nil
		},
	}

	v, err := NewVerifier(VerifierConfig{
		EpochParams:      NewEpochParams(),
		DifficultyParams: NewDifficultyAdjusterParams(),
		Chain:            &fakeChain{tip: 200, headers: map[uint64][32]byte{100: {0x01}}},
		Addresses:        permissiveAddr{},
		Dedup:            NewProofIDSet(16),
		Quarantine:       NewQuarantineSet(),
		DAGProvider:      func(uint64) (DAG, error) { return nil, errors.New("unused") },
		WorkSetProvider:  func(uint64) (WorkSet, error) { return WorkSet{}, errors.New("unused") },
		DifficultyAt:     func(uint64) (*big.Int, error) { return nil, errors.New("unused") },
		Attestation:      stub,
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	p := buildV2Proof(t, AttestationTypeHMAC)
	p.Attestation.GPUArch = "future-arch-2099" // not on the allowlist
	raw, err := p.CanonicalJSON()
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}

	_, verr := v.Verify(raw, 150)
	var rej *RejectError
	if !errors.As(verr, &rej) {
		t.Fatalf("expected RejectError, got %v", verr)
	}
	if rej.Reason != ReasonAttestation {
		t.Errorf("reason = %s, want %s", rej.Reason, ReasonAttestation)
	}
	if dispatcherCalled {
		t.Error("dispatcher should not have been invoked for out-of-allowlist gpu_arch")
	}
}

// TestV2Gate_AcceptsAdaAlias confirms the alias path: the
// qsdmminer-console-emitted "ada" short form is accepted by the
// outer gate (canonical to "ada-lovelace" in archcheck) and the
// dispatcher is invoked. Locks the backward-compat decision so
// a future tightening to require the long form lands as a
// deliberate test break, not silent miner outages.
func TestV2Gate_AcceptsAdaAlias(t *testing.T) {
	resetForkV2(t)
	SetForkV2Height(50)

	dispatcherCalled := false
	stub := &recordingVerifier{
		onVerify: func(_ Proof, _ time.Time) error {
			dispatcherCalled = true
			// Return error so the test doesn't get past the
			// gate's later steps (Chain / WorkSet stubs return
			// errors); we only care that we GOT to the
			// dispatcher.
			return errors.New("stub: ok-as-far-as-arch")
		},
	}

	v, err := NewVerifier(VerifierConfig{
		EpochParams:      NewEpochParams(),
		DifficultyParams: NewDifficultyAdjusterParams(),
		Chain:            &fakeChain{tip: 200, headers: map[uint64][32]byte{100: {0x01}}},
		Addresses:        permissiveAddr{},
		Dedup:            NewProofIDSet(16),
		Quarantine:       NewQuarantineSet(),
		DAGProvider:      func(uint64) (DAG, error) { return nil, errors.New("unused") },
		WorkSetProvider:  func(uint64) (WorkSet, error) { return WorkSet{}, errors.New("unused") },
		DifficultyAt:     func(uint64) (*big.Int, error) { return nil, errors.New("unused") },
		Attestation:      stub,
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	p := buildV2Proof(t, AttestationTypeHMAC)
	p.Attestation.GPUArch = "ada" // the alias
	raw, err := p.CanonicalJSON()
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}

	_, _ = v.Verify(raw, 150)
	if !dispatcherCalled {
		t.Error("dispatcher should have been invoked for the 'ada' alias")
	}
}

// TestV2Gate_DefaultPreForkBypassesArchCheck asserts the
// (default) pre-fork posture: with ForkV2Height at MaxUint64 a
// v1 proof is the only one that can pass the version gate, and
// v1 proofs have no GPUArch field — the arch-spoof check must
// not be reachable on v1 paths.
func TestV2Gate_DefaultPreForkBypassesArchCheck(t *testing.T) {
	resetForkV2(t)
	if ForkV2Height() != math.MaxUint64 {
		t.Fatalf("precondition: ForkV2Height = %d, want MaxUint64",
			ForkV2Height())
	}
	v := stubVerifier(t)

	// A v1 proof with no Attestation. Should not trip
	// arch-related checks.
	p := &Proof{
		Version:    ProtocolVersion,
		Epoch:      0,
		Height:     100,
		HeaderHash: [32]byte{0x01},
		BatchRoot:  [32]byte{0x02},
		BatchCount: 1,
		Nonce:      [16]byte{0x03},
		MixDigest:  [32]byte{0x04},
		MinerAddr:  "qsdm1test",
	}
	raw, err := p.CanonicalJSON()
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	_, verr := v.Verify(raw, 150)
	var rej *RejectError
	if !errors.As(verr, &rej) {
		t.Fatalf("expected RejectError, got %v", verr)
	}
	if rej.Reason == ReasonAttestation {
		t.Errorf("v1 pre-fork must not hit attestation gate; got %s (%s)",
			rej.Reason, rej.Detail)
	}
}
