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
	// New surface (2026-04-29): persist-error reporting is
	// optional. The adapter must satisfy all five interfaces —
	// the FilePersister probes via type assertion, so a method
	// drop here would silently break alerting without
	// failing the build. Compile-time assertion catches it.
	var _ recentrejects.PersistErrorRecorder = recentRejectsMetricsAdapter{}
	// Compaction observability (2026-04-30):
	var _ recentrejects.PersistCompactionRecorder = recentRejectsMetricsAdapter{}
	var _ recentrejects.PersistRecordsRecorder = recentRejectsMetricsAdapter{}
	// Hard-cap drop telemetry (2026-04-30):
	var _ recentrejects.PersistHardCapDropRecorder = recentRejectsMetricsAdapter{}
	// Per-miner rate-limit drop telemetry (2026-05-01):
	var _ recentrejects.RateLimitRecorder = recentRejectsMetricsAdapter{}
}

// TestRecordRecentRejectPersistError_IncrementsCounter locks
// the persist-error counter contract: a non-nil error
// increments qsdm_attest_rejection_persist_errors_total by
// exactly one; a nil error is a no-op (defensive against
// callers passing through zero values).
func TestRecordRecentRejectPersistError_IncrementsCounter(t *testing.T) {
	t.Cleanup(ResetRecentRejectMetricsForTest)
	ResetRecentRejectMetricsForTest()

	if got := RecentRejectPersistErrorsForTest(); got != 0 {
		t.Fatalf("baseline counter: got %d, want 0", got)
	}

	RecordRecentRejectPersistError(nil) // nil drops silently
	if got := RecentRejectPersistErrorsForTest(); got != 0 {
		t.Errorf("nil error bumped counter: got %d, want 0", got)
	}

	RecordRecentRejectPersistError(errFakePersistError("disk full"))
	RecordRecentRejectPersistError(errFakePersistError("disk full"))
	RecordRecentRejectPersistError(errFakePersistError("permission denied"))

	if got := RecentRejectPersistErrorsForTest(); got != 3 {
		t.Errorf("counter after 3 errors: got %d, want 3", got)
	}
}

// errFakePersistError is a minimal error type that lets the
// test express "any non-nil error" without depending on the
// errors package or fmt.Errorf (avoids extra imports for one
// test).
type errFakePersistError string

func (e errFakePersistError) Error() string { return string(e) }

// TestRecentRejectsMetricsAdapter_PersistErrorRoutes drives
// the dependency-inversion chain end-to-end for persist
// errors: install the adapter via the recentrejects setter,
// trigger a persister.Append failure through Store.Record,
// and observe the monitoring-side counter increment.
//
// Without this test, the wiring chain
//
//	Store.Record -> Persister.Append (err)
//	             -> notePersistError (recentrejects)
//	             -> recordersAtomic.Load (interface lookup)
//	             -> PersistErrorRecorder type assertion
//	             -> recentRejectsMetricsAdapter.RecordPersistError
//	             -> RecordRecentRejectPersistError
//	             -> rrPersistErrors.Add(1)
//
// could silently break at any step and the only operator
// signal would be a flat dashboard.
func TestRecentRejectsMetricsAdapter_PersistErrorRoutes(t *testing.T) {
	t.Cleanup(func() {
		recentrejects.SetMetricsRecorder(recentRejectsMetricsAdapter{})
		ResetRecentRejectMetricsForTest()
	})
	ResetRecentRejectMetricsForTest()
	recentrejects.SetMetricsRecorder(recentRejectsMetricsAdapter{})

	s := recentrejects.NewStore(8, nil)
	s.SetPersister(failingPersister{err: errFakePersistError("simulated disk full")})

	s.Record(recentrejects.Rejection{Kind: recentrejects.KindArchSpoofUnknown, Reason: "test"})
	s.Record(recentrejects.Rejection{Kind: recentrejects.KindArchSpoofUnknown, Reason: "test"})

	if got := RecentRejectPersistErrorsForTest(); got != 2 {
		t.Errorf("end-to-end persist-error count: got %d, want 2 (chain broken at one of: notePersistError / type-assertion / adapter / counter)", got)
	}
	if got := s.PersistErrorCount(); got != 2 {
		t.Errorf("Store-internal PersistErrorCount: got %d, want 2", got)
	}
}

