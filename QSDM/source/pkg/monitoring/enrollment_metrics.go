package monitoring

// Enrollment-pipeline counters and gauges. Counters are
// monotonically increasing across the process lifetime and
// drive `*_total` Prometheus metrics; the active-set numbers
// (current Active() count, current bonded dust, current
// pending-unbond count) are computed via callbacks supplied
// by the chain (so the monitoring package never holds a
// pointer to enrollment state and tests can swap fakes in).
//
// The corresponding Prometheus exposition lives in
// prometheus_scrape.go.

import "sync/atomic"

// ---------- enroll / unenroll counters ----------

var (
	enrollmentApplied   atomic.Uint64
	unenrollmentApplied atomic.Uint64

	enrollmentRejectStakeMismatch  atomic.Uint64
	enrollmentRejectGPUBound       atomic.Uint64
	enrollmentRejectNodeIDBound    atomic.Uint64
	enrollmentRejectInsufficient   atomic.Uint64
	enrollmentRejectDecode         atomic.Uint64
	enrollmentRejectFee            atomic.Uint64
	enrollmentRejectWrongContract  atomic.Uint64
	enrollmentRejectAdmission      atomic.Uint64
	enrollmentRejectOther          atomic.Uint64

	unenrollmentRejectNotEnrolled  atomic.Uint64
	unenrollmentRejectAlreadyRevoked atomic.Uint64
	unenrollmentRejectNotOwner     atomic.Uint64
	unenrollmentRejectDecode       atomic.Uint64
	unenrollmentRejectFee          atomic.Uint64
	unenrollmentRejectOther        atomic.Uint64

	enrollmentAutoRevokeUnbondSwept atomic.Uint64
)

// Enrollment reject reason tags. Stable, narrow, and mapped
// 1:1 to specific rejection paths in pkg/chain/enrollment_apply.go
// + pkg/mining/enrollment/validate.go.
const (
	EnrollRejectReasonStakeMismatch  = "stake_mismatch"
	EnrollRejectReasonGPUBound       = "gpu_bound"
	EnrollRejectReasonNodeIDBound    = "node_id_bound"
	EnrollRejectReasonInsufficient   = "insufficient_balance"
	EnrollRejectReasonDecode         = "decode_failed"
	EnrollRejectReasonFee            = "fee_invalid"
	EnrollRejectReasonWrongContract  = "wrong_contract"
	EnrollRejectReasonAdmission      = "admission_failed"
	EnrollRejectReasonOther          = "other"

	UnenrollRejectReasonNotEnrolled    = "not_enrolled"
	UnenrollRejectReasonAlreadyRevoked = "already_revoked"
	UnenrollRejectReasonNotOwner       = "not_owner"
	UnenrollRejectReasonDecode         = "decode_failed"
	UnenrollRejectReasonFee            = "fee_invalid"
	UnenrollRejectReasonOther          = "other"
)

// RecordEnrollmentApplied counts a successful
// qsdm/enroll/v1 application.
func RecordEnrollmentApplied() { enrollmentApplied.Add(1) }

// EnrollmentAppliedTotal returns successful enrollments since
// process start.
func EnrollmentAppliedTotal() uint64 { return enrollmentApplied.Load() }

// RecordUnenrollmentApplied counts a successful
// qsdm/unenroll/v1 application (operator-initiated revoke).
func RecordUnenrollmentApplied() { unenrollmentApplied.Add(1) }

// UnenrollmentAppliedTotal returns successful unenrollments
// since process start.
func UnenrollmentAppliedTotal() uint64 { return unenrollmentApplied.Load() }

// RecordEnrollmentRejected increments the reject counter for
// the supplied reason. Unknown reasons fall into "other".
func RecordEnrollmentRejected(reason string) {
	switch reason {
	case EnrollRejectReasonStakeMismatch:
		enrollmentRejectStakeMismatch.Add(1)
	case EnrollRejectReasonGPUBound:
		enrollmentRejectGPUBound.Add(1)
	case EnrollRejectReasonNodeIDBound:
		enrollmentRejectNodeIDBound.Add(1)
	case EnrollRejectReasonInsufficient:
		enrollmentRejectInsufficient.Add(1)
	case EnrollRejectReasonDecode:
		enrollmentRejectDecode.Add(1)
	case EnrollRejectReasonFee:
		enrollmentRejectFee.Add(1)
	case EnrollRejectReasonWrongContract:
		enrollmentRejectWrongContract.Add(1)
	case EnrollRejectReasonAdmission:
		enrollmentRejectAdmission.Add(1)
	default:
		enrollmentRejectOther.Add(1)
	}
}

