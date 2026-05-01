package monitoring

// Tests for EnrollmentMetricsSnapshot and the surrounding
// counter/gauge accessors. Coverage:
//
//   - Cleared snapshot returns all zeros.
//   - Counters increment under Record* helpers.
//   - Gauges proxy through to the installed
//     EnrollmentStateProvider; nil provider reads as zero.
//   - Reject-by-reason slices are dense (every closed-enum
//     label present even when zero) AND in the same stable
//     order as the *Labeled() functions, so a dashboard
//     renderer can rely on the ordering.

import (
	"testing"
)

type fakeEnrollmentStateProvider struct {
	active uint64
	bond   uint64
	unCnt  uint64
	unDust uint64
}

func (f fakeEnrollmentStateProvider) ActiveCount() uint64        { return f.active }
func (f fakeEnrollmentStateProvider) BondedDust() uint64         { return f.bond }
func (f fakeEnrollmentStateProvider) PendingUnbondCount() uint64 { return f.unCnt }
func (f fakeEnrollmentStateProvider) PendingUnbondDust() uint64  { return f.unDust }

func TestEnrollmentMetricsSnapshot_AllZerosOnFreshState(t *testing.T) {
	ResetEnrollmentMetricsForTest()
	view := EnrollmentMetricsSnapshot()

	if view.ActiveCount != 0 || view.BondedDust != 0 ||
		view.PendingUnbondCount != 0 || view.PendingUnbondDust != 0 {
		t.Errorf("gauges should be zero on cleared state; got %+v", view)
	}
	if view.EnrollAppliedTotal != 0 || view.UnenrollAppliedTotal != 0 ||
		view.EnrollUnbondSweptTotal != 0 {
		t.Errorf("counters should be zero on cleared state; got %+v", view)
	}
	// Reject slices are dense — one row per closed-enum
	// reason, all zero on a fresh state. The label set is
	// the contract operators rely on; tightening the
	// allowlist later is a wire-format change.
	if got := len(view.EnrollRejectedByReason); got != 9 {
		t.Errorf("EnrollRejectedByReason length: got %d, want 9", got)
	}
	if got := len(view.UnenrollRejectedByReason); got != 6 {
		t.Errorf("UnenrollRejectedByReason length: got %d, want 6", got)
	}
	for _, lc := range view.EnrollRejectedByReason {
		if lc.Value != 0 {
			t.Errorf("enroll reject %q: %d, want 0 on cleared state", lc.Label, lc.Value)
		}
	}
}

func TestEnrollmentMetricsSnapshot_CountersIncrementUnderRecord(t *testing.T) {
	ResetEnrollmentMetricsForTest()
	t.Cleanup(ResetEnrollmentMetricsForTest)

	RecordEnrollmentApplied()
	RecordEnrollmentApplied()
	RecordUnenrollmentApplied()
	RecordEnrollmentUnbondSwept(3)
	RecordEnrollmentRejected(EnrollRejectReasonStakeMismatch)
	RecordEnrollmentRejected(EnrollRejectReasonGPUBound)
	RecordEnrollmentRejected(EnrollRejectReasonGPUBound)
	RecordUnenrollmentRejected(UnenrollRejectReasonNotOwner)

	view := EnrollmentMetricsSnapshot()

	if view.EnrollAppliedTotal != 2 {
		t.Errorf("EnrollAppliedTotal = %d, want 2", view.EnrollAppliedTotal)
	}
	if view.UnenrollAppliedTotal != 1 {
		t.Errorf("UnenrollAppliedTotal = %d, want 1", view.UnenrollAppliedTotal)
	}
	if view.EnrollUnbondSweptTotal != 3 {
		t.Errorf("EnrollUnbondSweptTotal = %d, want 3", view.EnrollUnbondSweptTotal)
	}

	rejByLabel := func(rows []EnrollmentLabeledCount, label string) uint64 {
		for _, r := range rows {
			if r.Label == label {
				return r.Value
			}
		}
		return 0
	}
	if got := rejByLabel(view.EnrollRejectedByReason, EnrollRejectReasonStakeMismatch); got != 1 {
		t.Errorf("enroll reject stake_mismatch = %d, want 1", got)
	}
	if got := rejByLabel(view.EnrollRejectedByReason, EnrollRejectReasonGPUBound); got != 2 {
		t.Errorf("enroll reject gpu_bound = %d, want 2", got)
	}
	if got := rejByLabel(view.UnenrollRejectedByReason, UnenrollRejectReasonNotOwner); got != 1 {
		t.Errorf("unenroll reject not_owner = %d, want 1", got)
	}
	// Untouched reasons stay zero (sanity: no cross-bucket leakage).
	if got := rejByLabel(view.EnrollRejectedByReason, EnrollRejectReasonAdmission); got != 0 {
		t.Errorf("enroll reject admission_failed = %d, want 0 (untouched)", got)
	}
}

