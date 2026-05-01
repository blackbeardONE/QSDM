package dashboard

// Tests for handleEnrollmentOverview — the dashboard's
// enrollment-registry tile data endpoint. Coverage:
//
//   - Method gating (405 on non-GET).
//   - Limit query-parameter parsing + clamping.
//   - "v1-only deployment" path: lister not wired, the
//     handler still returns 200 with Available=false and a
//     metrics snapshot so the tile renders gracefully.
//   - "v2 deployment" path: lister wired, records returned
//     in the lister's natural order; pagination metadata
//     forwarded.
//   - Closed-enum filter validation: bogus phase returns
//     400 with a helpful message.
//   - Filter passthrough: phase / cursor are forwarded to
//     the lister verbatim.
//   - Echoed-filters block omitted on a bare call (keeps
//     the wire payload tight).
//   - Metrics snapshot reflects qsdm_enrollment_* counters /
//     gauges set via Record* / SetEnrollmentStateProvider.
//
// The tests use a tiny in-memory fakeEnrollmentLister
// because the real *enrollment.InMemoryState lives in
// pkg/mining/enrollment (a higher-level package) and
// pulling it in here would mean duplicating registry
// fixture setup. The interface contract is the same one
// v2wiring uses, so the fake exercises the same code path.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/api"
	"github.com/blackbeardONE/QSDM/pkg/mining/enrollment"
	"github.com/blackbeardONE/QSDM/pkg/monitoring"
)

// dashFakeEnrollmentStateProvider is the dashboard
// package's local copy of the gauge fake so tests don't
// have to cross the test-package boundary into pkg/monitoring.
// Same shape as the monitoring package's
// fakeEnrollmentStateProvider; kept tiny + duplicated rather
// than promoted to a shared testutil package because exactly
// two callers exist.
type dashFakeEnrollmentStateProvider struct {
	active uint64
	bond   uint64
	unCnt  uint64
	unDust uint64
}

func (f dashFakeEnrollmentStateProvider) ActiveCount() uint64        { return f.active }
func (f dashFakeEnrollmentStateProvider) BondedDust() uint64         { return f.bond }
func (f dashFakeEnrollmentStateProvider) PendingUnbondCount() uint64 { return f.unCnt }
func (f dashFakeEnrollmentStateProvider) PendingUnbondDust() uint64  { return f.unDust }

// fakeEnrollmentLister is a process-wide fake the tests
// install via api.SetEnrollmentLister. List ignores most
// of opts and just returns the canned page; lastOpts
// captures what the handler passed so tests can assert on
// filter propagation.
type fakeEnrollmentLister struct {
	page     enrollment.ListPage
	lastOpts enrollment.ListOptions
}

func (f *fakeEnrollmentLister) List(opts enrollment.ListOptions) enrollment.ListPage {
	f.lastOpts = opts
	return f.page
}

// withCleanEnrollmentWiring resets the package-level
// lister + enrollment counters / gauges at test start AND
// end so neighbouring tests in this package don't observe
// carry-over state.
func withCleanEnrollmentWiring(t *testing.T) {
	t.Helper()
	api.SetEnrollmentLister(nil)
	monitoring.ResetEnrollmentMetricsForTest()
	t.Cleanup(func() {
		api.SetEnrollmentLister(nil)
		monitoring.ResetEnrollmentMetricsForTest()
	})
}

// sampleEnrollmentRec returns a populated EnrollmentRecord
// for a given NodeID + StakeDust + revoked-or-not. Keeps
// the test fixtures tiny while exercising the Phase
// derivation in api.EnrollmentViewFromRecord.
func sampleEnrollmentRec(nodeID, owner, gpuUUID string, stakeDust uint64, revokedAtHeight, unbondMaturesAtHeight uint64) enrollment.EnrollmentRecord {
	return enrollment.EnrollmentRecord{
		NodeID:                nodeID,
		Owner:                 owner,
		GPUUUID:               gpuUUID,
		StakeDust:             stakeDust,
		EnrolledAtHeight:      100,
		RevokedAtHeight:       revokedAtHeight,
		UnbondMaturesAtHeight: unbondMaturesAtHeight,
	}
}

