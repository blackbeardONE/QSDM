package slashing

import "errors"

// ContractID tags a mempool.Tx as a slashing transaction.
// Bumping the suffix is the migration path for future formats.
const ContractID = "qsdm/slash/v1"

// EvidenceKind enumerates the slashable offence categories the
// protocol recognises today. Each kind has its own cryptographic
// verification spec living in a sub-package of pkg/mining/slashing
// (forged_attestation/, double_mining/, freshness_cheat/, ...);
// the canonical evidence bytes are opaque at this layer.
//
// Adding a new kind is forward-compatible:
//
//   1. Define a string constant here.
//   2. Implement EvidenceVerifier in a new sub-package.
//   3. Wire it into the production Dispatcher.
//
// Removing a kind is a hard fork.
type EvidenceKind string

const (
	// EvidenceKindForgedAttestation: the slashed miner produced
	// a Proof.Attestation that fails verifier checks (bad MAC,
	// stale challenge, mismatched gpu_uuid against the
	// EnrollmentRecord). The evidence blob carries the offending
	// proof + the context required to deterministically replay
	// the verifier failure.
	EvidenceKindForgedAttestation EvidenceKind = "forged-attestation"

	// EvidenceKindDoubleMining: the slashed miner submitted two
	// distinct proofs at the same height, both passing
	// freshness, both attributable to the same NodeID. The
	// evidence blob carries both proofs.
	EvidenceKindDoubleMining EvidenceKind = "double-mining"

	// EvidenceKindFreshnessCheat: the slashed miner re-used a
	// challenge nonce after the FreshnessWindow expiry, or
	// committed to a future-dated IssuedAt. Evidence blob
	// carries the offending proof + observed-at timestamp.
	EvidenceKindFreshnessCheat EvidenceKind = "freshness-cheat"
)

// AllEvidenceKinds enumerates the kinds defined in this version.
// Used by tests and the production dispatcher's coverage check.
var AllEvidenceKinds = []EvidenceKind{
	EvidenceKindForgedAttestation,
	EvidenceKindDoubleMining,
	EvidenceKindFreshnessCheat,
}

// SlashPayload is the canonical wire format. Encoded into
// mempool.Tx.Payload with ContractID == slashing.ContractID.
//
// The submitter (Tx.Sender) is whoever caught the offence —
// could be any peer. They earn nothing today; future
// governance may introduce a bounty paid out of the slashed
// stake, but that's beyond the scope of this scaffolding.
type SlashPayload struct {
	// NodeID identifies the enrolled miner being slashed.
	NodeID string `json:"node_id"`

	// EvidenceKind selects the verifier implementation.
	EvidenceKind EvidenceKind `json:"evidence_kind"`

	// EvidenceBlob is opaque at this layer — the per-kind
	// verifier owns the encoding. Bounded to MaxEvidenceLen so
	// a malformed slash tx cannot bloat the mempool.
	EvidenceBlob []byte `json:"evidence_blob"`

	// SlashAmountDust is the proposed forfeiture amount in
	// dust. The verifier MAY clamp it: e.g. a verifier might
	// say "this offence is worth at most 10% of stake" and the
	// applier will use min(SlashAmountDust, verifier-cap). The
	// field is present in the payload (rather than computed
	// from EvidenceKind alone) so governance can tune severity
	// over time without re-encoding evidence.
	SlashAmountDust uint64 `json:"slash_amount_dust"`

	// Memo is optional human-readable context (forensics
	// shorthand). Bounded to MaxMemoLen.
	Memo string `json:"memo,omitempty"`
}

// MaxEvidenceLen bounds the EvidenceBlob field. 1 MiB is large
// enough to carry a full v2 proof + attestation chain + a
// witness signature without forcing fragmentation, and small
// enough that one slash tx cannot dominate a block.
const MaxEvidenceLen = 1 << 20

// MaxMemoLen mirrors enrollment.MaxMemoLen for consistency.
const MaxMemoLen = 256

// Validation sentinels. Errors from the validators wrap one of
// these so callers can errors.Is against categories.
var (
	// ErrPayloadDecode is returned when the JSON is malformed.
	ErrPayloadDecode = errors.New("slashing: payload decode failed")

	// ErrPayloadInvalid is returned when the payload parses
	// but a stateless rule rejects it (empty NodeID, unknown
	// kind, oversized blob, etc.).
	ErrPayloadInvalid = errors.New("slashing: payload invalid")

	// ErrUnknownEvidenceKind is returned when EvidenceKind
	// does not correspond to a registered verifier.
	ErrUnknownEvidenceKind = errors.New("slashing: unknown evidence kind")

	// ErrEvidenceVerification is returned by EvidenceVerifier
	// implementations when the evidence does NOT prove the
	// alleged offence. Wrapping ensures callers can route
	// "no offence proven" failures separately from "decoder
	// crashed" failures.
	ErrEvidenceVerification = errors.New("slashing: evidence does not prove offence")

	// ErrNodeNotEnrolled is the chain-side rejection when the
	// target NodeID has no enrollment record (or the record
	// has already matured-and-swept). Returned by the
	// stateful path.
	ErrNodeNotEnrolled = errors.New("slashing: target node_id is not enrolled")
)
