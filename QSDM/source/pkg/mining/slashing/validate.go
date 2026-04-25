package slashing

import "fmt"

// MaxNodeIDLen mirrors enrollment.MaxNodeIDLen for cross-package
// consistency. Hard-coded (rather than imported) to keep this
// package free of upward dependencies.
const MaxNodeIDLen = 64

// ValidateSlashFields runs the stateless portion of slashing
// validation. Returns nil on accept, or an error wrapping one
// of the Err* sentinels.
//
// Stateless = doesn't touch the EnrollmentState or the
// per-evidence-kind verifier. Catches malformed traffic at the
// mempool admission gate before the verifier dispatch (which
// can be expensive — cryptographic signature checks, BFT
// equivocation analysis, etc.) ever runs.
func ValidateSlashFields(p SlashPayload, sender string) error {
	if sender == "" {
		return fmt.Errorf("%w: sender required", ErrPayloadInvalid)
	}
	if p.NodeID == "" {
		return fmt.Errorf("%w: node_id is empty", ErrPayloadInvalid)
	}
	if len(p.NodeID) > MaxNodeIDLen {
		return fmt.Errorf("%w: node_id length %d > MaxNodeIDLen %d",
			ErrPayloadInvalid, len(p.NodeID), MaxNodeIDLen)
	}
	if !validKind(p.EvidenceKind) {
		return fmt.Errorf("%w: %q", ErrUnknownEvidenceKind, p.EvidenceKind)
	}
	if len(p.EvidenceBlob) == 0 {
		return fmt.Errorf("%w: evidence_blob is empty", ErrPayloadInvalid)
	}
	if len(p.EvidenceBlob) > MaxEvidenceLen {
		return fmt.Errorf("%w: evidence_blob length %d > MaxEvidenceLen %d",
			ErrPayloadInvalid, len(p.EvidenceBlob), MaxEvidenceLen)
	}
	if p.SlashAmountDust == 0 {
		return fmt.Errorf("%w: slash_amount_dust must be > 0", ErrPayloadInvalid)
	}
	if len(p.Memo) > MaxMemoLen {
		return fmt.Errorf("%w: memo length %d > MaxMemoLen %d",
			ErrPayloadInvalid, len(p.Memo), MaxMemoLen)
	}
	return nil
}

// validKind reports whether `kind` is one of AllEvidenceKinds.
// O(N) on a tiny slice — fine, and avoids a map for code that's
// invoked once per slash tx.
func validKind(kind EvidenceKind) bool {
	for _, k := range AllEvidenceKinds {
		if k == kind {
			return true
		}
	}
	return false
}