// -----------------------------------------------------------
// Method gating + clamping
// -----------------------------------------------------------

func TestHandleEnrollmentOverview_MethodNotAllowed(t *testing.T) {
	withCleanEnrollmentWiring(t)
	d := newTestDashboard()

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		req := httptest.NewRequest(method, "/api/mining/enrollment-overview", nil)
		w := httptest.NewRecorder()
		d.handleEnrollmentOverview(w, req)

		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("method=%s: status = %d, want %d", method, w.Code, http.StatusMethodNotAllowed)
		}
		if got := w.Header().Get("Allow"); got != http.MethodGet {
			t.Errorf("method=%s: Allow header = %q, want %q", method, got, http.MethodGet)
		}
	}
}

func TestHandleEnrollmentOverview_LimitClamping(t *testing.T) {
	withCleanEnrollmentWiring(t)
	d := newTestDashboard()

	lister := &fakeEnrollmentLister{}
	api.SetEnrollmentLister(lister)

	// Over-cap: clamps to dashboardEnrollmentOverviewMaxLimit (200).
	req := httptest.NewRequest(http.MethodGet,
		"/api/mining/enrollment-overview?limit=99999", nil)
	w := httptest.NewRecorder()
	d.handleEnrollmentOverview(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if lister.lastOpts.Limit != 200 {
		t.Errorf("Limit clamp: got %d, want 200", lister.lastOpts.Limit)
	}

	// Negative: 400.
	req = httptest.NewRequest(http.MethodGet,
		"/api/mining/enrollment-overview?limit=-5", nil)
	w = httptest.NewRecorder()
	d.handleEnrollmentOverview(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("negative limit: status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	// Non-integer: 400.
	req = httptest.NewRequest(http.MethodGet,
		"/api/mining/enrollment-overview?limit=abc", nil)
	w = httptest.NewRecorder()
	d.handleEnrollmentOverview(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("non-integer limit: status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// -----------------------------------------------------------
// v1-only deployment path
// -----------------------------------------------------------

func TestHandleEnrollmentOverview_NoListerWired_AvailableFalse(t *testing.T) {
	withCleanEnrollmentWiring(t)
	d := newTestDashboard()
	if api.CurrentEnrollmentLister() != nil {
		t.Fatal("setup: expected nil lister")
	}

	// Bump some counters and gauges so the test asserts the
	// metrics block still surfaces even when the lister is
	// missing.
	monitoring.RecordEnrollmentApplied()
	monitoring.RecordEnrollmentRejected(monitoring.EnrollRejectReasonStakeMismatch)
	monitoring.SetEnrollmentStateProvider(dashFakeEnrollmentStateProvider{
		active: 5, bond: 50_000_000_000,
	})
	t.Cleanup(func() { monitoring.SetEnrollmentStateProvider(nil) })

	req := httptest.NewRequest(http.MethodGet,
		"/api/mining/enrollment-overview", nil)
	w := httptest.NewRecorder()
	d.handleEnrollmentOverview(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (handler must render gracefully even without lister)",
			w.Code, http.StatusOK)
	}

	var got dashboardEnrollmentOverviewView
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got.Available {
		t.Error("Available = true with no lister; want false")
	}
	if got.Records == nil {
		t.Error("Records = nil; want [] so JSON decodes to an empty slice")
	}
	if got.Limit != dashboardEnrollmentOverviewDefaultLimit {
		t.Errorf("Limit = %d, want %d", got.Limit, dashboardEnrollmentOverviewDefaultLimit)
	}
	if got.Metrics.ActiveCount != 5 {
		t.Errorf("Metrics.ActiveCount = %d, want 5 (gauges should surface even without lister)", got.Metrics.ActiveCount)
	}
	if got.Metrics.BondedDust != 50_000_000_000 {
		t.Errorf("Metrics.BondedDust = %d, want 50000000000", got.Metrics.BondedDust)
	}
	if got.Metrics.EnrollAppliedTotal != 1 {
		t.Errorf("Metrics.EnrollAppliedTotal = %d, want 1", got.Metrics.EnrollAppliedTotal)
	}
	var rejStakeMismatch uint64
	for _, lc := range got.Metrics.EnrollRejectedByReason {
		if lc.Label == monitoring.EnrollRejectReasonStakeMismatch {
			rejStakeMismatch = lc.Value
		}
	}
	if rejStakeMismatch != 1 {
		t.Errorf("Metrics.EnrollRejectedByReason[stake_mismatch] = %d, want 1", rejStakeMismatch)
	}
}

// -----------------------------------------------------------
// v2 deployment path: happy path
// -----------------------------------------------------------

func TestHandleEnrollmentOverview_HappyPath(t *testing.T) {
	withCleanEnrollmentWiring(t)
	d := newTestDashboard()

	lister := &fakeEnrollmentLister{
		page: enrollment.ListPage{
			Records: []enrollment.EnrollmentRecord{
				sampleEnrollmentRec("rig-001", "alice", "GPU-aaaa", 10_000_000_000, 0, 0),
				sampleEnrollmentRec("rig-002", "bob", "GPU-bbbb", 15_000_000_000, 0, 0),
				sampleEnrollmentRec("rig-003", "carol", "GPU-cccc", 8_000_000_000, 50_000, 100_000),
			},
			TotalMatches: 3,
			NextCursor:   "rig-003",
			HasMore:      false,
		},
	}
	api.SetEnrollmentLister(lister)

	req := httptest.NewRequest(http.MethodGet,
		"/api/mining/enrollment-overview?limit=10", nil)
	w := httptest.NewRecorder()
	d.handleEnrollmentOverview(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	var got dashboardEnrollmentOverviewView
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if !got.Available {
		t.Error("Available = false with lister wired; want true")
	}
	if len(got.Records) != 3 {
		t.Fatalf("len(Records) = %d, want 3", len(got.Records))
	}
	wantOrder := []string{"rig-001", "rig-002", "rig-003"}
	for i, rec := range got.Records {
		if rec.NodeID != wantOrder[i] {
			t.Errorf("Records[%d].NodeID = %q, want %q (must preserve lister order)",
				i, rec.NodeID, wantOrder[i])
		}
	}
	// Phase derivation passes through api.EnrollmentViewFromRecord.
	if got.Records[0].Phase != "active" {
		t.Errorf("rig-001 Phase = %q, want active", got.Records[0].Phase)
	}
	if got.Records[2].Phase != "pending_unbond" {
		t.Errorf("rig-003 Phase = %q, want pending_unbond (revoked + stake remaining)", got.Records[2].Phase)
	}
	if got.TotalMatches != 3 {
		t.Errorf("TotalMatches = %d, want 3", got.TotalMatches)
	}
	if got.NextCursor != "rig-003" {
		t.Errorf("NextCursor = %q, want rig-003", got.NextCursor)
	}
	if got.HasMore {
		t.Errorf("HasMore = true, want false")
	}
	if got.Limit != 10 {
		t.Errorf("Limit echo = %d, want 10", got.Limit)
	}
	if got.Filters != nil {
		t.Errorf("Filters block = %+v on bare call; want nil (omitempty)", got.Filters)
	}

	if lister.lastOpts.Limit != 10 {
		t.Errorf("lister.lastOpts.Limit = %d, want 10", lister.lastOpts.Limit)
	}
	if lister.lastOpts.Phase != enrollment.PhaseAny {
		t.Errorf("lister.lastOpts.Phase = %q, want PhaseAny on bare call", lister.lastOpts.Phase)
	}
	if lister.lastOpts.Cursor != "" {
		t.Errorf("lister.lastOpts.Cursor = %q, want \"\" on bare call", lister.lastOpts.Cursor)
	}
}

// -----------------------------------------------------------
// Closed-enum filter validation
// -----------------------------------------------------------

func TestHandleEnrollmentOverview_BogusPhase_400(t *testing.T) {
	withCleanEnrollmentWiring(t)
	d := newTestDashboard()
	api.SetEnrollmentLister(&fakeEnrollmentLister{})

	// Includes a SQL-injection-style payload to confirm the
	// closed-enum check rejects creative inputs as cleanly
	// as it rejects typos. Wrapped in url.QueryEscape so
	// httptest.NewRequest can parse the request line — the
	// payload is decoded back to its original form before
	// the handler sees it.
	for _, raw := range []string{"applied", "ACTIVE", "almost-revoked", "active' OR 1=1 --"} {
		req := httptest.NewRequest(http.MethodGet,
			"/api/mining/enrollment-overview?phase="+url.QueryEscape(raw), nil)
		w := httptest.NewRecorder()
		d.handleEnrollmentOverview(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("phase=%q: status = %d, want %d", raw, w.Code, http.StatusBadRequest)
		}
		if !strings.Contains(w.Body.String(), "phase must be one of") {
			t.Errorf("phase=%q: body = %q; want allowlist hint", raw, w.Body.String())
		}
	}
}

func TestHandleEnrollmentOverview_OversizedCursor_400(t *testing.T) {
	withCleanEnrollmentWiring(t)
	d := newTestDashboard()
	api.SetEnrollmentLister(&fakeEnrollmentLister{})

	cursor := strings.Repeat("a", enrollment.MaxNodeIDLen+1)
	req := httptest.NewRequest(http.MethodGet,
		"/api/mining/enrollment-overview?cursor="+cursor, nil)
	w := httptest.NewRecorder()
	d.handleEnrollmentOverview(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("oversized cursor: status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// -----------------------------------------------------------
// Filter passthrough + echo
// -----------------------------------------------------------

func TestHandleEnrollmentOverview_FilterPassthrough(t *testing.T) {
	withCleanEnrollmentWiring(t)
	d := newTestDashboard()

	lister := &fakeEnrollmentLister{
		page: enrollment.ListPage{},
	}
	api.SetEnrollmentLister(lister)

	req := httptest.NewRequest(http.MethodGet,
		"/api/mining/enrollment-overview?phase=pending_unbond&cursor=rig-100&limit=25", nil)
	w := httptest.NewRecorder()
	d.handleEnrollmentOverview(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if lister.lastOpts.Phase != enrollment.PhasePendingUnbond {
		t.Errorf("lister.lastOpts.Phase = %q, want pending_unbond", lister.lastOpts.Phase)
	}
	if lister.lastOpts.Cursor != "rig-100" {
		t.Errorf("lister.lastOpts.Cursor = %q, want rig-100", lister.lastOpts.Cursor)
	}
	if lister.lastOpts.Limit != 25 {
		t.Errorf("lister.lastOpts.Limit = %d, want 25", lister.lastOpts.Limit)
	}

	var got dashboardEnrollmentOverviewView
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Filters == nil {
		t.Fatal("Filters block missing on filtered call")
	}
	if got.Filters.Phase != "pending_unbond" || got.Filters.Cursor != "rig-100" {
		t.Errorf("Filters echo = %+v; want {pending_unbond, rig-100}", *got.Filters)
	}
}

func TestHandleEnrollmentOverview_AllPhasesAccepted(t *testing.T) {
	withCleanEnrollmentWiring(t)
	d := newTestDashboard()
	lister := &fakeEnrollmentLister{}
	api.SetEnrollmentLister(lister)

	for _, phase := range []string{"active", "pending_unbond", "revoked"} {
		req := httptest.NewRequest(http.MethodGet,
			"/api/mining/enrollment-overview?phase="+phase, nil)
		w := httptest.NewRecorder()
		d.handleEnrollmentOverview(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("phase=%q: status = %d, want %d; body=%s", phase, w.Code, http.StatusOK, w.Body.String())
		}
		if string(lister.lastOpts.Phase) != phase {
			t.Errorf("phase=%q: lister.lastOpts.Phase = %q", phase, lister.lastOpts.Phase)
		}
	}
}
