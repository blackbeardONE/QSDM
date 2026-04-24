package cc

import (
	"errors"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mining"
)

// TestStubVerifier_NeverAccepts: no input, ever, produces nil.
// This is the single most important invariant of the stub. If
// it ever regressed silently, a v2 proof claiming to be
// nvidia-cc-v1 would be unconditionally accepted on every
// validator running this build.
func TestStubVerifier_NeverAccepts(t *testing.T) {
	v := NewStubVerifier()
	cases := []mining.Proof{
		{},
		{Attestation: mining.Attestation{Type: mining.AttestationTypeCC}},
		{Attestation: mining.Attestation{Type: mining.AttestationTypeHMAC}},
		{Attestation: mining.Attestation{Type: "nvidia-cc-v2"}},
		{Attestation: mining.Attestation{Type: ""}},
	}
	now := time.Unix(1_700_000_000, 0)
	for i, p := range cases {
		if err := v.VerifyAttestation(p, now); err == nil {
			t.Fatalf("case %d: stub accepted a proof — %+v", i, p)
		}
	}
}

// TestStubVerifier_HappyPath_Type returns the friendly error
// when the (correctly-routed) cc type is passed.
func TestStubVerifier_HappyPath_Type(t *testing.T) {
	v := NewStubVerifier()
	p := mining.Proof{Attestation: mining.Attestation{Type: mining.AttestationTypeCC}}
	err := v.VerifyAttestation(p, time.Now())
	if err == nil {
		t.Fatal("expected non-nil error for cc type")
	}
	if !errors.Is(err, ErrNotYetAvailable) {
		t.Fatalf("expected ErrNotYetAvailable, got %v", err)
	}
	if !errors.Is(err, mining.ErrAttestationTypeUnknown) {
		t.Fatalf("expected wrapped ErrAttestationTypeUnknown, got %v", err)
	}
}

// TestStubVerifier_RoutingMismatch_DistinctError: a dispatcher
// bug that routes a non-CC type to this stub must produce a
// DIFFERENT error message so the bug is visible in logs. The
// sentinel is still mining.ErrAttestationTypeUnknown (correct
// semantically) but the wrapping message calls out "dispatcher
// routing bug."
func TestStubVerifier_RoutingMismatch_DistinctError(t *testing.T) {
	v := NewStubVerifier()
	p := mining.Proof{Attestation: mining.Attestation{Type: mining.AttestationTypeHMAC}}
	err := v.VerifyAttestation(p, time.Now())
	if err == nil {
		t.Fatal("expected non-nil error for routing mismatch")
	}
	// Should NOT be the "not yet available" error — should be
	// the dispatcher-bug variant.
	if errors.Is(err, ErrNotYetAvailable) {
		t.Fatal("routing mismatch should NOT surface as ErrNotYetAvailable")
	}
	if !errors.Is(err, mining.ErrAttestationTypeUnknown) {
		t.Fatalf("expected wrapped ErrAttestationTypeUnknown, got %v", err)
	}
	if !containsString(err.Error(), "dispatcher routing bug") {
		t.Fatalf("error message should mention routing bug, got %q", err.Error())
	}
}

// TestStubVerifier_ImplementsInterface is covered by the
// compile-time assertion at the bottom of stub.go — if
// StubVerifier doesn't satisfy mining.AttestationVerifier, the
// file won't even compile. This test is here as documentation.
func TestStubVerifier_ImplementsInterface(t *testing.T) {
	var _ mining.AttestationVerifier = NewStubVerifier()
}

func containsString(haystack, needle string) bool {
	return len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	n, m := len(haystack), len(needle)
	for i := 0; i+m <= n; i++ {
		if haystack[i:i+m] == needle {
			return i
		}
	}
	return -1
}
