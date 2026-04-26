package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mining/enrollment"
)

// fakeRegistry implements EnrollmentRegistry with explicit
// per-test fixtures. Decoupled from *InMemoryState so tests
// can express edge cases (storage-layer error, etc.) the real
// in-memory store never produces.
type fakeRegistry struct {
	records map[string]*enrollment.EnrollmentRecord
	err     error
}

func (f *fakeRegistry) Lookup(nodeID string) (*enrollment.EnrollmentRecord, error) {
	if f.err != nil {
		return nil, f.err
	}
	rec, ok := f.records[nodeID]
	if !ok {
		return nil, nil
	}
	cp := *rec
	return &cp, nil
}

func newFakeRegistryWithActive(t *testing.T) *fakeRegistry {
	t.Helper()
	return &fakeRegistry{records: map[string]*enrollment.EnrollmentRecord{
		"rig-77": {
			NodeID:           "rig-77",
			Owner:            "alice",
			GPUUUID:          "GPU-12345678-1234-1234-1234-123456789abc",
			HMACKey:          []byte("hot-secret-32-bytes-............."),
			StakeDust:        10 * 100_000_000,
			EnrolledAtHeight: 42,
		},
	}}
}

func newFakeRegistryWithUnbonding(t *testing.T) *fakeRegistry {
	t.Helper()
	return &fakeRegistry{records: map[string]*enrollment.EnrollmentRecord{
		"rig-77": {
			NodeID:                "rig-77",
			Owner:                 "alice",
			GPUUUID:               "GPU-12345678-1234-1234-1234-123456789abc",
			HMACKey:               []byte("hot-secret-32-bytes-............."),
			StakeDust:             10 * 100_000_000,
			EnrolledAtHeight:      42,
			RevokedAtHeight:       100,
			UnbondMaturesAtHeight: 100 + 201_600,
		},
	}}
}

func newFakeRegistryWithDrained(t *testing.T) *fakeRegistry {
	t.Helper()
	return &fakeRegistry{records: map[string]*enrollment.EnrollmentRecord{
		"rig-77": {
			NodeID:                "rig-77",
			Owner:                 "alice",
			GPUUUID:               "GPU-12345678-1234-1234-1234-123456789abc",
			HMACKey:               nil,
			StakeDust:             0,
			EnrolledAtHeight:      42,
			RevokedAtHeight:       100,
			UnbondMaturesAtHeight: 100 + 201_600,
		},
	}}
}

