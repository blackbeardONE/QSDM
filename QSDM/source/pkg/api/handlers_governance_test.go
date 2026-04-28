package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeGovernanceProvider implements GovernanceParamsProvider
// with a per-test fixture. Locked because some tests Set
// concurrently; the assumption is that reads inside a test
// are sequential.
type fakeGovernanceProvider struct {
	mu   sync.RWMutex
	view GovernanceParamsView
}

func (f *fakeGovernanceProvider) SnapshotGovernanceParams() GovernanceParamsView {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := GovernanceParamsView{
		Active:            map[string]uint64{},
		Pending:           append([]GovernancePendingView(nil), f.view.Pending...),
		Registry:          append([]GovernanceRegistryView(nil), f.view.Registry...),
		Authorities:       append([]string(nil), f.view.Authorities...),
		GovernanceEnabled: f.view.GovernanceEnabled,
	}
	for k, v := range f.view.Active {
		out.Active[k] = v
	}
	return out
}

func newFakeGovernanceProvider() *fakeGovernanceProvider {
	return &fakeGovernanceProvider{
		view: GovernanceParamsView{
			Active: map[string]uint64{
				"reward_bps":                500,
				"auto_revoke_min_stake_dust": 100_000_000,
			},
			Pending: []GovernancePendingView{
				{
					Param:             "reward_bps",
					Value:             750,
					EffectiveHeight:   1_000,
					SubmittedAtHeight: 800,
					Authority:         "alice-gov-key",
					Memo:              "post-mortem #14",
				},
			},
			Registry: []GovernanceRegistryView{
				{Name: "reward_bps", Description: "slasher reward share", MinValue: 0, MaxValue: 5000, DefaultValue: 500, Unit: "bps"},
				{Name: "auto_revoke_min_stake_dust", Description: "auto-revoke threshold", MinValue: 0, MaxValue: 1_000_000_000, DefaultValue: 100_000_000, Unit: "dust"},
			},
			Authorities:       []string{"alice-gov-key", "bob-gov-key"},
			GovernanceEnabled: true,
		},
	}
}

// resetGovernanceProvider clears the global so tests don't
// leak state into each other.
func resetGovernanceProvider(t *testing.T) {
	t.Helper()
	SetGovernanceProvider(nil)
	t.Cleanup(func() { SetGovernanceProvider(nil) })
}

// -----------------------------------------------------------------------------
// /api/v1/governance/params (full snapshot)
// -----------------------------------------------------------------------------

