package enrollment

// validate.go: consensus-layer validation of enroll/unenroll
// payloads. Two-stage by design:
//
//  1. ValidateEnrollFields / ValidateUnenrollFields — stateless
//     checks (length bounds, well-formedness). Safe to run in
//     the mempool before the tx has a position in the chain.
//
//  2. ValidateEnrollAgainstState / ValidateUnenrollAgainstState
//     — stateful checks (nodeID uniqueness, GPU UUID
//     uniqueness, ownership). Require an EnrollmentState read.
//
// The state-transition code in pkg/chain (follow-on commit)
// will call BOTH stages in order. Rejections at stage 1 are
// attributable to the miner's client; rejections at stage 2
// can legitimately race (two enrolls in the same block for the
// same node_id) and should be turned into block-level reject
// receipts, not mempool 400s.

import (
	"fmt"
	"strings"

	"github.com/blackbeardONE/QSDM/pkg/mining"
)

// ValidateEnrollFields runs the stateless portion of enroll
// validation. Pass the decoded payload and the sender's
// address. Returns nil on accept or an error wrapping one of
// the Err* sentinels.
//
// The sender address is required for one check: we reject
// empty senders here so downstream code can assume a non-empty
// Owner field when it later constructs the EnrollmentRecord.
func ValidateEnrollFields(p EnrollPayload, sender string) error {
	if p.Kind != PayloadKindEnroll {
		return fmt.Errorf("%w: kind must be %q, got %q",
			ErrPayloadInvalid, PayloadKindEnroll, p.Kind)
	}
	if err := validateNodeID(p.NodeID); err != nil {
		return err
	}
	if err := validateGPUUUID(p.GPUUUID); err != nil {
		return err
	}
	if len(p.HMACKey) < MinHMACKeyLen {
		return fmt.Errorf("%w: hmac_key length %d < MinHMACKeyLen %d",
			ErrPayloadInvalid, len(p.HMACKey), MinHMACKeyLen)
	}
	if len(p.HMACKey) > MaxHMACKeyLen {
		return fmt.Errorf("%w: hmac_key length %d > MaxHMACKeyLen %d",
			ErrPayloadInvalid, len(p.HMACKey), MaxHMACKeyLen)
	}
	if p.StakeDust != mining.MinEnrollStakeDust {
		return fmt.Errorf("%w: stake_dust %d != required %d",
			ErrStakeMismatch, p.StakeDust, mining.MinEnrollStakeDust)
	}
	if len(p.Memo) > MaxMemoLen {
		return fmt.Errorf("%w: memo length %d > MaxMemoLen %d",
			ErrPayloadInvalid, len(p.Memo), MaxMemoLen)
	}
	if sender == "" {
		return fmt.Errorf("%w: sender address required for enrollment", ErrPayloadInvalid)
	}
	return nil
}

// ValidateUnenrollFields is the stateless half for unenroll.
func ValidateUnenrollFields(p UnenrollPayload, sender string) error {
	if p.Kind != PayloadKindUnenroll {
		return fmt.Errorf("%w: kind must be %q, got %q",
			ErrPayloadInvalid, PayloadKindUnenroll, p.Kind)
	}
	if err := validateNodeID(p.NodeID); err != nil {
		return err
	}
	if len(p.Reason) > MaxMemoLen {
		return fmt.Errorf("%w: reason length %d > MaxMemoLen %d",
			ErrPayloadInvalid, len(p.Reason), MaxMemoLen)
	}
	if sender == "" {
		return fmt.Errorf("%w: sender address required for unenrollment", ErrPayloadInvalid)
	}
	return nil
}

// ValidateEnrollAgainstState runs the stateful portion: node_id
// and gpu_uuid uniqueness, sender-balance ≥ stake. Call this
// AFTER ValidateEnrollFields has passed.
//
// senderBalanceDust MUST be the sender's current balance AT the
// point the enroll tx is applied — NOT including the
// enroll-transaction's own stake debit. The balance check
// happens first; the stake lock happens as a consequence of
// this tx succeeding.
func ValidateEnrollAgainstState(
	p EnrollPayload,
	senderBalanceDust uint64,
	state EnrollmentState,
) error {
	if state == nil {
		return fmt.Errorf("%w: enrollment state is nil", ErrPayloadInvalid)
	}
	if senderBalanceDust < p.StakeDust {
		return fmt.Errorf("%w: sender has %d dust, need %d",
			ErrInsufficientBalance, senderBalanceDust, p.StakeDust)
	}

	// node_id uniqueness. A revoked-but-not-yet-matured record
	// still owns the node_id; its owner must wait for the
	// unbond to mature before someone (possibly themselves) can
	// re-use the name.
	existing, err := state.Lookup(p.NodeID)
	if err != nil {
		return fmt.Errorf("enrollment: state Lookup node_id: %w", err)
	}
	if existing != nil {
		return fmt.Errorf("%w: node_id %q (enrolled at height %d, revoked=%d)",
			ErrNodeIDTaken, p.NodeID, existing.EnrolledAtHeight, existing.RevokedAtHeight)
	}

	// gpu_uuid uniqueness among ACTIVE bindings only. State
	// implementations are expected to return "" when the only
	// binding is to a revoked record — callers inferred the
	// contract from the GPUUUIDBound doc string in types.go.
	boundTo, err := state.GPUUUIDBound(p.GPUUUID)
	if err != nil {
		return fmt.Errorf("enrollment: state GPUUUIDBound: %w", err)
	}
	if boundTo != "" {
		return fmt.Errorf("%w: gpu_uuid bound to node_id %q", ErrGPUUUIDTaken, boundTo)
	}

	return nil
}