// RecordUnenrollmentRejected increments the unenroll-reject
// counter for the supplied reason.
func RecordUnenrollmentRejected(reason string) {
	switch reason {
	case UnenrollRejectReasonNotEnrolled:
		unenrollmentRejectNotEnrolled.Add(1)
	case UnenrollRejectReasonAlreadyRevoked:
		unenrollmentRejectAlreadyRevoked.Add(1)
	case UnenrollRejectReasonNotOwner:
		unenrollmentRejectNotOwner.Add(1)
	case UnenrollRejectReasonDecode:
		unenrollmentRejectDecode.Add(1)
	case UnenrollRejectReasonFee:
		unenrollmentRejectFee.Add(1)
	default:
		unenrollmentRejectOther.Add(1)
	}
}

// RecordEnrollmentUnbondSwept counts revoked records released
// to their owners by SweepMaturedUnbonds. Both operator-
// initiated and slash-induced auto-revokes funnel through the
// same sweep, so a single counter is enough — if you need to
// distinguish "natural unbond" from "post-slash unbond" fall
// back to the structured event channel.
func RecordEnrollmentUnbondSwept(count uint64) {
	enrollmentAutoRevokeUnbondSwept.Add(count)
}

// EnrollmentUnbondSweptTotal returns the cumulative count of
// records released to owners since process start.
func EnrollmentUnbondSweptTotal() uint64 {
	return enrollmentAutoRevokeUnbondSwept.Load()
}

// EnrollmentRejectedLabeled returns (reason, count) pairs in
// stable order for Prometheus exposition.
func EnrollmentRejectedLabeled() []struct {
	Reason string
	Val    uint64
} {
	return []struct {
		Reason string
		Val    uint64
	}{
		{EnrollRejectReasonStakeMismatch, enrollmentRejectStakeMismatch.Load()},
		{EnrollRejectReasonGPUBound, enrollmentRejectGPUBound.Load()},
		{EnrollRejectReasonNodeIDBound, enrollmentRejectNodeIDBound.Load()},
		{EnrollRejectReasonInsufficient, enrollmentRejectInsufficient.Load()},
		{EnrollRejectReasonDecode, enrollmentRejectDecode.Load()},
		{EnrollRejectReasonFee, enrollmentRejectFee.Load()},
		{EnrollRejectReasonWrongContract, enrollmentRejectWrongContract.Load()},
		{EnrollRejectReasonAdmission, enrollmentRejectAdmission.Load()},
		{EnrollRejectReasonOther, enrollmentRejectOther.Load()},
	}
}

// UnenrollmentRejectedLabeled returns (reason, count) pairs in
// stable order for Prometheus exposition.
func UnenrollmentRejectedLabeled() []struct {
	Reason string
	Val    uint64
} {
	return []struct {
		Reason string
		Val    uint64
	}{
		{UnenrollRejectReasonNotEnrolled, unenrollmentRejectNotEnrolled.Load()},
		{UnenrollRejectReasonAlreadyRevoked, unenrollmentRejectAlreadyRevoked.Load()},
		{UnenrollRejectReasonNotOwner, unenrollmentRejectNotOwner.Load()},
		{UnenrollRejectReasonDecode, unenrollmentRejectDecode.Load()},
		{UnenrollRejectReasonFee, unenrollmentRejectFee.Load()},
		{UnenrollRejectReasonOther, unenrollmentRejectOther.Load()},
	}
}

// ---------- enrollment-state gauges (callback-driven) ----------

