package enrollment

import (
	"errors"
	"time"
)

// ContractID is the mempool.Tx.ContractID value that tags a
// transaction as a miner-enrollment operation. The state
// transition code dispatches on this string before parsing the
// payload.
//
// Format "qsdm/enroll/v1": the "/v1" suffix is intentional. If
// a future fork changes the enroll payload shape (e.g. adds
// asymmetric-signature keys), we bump to "qsdm/enroll/v2" and
// the state transition code can run both paths during a grace
// window.
const ContractID = "qsdm/enroll/v1"

// PayloadKind tags the two enrollment payload shapes that share
// the same ContractID. Encoded as the first field of the JSON
// payload so the decoder can dispatch before accessing
// variant-specific fields.
type PayloadKind string

const (
	// PayloadKindEnroll registers a (node_id, gpu_uuid,
	// hmac_key) tuple and locks MinEnrollStakeDust from the
	// sender's balance.
	PayloadKindEnroll PayloadKind = "enroll"

	// PayloadKindUnenroll begins the unbond process for a
	// previously-enrolled node_id. The stake is NOT released
	// immediately — the record's UnbondMaturesAtHeight is set
	// to currentHeight + UnbondWindow, and release is the job
	// of a block-time sweep.
	PayloadKindUnenroll PayloadKind = "unenroll"
)

// UnbondWindow is the number of blocks the stake stays locked
// after an unenroll. Prevents grief attacks where an operator
// races the slashing window by unenrolling the moment a bad
// proof is detected.
//
// 7 days at 3-second blocks ≈ 201,600 blocks. Governance can
// adjust post-fork — the constant here is the genesis default.
// Exported as a var rather than a const because a future fork
// may override it via chain config, and const would require a
// code change to adjust.
var UnbondWindow uint64 = 7 * 24 * 60 * 60 / 3

// MinHMACKeyLen is the minimum HMAC key length accepted at
// enrollment, in bytes. 32 bytes = 256 bits, matching the
// output size of SHA-256 which is what pkg/mining/attest/hmac
// uses under the hood. Keys shorter than the hash output give
// no security benefit over a shorter hash and are easier to
// brute-force catalog.
const MinHMACKeyLen = 32

// MaxHMACKeyLen prevents a misconfigured operator from
// committing megabytes of "key" to chain state. 128 bytes is
// far more than needed and still comfortably small on disk.
const MaxHMACKeyLen = 128

// MaxNodeIDLen and MaxGPUUUIDLen bound the size of user-
// provided string fields so an attacker cannot inflate state
// by submitting thousand-character node_ids.
const (
	MaxNodeIDLen  = 64
	MaxGPUUUIDLen = 96
)

// EnrollPayload is the consensus-critical wire format of a
// miner-enrollment transaction. Encoded as canonical JSON into
// mempool.Tx.Payload with ContractID == ContractID.
//
// All fields are validated by ValidateEnrollPayload. The sender
// (Tx.Sender address) becomes the Owner of the resulting
// EnrollmentRecord — it is NOT repeated in the payload, because
// deriving it from the signed Sender field makes it impossible
// for a third party to replay someone else's enrollment.
type EnrollPayload struct {
	// Kind MUST equal PayloadKindEnroll. Redundant-looking field
	// (ContractID already tells dispatch where to go) but
	// belt-and-braces: a client that gets ContractID right and
	// Kind wrong gets a clean rejection instead of an ambiguous
	// decode failure.
	Kind PayloadKind `json:"kind"`

	// NodeID is the operator's handle for this GPU. Must be
	// unique across the registry (any already-enrolled node_id
	// causes rejection — there is no "update" path, only
	// Unenroll + Enroll).
	NodeID string `json:"node_id"`

	// GPUUUID is the nvidia-smi UUID of the GPU being enrolled.
	// MUST be unique: one physical GPU cannot be bound to two
	// different node_ids simultaneously. This is the economic
	// anti-Sybil anchor — without it, one GPU could enroll a
	// thousand node_ids and pretend to be a thousand miners.
	GPUUUID string `json:"gpu_uuid"`

	// HMACKey is the symmetric key the operator will use to
	// sign bundles. See the package doc for why this is public
	// chain state and what the security model is. Must be at
	// least MinHMACKeyLen bytes.
	HMACKey []byte `json:"hmac_key"`

	// StakeDust is the amount of dust the sender commits to
	// lock. MUST equal MinEnrollStakeDust (mining.MinEnrollStakeDust
	// at fork activation; governance-adjustable post-fork).
	// Explicit in the payload rather than implied so an operator
	// who misunderstands the required value gets a clear
	// validation error instead of a silent partial debit.
	StakeDust uint64 `json:"stake_dust"`

	// Memo is a free-form optional field operators can use to
	// tag the enrollment (rig name, location, etc.). Not
	// consensus-critical; included in the canonical hash so
	// tampering with it invalidates the signature. Bounded to
	// 256 bytes to avoid state inflation.
	Memo string `json:"memo,omitempty"`
}

