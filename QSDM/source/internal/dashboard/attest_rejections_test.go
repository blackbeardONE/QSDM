package dashboard

// Tests for handleAttestRejections — the dashboard's
// attestation-rejection tile data endpoint. Coverage:
//
//   - Method gating (405 on non-GET).
//   - Limit query-parameter parsing + clamping.
//   - "v1-only deployment" path: lister not wired, the
//     handler still returns 200 with Available=false and a
//     metrics snapshot so the tile renders gracefully.
//   - "v2 deployment" path: lister wired, records returned,
//     newest-first ordering applied.
//   - Metrics snapshot reflects observed/truncated/persist
//     counters set via the package-level Record* entry points.
//
// The tests use a tiny in-memory fakeRecentRejectionLister
// because the real recentrejects.Store lives in pkg/mining
// (a higher-level package) and pulling it in here would
// invert the dependency arrow. The interface contract is
// the same one pkg/api uses, so the fake exercises the same
// code path the production wiring does.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/api"
	"github.com/blackbeardONE/QSDM/pkg/monitoring"
)

// fakeRecentRejectionLister is a process-wide fake the tests
// install via api.SetRecentRejectionLister. List ignores most
// of opts and just returns the canned page.
type fakeRecentRejectionLister struct {
	page api.RecentRejectionListPage
	// lastOpts captures what the handler passed to List so
	// tests can assert on limit propagation.
	lastOpts api.RecentRejectionListOptions
}

func (f *fakeRecentRejectionLister) List(opts api.RecentRejectionListOptions) api.RecentRejectionListPage {
	f.lastOpts = opts
	return f.page
}

// withCleanRecentRejectionWiring resets the package-level
// lister + monitoring counters at test start AND end so
// neighbouring tests in this package don't observe carry-over
// state.
func withCleanRecentRejectionWiring(t *testing.T) {
	t.Helper()
	api.SetRecentRejectionLister(nil)
	monitoring.ResetRecentRejectMetricsForTest()
	t.Cleanup(func() {
		api.SetRecentRejectionLister(nil)
		monitoring.ResetRecentRejectMetricsForTest()
	})
}

// newTestDashboard returns a Dashboard wired enough to call
// handleAttestRejections directly. Mirrors the shape used by
// TestDashboard above so any future Dashboard-construction
// changes only need to be applied in one place.
func newTestDashboard() *Dashboard {
	metrics := monitoring.GetMetrics()
	hc := monitoring.NewHealthChecker(metrics)
	return NewDashboard(metrics, hc, "0", false, DashboardNvidiaLock{}, "", "", false, "", nil)
}

