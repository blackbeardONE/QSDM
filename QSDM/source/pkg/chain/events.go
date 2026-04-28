package chain

// Structured event surface for the chain-side appliers.
//
// SlashApplier and EnrollmentApplier emit one event per
// state-changing outcome (apply, reject, sweep, auto-revoke).
// Subscribers — typically external indexers, audit log
// streamers, or governance dashboards — implement
// ChainEventPublisher and install themselves on the applier
// at boot. The default is NoopEventPublisher, so existing
// callers don't need any change.
//
// Why a publisher interface and not a callback or a channel:
//
//   - Multiple consumers can wrap each other (compose a
//     metrics-publisher with a Kafka-publisher with an
//     audit-log-publisher) without the applier knowing.
//
//   - A channel-based design forces the applier to know the
//     consumer's backpressure model. A typed interface lets
//     each consumer pick its own (drop-on-overflow, blocking,
//     etc.) — see the package-level docs in
//     pkg/monitoring/eventpublisher.go for the canonical
//     metrics-only adapter.
//
//   - Calls are ALWAYS synchronous from the applier's point of
//     view. Slow publishers slow the apply path. Implementers
//     are expected to fan out to a goroutine if they need to
//     do heavy work.
//
// All event structs are immutable from the publisher's view —
// they are pass-by-value so the publisher cannot accidentally
// retain a pointer into the applier's internal state. Future
// fields are additive; new fields go on the end with zero
// values that are safe defaults.

import (
	"sync/atomic"

	"github.com/blackbeardONE/QSDM/pkg/mining/slashing"
)

// ---------- monitoring recorder + reason tags ----------
//
// The metric counters themselves live in pkg/monitoring (see
// slashing_metrics.go and enrollment_metrics.go). pkg/monitoring
// transitively imports pkg/chain (via pkg/networking), so the
// dependency arrow MUST point monitoring -> chain, not the
// reverse. To keep the call sites here readable while honouring
// that direction, we declare:
//
//   - the Metrics function-table interface, with a no-op
//     implementation as the package-level default,
//   - the canonical reason-tag string constants,
//
// and let pkg/monitoring register a real adapter via
// SetChainMetricsRecorder at init() time. Anything that imports
// pkg/chain *and* pkg/monitoring (i.e. any production binary) gets
// real counters automatically; pure unit tests of pkg/chain can
// run with the no-op recorder.

// MetricsRecorder is the narrow surface SlashApplier and
// EnrollmentApplier call into. Implementations must be safe
// for concurrent use; the production adapter in pkg/monitoring
// uses sync/atomic.
type MetricsRecorder interface {
	RecordSlashApplied(kind string, drainedDust uint64)
	RecordSlashReward(rewardedDust, burnedDust uint64)
	RecordSlashRejected(reason string)
	RecordSlashAutoRevoke(reason string)
	RecordEnrollmentApplied()
	RecordUnenrollmentApplied()
	RecordEnrollmentRejected(reason string)
	RecordUnenrollmentRejected(reason string)
	RecordEnrollmentUnbondSwept(count uint64)

	// RecordGovParamStaged increments the
	// `qsdm_gov_param_staged_total{param}` counter. Fires
	// once per accepted `qsdm/gov/v1` param-set tx.
	RecordGovParamStaged(param string)

	// RecordGovParamActivated increments the
	// `qsdm_gov_param_activated_total{param}` counter and
	// updates the `qsdm_gov_param_value{param}` gauge to the
	// new value. Fires once per Promote-driven activation.
	RecordGovParamActivated(param string, value uint64)

	// RecordGovParamRejected increments the
	// `qsdm_gov_param_rejected_total{reason}` counter.
	RecordGovParamRejected(reason string)
}

