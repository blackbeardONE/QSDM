package dashboard

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/api"
	"github.com/blackbeardONE/QSDM/pkg/monitoring"
)

func TestLoginPageKeepsAccessTokenServerSide(t *testing.T) {
	raw, err := fs.ReadFile(staticFiles, "static/login.js")
	if err != nil {
		t.Fatalf("read embedded login script: %v", err)
	}
	script := string(raw)
	if !strings.Contains(script, "fetch('/api/auth/login'") {
		t.Fatal("login script must use the dashboard's server-side login endpoint")
	}
	if strings.Contains(script, "fetch('/api/v1/auth/login'") {
		t.Fatal("login script must not request an API access token directly")
	}
	if strings.Contains(script, "data.access_token") {
		t.Fatal("login script must not expose the API access token to page JavaScript")
	}
}

func TestLoginPageOffersPersistentDashboardRegistration(t *testing.T) {
	raw, err := fs.ReadFile(staticFiles, "static/login.js")
	if err != nil {
		t.Fatalf("read embedded login script: %v", err)
	}
	script := string(raw)
	if !strings.Contains(script, "fetch('/api/v1/auth/register'") {
		t.Fatal("login script must expose the validator's persistent registration endpoint")
	}
	if !strings.Contains(script, "Create dashboard login") {
		t.Fatal("login script must give first-time operators a clear registration path")
	}
	if !strings.Contains(script, "passwords do not match") {
		t.Fatal("registration must confirm the dashboard password before submission")
	}
	if !strings.Contains(script, "registrationPasswordError") || !strings.Contains(script, "at least 12 characters") {
		t.Fatal("registration must reject invalid passwords before consuming an API rate-limit attempt")
	}
	if !strings.Contains(script, "Retry-After") || !strings.Contains(script, "Try again in") {
		t.Fatal("login must explain and honor the server's rate-limit retry window")
	}
}

// TestAuthProxy_stripsBrowserOriginBeforeForwarding reproduces the login
// failure operators hit on a stock local stack:
//
//	dashboard on :8081, API on :8080, QSDM_CORS_ALLOWED_ORIGINS unset
//	-> POST /api/v1/auth/login returns 403 "origin not allowed"
//
// The dashboard reverse-proxies /api/v1/auth/* to the API. Go's
// NewSingleHostReverseProxy forwards every inbound header, so the browser's
// `Origin: http://localhost:8081` reached the API, whose CORS middleware
// applied a *browser* cross-origin policy to what is really a
// server-to-server call. With an empty allowlist (the default) that policy
// rejects every Origin it sees, so login could never succeed until the
// operator happened to add the dashboard's own origin to the API's CORS list.
//
// The proxy now deletes Origin, which is what a non-browser client sends and
// what CORSMiddleware passes through untouched.
func TestAuthProxy_stripsBrowserOriginBeforeForwarding(t *testing.T) {
	var gotOrigin string
	var sawRequest bool

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawRequest = true
		gotOrigin = r.Header.Get("Origin")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer backend.Close()

	metrics := monitoring.GetMetrics()
	d := NewDashboard(
		metrics, monitoring.NewHealthChecker(metrics), "0", false,
		DashboardNvidiaLock{}, "", "", false, backend.URL, nil,
	)
	handler, err := d.buildHandler()
	if err != nil {
		t.Fatalf("buildHandler: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login",
		strings.NewReader(`{"address":"a","password":"b"}`))
	req.Header.Set("Content-Type", "application/json")
	// Exactly what a browser sends for the dashboard's own login form.
	req.Header.Set("Origin", "http://localhost:8081")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !sawRequest {
		t.Fatal("expected the request to be proxied to the API backend")
	}
	if gotOrigin != "" {
		t.Fatalf("browser Origin must not be forwarded to the API backend, got %q", gotOrigin)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("proxied login should succeed, got %d: %s", rec.Code, rec.Body.String())
	}
}

// The API's CORS middleware treats an absent Origin as a non-browser client
// and passes it through. This pins the property the fix above relies on, so
// a future change to CORSMiddleware that starts rejecting Origin-less
// requests fails here rather than silently breaking dashboard login again.
func TestCORSMiddleware_allowsOriginlessRequests(t *testing.T) {
	reached := false
	h := api.CORSMiddleware(&api.CORSConfig{})(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			reached = true
			w.WriteHeader(http.StatusOK)
		}))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	// No Origin header at all — a server-to-server call.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if !reached {
		t.Fatal("a request with no Origin must pass through CORS")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 for an Origin-less request, got %d", rec.Code)
	}
}

// And the converse: with an empty allowlist a browser Origin IS rejected.
// That is the behaviour that made the un-stripped proxy fail, so it is worth
// pinning explicitly rather than leaving implicit.
func TestCORSMiddleware_emptyAllowlistRejectsAnyOrigin(t *testing.T) {
	h := api.CORSMiddleware(&api.CORSConfig{})(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	req.Header.Set("Origin", "http://localhost:8081")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("empty allowlist should reject a browser Origin, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "origin not allowed") {
		t.Fatalf("expected the origin-not-allowed message, got %s", rec.Body.String())
	}
}

// TestDashboard_proxiesPublicV1Endpoints is the regression test for the
// header pills and Tokenomics panel rendering as em-dashes.
//
// dashboard.js fetches '/api/v1/status' and
// '/api/v1/trust/attestations/summary' with RELATIVE URLs, so they resolve
// against the dashboard origin (:8081), not the API (:8080). Neither path
// was registered on the dashboard mux, so both fell through to the
// auth-required catch-all and returned 302. Both call sites swallow errors
// in a .catch() that deliberately leaves the panels in their loading state,
// so the failure was invisible and the UI showed:
//
//	Network: —  Role: —  Coin: —
//	Tokenomics: supply —, block reward —, epoch —, halving —, cap —
//
// while :8080 was serving a complete tokenomics snapshot the whole time.
func TestDashboard_proxiesPublicV1Endpoints(t *testing.T) {
	var gotPaths []string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"network":"QSDM","node_role":"validator","tokenomics":{"cap_cell":"90000000.00000000"}}`))
	}))
	defer backend.Close()

	metrics := monitoring.GetMetrics()
	d := NewDashboard(
		metrics, monitoring.NewHealthChecker(metrics), "0", false,
		DashboardNvidiaLock{}, "", "", false, backend.URL, nil,
	)
	handler, err := d.buildHandler()
	if err != nil {
		t.Fatalf("buildHandler: %v", err)
	}

	for _, path := range []string{
		"/api/v1/status",
		"/api/v1/trust/attestations/summary",
	} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s must be proxied to the API, got %d (302 = fell through to the auth catch-all)",
				path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "tokenomics") {
			t.Fatalf("%s should return the backend payload, got %s", path, rec.Body.String())
		}
	}

	if len(gotPaths) != 2 {
		t.Fatalf("both endpoints should reach the backend, saw %v", gotPaths)
	}
}

// Non-GET must not be proxied through the public passthrough.
func TestDashboard_publicV1RejectsNonGet(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("a non-GET request must not reach the backend")
	}))
	defer backend.Close()

	metrics := monitoring.GetMetrics()
	d := NewDashboard(
		metrics, monitoring.NewHealthChecker(metrics), "0", false,
		DashboardNvidiaLock{}, "", "", false, backend.URL, nil,
	)
	handler, _ := d.buildHandler()

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/status", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("want 405 for POST, got %d", rec.Code)
	}
}