// MaxMemoLen bounds the Memo field at 256 bytes.
const MaxMemoLen = 256

// UnenrollPayload begins the unbond process for an enrolled
// node_id. The sender MUST be the Owner of the record or the tx
// is rejected.
type UnenrollPayload struct {
	// Kind MUST equal PayloadKindUnenroll.
	Kind PayloadKind `json:"kind"`

	// NodeID identifies which enrollment is being wound down.
	NodeID string `json:"node_id"`

	// Reason is an optional free-form note (e.g. "retiring GPU,
	// upgrading to 5090"). Not consensus-critical.
	Reason string `json:"reason,omitempty"`
}

// EnrollmentRecord is the on-chain state entry produced by a
// successful Enroll transaction. Stored keyed by NodeID. The
// entire struct is covered by the chain's state-root hash so
// any mutation requires consensus.
type EnrollmentRecord struct {
	// NodeID is the primary key. Matches the Enroll payload.
	NodeID string `json:"node_id"`

	// Owner is the address of the sender who enrolled this
	// node_id. The authority for Unenroll / Revoke operations.
	Owner string `json:"owner"`

	// GPUUUID is copied from the Enroll payload. See the
	// hmac.Registry contract — bundle.gpu_uuid MUST match this
	// or the proof is rejected.
	GPUUUID string `json:"gpu_uuid"`

	// HMACKey is the operator's shared signing key. Public
	// chain state — see the package doc.
	HMACKey []byte `json:"hmac_key"`

	// StakeDust is the amount locked at enrollment. Returned
	// to Owner after UnbondWindow elapses post-unenroll.
	StakeDust uint64 `json:"stake_dust"`

	// EnrolledAtHeight is the chain height where the enroll
	// transaction committed. Used for analytics and, post-fork,
	// for time-since-enrolled bonus curves that governance may
	// introduce.
	EnrolledAtHeight uint64 `json:"enrolled_at_height"`

	// RevokedAtHeight is 0 while the record is active. Set to
	// the height of the Unenroll transaction once the owner
	// begins the unbond. After being set, the record is still
	// stored (to preserve slash-ability during the window) but
	// Lookup MUST return ErrNodeRevoked — a revoked node cannot
	// mine.
	RevokedAtHeight uint64 `json:"revoked_at_height,omitempty"`

	// UnbondMaturesAtHeight is RevokedAtHeight + UnbondWindow.
	// Zero while the record is active. When
	// currentHeight >= UnbondMaturesAtHeight, the block-time
	// sweep is free to delete the record and credit StakeDust
	// back to Owner's balance.
	UnbondMaturesAtHeight uint64 `json:"unbond_matures_at_height,omitempty"`

	// Memo is preserved from the Enroll payload. Purely
	// cosmetic but stored so the transparency API can show it.
	Memo string `json:"memo,omitempty"`
}

