package archcheck

// rejection_detail_test.go — locks the structured wrapper
// invariants the outer Verifier and the recent-rejections ring
// depend on:
//
//   - errors.Is(err, ErrArchUnknown / ErrArchGPUNameMismatch /
//     ErrArchCertSubjectMismatch) keeps working byte-for-byte
//     after the wrapper migration. Existing call sites
//     (pkg/mining/verifier.go, hmac/verifier.go, cc/verifier.go)
//     don't have to change.
//   - errors.As(err, &*RejectionDetail{}) walks the wrapper
//     chain — including the per-type verifier's
//     fmt.Errorf("...: %w: %w", err, sentinel) double-wrap —
//     and surfaces the structured GPUName / CertSubject /
//     GPUArch fields the outer verifier needs to populate the
//     RejectionEvent.
//   - The Error() rendering preserves the prior fmt.Errorf
//     output shape so log lines do not visibly drift after
//     the migration.

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// -----------------------------------------------------------------------------
// errors.Is preserved through Unwrap
// -----------------------------------------------------------------------------

func TestRejectionDetail_UnwrapPreservesIs_GPUName(t *testing.T) {
	err := ValidateBundleArchConsistencyHMAC(ArchHopper, "AMD Radeon RX 7900")
	if err == nil {
		t.Fatal("expected mismatch error")
	}
	if !errors.Is(err, ErrArchGPUNameMismatch) {
		t.Errorf("errors.Is must still match ErrArchGPUNameMismatch; got %v", err)
	}
}

func TestRejectionDetail_UnwrapPreservesIs_CertSubject(t *testing.T) {
	err := ValidateBundleArchConsistencyCC(ArchHopper, "NVIDIA GeForce RTX 4090")
	if err == nil {
		t.Fatal("expected mismatch error")
	}
	if !errors.Is(err, ErrArchCertSubjectMismatch) {
		t.Errorf("errors.Is must still match ErrArchCertSubjectMismatch; got %v", err)
	}
}

func TestRejectionDetail_UnwrapPreservesIs_OuterArchUnknown(t *testing.T) {
	_, err := ValidateOuterArch("future-arch-2099")
	if err == nil {
		t.Fatal("expected unknown-arch error")
	}
	if !errors.Is(err, ErrArchUnknown) {
		t.Errorf("errors.Is must still match ErrArchUnknown; got %v", err)
	}
}

// -----------------------------------------------------------------------------
// errors.As surfaces structured detail
// -----------------------------------------------------------------------------

func TestRejectionDetail_AsExtractsGPUName(t *testing.T) {
	err := ValidateBundleArchConsistencyHMAC(ArchHopper, "NVIDIA GeForce RTX 4090")
	var detail *RejectionDetail
	if !errors.As(err, &detail) {
		t.Fatalf("errors.As must find *RejectionDetail; got %v", err)
	}
	if detail.GPUName != "NVIDIA GeForce RTX 4090" {
		t.Errorf("GPUName: got %q, want %q", detail.GPUName, "NVIDIA GeForce RTX 4090")
	}
	if detail.GPUArch != "hopper" {
		t.Errorf("GPUArch: got %q, want %q", detail.GPUArch, "hopper")
	}
	if len(detail.Patterns) == 0 {
		t.Errorf("Patterns must be populated; got %v", detail.Patterns)
	}
	if detail.CertSubject != "" {
		t.Errorf("CertSubject should be empty on HMAC path; got %q", detail.CertSubject)
	}
}

func TestRejectionDetail_AsExtractsCertSubject(t *testing.T) {
	err := ValidateBundleArchConsistencyCC(ArchHopper, "NVIDIA GeForce RTX 4090")
	var detail *RejectionDetail
	if !errors.As(err, &detail) {
		t.Fatalf("errors.As must find *RejectionDetail; got %v", err)
	}
	if detail.CertSubject != "NVIDIA GeForce RTX 4090" {
		t.Errorf("CertSubject: got %q, want %q",
			detail.CertSubject, "NVIDIA GeForce RTX 4090")
	}
	if detail.GPUArch != "hopper" {
		t.Errorf("GPUArch: got %q, want %q", detail.GPUArch, "hopper")
	}
	if detail.GPUName != "" {
		t.Errorf("GPUName should be empty on CC path; got %q", detail.GPUName)
	}
}

func TestRejectionDetail_AsExtractsRawArch_OuterUnknown(t *testing.T) {
	_, err := ValidateOuterArch("future-arch-2099")
	var detail *RejectionDetail
	if !errors.As(err, &detail) {
		t.Fatalf("errors.As must find *RejectionDetail; got %v", err)
	}
	if detail.GPUArch != "future-arch-2099" {
		t.Errorf("GPUArch: got %q, want %q", detail.GPUArch, "future-arch-2099")
	}
}