// Slash reject reason tags. Stable, narrow, mapped 1:1 to the
// rejection branches in slash_apply.go. Mirror the
// SlashRejectReason* string values exposed by pkg/monitoring;
// the two MUST be kept in sync.
const (
	SlashRejectReasonVerifier        = "verifier_failed"
	SlashRejectReasonEvidenceReplay  = "evidence_replayed"
	SlashRejectReasonNodeNotEnrolled = "node_not_enrolled"
	SlashRejectReasonDecode          = "decode_failed"
	SlashRejectReasonFee             = "fee_invalid"
	SlashRejectReasonWrongContract   = "wrong_contract"
	SlashRejectReasonStateLookup     = "state_lookup_failed"
	SlashRejectReasonStakeMutation   = "stake_mutation_failed"
	SlashRejectReasonOther           = "other"

	SlashAutoRevokeReasonFullDrain   = "fully_drained"
	SlashAutoRevokeReasonUnderBonded = "under_bonded"
)

// Enrollment reject reason tags. Same sync requirement as the
// slash tags above.
const (
	EnrollRejectReasonStakeMismatch = "stake_mismatch"
	EnrollRejectReasonGPUBound      = "gpu_bound"
	EnrollRejectReasonNodeIDBound   = "node_id_bound"
	EnrollRejectReasonInsufficient  = "insufficient_balance"
	EnrollRejectReasonDecode        = "decode_failed"
	EnrollRejectReasonFee           = "fee_invalid"
	EnrollRejectReasonWrongContract = "wrong_contract"
	EnrollRejectReasonAdmission     = "admission_failed"
	EnrollRejectReasonOther         = "other"

	UnenrollRejectReasonNotEnrolled    = "not_enrolled"
	UnenrollRejectReasonAlreadyRevoked = "already_revoked"
	UnenrollRejectReasonNotOwner       = "not_owner"
	UnenrollRejectReasonDecode         = "decode_failed"
	UnenrollRejectReasonFee            = "fee_invalid"
	UnenrollRejectReasonOther          = "other"
)

// noopRecorder is the default. Every method is a no-op so
// pkg/chain unit tests don't have to wire anything.
type noopRecorder struct{}

func (noopRecorder) RecordSlashApplied(string, uint64)       {}
func (noopRecorder) RecordSlashReward(uint64, uint64)        {}
func (noopRecorder) RecordSlashRejected(string)              {}
func (noopRecorder) RecordSlashAutoRevoke(string)            {}
func (noopRecorder) RecordEnrollmentApplied()                {}
func (noopRecorder) RecordUnenrollmentApplied()              {}
func (noopRecorder) RecordEnrollmentRejected(string)         {}
func (noopRecorder) RecordUnenrollmentRejected(string)       {}
func (noopRecorder) RecordEnrollmentUnbondSwept(uint64)      {}
func (noopRecorder) RecordGovParamStaged(string)             {}
func (noopRecorder) RecordGovParamActivated(string, uint64)  {}
func (noopRecorder) RecordGovParamRejected(string)           {}

// recorderHolder wraps a MetricsRecorder so atomic.Value's
// "all stored values must share an identical concrete type"
// constraint is satisfied — we always store a recorderHolder,
// regardless of the wrapped impl. This is the standard
// idiom for atomic.Value of an interface.
type recorderHolder struct {
	r MetricsRecorder
}

var chainMetricsRecorder atomic.Value // holds recorderHolder

func init() {
	chainMetricsRecorder.Store(recorderHolder{r: noopRecorder{}})
}

// SetChainMetricsRecorder installs the recorder. pkg/monitoring
// calls this from its init() with a real Prometheus-backed
// adapter; tests can call it with a fake. Pass nil to detach.
func SetChainMetricsRecorder(r MetricsRecorder) {
	if r == nil {
		chainMetricsRecorder.Store(recorderHolder{r: noopRecorder{}})
		return
	}
	chainMetricsRecorder.Store(recorderHolder{r: r})
}

// metrics returns the active recorder, never nil.
func metrics() MetricsRecorder {
	v := chainMetricsRecorder.Load()
	if v == nil {
		return noopRecorder{}
	}
	h, ok := v.(recorderHolder)
	if !ok || h.r == nil {
		return noopRecorder{}
	}
	return h.r
}

