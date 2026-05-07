package telemetrycheck

// Verdict is the structured outcome of one Checker.Check
// call. Marshallable to JSON for the public
// /api/v1/mining/spec-anomalies endpoint.
//
// Field naming aligns with /metrics label conventions:
// VerdictKind values appear verbatim as label values on the
// qsdm_mining_spec_check_total counter, so renaming a Kind
// constant rolls a Prometheus dashboard. Don't rename
// without coordinating with the dashboard files in
// QSDM/docs/docs/ops/.
type Verdict struct {
	// Kind is the high-level outcome — see VerdictKind
	// constants for the four possible values.
	Kind VerdictKind `json:"kind"`

	// Mismatches enumerates every individual field that
	// failed. Empty when Kind == VerdictMatch.
	// Populated incrementally during a Check pass: a single
	// proof can flag both compute_cap AND driver_ver in one
	// verdict, so the operator gets a complete picture
	// rather than tripping repeatedly on the first failure.
	Mismatches []FieldMismatch `json:"mismatches,omitempty"`

	// MatchedReferences lists the catalog SignerIDs whose
	// profiles matched this claim. Empty for VerdictMatch
	// against the static baseline (which has SignerID
	// "baseline"). Populated on Match so an operator can
	// follow the trust path: "this miner's claim was
	// validated by attesters X, Y, Z."
	MatchedReferences []string `json:"matched_references,omitempty"`

	// CatalogSize is the number of (signer, gpu_name)
	// pairs the catalog held when this verdict was issued.
	// Lets a verdict reader contextualise an "unknown_sku"
	// — a 1-entry catalog has every right to flag unknown
	// SKUs; a 200-entry catalog with the same flag is more
	// suspicious.
	CatalogSize int `json:"catalog_size"`
}

// VerdictKind enumerates the four possible Verdict.Kind
// values. Stable string forms — these appear in Prometheus
// labels and JSON wire output.
type VerdictKind string

const (
	// VerdictMatch — the claim is consistent with at least
	// one catalog entry. No mismatches, action = none.
	VerdictMatch VerdictKind = "match"

	// VerdictMismatch — at least one rule fired. Mismatches
	// is populated; the operator should investigate but
	// the proof is still accepted.
	VerdictMismatch VerdictKind = "mismatch"

	// VerdictUnknownSKU — no catalog entry exists for the
	// claimed gpu_name. Distinct from VerdictMismatch
	// because the right operator response differs:
	// "unknown" usually just means "publish more reference
	// profiles" rather than "this miner is lying".
	VerdictUnknownSKU VerdictKind = "unknown_sku"

	// VerdictSkipped — the catalog had nothing to compare
	// against, or the claim was empty. Counted separately
	// so a "no checks fired" period is observable in
	// /metrics rather than masquerading as universal match.
	VerdictSkipped VerdictKind = "skipped"
)

// FieldMismatch is one rule firing. Captures (a) which
// field disagreed, (b) what the bundle said, and (c) the
// catalog's view. Severity is a soft hint for operators
// triaging /metrics — there's no enforcement-driven
// distinction between "minor" and "major" today.
type FieldMismatch struct {
	// Field is the canonical short name of the rule —
	// "compute_cap", "gpu_name", "arch", "driver_ver_format".
	// Used as a Prometheus label too.
	Field string `json:"field"`

	// Got is the value the bundle carried (or a synthetic
	// description if the value was structurally absent —
	// e.g. "<empty>" for an empty gpu_name).
	Got string `json:"got"`

	// Expected lists the values the catalog WOULD have
	// accepted. May be empty for "unknown_sku"-adjacent
	// failures where the catalog has no opinion.
	Expected []string `json:"expected,omitempty"`

	// Severity is "minor" (informational, expected to
	// resolve once the catalog catches up) or "major"
	// (genuinely impossible combination — e.g. ampere
	// arch with CC 9.0).
	Severity string `json:"severity"`
}

// HasMajor reports whether at least one mismatch is rated
// major severity. Useful for emitters that want to surface
// only the consequential anomalies on a dashboard.
func (v Verdict) HasMajor() bool {
	for _, m := range v.Mismatches {
		if m.Severity == "major" {
			return true
		}
	}
	return false
}

// MismatchedFields returns just the field names of every
// mismatch. Used by the metrics emitter to increment the
// per-field counter with the same set of labels each time.
func (v Verdict) MismatchedFields() []string {
	if len(v.Mismatches) == 0 {
		return nil
	}
	out := make([]string, 0, len(v.Mismatches))
	for _, m := range v.Mismatches {
		out = append(out, m.Field)
	}
	return out
}
