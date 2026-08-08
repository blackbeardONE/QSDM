package dashboard

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/api"
	"github.com/blackbeardONE/QSDM/pkg/monitoring"
)

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
