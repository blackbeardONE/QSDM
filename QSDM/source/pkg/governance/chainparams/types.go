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
// same ContractID. The field is encoded as the first JSON field
// so the decoder can dispatch before accessing variant-specific
// fields.
type PayloadKind string

const (
	// PayloadKindParamSet stages a single-parameter update
	// for activation at a specified future block height.
	PayloadKindParamSet PayloadKind = "param-set"

	// PayloadKindAuthoritySet stages an addition or removal of
	// a governance authority. Unlike param-set, which
	// activates on a single signed proposal, authority-set
	// requires M-of-N approval: each authority submits its
	// own vote tx for the same (op, address, effective_height)
	// tuple, and the change is staged for activation only
	// after `threshold` votes are tallied.
	//
	// See pkg/governance/chainparams/authority.go for the
	// vote-store and threshold semantics, and
	// pkg/chain/gov_apply.go for the applier dispatch +
	// promotion path.
	PayloadKindAuthoritySet PayloadKind = "authority-set"
)

// AuthorityOp names the kind of mutation an authority-set
// payload requests against the live AuthorityList.
type AuthorityOp string

const (
	// AuthorityOpAdd inserts the address into the
	// AuthorityList at activation time. A no-op if the
	// address is already present at promotion (the applier
	// emits an `authority-rejected` event with reason
	// `already_present` and skips the mutation).
	AuthorityOpAdd AuthorityOp = "add"

	// AuthorityOpRemove drops the address from the
	// AuthorityList at activation time. Refused at promotion
	// if it would leave the list empty (governance cannot
	// rotate itself into the disabled posture; binaries must
	// be redeployed for that).
	AuthorityOpRemove AuthorityOp = "remove"
)

// MaxAuthorityAddressLen bounds the on-wire address byte
// length carried by an AuthoritySetPayload. Mirrors the
// enrollment payload's address-length cap so a hand-crafted
// JSON cannot inject pathological state.
const MaxAuthorityAddressLen = 128

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

// AuthoritySetPayload is the consensus-critical wire format of
// a `qsdm/gov/v1` authority-rotation transaction (the one whose
// Kind == PayloadKindAuthoritySet). Each instance is one
// authority's VOTE on a single proposal; the chain tallies
// votes and stages the rotation when the M-of-N threshold is
// crossed.
//
// The proposal identity is the tuple
// (Op, Address, EffectiveHeight). Two votes on the same tuple
// from different authorities accumulate into a single staged
// change; two votes on the same tuple from the SAME authority
// reject as ErrDuplicateVote.
//
// Memo is purely informational and is recorded verbatim on the
// `authority-voted` / `authority-staged` events; it is included
// in the canonical hash so tampering invalidates the signature.
//
// Field naming mirrors ParamSetPayload to make canonical-JSON
// inspection consistent across the two payload shapes.
type AuthoritySetPayload struct {
	// Kind MUST equal PayloadKindAuthoritySet. Belt-and-braces
	// dispatch tag — a client that gets ContractID right and
	// Kind wrong gets a clean rejection rather than an
	// ambiguous decode failure.
	Kind PayloadKind `json:"kind"`

	// Op names the requested AuthorityList mutation.
	// Validators reject any value not in {add, remove}.
	Op AuthorityOp `json:"op"`

	// Address is the target of the rotation. For Op=add it
	// is the new authority being onboarded; for Op=remove it
	// is the existing authority being retired. Validated for
	// non-emptiness and length cap (MaxAuthorityAddressLen);
	// existence checks happen at applier time against the
	// live AuthorityList.
	Address string `json:"address"`

	// EffectiveHeight is the chain block height at which the
	// rotation MUST be visible to consensus. Same window as
	// ParamSetPayload — must satisfy
	//
	//   currentHeight <= EffectiveHeight <= currentHeight + MaxActivationDelay
	//
	// Each vote MUST carry the same EffectiveHeight to count
	// toward the same proposal tuple — different effective
	// heights name different proposals.
	EffectiveHeight uint64 `json:"effective_height"`

	// Memo is optional human-readable context (e.g.
	// "carol stepping down, dave taking over per board
	// resolution 2026-04"). Bounded by MaxMemoLen.
	Memo string `json:"memo,omitempty"`
}