// TestRejectionDetail_AsThroughDoubleWrap simulates the per-
// type verifier's fmt.Errorf("...: %w: %w", err, sentinel)
// double-wrap and confirms errors.As still finds the structured
// detail. This is the load-bearing invariant the outer verifier
// depends on.
func TestRejectionDetail_AsThroughDoubleWrap(t *testing.T) {
	innerErr := ValidateBundleArchConsistencyHMAC(ArchHopper, "AMD Radeon RX 7900")
	// The HMAC verifier wraps with mining.ErrAttestationSignatureInvalid
	// in production; we synthesise a stand-in sentinel here to
	// avoid an import cycle from this package.
	outerSentinel := errors.New("mining: attestation signature invalid (test stand-in)")
	wrapped := fmt.Errorf("hmac: %w: %w", innerErr, outerSentinel)

	var detail *RejectionDetail
	if !errors.As(wrapped, &detail) {
		t.Fatalf("errors.As through double-%%w must find *RejectionDetail; got %v", wrapped)
	}
	if detail.GPUName != "AMD Radeon RX 7900" {
		t.Errorf("GPUName: got %q", detail.GPUName)
	}
	// And both Is targets still match through the chain.
	if !errors.Is(wrapped, ErrArchGPUNameMismatch) {
		t.Error("errors.Is must still match ErrArchGPUNameMismatch through double-wrap")
	}
	if !errors.Is(wrapped, outerSentinel) {
		t.Error("errors.Is must still match the outer sentinel through double-wrap")
	}
}

// -----------------------------------------------------------------------------
// Error() rendering parity
// -----------------------------------------------------------------------------

func TestRejectionDetail_ErrorString_OuterUnknownPreservesAllowedSuffix(t *testing.T) {
	_, err := ValidateOuterArch("nope")
	got := err.Error()
	if !strings.Contains(got, "unknown gpu_arch") {
		t.Errorf("error string missing sentinel base: %q", got)
	}
	if !strings.Contains(got, `"nope"`) {
		t.Errorf("error string must quote the rejected arch: %q", got)
	}
	if !strings.Contains(got, "(allowed:") {
		t.Errorf("error string must surface the allowed list: %q", got)
	}
}

func TestRejectionDetail_ErrorString_HMACMismatchPreservesPatterns(t *testing.T) {
	err := ValidateBundleArchConsistencyHMAC(ArchHopper, "AMD Radeon RX 7900")
	got := err.Error()
	if !strings.Contains(got, `gpu_name="AMD Radeon RX 7900"`) {
		t.Errorf("error string must echo gpu_name: %q", got)
	}
	if !strings.Contains(got, `arch="hopper"`) {
		t.Errorf("error string must echo arch: %q", got)
	}
	if !strings.Contains(got, "patterns:") {
		t.Errorf("error string must surface patterns: %q", got)
	}
}

func TestRejectionDetail_ErrorString_HMACEmptyName(t *testing.T) {
	err := ValidateBundleArchConsistencyHMAC(ArchHopper, "")
	got := err.Error()
	if !strings.Contains(got, "empty gpu_name") {
		t.Errorf("error string must say 'empty gpu_name': %q", got)
	}
	if !strings.Contains(got, `"hopper"`) {
		t.Errorf("error string must quote the claimed arch: %q", got)
	}
}

func TestRejectionDetail_ErrorString_CCMismatchPreservesSubject(t *testing.T) {
	err := ValidateBundleArchConsistencyCC(ArchHopper, "NVIDIA GeForce RTX 4090")
	got := err.Error()
	if !strings.Contains(got, `cert subject="NVIDIA GeForce RTX 4090"`) {
		t.Errorf("error string must echo cert subject: %q", got)
	}
	if !strings.Contains(got, `arch="hopper"`) {
		t.Errorf("error string must echo claimed arch: %q", got)
	}
}

// -----------------------------------------------------------------------------
// nil and edge cases
// -----------------------------------------------------------------------------

func TestRejectionDetail_NilSafe(t *testing.T) {
	var d *RejectionDetail
	if got := d.Error(); !strings.Contains(got, "nil detail") {
		t.Errorf("nil detail Error: got %q", got)
	}
	if d.Unwrap() != nil {
		t.Errorf("nil detail Unwrap: got non-nil")
	}
}

func TestRejectionDetail_ZeroSentinelSafe(t *testing.T) {
	d := &RejectionDetail{}
	if got := d.Error(); !strings.Contains(got, "nil detail") {
		t.Errorf("zero detail Error: got %q", got)
	}
	if d.Unwrap() != nil {
		t.Errorf("zero detail Unwrap: got non-nil")
	}
}

// TestRejectionDetail_PatternsCopied locks the slice-defensive
// invariant: callers mutating their pattern slice after the
// constructor returns must not affect the wrapped detail.
func TestRejectionDetail_PatternsCopied(t *testing.T) {
	patterns := []string{"h100", "hopper"}
	d := newGPUNameMismatch(ArchHopper, "AMD", patterns)
	patterns[0] = "TAMPERED"
	if d.Patterns[0] != "h100" {
		t.Errorf("Patterns must be defensive-copied; got %q", d.Patterns[0])
	}
}
