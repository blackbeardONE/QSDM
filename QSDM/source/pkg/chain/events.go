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