// MiningSlashEvent is emitted exactly once per successful
// v2-mining slash and once per pre-mutation rejection.
// Auto-revoke information is included on the success path so a
// single subscriber sees the complete outcome of one slash tx.
//
// Distinct from the legacy validator SlashEvent in
// validator.go: that struct describes pre-fork validator-set
// slashing; this one describes the v2 mining-protocol slasher
// in slash_apply.go. Both can coexist on a single chain because
// they map to disjoint state machines.
type MiningSlashEvent struct {
	// TxID is the mempool tx id of the slash transaction —
	// the same string the submitter posted as
	// SlashSubmitRequest.ID. Always populated; carried so the
	// /api/v1/mining/slash/{tx_id} receipt store can key
	// receipts by client-known id without a separate lookup.
	// Empty only for synthetic events emitted before the tx
	// envelope was inspected (currently none — the wrong-
	// contract reject path also has the id available).
	TxID string

	// Outcome is "applied" for a successful slash, or
	// "rejected" for any pre-mutation rejection. Subscribers
	// MUST switch on Outcome before reading the per-outcome
	// fields below.
	Outcome string

	// Height is the chain height at which the applier
	// processed the slash. Always populated.
	Height uint64

	// Slasher is the address that submitted the slash tx.
	// Always populated.
	Slasher string

	// NodeID is the offending miner's node_id. Populated for
	// "applied" and for any "rejected" path that got far
	// enough to decode the payload (everything except
	// decode-failed and wrong-contract).
	NodeID string

	// EvidenceKind names the slash flavour. Populated only
	// when payload decode succeeded.
	EvidenceKind slashing.EvidenceKind

	// SlashedDust is the actually-forfeited stake on
	// "applied". Zero on "rejected".
	SlashedDust uint64

	// RewardedDust is the share paid to the slasher on
	// "applied". Zero on "rejected".
	RewardedDust uint64

	// BurnedDust = SlashedDust - RewardedDust. Convenience
	// for subscribers that don't want to do the arithmetic.
	BurnedDust uint64

	// AutoRevoked is true when the post-slash auto-revoke
	// step transitioned the offender's record into the
	// unbond window. Always false on "rejected".
	AutoRevoked bool

	// AutoRevokeRemainingDust is the stake still locked in
	// the auto-revoked record (released by SweepMaturedUnbonds
	// after the unbond window). Zero when AutoRevoked is false.
	AutoRevokeRemainingDust uint64

	// RejectReason carries the monitoring reason tag on
	// "rejected" (matches one of the SlashRejectReason* enums
	// in pkg/monitoring/slashing_metrics.go). Empty on
	// "applied".
	RejectReason string

	// Err carries the underlying error on "rejected". The
	// publisher MUST NOT retain a reference past the call;
	// errors are not guaranteed to be string-stable.
	Err error
}

const (
	SlashOutcomeApplied  = "applied"
	SlashOutcomeRejected = "rejected"
)

// EnrollmentEvent is emitted by EnrollmentApplier on every
// state-changing outcome (apply, reject, unenroll, unenroll-
// reject, sweep). Sweep events are emitted once per matured
// release so subscribers see one event per (node_id, owner,
// stake) tuple released back to its owner.
type EnrollmentEvent struct {
	// Kind names the event flavour. See the EnrollmentEventKind
	// constants below.
	Kind EnrollmentEventKind

	// Height is the chain height at which the applier
	// processed the event. Always populated.
	Height uint64

	// Sender is the tx submitter for "enroll-applied",
	// "enroll-rejected", "unenroll-applied",
	// "unenroll-rejected". Empty on "sweep" (where the
	// release is initiated by the chain, not a tx).
	Sender string

	// NodeID is the enrollment record's node_id. Populated on
	// every Kind that knows it (i.e. every Kind except
	// "enroll-rejected" with a decode failure).
	NodeID string

	// Owner is the address that owns the stake. Populated on
	// "enroll-applied", "unenroll-applied", "sweep".
	Owner string

	// StakeDust is the bonded stake amount: the new bond on
	// "enroll-applied", the released amount on "sweep". Zero
	// elsewhere.
	StakeDust uint64

	// RejectReason matches one of the EnrollRejectReason* /
	// UnenrollRejectReason* enums in
	// pkg/monitoring/enrollment_metrics.go. Populated only on
	// "*-rejected" Kinds.
	RejectReason string

	// Err carries the underlying error on "*-rejected" Kinds.
	// Same retention rules as SlashEvent.Err.
	Err error
}