// failingPersister is a recentrejects.Persister that always
// fails on Append. Identical shape to the version in
// pkg/mining/attest/recentrejects/persistence_test.go but
// duplicated here because Go's test files in different
// packages cannot share helpers.
type failingPersister struct {
	err error
}

func (p failingPersister) Append(recentrejects.Rejection) error { return p.err }
func (p failingPersister) LoadAll() ([]recentrejects.Rejection, error) {
	return nil, nil
}
func (p failingPersister) Close() error { return nil }

// RecentRejectMetricsSnapshot is the in-process consumer-
// facing accessor used by the operator dashboard's
// attestation-rejections tile (see internal/dashboard/
// attest_rejections.go). The two tests below pin the wire
// shape so a future refactor doesn't silently change the
// JSON contract the tile depends on.
func TestRecentRejectMetricsSnapshot_ZeroState(t *testing.T) {
	t.Cleanup(ResetRecentRejectMetricsForTest)
	ResetRecentRejectMetricsForTest()

	snap := RecentRejectMetricsSnapshot()

	if got, want := len(snap.Fields), 3; got != want {
		t.Fatalf("len(Fields)=%d, want %d (detail, gpu_name, cert_subject)", got, want)
	}
	wantOrder := []string{
		RecentRejectFieldDetail,
		RecentRejectFieldGPUName,
		RecentRejectFieldCertSubject,
	}
	for i, want := range wantOrder {
		if snap.Fields[i].Field != want {
			t.Errorf("Fields[%d].Field=%q, want %q (stable ordering broken — tile assumes detail/gpu_name/cert_subject)",
				i, snap.Fields[i].Field, want)
		}
		if snap.Fields[i].ObservedTotal != 0 {
			t.Errorf("Fields[%d].ObservedTotal=%d, want 0 (post-reset)",
				i, snap.Fields[i].ObservedTotal)
		}
		if snap.Fields[i].TruncatedTotal != 0 {
			t.Errorf("Fields[%d].TruncatedTotal=%d, want 0", i, snap.Fields[i].TruncatedTotal)
		}
		if snap.Fields[i].RunesMax != 0 {
			t.Errorf("Fields[%d].RunesMax=%d, want 0", i, snap.Fields[i].RunesMax)
		}
	}
	if snap.PersistErrorsTotal != 0 {
		t.Errorf("PersistErrorsTotal=%d, want 0", snap.PersistErrorsTotal)
	}
}

func TestRecentRejectMetricsSnapshot_ReflectsCounters(t *testing.T) {
	t.Cleanup(ResetRecentRejectMetricsForTest)
	ResetRecentRejectMetricsForTest()

	// Two detail observations (one truncated at 4096), one
	// gpu_name observation (not truncated), one cert_subject
	// observation (truncated at 256). Two persist errors.
	RecordRecentRejectField(RecentRejectFieldDetail, 4096, true)
	RecordRecentRejectField(RecentRejectFieldDetail, 1024, false)
	RecordRecentRejectField(RecentRejectFieldGPUName, 32, false)
	RecordRecentRejectField(RecentRejectFieldCertSubject, 256, true)
	RecordRecentRejectPersistError(errFakePersistError("disk full"))
	RecordRecentRejectPersistError(errFakePersistError("permission denied"))

	snap := RecentRejectMetricsSnapshot()

	want := map[string]struct {
		observed, truncated, runesMax uint64
	}{
		RecentRejectFieldDetail:      {2, 1, 4096},
		RecentRejectFieldGPUName:     {1, 0, 32},
		RecentRejectFieldCertSubject: {1, 1, 256},
	}
	for _, row := range snap.Fields {
		w, ok := want[row.Field]
		if !ok {
			t.Errorf("unexpected field in snapshot: %q", row.Field)
			continue
		}
		if row.ObservedTotal != w.observed {
			t.Errorf("%s ObservedTotal=%d, want %d", row.Field, row.ObservedTotal, w.observed)
		}
		if row.TruncatedTotal != w.truncated {
			t.Errorf("%s TruncatedTotal=%d, want %d", row.Field, row.TruncatedTotal, w.truncated)
		}
		if row.RunesMax != w.runesMax {
			t.Errorf("%s RunesMax=%d, want %d", row.Field, row.RunesMax, w.runesMax)
		}
	}
	if snap.PersistErrorsTotal != 2 {
		t.Errorf("PersistErrorsTotal=%d, want 2", snap.PersistErrorsTotal)
	}
}