func TestHandleAttestRejections_MethodNotAllowed(t *testing.T) {
	withCleanRecentRejectionWiring(t)
	d := newTestDashboard()

	req := httptest.NewRequest(http.MethodPost, "/api/attest/rejections", nil)
	w := httptest.NewRecorder()
	d.handleAttestRejections(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
	if got := w.Header().Get("Allow"); got != http.MethodGet {
		t.Errorf("Allow header = %q, want %q", got, http.MethodGet)
	}
}

func TestHandleAttestRejections_BadLimit(t *testing.T) {
	withCleanRecentRejectionWiring(t)
	d := newTestDashboard()

	cases := []string{"0", "-1", "abc", "1.5"}
	for _, raw := range cases {
		req := httptest.NewRequest(http.MethodGet,
			"/api/attest/rejections?limit="+raw, nil)
		w := httptest.NewRecorder()
		d.handleAttestRejections(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("limit=%q: status = %d, want %d",
				raw, w.Code, http.StatusBadRequest)
		}
	}
}

func TestHandleAttestRejections_NoListerWired_StillRenders(t *testing.T) {
	// Operators on v1-only deployments must still see the
	// tile with metrics=zeros + Available=false rather than
	// a 503 / blank panel.
	withCleanRecentRejectionWiring(t)
	d := newTestDashboard()

	req := httptest.NewRequest(http.MethodGet, "/api/attest/rejections", nil)
	w := httptest.NewRecorder()
	d.handleAttestRejections(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var got dashboardAttestRejectionsView
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got.Available {
		t.Errorf("Available = true, want false (no lister wired)")
	}
	if got.Records == nil {
		t.Errorf("Records is nil; want empty slice (so JSON renders [])")
	}
	if len(got.Records) != 0 {
		t.Errorf("Records = %v, want empty", got.Records)
	}
	if got.Limit != dashboardAttestRejectionsDefaultLimit {
		t.Errorf("Limit = %d, want %d",
			got.Limit, dashboardAttestRejectionsDefaultLimit)
	}
	// Metrics surface is always populated, even with zeros.
	if len(got.Metrics.Fields) != 3 {
		t.Errorf("Metrics.Fields len = %d, want 3 (detail, gpu_name, cert_subject)",
			len(got.Metrics.Fields))
	}
}

func TestHandleAttestRejections_ListerWired_RecordsReversedNewestFirst(t *testing.T) {
	withCleanRecentRejectionWiring(t)
	d := newTestDashboard()

	// Lister returns ascending Seq (matching pkg/api's
	// adapter contract); the handler must reverse for tile
	// presentation.
	now := time.Now().UTC()
	fake := &fakeRecentRejectionLister{
		page: api.RecentRejectionListPage{
			Records: []api.RecentRejectionView{
				{Seq: 1, RecordedAt: now.Add(-3 * time.Minute), Kind: "archspoof_unknown_arch"},
				{Seq: 2, RecordedAt: now.Add(-2 * time.Minute), Kind: "archspoof_gpu_name_mismatch"},
				{Seq: 3, RecordedAt: now.Add(-1 * time.Minute), Kind: "hashrate_out_of_band"},
			},
			TotalMatches: 3,
		},
	}
	api.SetRecentRejectionLister(fake)

	req := httptest.NewRequest(http.MethodGet,
		"/api/attest/rejections?limit=10", nil)
	w := httptest.NewRecorder()
	d.handleAttestRejections(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var got dashboardAttestRejectionsView
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Available {
		t.Fatalf("Available = false, want true")
	}
	if got.TotalMatches != 3 {
		t.Errorf("TotalMatches = %d, want 3", got.TotalMatches)
	}
	if len(got.Records) != 3 {
		t.Fatalf("len(Records) = %d, want 3", len(got.Records))
	}
	// Newest-first: Seq 3, 2, 1.
	wantSeqs := []uint64{3, 2, 1}
	for i, want := range wantSeqs {
		if got.Records[i].Seq != want {
			t.Errorf("Records[%d].Seq = %d, want %d (newest-first ordering broken)",
				i, got.Records[i].Seq, want)
		}
	}
	// Limit propagated through to the underlying lister.
	if fake.lastOpts.Limit != 10 {
		t.Errorf("lister.opts.Limit = %d, want 10",
			fake.lastOpts.Limit)
	}
}

func TestHandleAttestRejections_LimitClampedToMax(t *testing.T) {
	withCleanRecentRejectionWiring(t)
	d := newTestDashboard()

	fake := &fakeRecentRejectionLister{}
	api.SetRecentRejectionLister(fake)

	// Request a limit way above dashboardAttestRejectionsMaxLimit.
	req := httptest.NewRequest(http.MethodGet,
		"/api/attest/rejections?limit=99999", nil)
	w := httptest.NewRecorder()
	d.handleAttestRejections(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var got dashboardAttestRejectionsView
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Limit != dashboardAttestRejectionsMaxLimit {
		t.Errorf("Limit = %d, want %d (clamp)",
			got.Limit, dashboardAttestRejectionsMaxLimit)
	}
	if fake.lastOpts.Limit != dashboardAttestRejectionsMaxLimit {
		t.Errorf("lister.opts.Limit = %d, want %d (clamp must propagate)",
			fake.lastOpts.Limit, dashboardAttestRejectionsMaxLimit)
	}
}

func TestHandleAttestRejections_MetricsSnapshotReflectsCounters(t *testing.T) {
	withCleanRecentRejectionWiring(t)
	d := newTestDashboard()

	// Drive a few observations through the package-level
	// recorder so the snapshot has non-zero counters.
	monitoring.RecordRecentRejectField(monitoring.RecentRejectFieldDetail, 4096, true)
	monitoring.RecordRecentRejectField(monitoring.RecentRejectFieldDetail, 2048, false)
	monitoring.RecordRecentRejectField(monitoring.RecentRejectFieldGPUName, 64, false)
	monitoring.RecordRecentRejectField(monitoring.RecentRejectFieldCertSubject, 128, true)

	// Two persister failures (the handler doesn't care
	// about the error contents, only the count).
	monitoring.RecordRecentRejectPersistError(errBoom("disk full"))
	monitoring.RecordRecentRejectPersistError(errBoom("permission denied"))

	req := httptest.NewRequest(http.MethodGet, "/api/attest/rejections", nil)
	w := httptest.NewRecorder()
	d.handleAttestRejections(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var got dashboardAttestRejectionsView
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Cumulative rejections:
	//   detail: 2 observed, 1 truncated, max=4096
	//   gpu_name: 1 observed, 0 truncated, max=64
	//   cert_subject: 1 observed, 1 truncated, max=128
	wantByField := map[string]struct {
		observed, truncated, runesMax uint64
	}{
		monitoring.RecentRejectFieldDetail:      {2, 1, 4096},
		monitoring.RecentRejectFieldGPUName:     {1, 0, 64},
		monitoring.RecentRejectFieldCertSubject: {1, 1, 128},
	}

	if len(got.Metrics.Fields) != len(wantByField) {
		t.Fatalf("Metrics.Fields len = %d, want %d",
			len(got.Metrics.Fields), len(wantByField))
	}
	for _, row := range got.Metrics.Fields {
		want, ok := wantByField[row.Field]
		if !ok {
			t.Errorf("unexpected field in snapshot: %q", row.Field)
			continue
		}
		if row.ObservedTotal != want.observed {
			t.Errorf("Field %q ObservedTotal = %d, want %d",
				row.Field, row.ObservedTotal, want.observed)
		}
		if row.TruncatedTotal != want.truncated {
			t.Errorf("Field %q TruncatedTotal = %d, want %d",
				row.Field, row.TruncatedTotal, want.truncated)
		}
		if row.RunesMax != want.runesMax {
			t.Errorf("Field %q RunesMax = %d, want %d",
				row.Field, row.RunesMax, want.runesMax)
		}
	}

	if got.Metrics.PersistErrorsTotal != 2 {
		t.Errorf("PersistErrorsTotal = %d, want 2",
			got.Metrics.PersistErrorsTotal)
	}
}

// errBoom is a tiny error type the persist-error tests use so
// the assertion is on the COUNT, not the wrapped error string.
type errBoom string

func (e errBoom) Error() string { return string(e) }