// EnrollmentEventKind enumerates the event flavours.
type EnrollmentEventKind string

const (
	EnrollmentEventEnrollApplied    EnrollmentEventKind = "enroll-applied"
	EnrollmentEventEnrollRejected   EnrollmentEventKind = "enroll-rejected"
	EnrollmentEventUnenrollApplied  EnrollmentEventKind = "unenroll-applied"
	EnrollmentEventUnenrollRejected EnrollmentEventKind = "unenroll-rejected"
	EnrollmentEventSweep            EnrollmentEventKind = "sweep"
)

// ChainEventPublisher is the consumer-facing surface. The
// applier calls these methods synchronously from inside the
// apply path; implementations that need durability or fan-out
// should hand off to an internal goroutine and return
// immediately.
type ChainEventPublisher interface {
	PublishMiningSlash(MiningSlashEvent)
	PublishEnrollment(EnrollmentEvent)
}

// NoopEventPublisher is the default publisher: every method is
// a no-op. Installed on a freshly-constructed applier so
// callers can opt into events by replacing the field.
type NoopEventPublisher struct{}

// PublishMiningSlash implements ChainEventPublisher.
func (NoopEventPublisher) PublishMiningSlash(MiningSlashEvent) {}

// PublishEnrollment implements ChainEventPublisher.
func (NoopEventPublisher) PublishEnrollment(EnrollmentEvent) {}

// GovParamEventKind enumerates the v2-governance parameter
// event flavours emitted by GovApplier (see gov_apply.go) and
// by the post-seal Promote hook on the ParamStore.
//
// Distinct from MiningSlashEvent / EnrollmentEvent because
// governance has its own subscriber audience (CLI watchers,
// audit indexers, monitoring gauges) and a separate publisher
// surface keeps existing consumers from having to implement a
// no-op handler.
type GovParamEventKind string

const (
	// GovParamEventStaged fires once per accepted
	// `qsdm/gov/v1` param-set tx. The change is now in the
	// pending slot for its parameter; activation happens at
	// `EffectiveHeight`.
	GovParamEventStaged GovParamEventKind = "param-staged"

	// GovParamEventSuperseded fires when a Stage call
	// replaced an existing pending change for the same
	// parameter. The PriorValue / PriorEffectiveHeight fields
	// describe what was overwritten.
	GovParamEventSuperseded GovParamEventKind = "param-superseded"

	// GovParamEventActivated fires when Promote(currentHeight)
	// transitions a pending change into active state — the
	// chain now reads the new value.
	GovParamEventActivated GovParamEventKind = "param-activated"

	// GovParamEventRejected fires on every pre-mutation
	// rejection (decode, unauthorized, bounds, height window,
	// fee). Mirrors MiningSlashEvent's rejected path so audit
	// consumers see attempted-but-blocked governance activity.
	GovParamEventRejected GovParamEventKind = "param-rejected"
)

// GovParamEvent is the structured event emitted by the
// governance subsystem. Pass-by-value; subscribers MUST NOT
// retain a pointer into the event.
type GovParamEvent struct {
	// Kind names the event flavour.
	Kind GovParamEventKind

	// TxID is the mempool tx id of the originating gov tx.
	// Empty for `param-activated` (the activation is driven
	// by the post-seal hook, not by a tx).
	TxID string

	// Height is the chain height at which the event fired.
	// For `param-activated` this is the height that crossed
	// the change's EffectiveHeight, NOT the EffectiveHeight
	// itself (those can differ if a height advances by more
	// than one in a single sealed block, e.g. during catch-up).
	Height uint64

	// Authority is the tx.Sender on staged / superseded /
	// rejected events. Empty for `param-activated`.
	Authority string

	// Param is the parameter name. Always populated except
	// on rejected events that failed before payload decode.
	Param string

	// Value is the new value the change carries. Always
	// populated except on rejected-decode events.
	Value uint64

	// EffectiveHeight is the change's activation height.
	// Always populated when known (the rejected paths that
	// don't reach decode leave it 0).
	EffectiveHeight uint64

	// PriorValue is the value that was just superseded /
	// activated-over. Zero when no prior value existed.
	PriorValue uint64

	// PriorEffectiveHeight is the prior change's
	// EffectiveHeight on `param-superseded`. Zero on other
	// kinds.
	PriorEffectiveHeight uint64

	// Memo is the operator-supplied memo, verbatim. Empty
	// when the event has no associated tx (activated) or the
	// payload didn't carry one.
	Memo string

	// RejectReason names the rejection branch on
	// `param-rejected`; matches the GovRejectReason* tags
	// below. Empty on other kinds.
	RejectReason string

	// Err carries the underlying error on `param-rejected`.
	// Subscribers MUST NOT retain a reference past the call.
	Err error
}