// ----- Compaction observability surface --------------------
//
// The four tests below pin the new (2026-04-30) compaction-
// observability counters: the counter must increment on every
// call to RecordRecentRejectPersistCompaction, the gauge must
// reflect the latest call to SetRecentRejectPersistRecordsOnDisk,
// the snapshot must surface both, and the adapter must route
// both end-to-end via type assertion when invoked through the
// FilePersister hot path.

// TestRecordRecentRejectPersistCompaction_Increments locks the
// counter contract: each invocation increments by exactly one,
// and the recordsAfter argument is intentionally ignored at the
// counter level (it lives in the gauge, not the counter).
func TestRecordRecentRejectPersistCompaction_Increments(t *testing.T) {
	t.Cleanup(ResetRecentRejectMetricsForTest)
	ResetRecentRejectMetricsForTest()

	if got := RecentRejectPersistCompactionsForTest(); got != 0 {
		t.Fatalf("baseline counter: got %d, want 0", got)
	}

	RecordRecentRejectPersistCompaction(0)
	RecordRecentRejectPersistCompaction(1024)
	RecordRecentRejectPersistCompaction(512)
	RecordRecentRejectPersistCompaction(0)

	if got := RecentRejectPersistCompactionsForTest(); got != 4 {
		t.Errorf("counter after 4 compactions: got %d, want 4", got)
	}
}

// TestSetRecentRejectPersistRecordsOnDisk_Stores locks the
// gauge contract: SetRecentRejectPersistRecordsOnDisk overwrites
// the stored value (it's a gauge, not a counter), the value is
// retrievable via the test accessor, and zero is a valid input
// (the gauge can drop after a manual file truncation in
// production, although that is not currently a code path).
func TestSetRecentRejectPersistRecordsOnDisk_Stores(t *testing.T) {
	t.Cleanup(ResetRecentRejectMetricsForTest)
	ResetRecentRejectMetricsForTest()

	if got := RecentRejectPersistRecordsOnDiskForTest(); got != 0 {
		t.Fatalf("baseline gauge: got %d, want 0", got)
	}

	SetRecentRejectPersistRecordsOnDisk(7)
	if got := RecentRejectPersistRecordsOnDiskForTest(); got != 7 {
		t.Errorf("after Set(7): got %d, want 7", got)
	}

	SetRecentRejectPersistRecordsOnDisk(1024)
	if got := RecentRejectPersistRecordsOnDiskForTest(); got != 1024 {
		t.Errorf("after Set(1024): got %d, want 1024 (gauge must overwrite, not accumulate)", got)
	}

	SetRecentRejectPersistRecordsOnDisk(0)
	if got := RecentRejectPersistRecordsOnDiskForTest(); got != 0 {
		t.Errorf("after Set(0): got %d, want 0 (gauge must accept zero)", got)
	}
}

