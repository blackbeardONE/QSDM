package mining

// verifier_hashrate_test.go: outer-Verifier coverage for the
// MINING_PROTOCOL_V2 §4.6 hashrate-band plausibility check
// (pkg/mining/attest/archcheck.ValidateClaimedHashrate).
// Companion to verifier_archspoof_test.go.
//
// Three distinct behaviours are locked here:
//
//   1. "Not asserted" (claimed_hashrate_hps = 0) passes through
//      so existing test fixtures that predate this check
//      keep working.
//
//   2. An obvious lazy-spoof (claimed >> per-arch peak) is
//      rejected BEFORE the per-type AttestationVerifier is
//      invoked — the dispatcher MUST NOT fire on a hashrate
//      reject.
//
//   3. An in-band claim is allowed through to the dispatcher.

import (
	"errors"
	"math/big"
	"testing"
	"time"
)

// TestV2Gate_AcceptsZeroHashrateAsNotAsserted locks the
// backward-compat sentinel. The fixture sets ClaimedHashrateHPS=0
// implicitly (zero value), and existing test fixtures across the
// codebase do the same. If ValidateClaimedHashrate ever stops
// treating 0 as "not asserted" this test is the regression bar.
func TestV2Gate_AcceptsZeroHashrateAsNotAsserted(t *testing.T) {
	resetForkV2(t)
	SetForkV2Height(50)

	dispatcherCalled := false
	stub := &recordingVerifier{
		onVerify: func(_ Proof, _ time.Time) error {
			dispatcherCalled = true
			return errors.New("stub: ok-as-far-as-arch-and-hashrate")
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
	// Confirm the fixture's ClaimedHashrateHPS is the sentinel.
	if p.Attestation.ClaimedHashrateHPS != 0 {
		t.Fatalf("fixture ClaimedHashrateHPS = %d, want 0 (sentinel)",
			p.Attestation.ClaimedHashrateHPS)
	}
	raw, err := p.CanonicalJSON()
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	_, _ = v.Verify(raw, 150)

	if !dispatcherCalled {
		t.Error("dispatcher should have been invoked for a 0 (not-asserted) hashrate")
	}
}

// TestV2Gate_RejectsImplausiblyHighHashrate covers the lazy
// hashrate-spoof: a claim wildly above the per-arch peak. Each
// case is a real-world spoof shape an attacker would attempt —
// claiming H100-class throughput on a consumer card or vice
// versa.
//
// The dispatcher MUST NOT fire — the cheap reject saves the
// validator the expensive HMAC / X.509 work on garbage.
func TestV2Gate_RejectsImplausiblyHighHashrate(t *testing.T) {
	cases := []struct {
		name    string
		arch    string
		claimed uint64
	}{
		{"hopper-200x", "hopper", 200_000_000_000},          // 200 GH/s
		{"ada-100x", "ada-lovelace", 5_000_000_000},          // 5 GH/s on RTX 40
		{"turing-1000x", "turing", 5_000_000_000},            // 5 GH/s on T4
		{"obvious-typo", "ampere", 18_000_000_000_000_000},   // 18 PH/s
		{"datacenter-on-consumer-arch", "ada-lovelace", 100_000_000}, // 100 MH/s on RTX 40 (>2x band Max)
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
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
			p.Attestation.GPUArch = c.arch
			p.Attestation.ClaimedHashrateHPS = c.claimed
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
				t.Error("dispatcher must not be invoked for an implausible hashrate")
			}
		})
	}
}

// TestV2Gate_RejectsImplausiblyLowHashrate covers the inverse
// downgrade: claiming GPU arch but reporting CPU-class
// hashrate. Suggests CPU mining with a forged attestation.
func TestV2Gate_RejectsImplausiblyLowHashrate(t *testing.T) {
	resetForkV2(t)
	SetForkV2Height(50)

	stub := &recordingVerifier{
		onVerify: func(_ Proof, _ time.Time) error { return nil },
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
	// Hopper claiming 100 H/s — solidly in CPU territory and
	// 4-5 orders of magnitude below the H100 peak.
	p.Attestation.GPUArch = "hopper"
	p.Attestation.ClaimedHashrateHPS = 100
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
}

// TestV2Gate_AcceptsInBandHashrate is the happy-path
// regression bar: a claim solidly inside the per-arch band
// flows past the gate to the dispatcher.
func TestV2Gate_AcceptsInBandHashrate(t *testing.T) {
	resetForkV2(t)
	SetForkV2Height(50)

	dispatcherCalled := false
	stub := &recordingVerifier{
		onVerify: func(_ Proof, _ time.Time) error {
			dispatcherCalled = true
			return errors.New("stub: ok-as-far-as-arch-and-hashrate")
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
	p.Attestation.GPUArch = "ada"          // alias for ada-lovelace
	p.Attestation.ClaimedHashrateHPS = 5_000_000 // 5 MH/s, classic RTX 4090
	raw, err := p.CanonicalJSON()
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	_, _ = v.Verify(raw, 150)
	if !dispatcherCalled {
		t.Error("dispatcher should have been invoked for an in-band hashrate")
	}
}