// AuthorityVote captures one authority's casting of a vote on
// a proposal tuple. Stored in AuthorityProposal.Voters and
// emitted on `authority-voted` events.
type AuthorityVote struct {
	// Voter is the authority address that cast the vote
	// (i.e. tx.Sender). Must be on the AuthorityList at the
	// time the vote was applied.
	Voter string

	// SubmittedAtHeight is the chain height at which the vote
	// tx was applied. Used for receipt chronology and for
	// expiring stale votes (today votes don't expire pre-
	// activation; the field is reserved for a future
	// expiry-window extension).
	SubmittedAtHeight uint64

	// Memo is the operator-supplied memo, verbatim.
	Memo string
}

// AuthorityProposal is the post-tally state of a single
// (Op, Address, EffectiveHeight) tuple. Held in the
// AuthorityVoteStore and persisted alongside the param store.
//
// Lifecycle:
//
//  1. First vote: proposal created with one entry in Voters.
//     Crossed=false until threshold is reached.
//  2. Subsequent votes (different voters): appended to Voters.
//     If the new tally meets threshold, Crossed flips to true
//     and CrossedAtHeight is recorded.
//  3. Promotion: when the chain reaches EffectiveHeight, a
//     Crossed proposal is applied to the AuthorityList and
//     deleted from the store. Non-crossed proposals never
//     activate (votes accumulated short of threshold are
//     discarded silently when the chain passes EffectiveHeight).
type AuthorityProposal struct {
	// Op is the rotation kind.
	Op AuthorityOp

	// Address is the rotation target.
	Address string

	// EffectiveHeight is the activation height — same value
	// every voter must have submitted.
	EffectiveHeight uint64

	// Voters is the set of authorities that have cast a vote
	// on this tuple. Ordered by SubmittedAtHeight ascending,
	// then voter address ascending, so two nodes' snapshots
	// of the same proposal are byte-stable.
	Voters []AuthorityVote

	// Crossed is true once len(Voters) reached the threshold
	// at the time of the latest vote. Once true, never flips
	// back to false (a captured authority cannot un-vote).
	Crossed bool

	// CrossedAtHeight is the height at which Crossed flipped.
	// Zero before crossing; non-zero after.
	CrossedAtHeight uint64
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

	// ErrAuthorityAlreadyPresent is returned by the applier
	// when an `authority-set / add` proposal targets an
	// address already on the AuthorityList. Catching this at
	// admit-or-apply time prevents wasted votes on no-ops.
	ErrAuthorityAlreadyPresent = errors.New(
		"chainparams: authority already on the list")

	// ErrAuthorityNotPresent is returned when an
	// `authority-set / remove` proposal targets an address
	// not on the current AuthorityList. Same rationale as
	// ErrAuthorityAlreadyPresent.
	ErrAuthorityNotPresent = errors.New(
		"chainparams: authority not on the list")

	// ErrAuthorityListWouldEmpty is returned at promotion
	// time when applying a remove would leave the
	// AuthorityList empty. Governance cannot rotate itself
	// into the disabled posture from on-chain — the operator
	// must redeploy binaries.
	ErrAuthorityListWouldEmpty = errors.New(
		"chainparams: authority list cannot drop to zero via on-chain rotation")

	// ErrDuplicateVote is returned by the applier when an
	// authority casts a second vote on the same proposal
	// tuple. The mempool has its own dedup but cross-block
	// resubmission would still slip through without this
	// check.
	ErrDuplicateVote = errors.New(
		"chainparams: authority has already voted on this proposal")
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
