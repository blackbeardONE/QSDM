// Package chainparams implements the on-chain governance
// parameter-tuning surface for QSDM v2.
//
// # Why this package exists
//
// Two protocol-economy parameters live as construction-time
// arguments to chain.SlashApplier today:
//
//   - RewardBPS: the slasher's reward share, in basis points
//     of the forfeited stake.
//   - AutoRevokeMinStakeDust: the threshold below which a
//     post-slash record is auto-revoked into the unbond window.
//
// To retune either, every validator has to swap binaries —
// which means coordinated downtime. This package introduces a
// `qsdm/gov/v1` transaction type that lets a configured set of
// governance authorities retune them at runtime, with staged
// activation at a future block height so validators see the
// change coming.
//
// # Scope
//
// This is intentionally a small, surgical surface: a whitelist
// of tunable parameters with bounds, a ParamStore that
// SlashApplier reads from at apply time, and an admission /
// applier pair that mirrors the enrollment / slashing pattern.
// Anything more ambitious (governance-as-multisig-on-chain,
// arbitrary contract upgrades, treasury votes) is explicitly
// out of scope; pkg/governance/{voting,multisig} owns the off-
// chain proposal lifecycle and submits a `qsdm/gov/v1` tx via
// the same path any other client would use, after collecting
// the required signatures.
//
// # Auth model
//
// `chain.GovApplier` is constructed with an `AuthorityList`
// slice of addresses. A `qsdm/gov/v1` tx is accepted only if
// `tx.Sender` is on that list. An empty list disables on-chain
// governance entirely (every gov tx rejects with
// `ErrGovernanceNotConfigured`), which is the genesis posture
// for chains that have not yet bootstrapped a governance
// authority.
//
// The on-chain authority list is itself NOT governance-tunable
// in this revision — modifying it requires a binary upgrade
// or a chain-config reload. That's deliberate: a circular
// "governance can change the list of governors" would let a
// captured authority lock out the rest, which is the nightmare
// scenario for this kind of subsystem. Adding a multisig-gated
// authority-rotation tx is a follow-on once the basic surface
// is battle-tested.
package chainparams

import (
	"errors"
	"time"
)

// ContractID is the mempool.Tx.ContractID value that tags a
// transaction as a governance-parameter operation. Mirrors the
// `qsdm/{enroll,slash}/v1` naming convention; the `/v1` suffix
// reserves room for a future fork to ship `qsdm/gov/v2` with a
// different payload shape (e.g. bounds-relaxation, param-list
// rotation).
const ContractID = "qsdm/gov/v1"

// PayloadKind tags the supported payload shapes that share the
// same ContractID. Today there is only one kind (param-set);
// the field is encoded as the first JSON field so the decoder
// can dispatch before accessing variant-specific fields.
type PayloadKind string

const (
	// PayloadKindParamSet stages a single-parameter update
	// for activation at a specified future block height.
	PayloadKindParamSet PayloadKind = "param-set"
)

// MaxMemoLen bounds the optional memo on a param-set tx. The
// memo is stored verbatim on the chain receipt so an inflated
// memo would inflate state — capping at 256 bytes mirrors the
// enrollment / slashing convention.
const MaxMemoLen = 256

// MaxActivationDelay bounds how far in the future a param
// change may be scheduled. Without an upper bound a malicious
// authority could schedule a change at height 2^64-1 and
// permanently fill the pending slot for that parameter,
// blocking all subsequent updates (the slot is one-per-param
// and supersedable, but a far-future entry still occupies it
// until promoted).
//
// Three days at 3-second blocks ≈ 86 400 blocks. Picked to be
// long enough for off-chain signalling ("we're going to lower
// the reward share next Tuesday") while short enough that an
// abandoned change drops out of operator attention.
const MaxActivationDelay uint64 = 3 * 24 * 60 * 60 / 3

