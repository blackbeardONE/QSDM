package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mining/enrollment"
)

// fakeLister implements EnrollmentLister with a fixed page
// the handler tests can dictate per case. Decoupled from
// *enrollment.InMemoryState so phase semantics, total counts,
// and HasMore can be exercised independently of the registry's
// actual filter logic (which is covered by
// pkg/mining/enrollment/registry_list_test.go).
type fakeLister struct {
	page enrollment.ListPage
	last enrollment.ListOptions
}

func (f *fakeLister) List(opts enrollment.ListOptions) enrollment.ListPage {
	f.last = opts
	return f.page
}

func newFakeListerWithRecords() *fakeLister {
	return &fakeLister{page: enrollment.ListPage{
		Records: []enrollment.EnrollmentRecord{
			{
				NodeID:           "rig-active-01",
				Owner:            "alice",
				GPUUUID:          "GPU-AAAAAAAA-0001",
				HMACKey:          []byte("hot-secret-32-bytes-............."),
				StakeDust:        10_000_000_000,
				EnrolledAtHeight: 10,
			},
			{
				NodeID:           "rig-active-02",
				Owner:            "bob",
				GPUUUID:          "GPU-AAAAAAAA-0002",
				HMACKey:          []byte("hot-secret-32-bytes-............."),
				StakeDust:        12_000_000_000,
				EnrolledAtHeight: 12,
			},
		},
		NextCursor:   "rig-active-02",
		HasMore:      true,
		TotalMatches: 5,
	}}
}

func TestEnrollmentList_HappyPath(t *testing.T) {
	SetEnrollmentLister(newFakeListerWithRecords())
	t.Cleanup(func() { SetEnrollmentLister(nil) })

	h := &Handlers{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/mining/enrollments", nil)
	rec := httptest.NewRecorder()
	h.EnrollmentListHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var view EnrollmentListPageView
	if err := json.NewDecoder(rec.Body).Decode(&view); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(view.Records) != 2 {
		t.Errorf("records len: got %d, want 2", len(view.Records))
	}
	if view.NextCursor != "rig-active-02" {
		t.Errorf("next_cursor: got %q, want %q", view.NextCursor, "rig-active-02")
	}
	if !view.HasMore {
		t.Error("has_more should be true")
	}
	if view.TotalMatches != 5 {
		t.Errorf("total_matches: got %d, want 5", view.TotalMatches)
	}
}

func TestEnrollmentList_OmitsHMACKey(t *testing.T) {
	SetEnrollmentLister(newFakeListerWithRecords())
	t.Cleanup(func() { SetEnrollmentLister(nil) })

	h := &Handlers{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/mining/enrollments", nil)
	rec := httptest.NewRecorder()
	h.EnrollmentListHandler(rec, req)

	body := rec.Body.String()
	if strings.Contains(strings.ToLower(body), "hmac") {
		t.Errorf("response body must not mention hmac: %s", body)
	}
	if strings.Contains(body, "hot-secret") {
		t.Errorf("response body leaked HMAC key bytes: %s", body)
	}
}

func TestEnrollmentList_PassesQueryParamsThroughToLister(t *testing.T) {
	f := newFakeListerWithRecords()
	SetEnrollmentLister(f)
	t.Cleanup(func() { SetEnrollmentLister(nil) })

	h := &Handlers{}
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/mining/enrollments?cursor=rig-active-01&limit=42&phase=active", nil)
	rec := httptest.NewRecorder()
	h.EnrollmentListHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rec.Code)
	}
	if f.last.Cursor != "rig-active-01" {
		t.Errorf("Cursor not passed to lister: got %q", f.last.Cursor)
	}
	if f.last.Limit != 42 {
		t.Errorf("Limit not passed to lister: got %d", f.last.Limit)
	}
	if f.last.Phase != enrollment.PhaseActive {
		t.Errorf("Phase not passed to lister: got %q", f.last.Phase)
	}

	var view EnrollmentListPageView
	if err := json.NewDecoder(rec.Body).Decode(&view); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if view.Phase != "active" {
		t.Errorf("view.Phase echo: got %q, want active", view.Phase)
	}
}

func TestEnrollmentList_RejectsUnknownPhase(t *testing.T) {
	SetEnrollmentLister(newFakeListerWithRecords())
	t.Cleanup(func() { SetEnrollmentLister(nil) })

	h := &Handlers{}
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/mining/enrollments?phase=garbage", nil)
	rec := httptest.NewRecorder()
	h.EnrollmentListHandler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
}

