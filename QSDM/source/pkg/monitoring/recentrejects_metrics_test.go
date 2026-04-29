package monitoring

// recentrejects_metrics_test.go: unit tests for the
// pkg/monitoring side of the recent-rejection ring's
// truncation telemetry. Mirrors archcheck_metrics_test.go in
// posture and reasoning.
//
// What we lock here that the recentrejects-side test
// (pkg/mining/attest/recentrejects/metrics_test.go) cannot:
//
//   - The atomic counter increments are correctly bucketed
//     by field. A switch-table regression that puts gpu_name
//     traffic on the cert_subject counter surfaces here.
//   - The runes_max gauge is monotonic across observations.
//   - The init()-time SetMetricsRecorder wiring is live, so
//     a regression that breaks the dependency-arrow inversion
//     between recentrejects and monitoring trips a loud
//     test failure rather than going dark in dashboards.
//   - The labelled-output ordering is stable so the
//     prometheus_scrape collector emits the three field
//     series in (detail, gpu_name, cert_subject) order — the
//     order the dashboard's PromQL expressions assume.

import (
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mining/attest/recentrejects"
)

// indexRecentRejectFields materialises the labelled-counter
// list into a map keyed by field for terse test-side
// assertions. Mirror of indexArchSpoofRejected in
// archcheck_metrics_test.go.
func indexRecentRejectFields(t *testing.T) map[string]recentRejectFieldLabeled {
	t.Helper()
	out := make(map[string]recentRejectFieldLabeled)
	for _, p := range recentRejectFieldsLabeled() {
		out[p.Field] = p
	}
	return out
}

// TestRecordRecentRejectField_BucketsObservedAndTruncated locks
// the basic switch-table contract: a known field name lands
// on its dedicated triple of (observed, truncated, runes_max)
// counters. A typo'd switch case would surface here as a
// wrong-bucket increment.
func TestRecordRecentRejectField_BucketsObservedAndTruncated(t *testing.T) {
	t.Cleanup(ResetRecentRejectMetricsForTest)
	ResetRecentRejectMetricsForTest()

	// Detail: 3 observations, 1 of which is truncated.
	RecordRecentRejectField(RecentRejectFieldDetail, 50, false)
	RecordRecentRejectField(RecentRejectFieldDetail, 250, true) // ran past 200-rune cap
	RecordRecentRejectField(RecentRejectFieldDetail, 100, false)

	// GPUName: 2 observations, 0 truncated.
	RecordRecentRejectField(RecentRejectFieldGPUName, 30, false)
	RecordRecentRejectField(RecentRejectFieldGPUName, 64, false)

	// CertSubject: 1 observation, 1 truncated.
	RecordRecentRejectField(RecentRejectFieldCertSubject, 300, true)

	got := indexRecentRejectFields(t)

	if got[RecentRejectFieldDetail].Observed != 3 {
		t.Errorf("detail observed = %d, want 3", got[RecentRejectFieldDetail].Observed)
	}
	if got[RecentRejectFieldDetail].Truncated != 1 {
		t.Errorf("detail truncated = %d, want 1", got[RecentRejectFieldDetail].Truncated)
	}
	if got[RecentRejectFieldDetail].RunesMax != 250 {
		t.Errorf("detail runes_max = %d, want 250 (largest observed value)",
			got[RecentRejectFieldDetail].RunesMax)
	}

	if got[RecentRejectFieldGPUName].Observed != 2 {
		t.Errorf("gpu_name observed = %d, want 2", got[RecentRejectFieldGPUName].Observed)
	}
	if got[RecentRejectFieldGPUName].Truncated != 0 {
		t.Errorf("gpu_name truncated = %d, want 0 (no over-cap inputs)",
			got[RecentRejectFieldGPUName].Truncated)
	}
	if got[RecentRejectFieldGPUName].RunesMax != 64 {
		t.Errorf("gpu_name runes_max = %d, want 64", got[RecentRejectFieldGPUName].RunesMax)
	}

	if got[RecentRejectFieldCertSubject].Observed != 1 {
		t.Errorf("cert_subject observed = %d, want 1",
			got[RecentRejectFieldCertSubject].Observed)
	}
	if got[RecentRejectFieldCertSubject].Truncated != 1 {
		t.Errorf("cert_subject truncated = %d, want 1",
			got[RecentRejectFieldCertSubject].Truncated)
	}
	if got[RecentRejectFieldCertSubject].RunesMax != 300 {
		t.Errorf("cert_subject runes_max = %d, want 300",
			got[RecentRejectFieldCertSubject].RunesMax)
	}
}

// TestRecordRecentRejectField_UnknownFieldIgnored covers the
// cardinality bound: a future code path that passes a typo'd
// field name (e.g. "gpuname" without the underscore) MUST be
// silently ignored rather than creating a new label.
func TestRecordRecentRejectField_UnknownFieldIgnored(t *testing.T) {
	t.Cleanup(ResetRecentRejectMetricsForTest)
	ResetRecentRejectMetricsForTest()

	RecordRecentRejectField("not-a-real-field", 9999, true)

	got := indexRecentRejectFields(t)
	for _, p := range got {
		if p.Observed != 0 || p.Truncated != 0 || p.RunesMax != 0 {
			t.Errorf("unknown field name leaked into counter %q: %+v", p.Field, p)
		}
	}
}