// TestRecentRejectMetricsSnapshot_IncludesCompactionAndOnDisk
// pins the wire contract for the dashboard tile: the snapshot
// must surface both new fields with their current values. A
// future refactor that drops PersistCompactionsTotal /
// PersistRecordsOnDisk from the JSON shape would silently break
// the operator dashboard's compaction-rate cell.
func TestRecentRejectMetricsSnapshot_IncludesCompactionAndOnDisk(t *testing.T) {
	t.Cleanup(ResetRecentRejectMetricsForTest)
	ResetRecentRejectMetricsForTest()

	RecordRecentRejectPersistCompaction(512)
	RecordRecentRejectPersistCompaction(512)
	RecordRecentRejectPersistCompaction(512)
	SetRecentRejectPersistRecordsOnDisk(847)

	snap := RecentRejectMetricsSnapshot()

	if snap.PersistCompactionsTotal != 3 {
		t.Errorf("PersistCompactionsTotal=%d, want 3", snap.PersistCompactionsTotal)
	}
	if snap.PersistRecordsOnDisk != 847 {
		t.Errorf("PersistRecordsOnDisk=%d, want 847", snap.PersistRecordsOnDisk)
	}
	// The pre-existing fields must still surface unchanged
	// — this catches a regression where the new fields'
	// addition broke the snapshot's per-field row population.
	if len(snap.Fields) != 3 {
		t.Errorf("len(Fields)=%d, want 3 (regression in pre-existing surface)", len(snap.Fields))
	}
}

// TestRecentRejectsMetricsAdapter_CompactionAndRecordsRoute
// drives the dependency-inversion chain end-to-end for the
// new hooks: install the production adapter via the
// recentrejects setter, fire the hook by hand (we'd need a
// real FilePersister to drive it via the natural path, but
// that lives in pkg/mining/attest/recentrejects' own test
// binary — here we verify the adapter's RecordPersistCompaction
// and SetPersistRecordsOnDisk methods route to the package-
// level functions).
func TestRecentRejectsMetricsAdapter_CompactionAndRecordsRoute(t *testing.T) {
	t.Cleanup(func() {
		recentrejects.SetMetricsRecorder(recentRejectsMetricsAdapter{})
		ResetRecentRejectMetricsForTest()
	})
	ResetRecentRejectMetricsForTest()
	recentrejects.SetMetricsRecorder(recentRejectsMetricsAdapter{})

	a := recentRejectsMetricsAdapter{}
	a.RecordPersistCompaction(256)
	a.RecordPersistCompaction(512)
	a.SetPersistRecordsOnDisk(999)

	if got := RecentRejectPersistCompactionsForTest(); got != 2 {
		t.Errorf("compactions counter via adapter: got %d, want 2", got)
	}
	if got := RecentRejectPersistRecordsOnDiskForTest(); got != 999 {
		t.Errorf("records-on-disk gauge via adapter: got %d, want 999", got)
	}
}

// -----------------------------------------------------------------------------
// Hard-cap drop telemetry (2026-04-30)
// -----------------------------------------------------------------------------

// TestRecordRecentRejectPersistHardCapDrop_Increments locks the
// counter contract: each call increments by exactly 1 regardless
// of the droppedBytes argument (which is currently dropped on
// the floor — operators alert on the rate, not the byte volume).
func TestRecordRecentRejectPersistHardCapDrop_Increments(t *testing.T) {
	t.Cleanup(ResetRecentRejectMetricsForTest)
	ResetRecentRejectMetricsForTest()

	if got := RecentRejectPersistHardCapDropsForTest(); got != 0 {
		t.Fatalf("baseline counter: got %d, want 0", got)
	}

	RecordRecentRejectPersistHardCapDrop(0)
	RecordRecentRejectPersistHardCapDrop(512)
	RecordRecentRejectPersistHardCapDrop(1024)

	if got := RecentRejectPersistHardCapDropsForTest(); got != 3 {
		t.Errorf("counter after 3 drops: got %d, want 3", got)
	}
}