func TestEnrollmentMetricsSnapshot_GaugesProxyThroughProvider(t *testing.T) {
	ResetEnrollmentMetricsForTest()
	t.Cleanup(ResetEnrollmentMetricsForTest)

	SetEnrollmentStateProvider(fakeEnrollmentStateProvider{
		active: 42,
		bond:   123_456_789,
		unCnt:  7,
		unDust: 9_876_543,
	})

	view := EnrollmentMetricsSnapshot()
	if view.ActiveCount != 42 {
		t.Errorf("ActiveCount: got %d, want 42", view.ActiveCount)
	}
	if view.BondedDust != 123_456_789 {
		t.Errorf("BondedDust: got %d, want 123456789", view.BondedDust)
	}
	if view.PendingUnbondCount != 7 {
		t.Errorf("PendingUnbondCount: got %d, want 7", view.PendingUnbondCount)
	}
	if view.PendingUnbondDust != 9_876_543 {
		t.Errorf("PendingUnbondDust: got %d, want 9876543", view.PendingUnbondDust)
	}

	// Detach: gauges read as zero again, counters keep
	// their last value (callers MUST NOT depend on a
	// detach to reset counters; the dedicated
	// ResetEnrollmentMetricsForTest does that).
	SetEnrollmentStateProvider(nil)
	view = EnrollmentMetricsSnapshot()
	if view.ActiveCount != 0 || view.BondedDust != 0 ||
		view.PendingUnbondCount != 0 || view.PendingUnbondDust != 0 {
		t.Errorf("gauges should re-zero after SetEnrollmentStateProvider(nil); got %+v", view)
	}
}

func TestEnrollmentMetricsSnapshot_LabelOrderingMatchesPrometheusExposition(t *testing.T) {
	ResetEnrollmentMetricsForTest()
	view := EnrollmentMetricsSnapshot()

	// Compare element-wise against the *Labeled() helpers
	// — these are the same accessors the Prometheus exposer
	// reads, so the dashboard sees the same row order
	// Prometheus does. Drift here would silently misalign
	// kind columns between the dashboard tile and any
	// PromQL visualisation.
	enrollRej := EnrollmentRejectedLabeled()
	if len(view.EnrollRejectedByReason) != len(enrollRej) {
		t.Fatalf("len mismatch: snapshot %d vs Labeled %d",
			len(view.EnrollRejectedByReason), len(enrollRej))
	}
	for i := range enrollRej {
		if view.EnrollRejectedByReason[i].Label != enrollRej[i].Reason {
			t.Errorf("EnrollRejectedByReason[%d].Label = %q, want %q",
				i, view.EnrollRejectedByReason[i].Label, enrollRej[i].Reason)
		}
	}

	unenrollRej := UnenrollmentRejectedLabeled()
	if len(view.UnenrollRejectedByReason) != len(unenrollRej) {
		t.Fatalf("len mismatch: snapshot %d vs Labeled %d",
			len(view.UnenrollRejectedByReason), len(unenrollRej))
	}
	for i := range unenrollRej {
		if view.UnenrollRejectedByReason[i].Label != unenrollRej[i].Reason {
			t.Errorf("UnenrollRejectedByReason[%d].Label = %q, want %q",
				i, view.UnenrollRejectedByReason[i].Label, unenrollRej[i].Reason)
		}
	}
}