func TestEnrollmentList_RejectsNegativeLimit(t *testing.T) {
	SetEnrollmentLister(newFakeListerWithRecords())
	t.Cleanup(func() { SetEnrollmentLister(nil) })

	h := &Handlers{}
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/mining/enrollments?limit=-3", nil)
	rec := httptest.NewRecorder()
	h.EnrollmentListHandler(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("negative limit: got %d, want 400", rec.Code)
	}
}

func TestEnrollmentList_RejectsNonNumericLimit(t *testing.T) {
	SetEnrollmentLister(newFakeListerWithRecords())
	t.Cleanup(func() { SetEnrollmentLister(nil) })

	h := &Handlers{}
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/mining/enrollments?limit=banana", nil)
	rec := httptest.NewRecorder()
	h.EnrollmentListHandler(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("non-numeric limit: got %d, want 400", rec.Code)
	}
}

func TestEnrollmentList_RejectsCursorTooLong(t *testing.T) {
	SetEnrollmentLister(newFakeListerWithRecords())
	t.Cleanup(func() { SetEnrollmentLister(nil) })

	huge := strings.Repeat("a", enrollment.MaxNodeIDLen+1)
	h := &Handlers{}
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/mining/enrollments?cursor="+huge, nil)
	rec := httptest.NewRecorder()
	h.EnrollmentListHandler(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("oversized cursor: got %d, want 400", rec.Code)
	}
}

func TestEnrollmentList_NotConfiguredReturns503(t *testing.T) {
	SetEnrollmentLister(nil)
	h := &Handlers{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/mining/enrollments", nil)
	rec := httptest.NewRecorder()
	h.EnrollmentListHandler(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status: got %d, want 503", rec.Code)
	}
}

func TestEnrollmentList_RejectsWrongMethod(t *testing.T) {
	SetEnrollmentLister(newFakeListerWithRecords())
	t.Cleanup(func() { SetEnrollmentLister(nil) })

	h := &Handlers{}
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		req := httptest.NewRequest(method, "/api/v1/mining/enrollments", nil)
		rec := httptest.NewRecorder()
		h.EnrollmentListHandler(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: status got %d, want 405", method, rec.Code)
		}
	}
}

func TestEnrollmentList_EmptyResultEncodesEmptyArray(t *testing.T) {
	// Empty Records must serialise as "[]", not "null". A
	// "null" array would force every client to handle two
	// shapes for "no records" — clients would pile workarounds
	// at the wire boundary. Pre-allocating the slice in the
	// handler is what guarantees the marshaller emits "[]".
	SetEnrollmentLister(&fakeLister{page: enrollment.ListPage{
		Records:      nil, // intentionally nil
		HasMore:      false,
		TotalMatches: 0,
	}})
	t.Cleanup(func() { SetEnrollmentLister(nil) })

	h := &Handlers{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/mining/enrollments", nil)
	rec := httptest.NewRecorder()
	h.EnrollmentListHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"records":[]`) {
		t.Errorf("expected empty records as [], got: %s", body)
	}
	if strings.Contains(body, `"records":null`) {
		t.Errorf("records serialised as null: %s", body)
	}
}

func TestEnrollmentList_DerivedPhaseInRecords(t *testing.T) {
	// Confirm the per-record phase derivation flows through.
	// The fixture has one active record, one revoked-with-stake.
	SetEnrollmentLister(&fakeLister{page: enrollment.ListPage{
		Records: []enrollment.EnrollmentRecord{
			{NodeID: "rig-1", StakeDust: 10_000_000_000, EnrolledAtHeight: 1},
			{
				NodeID:                "rig-2",
				StakeDust:             5_000_000_000,
				EnrolledAtHeight:      1,
				RevokedAtHeight:       100,
				UnbondMaturesAtHeight: 100 + 201600,
			},
		},
	}})
	t.Cleanup(func() { SetEnrollmentLister(nil) })

	h := &Handlers{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/mining/enrollments", nil)
	rec := httptest.NewRecorder()
	h.EnrollmentListHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rec.Code)
	}
	var view EnrollmentListPageView
	if err := json.NewDecoder(rec.Body).Decode(&view); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(view.Records) != 2 {
		t.Fatalf("len: got %d, want 2", len(view.Records))
	}
	if view.Records[0].Phase != "active" {
		t.Errorf("rec[0].phase: got %q, want active", view.Records[0].Phase)
	}
	if view.Records[1].Phase != "pending_unbond" {
		t.Errorf("rec[1].phase: got %q, want pending_unbond", view.Records[1].Phase)
	}
}