// TestRecordRecentRejectField_NegativeRunesClampedToZero
// pins the defensive negative-input clamp. A future helper
// that emits a negative count due to an unsigned-vs-signed
// arithmetic bug would otherwise underflow the uint64
// runes_max gauge to a huge value.
func TestRecordRecentRejectField_NegativeRunesClampedToZero(t *testing.T) {
	t.Cleanup(ResetRecentRejectMetricsForTest)
	ResetRecentRejectMetricsForTest()

	RecordRecentRejectField(RecentRejectFieldDetail, -42, false)

	got := indexRecentRejectFields(t)[RecentRejectFieldDetail]
	if got.Observed != 1 {
		t.Errorf("observed should still increment for clamped negative input: got %d, want 1",
			got.Observed)
	}
	if got.RunesMax != 0 {
		t.Errorf("runes_max should clamp negative input to 0: got %d", got.RunesMax)
	}
}

// TestRecordRecentRejectField_RunesMaxIsMonotonic locks the
// CAS-loop semantics in storeMaxIfGreater. A regression
// where the loop bumped the value on every Add (rather than
// only on max-exceeded) would surface here as a regressed
// value after a smaller observation.
func TestRecordRecentRejectField_RunesMaxIsMonotonic(t *testing.T) {
	t.Cleanup(ResetRecentRejectMetricsForTest)
	ResetRecentRejectMetricsForTest()

	RecordRecentRejectField(RecentRejectFieldDetail, 100, false)
	RecordRecentRejectField(RecentRejectFieldDetail, 250, true)
	RecordRecentRejectField(RecentRejectFieldDetail, 5, false)
	RecordRecentRejectField(RecentRejectFieldDetail, 200, false)

	got := indexRecentRejectFields(t)[RecentRejectFieldDetail]
	if got.RunesMax != 250 {
		t.Errorf("runes_max regressed: got %d, want 250 (the all-time max)", got.RunesMax)
	}
}

// TestRecentRejectFieldsLabeled_StableOrdering pins the
// emission order so dashboard PromQL expressions can rely on
// a fixed series order during scrape rendering. A reordering
// PR that flips gpu_name and cert_subject would surface
// here.
func TestRecentRejectFieldsLabeled_StableOrdering(t *testing.T) {
	got := recentRejectFieldsLabeled()
	want := []string{
		RecentRejectFieldDetail,
		RecentRejectFieldGPUName,
		RecentRejectFieldCertSubject,
	}
	if len(got) != len(want) {
		t.Fatalf("recentRejectFieldsLabeled() returned %d rows, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Field != w {
			t.Errorf("row[%d].field = %q, want %q", i, got[i].Field, w)
		}
	}
}

// TestRecentRejectsMetricsAdapter_IsRegistered locks the
// init-time wiring. If a future refactor breaks the chain
// (recentrejects.SetMetricsRecorder never called, or the
// adapter forwards to the wrong package-level function) the
// production binary would silently lose the truncation
// telemetry — every dashboard reading the truncation rate
// would go flat. Driving the recorder through the public
// package surface here catches it.
func TestRecentRejectsMetricsAdapter_IsRegistered(t *testing.T) {
	t.Cleanup(func() {
		// Reinstall the production adapter so any sibling
		// test in the same `go test` invocation gets the
		// real wiring back.
		recentrejects.SetMetricsRecorder(recentRejectsMetricsAdapter{})
		ResetRecentRejectMetricsForTest()
	})
	ResetRecentRejectMetricsForTest()

	// Assert the production adapter is the package-default
	// recorder by driving a Store.Record() and observing the
	// monitoring counters increment. (We cannot read the
	// recentrejects-internal atomic.Value directly, but a
	// successful round-trip through Store.Record proves the
	// init() wiring fired before this test ran.)
	s := recentrejects.NewStore(8, nil)
	s.Record(recentrejects.Rejection{
		Kind:    recentrejects.KindArchSpoofGPUNameMismatch,
		Detail:  "step 8: gpu_name vs gpu_arch (test fixture)",
		GPUName: "NVIDIA H100 80GB HBM3 (test)",
	})

	got := indexRecentRejectFields(t)
	if got[RecentRejectFieldDetail].Observed != 1 {
		t.Errorf("adapter not forwarding Detail observations: got %d, want 1",
			got[RecentRejectFieldDetail].Observed)
	}
	if got[RecentRejectFieldGPUName].Observed != 1 {
		t.Errorf("adapter not forwarding GPUName observations: got %d, want 1",
			got[RecentRejectFieldGPUName].Observed)
	}
	if got[RecentRejectFieldCertSubject].Observed != 0 {
		t.Errorf("adapter forwarded an empty CertSubject (must skip): got %d, want 0",
			got[RecentRejectFieldCertSubject].Observed)
	}
}

// TestRecentRejectsMetricsAdapter_ImplementsInterface is a
// pure compile-time assertion that the adapter type
// satisfies recentrejects.MetricsRecorder. A method-rename
// regression in the interface that the adapter does not
// catch up to would otherwise only show up at the init()
// call site as a "cannot use ... as ... in argument" build
// error — fine but obscures the root cause. Locking the
// satisfaction here surfaces the regression next to the
// other recorder tests.
func TestRecentRejectsMetricsAdapter_ImplementsInterface(t *testing.T) {
	var _ recentrejects.MetricsRecorder = recentRejectsMetricsAdapter{}
}