func TestEnrollmentQuery_HappyPath_Active(t *testing.T) {
	SetEnrollmentRegistry(newFakeRegistryWithActive(t))
	t.Cleanup(func() { SetEnrollmentRegistry(nil) })

	h := &Handlers{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/mining/enrollment/rig-77", nil)
	rec := httptest.NewRecorder()
	h.EnrollmentQueryHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var view EnrollmentRecordView
	if err := json.NewDecoder(rec.Body).Decode(&view); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if view.NodeID != "rig-77" || view.Phase != "active" || !view.Slashable {
		t.Errorf("view: %+v", view)
	}
	if view.RevokedAtHeight != 0 || view.UnbondMaturesAtHeight != 0 {
		t.Errorf("active record leaked unbond fields: %+v", view)
	}
}

func TestEnrollmentQuery_HappyPath_PendingUnbond(t *testing.T) {
	SetEnrollmentRegistry(newFakeRegistryWithUnbonding(t))
	t.Cleanup(func() { SetEnrollmentRegistry(nil) })

	h := &Handlers{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/mining/enrollment/rig-77", nil)
	rec := httptest.NewRecorder()
	h.EnrollmentQueryHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var view EnrollmentRecordView
	if err := json.NewDecoder(rec.Body).Decode(&view); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if view.Phase != "pending_unbond" {
		t.Errorf("phase: got %q, want pending_unbond", view.Phase)
	}
	if !view.Slashable {
		t.Errorf("pending_unbond with stake should remain slashable")
	}
	if view.UnbondMaturesAtHeight == 0 {
		t.Errorf("unbond_matures_at_height not surfaced for pending_unbond")
	}
}

func TestEnrollmentQuery_HappyPath_Revoked(t *testing.T) {
	SetEnrollmentRegistry(newFakeRegistryWithDrained(t))
	t.Cleanup(func() { SetEnrollmentRegistry(nil) })

	h := &Handlers{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/mining/enrollment/rig-77", nil)
	rec := httptest.NewRecorder()
	h.EnrollmentQueryHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rec.Code)
	}
	var view EnrollmentRecordView
	if err := json.NewDecoder(rec.Body).Decode(&view); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if view.Phase != "revoked" {
		t.Errorf("phase: got %q, want revoked", view.Phase)
	}
	if view.Slashable {
		t.Errorf("drained record must not be slashable")
	}
}

// TestEnrollmentQuery_OmitsHMACKey is the least-privilege contract:
// no matter what the registry returns, the wire response must not
// carry hmac_key. A bug here is a credential leak.
func TestEnrollmentQuery_OmitsHMACKey(t *testing.T) {
	SetEnrollmentRegistry(newFakeRegistryWithActive(t))
	t.Cleanup(func() { SetEnrollmentRegistry(nil) })

	h := &Handlers{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/mining/enrollment/rig-77", nil)
	rec := httptest.NewRecorder()
	h.EnrollmentQueryHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(strings.ToLower(body), "hmac") {
		t.Errorf("response body must not mention hmac: %s", body)
	}
	if strings.Contains(body, "hot-secret") {
		t.Errorf("response body leaked HMAC key bytes: %s", body)
	}
}

func TestEnrollmentQuery_NotFound(t *testing.T) {
	SetEnrollmentRegistry(&fakeRegistry{records: nil})
	t.Cleanup(func() { SetEnrollmentRegistry(nil) })

	h := &Handlers{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/mining/enrollment/missing-rig", nil)
	rec := httptest.NewRecorder()
	h.EnrollmentQueryHandler(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", rec.Code)
	}
}

func TestEnrollmentQuery_RejectsWrongMethod(t *testing.T) {
	SetEnrollmentRegistry(newFakeRegistryWithActive(t))
	t.Cleanup(func() { SetEnrollmentRegistry(nil) })

	h := &Handlers{}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/mining/enrollment/rig-77", nil)
	rec := httptest.NewRecorder()
	h.EnrollmentQueryHandler(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status: got %d, want 405", rec.Code)
	}
}

func TestEnrollmentQuery_NoRegistry_Returns503(t *testing.T) {
	SetEnrollmentRegistry(nil)
	h := &Handlers{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/mining/enrollment/rig-77", nil)
	rec := httptest.NewRecorder()
	h.EnrollmentQueryHandler(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status: got %d, want 503", rec.Code)
	}
}

func TestEnrollmentQuery_RejectsEmptyNodeID(t *testing.T) {
	SetEnrollmentRegistry(newFakeRegistryWithActive(t))
	t.Cleanup(func() { SetEnrollmentRegistry(nil) })

	h := &Handlers{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/mining/enrollment/", nil)
	rec := httptest.NewRecorder()
	h.EnrollmentQueryHandler(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestEnrollmentQuery_RejectsNestedPath(t *testing.T) {
	SetEnrollmentRegistry(newFakeRegistryWithActive(t))
	t.Cleanup(func() { SetEnrollmentRegistry(nil) })

	h := &Handlers{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/mining/enrollment/rig-77/extra", nil)
	rec := httptest.NewRecorder()
	h.EnrollmentQueryHandler(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
}

func TestEnrollmentQuery_RejectsOversizedNodeID(t *testing.T) {
	SetEnrollmentRegistry(newFakeRegistryWithActive(t))
	t.Cleanup(func() { SetEnrollmentRegistry(nil) })

	h := &Handlers{}
	long := strings.Repeat("x", enrollment.MaxNodeIDLen+1)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/mining/enrollment/"+long, nil)
	rec := httptest.NewRecorder()
	h.EnrollmentQueryHandler(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
}

func TestEnrollmentQuery_StorageError_Returns500(t *testing.T) {
	SetEnrollmentRegistry(&fakeRegistry{err: errors.New("disk on fire")})
	t.Cleanup(func() { SetEnrollmentRegistry(nil) })

	h := &Handlers{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/mining/enrollment/rig-77", nil)
	rec := httptest.NewRecorder()
	h.EnrollmentQueryHandler(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want 500; body=%s", rec.Code, rec.Body.String())
	}
}

func TestEnrollmentQuery_TrailingSlashTolerated(t *testing.T) {
	SetEnrollmentRegistry(newFakeRegistryWithActive(t))
	t.Cleanup(func() { SetEnrollmentRegistry(nil) })

	h := &Handlers{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/mining/enrollment/rig-77/", nil)
	rec := httptest.NewRecorder()
	h.EnrollmentQueryHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("trailing-slash should resolve: got %d, want 200", rec.Code)
	}
}
