package archcheck

// rejection_detail.go — structured wrapper error type carried
// out of ValidateBundleArchConsistencyHMAC / ...CC so call
// sites further up the stack can capture the OFFENDING value
// (gpu_name on the HMAC path, leaf cert subject on the CC
// path) without a side-channel.
//
// Why a structured wrapper instead of growing the function
// signatures:
//
//	The verifier hot path is layered:
//
//	    pkg/mining/verifier.go  (outer, fork gate + arch enum)
//	      └─ pkg/mining/attest/{hmac,cc}/verifier.go  (per-type)
//	          └─ pkg/mining/attest/archcheck             (policy)
//
//	Today the recent-rejections ring (see pkg/mining/attest/
//	recentrejects) populates GPUName / CertSubject with empty
//	strings because the OUTER verifier — which calls the per-
//	type verifier through the AttestationVerifier interface —
//	never sees the bundle or the cert. The per-type verifier
//	parses both, but only returns a string-wrapped error that
//	loses the structured detail by the time the outer caller
//	hits errors.Is.
//
//	A structured wrapper traversed via errors.As is the
//	canonical Go idiom for exactly this — pluck the detail
//	without touching the per-type verifier's interface
//	signature. Backward-compatible: every existing
//	`errors.Is(err, archcheck.ErrArch...)` site keeps working
//	because Unwrap returns the sentinel.
//
// Why three error fields and not one:
//
//	The same wrapper is returned from three different rejection
//	sites — ValidateOuterArch (Sentinel=ErrArchUnknown) and the
//	HMAC / CC consistency checks. Operators consuming the wire
//	view want the populated field set to tell them which
//	rejection site fired without a second errors.Is dispatch.
//	Empty fields naturally round-trip as omitempty in the JSON
//	view.

import (
	"fmt"
	"strings"
)

// RejectionDetail is the structured wrapper returned from each
// archcheck rejection site. The .Sentinel field carries the
// canonical sentinel (one of ErrArchUnknown,
// ErrArchGPUNameMismatch, ErrArchCertSubjectMismatch); .Unwrap
// returns it so existing errors.Is(err, archcheck.ErrArch*)
// callers keep working byte-identically.
//
// The remaining fields surface the offending operator-supplied
// values for consumption by:
//
//   - pkg/mining/verifier.go's recordRejectionForArchSpoof,
//     which populates the recentrejects.Rejection {GPUName,
//     CertSubject} fields, in turn surfaced through
//     /api/v1/attest/recent-rejections and
//     `qsdmcli watch archspoof --detailed`.
//
//   - Any future operator-facing structured logging hook that
//     wants the detail without re-parsing free-form error
//     messages.
//
// Field availability:
//
//   - Sentinel: ALWAYS populated.
//   - GPUArch: populated whenever the caller knew the canonical
//     or raw arch string. The HMAC / CC consistency paths know
//     the canonical arch; ValidateOuterArch knows only the raw
//     operator value.
//   - GPUName: populated by ValidateBundleArchConsistencyHMAC.
//     Empty everywhere else.
//   - CertSubject: populated by ValidateBundleArchConsistencyCC.
//     Empty everywhere else.
//   - Patterns: populated on mismatch paths so the operator log
//     shows "what we were looking for" alongside "what we got".
type RejectionDetail struct {
	// Sentinel is the canonical archcheck sentinel this
	// rejection wraps. Required.
	Sentinel error

	// GPUArch is the canonical or raw arch string the proof
	// claimed.
	GPUArch string

	// GPUName is the bundle-reported gpu_name on the HMAC
	// rejection path.
	GPUName string

	// CertSubject is the leaf cert Subject value (typically
	// CommonName) on the CC rejection path.
	CertSubject string

	// Patterns is the substring set we tried to match the
	// gpu_name / cert subject against. Useful for operator
	// log lines and structured-detail readers; ignored by
	// errors.Is dispatch.
	Patterns []string
}

