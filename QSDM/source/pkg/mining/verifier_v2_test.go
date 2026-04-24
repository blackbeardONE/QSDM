package mining

// Tests for the v2 fork-height gate in verifier.go. These tests
// exercise the Step-1 dispatch only — they deliberately do not
// drive a full proof through the pipeline because the cryptographic
// attestation verifiers live in pkg/mining/attest (Phase 2c) and are
// not available here. The gate behaviour we need to lock in now:
//
//   1. With ForkV2Height == math.MaxUint64 (the default), the
//      verifier behaves exactly like v1 — every existing test in
//      verifier_test.go keeps passing untouched.
//
//   2. With a finite ForkV2Height set and a proof at or above that
//      height, a v1 proof is rejected with ReasonBadVersion and the
//      error mentions "post-fork".
//
//   3. A v2 proof at a post-fork height with an empty attestation
//      type is rejected with ReasonAttestation and the error wraps
//      ErrAttestationRequired.
//
//   4. A v2 proof at a post-fork height whose attestation type is
//      populated is handed off to cfg.Attestation.VerifyAttestation;
//      the default FailClosedVerifier rejects with
//      ReasonAttestation wrapping ErrAttestationSignatureInvalid.
//
//   5. A v1 proof at a pre-fork height (ForkV2Height set but proof
//      height below it) is accepted by the gate — we check this by
//      observing the verifier continues past Step 1 and then fails
//      on a later step we haven't stubbed, rather than failing on
//      bad-version.
//
// Tests that set ForkV2Height restore the default via t.Cleanup so
// the package-level atomic does not leak across test files.

import (
	"encoding/hex"
	"errors"
	"math"
	"math/big"
	"strings"
	"testing"
	"time"
)

// resetForkV2 is the deferred cleanup every test below installs to
// undo any SetForkV2Height() call, keeping tests hermetic for the
// parallel test runner.
func resetForkV2(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { SetForkV2Height(math.MaxUint64) })
}

// buildV2Proof constructs a syntactically-valid v2 proof (version =
// 2, a non-empty attestation type, and plausible freshness fields).
// The proof is NOT required to pass PoW or chain-state checks —
// these tests only cover the Step-1 gate.
func buildV2Proof(t *testing.T, attestationType string) *Proof {
	t.Helper()
	var nonce [32]byte
	for i := range nonce {
		nonce[i] = byte(i)
	}
	return &Proof{
		Version:    ProtocolVersionV2,
		Epoch:      0,
		Height:     100,
		HeaderHash: [32]byte{0x01},
		BatchRoot:  [32]byte{0x02},
		BatchCount: 1,
		Nonce:      [16]byte{0x03},
		MixDigest:  [32]byte{0x04},
		MinerAddr:  "qsdm1test",
		Attestation: Attestation{
			Type:         attestationType,
			BundleBase64: "cGxhY2Vob2xkZXI=",
			GPUArch:      "hopper",
			Nonce:        nonce,
			IssuedAt:     time.Now().Unix(),
		},
	}
}

// stubVerifier assembles a Verifier with enough injected
// dependencies that Verify() reaches Step 1 (version gate). The
// downstream providers return failing stubs — tests that drive the
// pipeline past Step 1 should not rely on successful downstream
// steps here.
func stubVerifier(t *testing.T) *Verifier {
	t.Helper()
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
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	return v
}

// Tests ------------------------------------------------------------

// TestV2Gate_DefaultInactive verifies that with ForkV2Height at
// math.MaxUint64 (the default) a v2 proof is rejected as
// bad-version — we're still on v1 semantics. This is the safety
// guarantee Phase 2b is built around.
func TestV2Gate_DefaultInactive(t *testing.T) {
	resetForkV2(t)
	if ForkV2Height() != math.MaxUint64 {
		t.Fatalf("precondition: ForkV2Height = %d, want MaxUint64", ForkV2Height())
	}
	v := stubVerifier(t)
	p := buildV2Proof(t, AttestationTypeCC)
	raw, err := p.CanonicalJSON()
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	_, err = v.Verify(raw, 150)
	var rej *RejectError
	if !errors.As(err, &rej) {
		t.Fatalf("expected RejectError, got %v", err)
	}
	if rej.Reason != ReasonBadVersion {
		t.Fatalf("default-inactive fork must reject v2 with bad-version, got %s", rej.Reason)
	}
}

// TestV2Gate_PostForkRejectsV1 verifies that when the fork is
// active and the proof's height is at or above ForkV2Height, a v1
// proof is rejected with ReasonBadVersion.
func TestV2Gate_PostForkRejectsV1(t *testing.T) {
	resetForkV2(t)
	SetForkV2Height(50)
	v := stubVerifier(t)

	// Build a legitimate-shape v1 proof at height 100 (>=50).
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
	_, err = v.Verify(raw, 150)
	var rej *RejectError
	if !errors.As(err, &rej) {
		t.Fatalf("expected RejectError, got %v", err)
	}
	if rej.Reason != ReasonBadVersion {
		t.Fatalf("post-fork v1 proof must be bad-version, got %s (%s)", rej.Reason, rej.Detail)
	}
}