// TestRecentRejectMetricsSnapshot_IncludesHardCapDrops pins the
// wire contract for the dashboard tile: the snapshot must
// surface PersistHardCapDropsTotal so the new persistence cell
// renders correctly. A future refactor that drops the field
// from the JSON shape would silently blank the operator's
// flood-detection signal.
func TestRecentRejectMetricsSnapshot_IncludesHardCapDrops(t *testing.T) {
	t.Cleanup(ResetRecentRejectMetricsForTest)
	ResetRecentRejectMetricsForTest()

	RecordRecentRejectPersistHardCapDrop(256)
	RecordRecentRejectPersistHardCapDrop(256)

	snap := RecentRejectMetricsSnapshot()

	if snap.PersistHardCapDropsTotal != 2 {
		t.Errorf("PersistHardCapDropsTotal=%d, want 2", snap.PersistHardCapDropsTotal)
	}
	// Pre-existing fields must still surface unchanged.
	if len(snap.Fields) != 3 {
		t.Errorf("len(Fields)=%d, want 3 (regression in pre-existing surface)",
			len(snap.Fields))
	}
}

// TestRecentRejectsMetricsAdapter_HardCapDropRoutes drives the
// dependency-inversion chain end-to-end for the new hard-cap
// hook: invoking the adapter method must reach the package-
// level counter, with no leakage between this counter and the
// other persistence-lifecycle counters.
func TestRecentRejectsMetricsAdapter_HardCapDropRoutes(t *testing.T) {
	t.Cleanup(func() {
		recentrejects.SetMetricsRecorder(recentRejectsMetricsAdapter{})
		ResetRecentRejectMetricsForTest()
	})
	ResetRecentRejectMetricsForTest()
	recentrejects.SetMetricsRecorder(recentRejectsMetricsAdapter{})

	a := recentRejectsMetricsAdapter{}
	a.RecordPersistHardCapDrop(512)
	a.RecordPersistHardCapDrop(1024)

	if got := RecentRejectPersistHardCapDropsForTest(); got != 2 {
		t.Errorf("hard-cap counter via adapter: got %d, want 2", got)
	}
	// Other persistence counters must remain at zero — a
	// shared-storage regression would surface as a stray
	// increment here.
	if got := RecentRejectPersistErrorsForTest(); got != 0 {
		t.Errorf("persist-errors counter leaked: got %d, want 0", got)
	}
	if got := RecentRejectPersistCompactionsForTest(); got != 0 {
		t.Errorf("compactions counter leaked: got %d, want 0", got)
	}
}

// TestResetRecentRejectMetricsForTest_ResetsHardCapDrops pins
// that the reset helper clears the new counter alongside the
// existing ones. Tests rely on a known-zero baseline; a missed
// reset would cross-contaminate parallel test runs.
func TestResetRecentRejectMetricsForTest_ResetsHardCapDrops(t *testing.T) {
	RecordRecentRejectPersistHardCapDrop(1)
	if got := RecentRejectPersistHardCapDropsForTest(); got != 1 {
		t.Fatalf("setup: counter not at 1: %d", got)
	}
	ResetRecentRejectMetricsForTest()
	if got := RecentRejectPersistHardCapDropsForTest(); got != 0 {
		t.Errorf("after reset: got %d, want 0", got)
	}
}

// -----------------------------------------------------------------------------
// Per-miner rate-limit drop telemetry (2026-05-01)
// -----------------------------------------------------------------------------

// TestRecordRecentRejectPerMinerRateLimited_Increments locks the
// counter contract: each call increments by exactly 1 regardless
// of the minerAddr argument. The addr is accepted on the API so
// a future structured-log adapter can use it but the production
// Prometheus mirror discards it (cardinality safety).
func TestRecordRecentRejectPerMinerRateLimited_Increments(t *testing.T) {
	t.Cleanup(ResetRecentRejectMetricsForTest)
	ResetRecentRejectMetricsForTest()

	if got := RecentRejectPerMinerRateLimitedForTest(); got != 0 {
		t.Fatalf("baseline counter: got %d, want 0", got)
	}

	RecordRecentRejectPerMinerRateLimited("")
	RecordRecentRejectPerMinerRateLimited("0xA")
	RecordRecentRejectPerMinerRateLimited("0xB")

	if got := RecentRejectPerMinerRateLimitedForTest(); got != 3 {
		t.Errorf("counter after 3 drops: got %d, want 3", got)
	}
}