// Gov reject reason tags. Stable, narrow, mapped 1:1 to the
// rejection branches in gov_apply.go.
const (
	GovRejectReasonDecode         = "decode_failed"
	GovRejectReasonWrongContract  = "wrong_contract"
	GovRejectReasonFee            = "fee_invalid"
	GovRejectReasonUnauthorized   = "unauthorized"
	GovRejectReasonNotConfigured  = "not_configured"
	GovRejectReasonHeightInPast   = "effective_height_in_past"
	GovRejectReasonHeightTooFar   = "effective_height_too_far"
	GovRejectReasonStageRejected  = "stage_rejected"
	GovRejectReasonNonceFee       = "nonce_or_fee_failed"
	GovRejectReasonOther          = "other"
)

// GovEventPublisher is the consumer-facing surface for
// governance events. Kept distinct from ChainEventPublisher so
// existing slash / enrollment subscribers do not have to grow
// a no-op PublishGovParam method.
type GovEventPublisher interface {
	PublishGovParam(GovParamEvent)
}

// NoopGovEventPublisher is the default. Implementations that
// want the events should install themselves on GovApplier.
type NoopGovEventPublisher struct{}

// PublishGovParam implements GovEventPublisher.
func (NoopGovEventPublisher) PublishGovParam(GovParamEvent) {}

// CompositeGovPublisher fans out gov events to every wrapped
// publisher in registration order. Mirrors CompositePublisher
// for gov-only subscribers.
type CompositeGovPublisher struct {
	publishers []GovEventPublisher
}

// NewCompositeGovPublisher returns a fan-out publisher.
func NewCompositeGovPublisher(subs ...GovEventPublisher) *CompositeGovPublisher {
	out := &CompositeGovPublisher{}
	for _, s := range subs {
		if s == nil {
			continue
		}
		out.publishers = append(out.publishers, s)
	}
	return out
}

// PublishGovParam fans out to every wrapped publisher.
func (c *CompositeGovPublisher) PublishGovParam(ev GovParamEvent) {
	if c == nil {
		return
	}
	for _, p := range c.publishers {
		p.PublishGovParam(ev)
	}
}

// CompositePublisher dispatches each event to every wrapped
// publisher in registration order. Failures inside one
// subscriber don't affect the others (subscribers are expected
// to handle their own errors; a panicking subscriber will
// propagate up — by design, so misbehaviour is loud, not
// silent).
type CompositePublisher struct {
	publishers []ChainEventPublisher
}

// NewCompositePublisher returns a publisher that fans out to
// each of the supplied subscribers in order. Nil entries are
// silently ignored.
func NewCompositePublisher(subs ...ChainEventPublisher) *CompositePublisher {
	out := &CompositePublisher{}
	for _, s := range subs {
		if s == nil {
			continue
		}
		out.publishers = append(out.publishers, s)
	}
	return out
}

// PublishMiningSlash fans out to every wrapped publisher.
func (c *CompositePublisher) PublishMiningSlash(ev MiningSlashEvent) {
	if c == nil {
		return
	}
	for _, p := range c.publishers {
		p.PublishMiningSlash(ev)
	}
}

// PublishEnrollment fans out to every wrapped publisher.
func (c *CompositePublisher) PublishEnrollment(ev EnrollmentEvent) {
	if c == nil {
		return
	}
	for _, p := range c.publishers {
		p.PublishEnrollment(ev)
	}
}