// ValidateUnenrollAgainstState checks ownership + not-already-
// unenrolled. Call after ValidateUnenrollFields.
func ValidateUnenrollAgainstState(
	p UnenrollPayload,
	sender string,
	state EnrollmentState,
) error {
	if state == nil {
		return fmt.Errorf("%w: enrollment state is nil", ErrPayloadInvalid)
	}
	rec, err := state.Lookup(p.NodeID)
	if err != nil {
		return fmt.Errorf("enrollment: state Lookup node_id: %w", err)
	}
	if rec == nil {
		return fmt.Errorf("%w: node_id %q not enrolled",
			ErrNodeNotOwned, p.NodeID)
	}
	if rec.Owner != sender {
		return fmt.Errorf("%w: node_id %q owned by %q, sender is %q",
			ErrNodeNotOwned, p.NodeID, rec.Owner, sender)
	}
	if !rec.Active() {
		return fmt.Errorf("%w: node_id %q unenrolled at height %d",
			ErrNodeAlreadyUnenrolled, p.NodeID, rec.RevokedAtHeight)
	}
	return nil
}

// validateNodeID enforces the shape of a node_id string. Kept
// private because callers shouldn't invoke it directly; the
// top-level Validate* functions call it as part of a bigger
// check.
//
// Characters allowed: lowercase ASCII letters, digits, hyphen,
// underscore. Matches what real-world miner tooling generates
// without requiring unicode normalization. No spaces, no
// punctuation, no case-folding surprises.
func validateNodeID(nodeID string) error {
	if nodeID == "" {
		return fmt.Errorf("%w: node_id is empty", ErrPayloadInvalid)
	}
	if len(nodeID) > MaxNodeIDLen {
		return fmt.Errorf("%w: node_id length %d > MaxNodeIDLen %d",
			ErrPayloadInvalid, len(nodeID), MaxNodeIDLen)
	}
	for i, c := range nodeID {
		switch {
		case c >= 'a' && c <= 'z':
		case c >= '0' && c <= '9':
		case c == '-' || c == '_':
		default:
			return fmt.Errorf("%w: node_id char %d (%q) not in [a-z0-9_-]",
				ErrPayloadInvalid, i, string(c))
		}
	}
	return nil
}

// validateGPUUUID is lenient on format because nvidia-smi UUIDs
// don't always follow RFC 4122 (driver version, vendor, and
// virtualization flavour all influence the exact shape). We
// require non-empty, bounded length, and that the string is
// printable ASCII — nothing more. The hmac verifier's step 5
// already case-folds GPU UUIDs via strings.EqualFold.
func validateGPUUUID(uuid string) error {
	if uuid == "" {
		return fmt.Errorf("%w: gpu_uuid is empty", ErrPayloadInvalid)
	}
	if len(uuid) > MaxGPUUUIDLen {
		return fmt.Errorf("%w: gpu_uuid length %d > MaxGPUUUIDLen %d",
			ErrPayloadInvalid, len(uuid), MaxGPUUUIDLen)
	}
	// Reject embedded whitespace to avoid "GPU-abc " vs
	// "GPU-abc" confusion (they would hash differently and
	// break the uniqueness check) as well as control characters.
	for i, c := range uuid {
		if c < 0x21 || c > 0x7E {
			return fmt.Errorf("%w: gpu_uuid char %d (%U) not printable ASCII",
				ErrPayloadInvalid, i, c)
		}
	}
	// Enforce uppercase for the "GPU-" prefix if present — the
	// canonical nvidia-smi form is uppercase. This is a soft
	// normalisation to reduce mistyping issues.
	if strings.HasPrefix(uuid, "gpu-") {
		return fmt.Errorf("%w: gpu_uuid should start with 'GPU-' (uppercase), got %q",
			ErrPayloadInvalid, uuid[:4])
	}
	return nil
}