// Active reports whether the record is currently usable for
// mining. A revoked record is inactive even during its unbond
// window.
func (r EnrollmentRecord) Active() bool { return r.RevokedAtHeight == 0 }

// MatureForUnbond reports whether currentHeight has reached the
// unbond maturity. Used by the block-time sweep to decide
// whether to release the stake.
func (r EnrollmentRecord) MatureForUnbond(currentHeight uint64) bool {
	if r.Active() {
		return false
	}
	return currentHeight >= r.UnbondMaturesAtHeight
}

// Age reports how long this record has been enrolled.
// Best-effort — the height-to-seconds translation assumes a
// rough 3-second blocktime. Callers who need exact wall-clock
// should compute it at the call site using the actual block
// timestamp.
func (r EnrollmentRecord) Age(currentHeight uint64) time.Duration {
	if currentHeight <= r.EnrolledAtHeight {
		return 0
	}
	return time.Duration(currentHeight-r.EnrolledAtHeight) * 3 * time.Second
}

// Validation sentinels. All ValidateEnrollPayload /
// ValidateUnenrollPayload errors wrap one of these so
// downstream code can errors.Is against the category without
// parsing messages.
var (
	// ErrPayloadDecode is returned when the JSON is malformed.
	// Distinct from ErrPayloadInvalid so "attacker sent
	// garbage" metrics can be split from "operator filled in a
	// wrong field."
	ErrPayloadDecode = errors.New("enrollment: payload decode failed")

	// ErrPayloadInvalid is returned when the payload parses but
	// one of its fields violates a consensus rule (too-short
	// key, too-long node_id, wrong kind tag, etc.).
	ErrPayloadInvalid = errors.New("enrollment: payload invalid")

	// ErrStakeMismatch is returned when StakeDust does not
	// exactly equal the currently-required stake. Equal-only
	// (not ≥) to keep accounting simple — overpayment would
	// leave the surplus in limbo.
	ErrStakeMismatch = errors.New("enrollment: stake does not match required amount")

	// ErrInsufficientBalance is returned when the sender's
	// balance is below StakeDust. Stateless validation can't
	// check this; it's surfaced here so callers can uniformly
	// wrap chain-state errors.
	ErrInsufficientBalance = errors.New("enrollment: sender balance below stake")

	// ErrNodeIDTaken is returned when the EnrollmentState
	// already has an active record for this NodeID.
	ErrNodeIDTaken = errors.New("enrollment: node_id already enrolled")

	// ErrGPUUUIDTaken is returned when another ACTIVE
	// enrollment is already bound to this GPU UUID.
	ErrGPUUUIDTaken = errors.New("enrollment: gpu_uuid already bound to an active enrollment")

	// ErrNodeNotOwned is returned by ValidateUnenrollPayload
	// when the sender is not the Owner of the target record.
	ErrNodeNotOwned = errors.New("enrollment: sender is not the owner of this node_id")

	// ErrNodeAlreadyUnenrolled is returned when the target
	// record is already in its unbond window.
	ErrNodeAlreadyUnenrolled = errors.New("enrollment: node_id already unenrolled")
)

// EnrollmentState is the read-only view the validator uses to
// answer "has this node_id been enrolled? is it still active?
// is that GPU UUID already bound?" It is the interface
// implemented by the chain's state store (once wired in a
// follow-on commit) AND by the InMemoryState used for tests.
//
// All methods MUST be safe for concurrent use — the attestation
// verifier calls Lookup from many goroutines simultaneously.
type EnrollmentState interface {
	// Lookup returns the record for nodeID or an error. Returns
	// (nil, nil) if the record does not exist — callers treat
	// that distinctly from (nil, err).
	Lookup(nodeID string) (*EnrollmentRecord, error)

	// GPUUUIDBound returns the node_id currently bound to the
	// given gpu_uuid, or "" if no active binding exists. Used
	// by ValidateEnrollPayload to enforce the one-GPU-per-
	// node_id rule.
	GPUUUIDBound(gpuUUID string) (string, error)
}