// TestRecentRejectMetricsSnapshot_IncludesPerMinerRateLimited
// pins the wire contract: the snapshot must surface
// PerMinerRateLimitedTotal so the dashboard tile renders the
// new cell. Removing the field from the JSON shape silently
// blanks the operator's "single bad actor" signal.
func TestRecentRejectMetricsSnapshot_IncludesPerMinerRateLimited(t *testing.T) {
	t.Cleanup(ResetRecentRejectMetricsForTest)
	ResetRecentRejectMetricsForTest()

	RecordRecentRejectPerMinerRateLimited("0xflood")
	RecordRecentRejectPerMinerRateLimited("0xflood")
	RecordRecentRejectPerMinerRateLimited("0xflood")

	snap := RecentRejectMetricsSnapshot()

	if snap.PerMinerRateLimitedTotal != 3 {
		t.Errorf("PerMinerRateLimitedTotal=%d, want 3", snap.PerMinerRateLimitedTotal)
	}
	// Pre-existing surface must still survive unchanged.
	if len(snap.Fields) != 3 {
		t.Errorf("len(Fields)=%d, want 3 (regression in pre-existing surface)",
			len(snap.Fields))
	}
	if snap.PersistHardCapDropsTotal != 0 {
		t.Errorf("hard-cap drops leaked into rate-limit test: %d",
			snap.PersistHardCapDropsTotal)
	}
}

// TestRecentRejectsMetricsAdapter_RateLimitRoutes drives the
// dependency-inversion chain end-to-end for the new
// rate-limit hook: invoking the adapter method must reach the
// package-level counter, with no leakage between this
// counter and the other persistence-lifecycle counters.
func TestRecentRejectsMetricsAdapter_RateLimitRoutes(t *testing.T) {
	t.Cleanup(func() {
		recentrejects.SetMetricsRecorder(recentRejectsMetricsAdapter{})
		ResetRecentRejectMetricsForTest()
	})
	ResetRecentRejectMetricsForTest()
	recentrejects.SetMetricsRecorder(recentRejectsMetricsAdapter{})

	a := recentRejectsMetricsAdapter{}
	a.RecordRateLimited("0xA")
	a.RecordRateLimited("0xA")
	a.RecordRateLimited("0xB")

	if got := RecentRejectPerMinerRateLimitedForTest(); got != 3 {
		t.Errorf("rate-limit counter via adapter: got %d, want 3", got)
	}
	// Cross-leak guards: every other counter must remain
	// at zero. A shared-storage regression would surface as
	// a stray increment here.
	if got := RecentRejectPersistErrorsForTest(); got != 0 {
		t.Errorf("persist-errors counter leaked: got %d, want 0", got)
	}
	if got := RecentRejectPersistCompactionsForTest(); got != 0 {
		t.Errorf("compactions counter leaked: got %d, want 0", got)
	}
	if got := RecentRejectPersistHardCapDropsForTest(); got != 0 {
		t.Errorf("hard-cap drops counter leaked: got %d, want 0", got)
	}
}

// TestResetRecentRejectMetricsForTest_ResetsPerMinerRateLimited
// pins that the reset helper clears the new counter alongside
// the existing ones. Tests rely on a known-zero baseline; a
// missed reset would cross-contaminate parallel test runs.
func TestResetRecentRejectMetricsForTest_ResetsPerMinerRateLimited(t *testing.T) {
	RecordRecentRejectPerMinerRateLimited("0xA")
	if got := RecentRejectPerMinerRateLimitedForTest(); got != 1 {
		t.Fatalf("setup: counter not at 1: %d", got)
	}
	ResetRecentRejectMetricsForTest()
	if got := RecentRejectPerMinerRateLimitedForTest(); got != 0 {
		t.Errorf("after reset: got %d, want 0", got)
	}
}
