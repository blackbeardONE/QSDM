package forgedattest

import (
	"errors"
	"os"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mining/slashing"
)

// The verifier is gated closed in production because nvidia-hmac-v1 bundles
// cannot be attributed to their producer while EnrollmentRecord.HMACKey is
// public chain state (see pkg/mining/slashing/attribution.go). The tests in
// this package exercise the verifier's MECHANICS -- kind routing, binding
// checks, cap handling -- which are only reachable past that gate, so they run
// with attribution enabled.
//
// The gate itself is asserted by TestVerify_GateClosedByDefault below, which
// restores the production default first. Nothing here changes what a node
// does: SetHMACAttestationAttributable is never called outside tests.
func TestMain(m *testing.M) {
	slashing.SetHMACAttestationAttributable(true)
	code := m.Run()
	slashing.SetHMACAttestationAttributable(false)
	os.Exit(code)
}

// With the production default restored, no payload may reach a slash.
func TestVerify_GateClosedByDefault(t *testing.T) {
	slashing.SetHMACAttestationAttributable(false)
	t.Cleanup(func() { slashing.SetHMACAttestationAttributable(true) })

	// Asserts the refusal only. The inert "0 dust" check that used to sit
	// here was removed rather than annotated: a nil-Registry verifier returns
	// 0 regardless of the gate, so it could never fail and reads as coverage
	// that does not exist.
	//
	// The SIDE EFFECT -- bond unmoved, slasher unpaid -- is asserted where it
	// is real, on the block-apply path, by
	// TestSlashE2E_AttributionGateClosed_NoDrainNoPayout in pkg/chain.
	v := NewVerifier(nil, 0)
	if _, err := v.Verify(slashing.SlashPayload{}, 0); !errors.Is(err, slashing.ErrAttestationUnattributable) {
		t.Fatalf("expected ErrAttestationUnattributable, got %v", err)
	}
}

// The guard must run before the nil-Registry check, so a misconfigured node
// still cannot be argued into slashing.
func TestVerify_GateClosedBeatsOtherRejections(t *testing.T) {
	slashing.SetHMACAttestationAttributable(false)
	t.Cleanup(func() { slashing.SetHMACAttestationAttributable(true) })

	v := NewVerifier(nil, 0)
	_, err := v.Verify(slashing.SlashPayload{EvidenceKind: "totally-wrong-kind"}, 0)
	if !errors.Is(err, slashing.ErrAttestationUnattributable) {
		t.Fatalf("attribution must be refused first, got %v", err)
	}
}