// Error renders a human-readable detail consistent with the
// pre-existing fmt.Errorf("%w: ...", sentinel, ...) shape, so
// log lines do not visibly drift after the structured wrapper
// migration. Format is one of:
//
//	archcheck: ... : empty gpu_name (claimed arch X)
//	archcheck: ... : gpu_name="..." does not match arch="..." (patterns: ...)
//	archcheck: ... : cert subject="..." strongest-evidence patterns=[...] but claimed arch=X
//	archcheck: unknown gpu_arch: "..." (allowed: ...)
//
// Drives ALL three rejection sites; the helper picks the right
// format from the populated field set, so a future call site
// that returns a RejectionDetail with only Sentinel + GPUArch
// gets a sensible default rather than a blank message.
func (r *RejectionDetail) Error() string {
	if r == nil || r.Sentinel == nil {
		return "archcheck: rejection (nil detail)"
	}
	base := r.Sentinel.Error()
	switch {
	case r.Sentinel == ErrArchUnknown && r.GPUArch != "" && len(r.Patterns) > 0:
		// Outer-arch path: matches the prior fmt.Errorf
		// rendering byte-for-byte ("%w: %q (allowed: %s)").
		return fmt.Sprintf("%s: %q (allowed: %s)",
			base, r.GPUArch, r.Patterns[0])
	case r.GPUName != "" && len(r.Patterns) > 0:
		return fmt.Sprintf("%s: gpu_name=%q does not match arch=%q (patterns: %s)",
			base, r.GPUName, r.GPUArch, strings.Join(r.Patterns, ", "))
	case r.GPUName == "" && r.CertSubject == "" && r.GPUArch != "" && r.Sentinel == ErrArchGPUNameMismatch:
		// Empty gpu_name special case — preserves the prior
		// "%w: empty gpu_name (claimed arch %q)" message.
		return fmt.Sprintf("%s: empty gpu_name (claimed arch %q)",
			base, r.GPUArch)
	case r.CertSubject != "" && len(r.Patterns) > 0:
		return fmt.Sprintf("%s: cert subject=%q strongest-evidence patterns=%v but claimed arch=%q",
			base, r.CertSubject, r.Patterns, r.GPUArch)
	case r.CertSubject != "":
		return fmt.Sprintf("%s: cert subject=%q claimed arch=%q",
			base, r.CertSubject, r.GPUArch)
	case r.GPUArch != "":
		return fmt.Sprintf("%s: %q", base, r.GPUArch)
	default:
		return base
	}
}

// Unwrap returns the canonical sentinel so existing callers
// using errors.Is(err, archcheck.ErrArch*) keep working
// without modification. The structural detail (GPUName /
// CertSubject / Patterns) is reachable via errors.As targeting
// *RejectionDetail.
func (r *RejectionDetail) Unwrap() error {
	if r == nil {
		return nil
	}
	return r.Sentinel
}

// newGPUNameMismatchEmpty constructs the "empty gpu_name"
// detail. Centralised so the three call shapes stay
// byte-consistent.
func newGPUNameMismatchEmpty(arch Architecture) *RejectionDetail {
	return &RejectionDetail{
		Sentinel: ErrArchGPUNameMismatch,
		GPUArch:  string(arch),
	}
}

// newGPUNameMismatch constructs the "gpu_name does not match"
// detail. Patterns is the substring set we tried (so operator
// log lines tell them WHAT we expected).
func newGPUNameMismatch(arch Architecture, gpuName string, patterns []string) *RejectionDetail {
	return &RejectionDetail{
		Sentinel: ErrArchGPUNameMismatch,
		GPUArch:  string(arch),
		GPUName:  gpuName,
		Patterns: append([]string(nil), patterns...),
	}
}

// newCertSubjectMismatch constructs the CC-path "cert subject
// contradicts arch" detail. Patterns carries the strongest-
// match labels (e.g. ["hopper(\"h100\")", "ada(\"rtx 4090\")"])
// for operator readability — clients consume the structured
// CertSubject + GPUArch fields and ignore the formatted
// patterns string.
func newCertSubjectMismatch(arch Architecture, certSubject string, patterns []string) *RejectionDetail {
	return &RejectionDetail{
		Sentinel:    ErrArchCertSubjectMismatch,
		GPUArch:     string(arch),
		CertSubject: certSubject,
		Patterns:    append([]string(nil), patterns...),
	}
}

// newOuterArchUnknown constructs the ValidateOuterArch
// detail. GPUArch carries the raw (non-canonical) operator
// value so the wire view can surface it on the
// archspoof_unknown_arch path.
func newOuterArchUnknown(rawArch, allowed string) *RejectionDetail {
	return &RejectionDetail{
		Sentinel: ErrArchUnknown,
		GPUArch:  rawArch,
		Patterns: []string{allowed}, // single-string allowlist for the message
	}
}