// ParamSetPayload is the consensus-critical wire format of a
// `qsdm/gov/v1` parameter-set transaction. Encoded as canonical
// JSON into mempool.Tx.Payload with ContractID == ContractID.
//
// All fields are validated by ValidateParamSetFields. The
// sender (Tx.Sender address) is the proposing authority — it
// is NOT repeated in the payload because deriving it from the
// signed Sender field makes it impossible for a third party to
// replay someone else's gov tx.
type ParamSetPayload struct {
	// Kind MUST equal PayloadKindParamSet. Belt-and-braces:
	// a client that gets ContractID right and Kind wrong gets
	// a clean rejection rather than an ambiguous decode failure.
	Kind PayloadKind `json:"kind"`

	// Param is the canonical name of the parameter being
	// tuned. MUST be a member of the Param registry (see
	// params.go). Unknown names are rejected at admission
	// time so a malformed proposal cannot silently be
	// accepted into a pending slot.
	Param string `json:"param"`

	// Value is the proposed new value. Currently every
	// tunable parameter is uint64-shaped; if a future
	// parameter needs a different type the registry grows a
	// type tag and Value becomes a polymorphic field. For now
	// the simpler shape is good enough.
	Value uint64 `json:"value"`

	// EffectiveHeight is the chain block height at which the
	// new value MUST be visible to consensus. Must satisfy
	//
	//   currentHeight <= EffectiveHeight <= currentHeight + MaxActivationDelay
	//
	// The applier accepts the tx if the bound holds; the
	// post-seal Promote(height) hook flips pending → active
	// when currentHeight >= EffectiveHeight. Setting
	// EffectiveHeight == currentHeight is the "apply
	// immediately" knob.
	EffectiveHeight uint64 `json:"effective_height"`

	// Memo is optional human-readable context (e.g.
	// "post-mortem #14: lowering reward share to discourage
	// griefing"). Bounded by MaxMemoLen. Not consensus-
	// critical but is included in the canonical hash so
	// tampering invalidates the signature.
	Memo string `json:"memo,omitempty"`
}

// ParamChange is the post-decode, post-validation shape passed
// to the ParamStore. Distinct from the wire payload because
// the store also needs to know which authority submitted the
// change and at what height (for receipt rendering / events).
type ParamChange struct {
	// Param matches Param registry name.
	Param string

	// Value is the new value.
	Value uint64

	// EffectiveHeight is when the change becomes active.
	EffectiveHeight uint64

	// SubmittedAtHeight is the block height at which the tx
	// committed (i.e. the apply height). Used for receipt
	// chronology.
	SubmittedAtHeight uint64

	// Authority is the tx.Sender that proposed the change.
	Authority string

	// Memo is the operator-supplied memo, verbatim.
	Memo string
}

// Sentinel errors. All exported so callers can errors.Is
// against them.
var (
	// ErrPayloadDecode is returned when the JSON is malformed.
	ErrPayloadDecode = errors.New("chainparams: payload decode failed")

	// ErrPayloadInvalid is returned when the payload parses
	// but a field violates a consensus rule (unknown param,
	// out-of-bounds value, wrong kind tag, oversized memo).
	ErrPayloadInvalid = errors.New("chainparams: payload invalid")

	// ErrUnknownParam is returned when ParamSetPayload.Param
	// is not a member of the Param registry.
	ErrUnknownParam = errors.New("chainparams: param not in registry")

	// ErrValueOutOfBounds is returned when the proposed value
	// violates the registered (Min, Max) bounds for the named
	// parameter.
	ErrValueOutOfBounds = errors.New("chainparams: value out of registered bounds")

	// ErrEffectiveHeightInPast is returned when
	// EffectiveHeight < currentHeight at applier time.
	ErrEffectiveHeightInPast = errors.New(
		"chainparams: effective_height precedes current chain height")

	// ErrEffectiveHeightTooFar is returned when
	// EffectiveHeight > currentHeight + MaxActivationDelay.
	ErrEffectiveHeightTooFar = errors.New(
		"chainparams: effective_height exceeds MaxActivationDelay")

	// ErrUnauthorized is returned when tx.Sender is not on
	// the GovApplier's AuthorityList.
	ErrUnauthorized = errors.New(
		"chainparams: sender is not on the governance authority list")

	// ErrGovernanceNotConfigured is returned when a gov tx
	// arrives but the GovApplier has an empty AuthorityList
	// (governance disabled).
	ErrGovernanceNotConfigured = errors.New(
		"chainparams: governance not configured (empty authority list)")
)

// MaxPendingPerParam is the maximum number of pending entries
// the store keeps per parameter. Today the rule is "one pending
// at a time" — any new change supersedes the existing pending
// entry for that parameter — so this is effectively 1 with a
// small head-room reserve in case the spec evolves to support
// FIFO queuing.
const MaxPendingPerParam = 1

// DefaultPromotionGrace is added to a ParamStore.Promote
// invocation's height to account for the reorg horizon — a
// change with EffectiveHeight = H is promoted when the chain
// is N blocks past H, where N is the operator's reorg-safety
// margin. Today this is 0 (promote on equality), exposed as
// a package-level var so a future commit can wire it up to a
// chain-config tunable without changing the call sites.
var DefaultPromotionGrace uint64 = 0

// blockTimeApprox is the rough block-time used by the
// MaxActivationDelay calculation. NOT consensus-critical; held
// here as documentation for the constant's derivation.
const blockTimeApprox = 3 * time.Second