// EnrollmentStateProvider supplies the current point-in-time
// gauges for the enrollment state. Wired in by the chain at
// boot via SetEnrollmentStateProvider; left nil otherwise so
// the gauges report 0 (rather than crashing the scrape).
type EnrollmentStateProvider interface {
	// ActiveCount returns the number of EnrollmentRecord
	// values where Active() == true.
	ActiveCount() uint64

	// BondedDust returns the sum of StakeDust across all
	// Active() records (i.e. miners eligible to mine).
	BondedDust() uint64

	// PendingUnbondCount returns the number of records that
	// have been revoked (operator unenroll OR post-slash
	// auto-revoke) but whose unbond window has not yet
	// matured.
	PendingUnbondCount() uint64

	// PendingUnbondDust returns the sum of StakeDust still
	// locked in pending-unbond records. These funds are
	// owed back to owners but not yet released.
	PendingUnbondDust() uint64
}

// enrollmentStateProviderHolder wraps the interface so
// atomic.Value's identical-concrete-type constraint is
// satisfied across heterogeneous impls.
type enrollmentStateProviderHolder struct {
	p EnrollmentStateProvider
}

var enrollmentStateProvider atomic.Value // holds enrollmentStateProviderHolder

// SetEnrollmentStateProvider installs the live state provider
// for gauge metrics. Pass nil to detach (gauges will read as
// zero). Safe to call concurrently and from a hot path.
func SetEnrollmentStateProvider(p EnrollmentStateProvider) {
	if p == nil {
		enrollmentStateProvider.Store(enrollmentStateProviderHolder{p: nopEnrollmentStateProvider{}})
		return
	}
	enrollmentStateProvider.Store(enrollmentStateProviderHolder{p: p})
}

// EnrollmentStateActiveCount returns the current Active()
// record count or 0 if no provider has been installed.
func EnrollmentStateActiveCount() uint64 {
	return loadEnrollmentStateProvider().ActiveCount()
}

// EnrollmentStateBondedDust returns the current sum of bonded
// stake dust or 0 if no provider has been installed.
func EnrollmentStateBondedDust() uint64 {
	return loadEnrollmentStateProvider().BondedDust()
}

// EnrollmentStatePendingUnbondCount returns the current count
// of records in the unbond window or 0 if no provider has
// been installed.
func EnrollmentStatePendingUnbondCount() uint64 {
	return loadEnrollmentStateProvider().PendingUnbondCount()
}

// EnrollmentStatePendingUnbondDust returns the current sum of
// stake dust pending unbond release or 0 if no provider has
// been installed.
func EnrollmentStatePendingUnbondDust() uint64 {
	return loadEnrollmentStateProvider().PendingUnbondDust()
}

func loadEnrollmentStateProvider() EnrollmentStateProvider {
	v := enrollmentStateProvider.Load()
	if v == nil {
		return nopEnrollmentStateProvider{}
	}
	h, ok := v.(enrollmentStateProviderHolder)
	if !ok || h.p == nil {
		return nopEnrollmentStateProvider{}
	}
	return h.p
}

type nopEnrollmentStateProvider struct{}

func (nopEnrollmentStateProvider) ActiveCount() uint64         { return 0 }
func (nopEnrollmentStateProvider) BondedDust() uint64          { return 0 }
func (nopEnrollmentStateProvider) PendingUnbondCount() uint64  { return 0 }
func (nopEnrollmentStateProvider) PendingUnbondDust() uint64   { return 0 }

// ---------- test reset ----------

// ResetEnrollmentMetricsForTest clears every counter in this
// file and detaches any state provider. Tests-only.
func ResetEnrollmentMetricsForTest() {
	enrollmentApplied.Store(0)
	unenrollmentApplied.Store(0)
	enrollmentRejectStakeMismatch.Store(0)
	enrollmentRejectGPUBound.Store(0)
	enrollmentRejectNodeIDBound.Store(0)
	enrollmentRejectInsufficient.Store(0)
	enrollmentRejectDecode.Store(0)
	enrollmentRejectFee.Store(0)
	enrollmentRejectWrongContract.Store(0)
	enrollmentRejectAdmission.Store(0)
	enrollmentRejectOther.Store(0)
	unenrollmentRejectNotEnrolled.Store(0)
	unenrollmentRejectAlreadyRevoked.Store(0)
	unenrollmentRejectNotOwner.Store(0)
	unenrollmentRejectDecode.Store(0)
	unenrollmentRejectFee.Store(0)
	unenrollmentRejectOther.Store(0)
	enrollmentAutoRevokeUnbondSwept.Store(0)
	enrollmentStateProvider.Store(enrollmentStateProviderHolder{p: nopEnrollmentStateProvider{}})
}