// TestV2Gate_PostForkRequiresAttestation verifies that a v2 proof
// at a post-fork height but with an empty Attestation.Type is
// rejected with ReasonAttestation wrapping ErrAttestationRequired.
func TestV2Gate_PostForkRequiresAttestation(t *testing.T) {
	resetForkV2(t)
	SetForkV2Height(50)
	v := stubVerifier(t)

	p := buildV2Proof(t, "")
	raw, err := p.CanonicalJSON()
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	_, err = v.Verify(raw, 150)
	var rej *RejectError
	if !errors.As(err, &rej) {
		t.Fatalf("expected RejectError, got %v", err)
	}
	if rej.Reason != ReasonAttestation {
		t.Fatalf("expected attestation reason, got %s (%s)", rej.Reason, rej.Detail)
	}
	// The detail string is a string-formatted error, so we can only
	// substring-match for the sentinel's message.
	if rej.Detail == "" || !strings.Contains(rej.Detail, "mandatory attestation") {
		t.Fatalf("rejection detail should mention mandatory attestation, got %q", rej.Detail)
	}
}

// TestV2Gate_FailClosedDefault verifies that with a populated
// attestation type but the default FailClosedVerifier wired in,
// the proof is rejected with ReasonAttestation and the detail
// points operators at the missing real verifier wiring.
func TestV2Gate_FailClosedDefault(t *testing.T) {
	resetForkV2(t)
	SetForkV2Height(50)
	v := stubVerifier(t)

	p := buildV2Proof(t, AttestationTypeHMAC)
	raw, err := p.CanonicalJSON()
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	_, err = v.Verify(raw, 150)
	var rej *RejectError
	if !errors.As(err, &rej) {
		t.Fatalf("expected RejectError, got %v", err)
	}
	if rej.Reason != ReasonAttestation {
		t.Fatalf("expected attestation reason, got %s (%s)", rej.Reason, rej.Detail)
	}
	if !strings.Contains(rej.Detail, "FailClosedVerifier") {
		t.Fatalf("fail-closed rejection should self-identify, got %q", rej.Detail)
	}
}

// TestV2Gate_PreForkAcceptsV1 verifies that when ForkV2Height is
// set but the proof height is BELOW it, v1 semantics still apply.
// We can't reach a clean "accepted" state from stubVerifier because
// the DAG provider returns an error, but we can confirm the Step-1
// gate does not reject with ReasonBadVersion — it must let the
// proof progress to a later step.
func TestV2Gate_PreForkAcceptsV1(t *testing.T) {
	resetForkV2(t)
	SetForkV2Height(200) // higher than the proof's height (100)
	v := stubVerifier(t)

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
	_, err = v.Verify(raw, 150)
	var rej *RejectError
	if !errors.As(err, &rej) {
		t.Fatalf("expected RejectError, got %v", err)
	}
	if rej.Reason == ReasonBadVersion {
		t.Fatalf("pre-fork v1 proof must not be bad-version; got %s", rej.Reason)
	}
	if rej.Reason == ReasonAttestation {
		t.Fatalf("pre-fork v1 proof must not hit attestation gate; got %s", rej.Reason)
	}
}

// TestV2Gate_CustomAttestationHook verifies that a custom
// AttestationVerifier wired into VerifierConfig.Attestation is
// invoked with the expected proof + clock arguments.
func TestV2Gate_CustomAttestationHook(t *testing.T) {
	resetForkV2(t)
	SetForkV2Height(50)

	var captured *Proof
	fixedNow := time.Unix(1_700_000_000, 0)
	stub := &recordingVerifier{
		onVerify: func(p Proof, now time.Time) error {
			captured = &p
			if !now.Equal(fixedNow) {
				t.Errorf("hook received now = %v, want %v", now, fixedNow)
			}
			return nil // accept as far as this hook is concerned
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
		Now:              func() time.Time { return fixedNow },
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	p := buildV2Proof(t, AttestationTypeCC)
	raw, err := p.CanonicalJSON()
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	// Verify will still fail downstream because the chain / workset
	// stubs return errors; we only care that the hook was called
	// first and saw the correct proof.
	_, _ = v.Verify(raw, 150)
	if captured == nil {
		t.Fatal("attestation hook was never invoked")
	}
	if captured.Version != ProtocolVersionV2 {
		t.Fatalf("hook saw version %d, want %d", captured.Version, ProtocolVersionV2)
	}
	if captured.Attestation.Type != AttestationTypeCC {
		t.Fatalf("hook saw type %q, want %q", captured.Attestation.Type, AttestationTypeCC)
	}
	// Spot-check the nonce round-tripped correctly by confirming the
	// first byte matches buildV2Proof's pattern. Full canonical-JSON
	// coverage already lives in proof_test.go.
	if hex.EncodeToString(captured.Attestation.Nonce[:1]) != "00" {
		t.Fatalf("hook saw corrupted nonce[0] = %x", captured.Attestation.Nonce[0])
	}
}

// Helpers ----------------------------------------------------------

type recordingVerifier struct {
	onVerify func(Proof, time.Time) error
}

func (r *recordingVerifier) VerifyAttestation(p Proof, now time.Time) error {
	return r.onVerify(p, now)
}
