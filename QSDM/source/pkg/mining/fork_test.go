package mining

import (
	"errors"
	"math"
	"testing"
	"time"
)

// TestForkConstantsRatified locks the values of the three ratified
// v2 protocol parameters. Every one of these values is pinned in
// QSDM/docs/docs/MINING_PROTOCOL_V2.md §13 (2026-04-24 owner
// sign-off); changing any of them at this layer is a consensus
// change and must be accompanied by a new ratification entry in
// §13 and a companion CHANGELOG note. The test exists so
// that change shows up as a failing test in every CI run, not as
// a quiet two-line diff.
func TestForkConstantsRatified(t *testing.T) {
	if ProtocolVersionV2 != 2 {
		t.Errorf("ProtocolVersionV2 = %d; ratified value is 2", ProtocolVersionV2)
	}
	if FreshnessWindow != 60*time.Second {
		t.Errorf("FreshnessWindow = %v; ratified value is 60s", FreshnessWindow)
	}
	if MinEnrollStakeDust != 10*100_000_000 {
		t.Errorf("MinEnrollStakeDust = %d; ratified value is 10 * 10^8 dust (10 CELL)", MinEnrollStakeDust)
	}
	if AttestationTypeCC != "nvidia-cc-v1" {
		t.Errorf(`AttestationTypeCC = %q; ratified value is "nvidia-cc-v1"`, AttestationTypeCC)
	}
	if AttestationTypeHMAC != "nvidia-hmac-v1" {
		t.Errorf(`AttestationTypeHMAC = %q; ratified value is "nvidia-hmac-v1"`, AttestationTypeHMAC)
	}
}

// TestForkV2HeightDefault confirms v2 is disabled by default at
// package init time. If this ever flips (e.g. someone tries to
// "helpfully" default it to 0) a test in every downstream package
// that touches mining will start failing, which is exactly the
// signal we want — Phase 4 owns the activation decision.
func TestForkV2HeightDefault(t *testing.T) {
	got := ForkV2Height()
	if got != math.MaxUint64 {
		t.Errorf("ForkV2Height() = %d; default must be math.MaxUint64 (v2 disabled until Phase 4)", got)
	}
	// Spot-check the IsV2 gate at a realistic production height.
	if IsV2(1_000_000) {
		t.Errorf("IsV2(1_000_000) must be false when fork height is the default")
	}
	if IsV2(math.MaxUint64) {
		// math.MaxUint64 >= math.MaxUint64 is true — the boundary
		// is inclusive by design (MINING_PROTOCOL_V2 §8.1). Document
		// that behaviour with a guard instead of asserting
		// otherwise.
		t.Logf("IsV2(MaxUint64) is true — expected, inclusive boundary")
	}
}

// TestSetForkV2Height exercises the runtime-set path the Phase 4
// activation (and every test that needs to exercise v2 code paths)
// will use. The test restores the default at exit so other tests
// in this package keep seeing the default.
func TestSetForkV2Height(t *testing.T) {
	orig := ForkV2Height()
	t.Cleanup(func() { SetForkV2Height(orig) })

	SetForkV2Height(0)
	if !IsV2(0) {
		t.Errorf("IsV2(0) must be true after SetForkV2Height(0)")
	}
	if !IsV2(1) {
		t.Errorf("IsV2(1) must be true after SetForkV2Height(0)")
	}

	SetForkV2Height(100)
	if IsV2(99) {
		t.Errorf("IsV2(99) must be false when fork activates at 100")
	}
	if !IsV2(100) {
		t.Errorf("IsV2(100) must be true when fork activates at 100 (inclusive boundary)")
	}
	if !IsV2(101) {
		t.Errorf("IsV2(101) must be true when fork activates at 100")
	}
}

// TestAttestationSentinelErrorsDistinct confirms every sentinel
// error added in fork.go is a distinct value — critical for
// errors.Is dispatch in downstream code and for per-reason
// Prometheus counters.
func TestAttestationSentinelErrorsDistinct(t *testing.T) {
	sentinels := []error{
		ErrAttestationRequired,
		ErrAttestationTypeUnknown,
		ErrAttestationStale,
		ErrAttestationNonceMismatch,
		ErrAttestationSignatureInvalid,
		ErrAttestationBundleMalformed,
	}
	seen := make(map[string]bool, len(sentinels))
	for _, e := range sentinels {
		if e == nil {
			t.Error("sentinel must not be nil")
			continue
		}
		if seen[e.Error()] {
			t.Errorf("sentinel message collision: %q appears twice", e.Error())
		}
		seen[e.Error()] = true
		// Ensure each sentinel is errors.Is-compatible with
		// itself — trivially true for package-level var errors,
		// but worth asserting in case someone refactors to a
		// method-based error type.
		if !errors.Is(e, e) {
			t.Errorf("errors.Is(%v, %v) must be true", e, e)
		}
	}
}