func TestGovernanceParamsHandler_503WhenProviderNotInstalled(t *testing.T) {
	resetGovernanceProvider(t)

	h := &Handlers{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/governance/params", nil)
	rec := httptest.NewRecorder()
	h.GovernanceParamsHandler(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503 when provider unset, got %d (body=%q)",
			rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "governance") {
		t.Errorf("503 body should mention governance, got %q",
			rec.Body.String())
	}
}

func TestGovernanceParamsHandler_HappyPath(t *testing.T) {
	resetGovernanceProvider(t)
	SetGovernanceProvider(newFakeGovernanceProvider())

	h := &Handlers{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/governance/params", nil)
	rec := httptest.NewRecorder()
	h.GovernanceParamsHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (body=%q)", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	var out GovernanceParamsView
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !out.GovernanceEnabled {
		t.Error("governance_enabled should be true")
	}
	if got := out.Active["reward_bps"]; got != 500 {
		t.Errorf("Active[reward_bps]=%d, want 500", got)
	}
	if len(out.Pending) != 1 || out.Pending[0].Param != "reward_bps" {
		t.Errorf("Pending = %+v, want one entry for reward_bps", out.Pending)
	}
	if len(out.Authorities) != 2 {
		t.Errorf("Authorities len=%d, want 2", len(out.Authorities))
	}
	// Authorities deterministically sorted.
	if out.Authorities[0] >= out.Authorities[1] {
		t.Errorf("Authorities not sorted: %v", out.Authorities)
	}
	// Registry deterministically sorted by name ASC.
	if len(out.Registry) != 2 ||
		out.Registry[0].Name != "auto_revoke_min_stake_dust" ||
		out.Registry[1].Name != "reward_bps" {
		t.Errorf("Registry not sorted by name: %+v", out.Registry)
	}
}

func TestGovernanceParamsHandler_DisabledPostureRendersExplicitFalse(t *testing.T) {
	resetGovernanceProvider(t)
	prov := &fakeGovernanceProvider{
		view: GovernanceParamsView{
			Active:            map[string]uint64{"reward_bps": 500},
			GovernanceEnabled: false,
		},
	}
	SetGovernanceProvider(prov)

	h := &Handlers{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/governance/params", nil)
	rec := httptest.NewRecorder()
	h.GovernanceParamsHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	var out GovernanceParamsView
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.GovernanceEnabled {
		t.Error("governance_enabled should be false")
	}
	if out.Pending == nil || out.Registry == nil || out.Authorities == nil {
		t.Errorf("nil slices should be normalised to []: pending=%v registry=%v authorities=%v",
			out.Pending, out.Registry, out.Authorities)
	}
}

func TestGovernanceParamsHandler_RejectsNonGet(t *testing.T) {
	resetGovernanceProvider(t)
	SetGovernanceProvider(newFakeGovernanceProvider())

	h := &Handlers{}
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		req := httptest.NewRequest(m, "/api/v1/governance/params", nil)
		rec := httptest.NewRecorder()
		h.GovernanceParamsHandler(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("method=%s: want 405, got %d", m, rec.Code)
		}
		if got := rec.Header().Get("Allow"); got != http.MethodGet {
			t.Errorf("method=%s: Allow=%q, want GET", m, got)
		}
	}
}

// -----------------------------------------------------------------------------
// /api/v1/governance/params/{name} (single-param view)
// -----------------------------------------------------------------------------

func TestGovernanceParamHandler_503WhenProviderNotInstalled(t *testing.T) {
	resetGovernanceProvider(t)

	h := &Handlers{}
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/governance/params/reward_bps", nil)
	rec := httptest.NewRecorder()
	h.GovernanceParamHandler(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d", rec.Code)
	}
}

func TestGovernanceParamHandler_HappyPathWithPending(t *testing.T) {
	resetGovernanceProvider(t)
	SetGovernanceProvider(newFakeGovernanceProvider())

	h := &Handlers{}
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/governance/params/reward_bps", nil)
	rec := httptest.NewRecorder()
	h.GovernanceParamHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (body=%q)", rec.Code, rec.Body.String())
	}
	var out GovernanceParamView
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Name != "reward_bps" {
		t.Errorf("Name=%q, want reward_bps", out.Name)
	}
	if out.ActiveValue != 500 {
		t.Errorf("ActiveValue=%d, want 500", out.ActiveValue)
	}
	if out.Pending == nil || out.Pending.Value != 750 {
		t.Errorf("Pending=%+v, want value=750", out.Pending)
	}
	if out.RegistryInfo.Name != "reward_bps" {
		t.Errorf("RegistryInfo.Name=%q, want reward_bps", out.RegistryInfo.Name)
	}
}

func TestGovernanceParamHandler_HappyPathNoPending(t *testing.T) {
	resetGovernanceProvider(t)
	SetGovernanceProvider(newFakeGovernanceProvider())

	h := &Handlers{}
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/governance/params/auto_revoke_min_stake_dust", nil)
	rec := httptest.NewRecorder()
	h.GovernanceParamHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	var out GovernanceParamView
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.ActiveValue != 100_000_000 {
		t.Errorf("ActiveValue=%d", out.ActiveValue)
	}
	if out.Pending != nil {
		t.Errorf("Pending should be nil, got %+v", out.Pending)
	}
}

func TestGovernanceParamHandler_404OnUnknownParam(t *testing.T) {
	resetGovernanceProvider(t)
	SetGovernanceProvider(newFakeGovernanceProvider())

	h := &Handlers{}
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/governance/params/does_not_exist", nil)
	rec := httptest.NewRecorder()
	h.GovernanceParamHandler(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", rec.Code)
	}
}

func TestGovernanceParamHandler_400OnEmptyName(t *testing.T) {
	resetGovernanceProvider(t)
	SetGovernanceProvider(newFakeGovernanceProvider())

	h := &Handlers{}
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/governance/params/", nil)
	rec := httptest.NewRecorder()
	h.GovernanceParamHandler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rec.Code)
	}
}

func TestGovernanceParamHandler_400OnTooLongName(t *testing.T) {
	resetGovernanceProvider(t)
	SetGovernanceProvider(newFakeGovernanceProvider())

	h := &Handlers{}
	huge := strings.Repeat("x", 200)
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/governance/params/"+huge, nil)
	rec := httptest.NewRecorder()
	h.GovernanceParamHandler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rec.Code)
	}
}

func TestGovernanceParamHandler_RejectsNonGet(t *testing.T) {
	resetGovernanceProvider(t)
	SetGovernanceProvider(newFakeGovernanceProvider())

	h := &Handlers{}
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		req := httptest.NewRequest(m,
			"/api/v1/governance/params/reward_bps", nil)
		rec := httptest.NewRecorder()
		h.GovernanceParamHandler(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("method=%s: want 405, got %d", m, rec.Code)
		}
	}
}
