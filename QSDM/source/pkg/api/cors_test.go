package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func corsTestHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
}

func TestCORS_PassThroughNoOriginHeader(t *testing.T) {
	cfg := &CORSConfig{AllowedOrigins: []string{"https://allowed.example"}}
	h := CORSMiddleware(cfg)(corsTestHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("server-to-server request should pass through, got %d", w.Code)
	}
}

func TestCORS_DenyDisallowedOrigin(t *testing.T) {
	cfg := &CORSConfig{AllowedOrigins: []string{"https://allowed.example"}}
	h := CORSMiddleware(cfg)(corsTestHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	req.Header.Set("Origin", "https://evil.example")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("denied origin expected 403, got %d", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("denied origin must not echo ACAO, got %q", got)
	}
}

func TestCORS_AllowExactOrigin(t *testing.T) {
	cfg := &CORSConfig{AllowedOrigins: []string{"https://allowed.example"}}
	h := CORSMiddleware(cfg)(corsTestHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	req.Header.Set("Origin", "https://allowed.example")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("allowed origin expected 200, got %d", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://allowed.example" {
		t.Fatalf("ACAO=%q", got)
	}
	if got := w.Header().Get("Vary"); got != "Origin" {
		t.Errorf("expected Vary: Origin, got %q", got)
	}
}

func TestCORS_PreflightSucceeds(t *testing.T) {
	cfg := &CORSConfig{
		AllowedOrigins:   []string{"https://allowed.example"},
		AllowCredentials: true,
	}
	h := CORSMiddleware(cfg)(corsTestHandler())

	req := httptest.NewRequest(http.MethodOptions, "/api/v1/wallet/send", nil)
	req.Header.Set("Origin", "https://allowed.example")
	req.Header.Set("Access-Control-Request-Method", "POST")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("preflight expected 204, got %d", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Methods"); got == "" {
		t.Errorf("missing Access-Control-Allow-Methods")
	}
	if got := w.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("expected ACAC=true with credentials, got %q", got)
	}
}

func TestCORS_WildcardWithoutCredentials(t *testing.T) {
	cfg := &CORSConfig{AllowedOrigins: []string{"*"}}
	h := CORSMiddleware(cfg)(corsTestHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	req.Header.Set("Origin", "https://anything.example")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("wildcard expected 200, got %d", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("ACAO=%q", got)
	}
}

func TestCORS_WildcardWithCredentialsFallsThroughToAllowlist(t *testing.T) {
	// "*" + credentials → spec disallows the combination. Implementation
	// MUST refuse the request unless the origin is on the explicit list.
	cfg := &CORSConfig{
		AllowedOrigins:   []string{"*"},
		AllowCredentials: true,
	}
	h := CORSMiddleware(cfg)(corsTestHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	req.Header.Set("Origin", "https://anything.example")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("wildcard+credentials must reject non-allowlisted origin, got %d", w.Code)
	}
}

func TestCORS_LoadConfigFromEnv(t *testing.T) {
	t.Setenv("QSDM_CORS_ALLOWED_ORIGINS", "https://a.example, https://b.example")
	t.Setenv("QSDM_CORS_ALLOW_CREDENTIALS", "true")
	cfg := LoadCORSConfigFromEnv(func(k string) string { return testEnvGet(t, k) })
	if len(cfg.AllowedOrigins) != 2 {
		t.Fatalf("expected 2 origins, got %v", cfg.AllowedOrigins)
	}
	if !cfg.AllowCredentials {
		t.Fatal("expected AllowCredentials=true")
	}
}

func testEnvGet(t *testing.T, k string) string {
	t.Helper()
	return getEnvForCORS(k)
}
