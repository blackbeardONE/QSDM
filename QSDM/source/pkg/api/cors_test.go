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

	// An authenticated read: not one of the public read paths any origin may read.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/transactions", nil)
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

	req := httptest.NewRequest(http.MethodGet, "/api/v1/transactions", nil)
	req.Header.Set("Origin", "https://anything.example")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("wildcard+credentials must reject non-allowlisted origin, got %d", w.Code)
	}
}

// Any origin may read the public read-only endpoints (wallets, the browser
// extension, explorers): "*", and never credentials.
func TestCORS_PublicReadAnyOrigin(t *testing.T) {
	cfg := &CORSConfig{AllowedOrigins: []string{"https://allowed.example"}}
	h := CORSMiddleware(cfg)(corsTestHandler())

	for _, path := range []string{
		"/api/v1/status",
		"/api/v1/wallet/balance?address=13d786706accfbe77c5ddf6fc6757e1cca07bd01aff0cad3dcf9411d92cf11c9",
		"/api/v1/wallet/nonce?sender=13d786706accfbe77c5ddf6fc6757e1cca07bd01aff0cad3dcf9411d92cf11c9",
		"/api/v1/receipts?from=1&to=2",
		"/api/v1/receipts/hive_wallet_1762390000000_9f8e7d6c5b4a3921",
		"/api/v1/chain/blocks?from=1&to=1",
		"/api/v1/mining/emission",
	} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			req := httptest.NewRequest(method, path, nil)
			req.Header.Set("Origin", "chrome-extension://homapiejinlbjdhhdegcbnldkpkodepo")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("%s %s: expected 200, got %d", method, path, w.Code)
			}
			if got := w.Header().Get("Access-Control-Allow-Origin"); got != "*" {
				t.Fatalf("%s %s: ACAO=%q, want *", method, path, got)
			}
			if got := w.Header().Get("Access-Control-Allow-Credentials"); got != "" {
				t.Fatalf("%s %s: public reads must never allow credentials, got %q", method, path, got)
			}
		}
	}
}

func TestCORS_PublicReadNeverWithCredentials(t *testing.T) {
	cfg := &CORSConfig{
		AllowedOrigins:   []string{"https://allowed.example"},
		AllowCredentials: true,
	}
	h := CORSMiddleware(cfg)(corsTestHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	req.Header.Set("Origin", "https://wallet.example")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK || w.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("public read: code %d ACAO %q", w.Code, w.Header().Get("Access-Control-Allow-Origin"))
	}
	if got := w.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Fatalf("credentials must not be allowed with *, got %q", got)
	}

	// The allowlisted origin keeps its exact echo and credentials.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	req.Header.Set("Origin", "https://allowed.example")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Header().Get("Access-Control-Allow-Origin") != "https://allowed.example" || w.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatalf("allowlisted origin changed: ACAO %q ACAC %q",
			w.Header().Get("Access-Control-Allow-Origin"), w.Header().Get("Access-Control-Allow-Credentials"))
	}
}

func TestCORS_PublicReadPreflight(t *testing.T) {
	h := CORSMiddleware(&CORSConfig{})(corsTestHandler())

	req := httptest.NewRequest(http.MethodOptions, "/api/v1/receipts/abc", nil)
	req.Header.Set("Origin", "https://wallet.example")
	req.Header.Set("Access-Control-Request-Method", "GET")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("public read preflight expected 204, got %d", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("ACAO=%q", got)
	}
	if got := w.Header().Get("Access-Control-Allow-Methods"); got != "GET, HEAD, OPTIONS" {
		t.Fatalf("Access-Control-Allow-Methods=%q", got)
	}
}

// Writes stay on the allowlist, on the public paths too.
func TestCORS_PublicPathWritesStayDenied(t *testing.T) {
	h := CORSMiddleware(&CORSConfig{AllowedOrigins: []string{"https://allowed.example"}})(corsTestHandler())

	cases := []struct {
		method, path, preflight string
	}{
		{http.MethodPost, "/api/v1/status", ""},
		{http.MethodPost, "/api/v1/wallet/submit-signed", ""},
		{http.MethodPut, "/api/v1/receipts/abc", ""},
		{http.MethodOptions, "/api/v1/status", "POST"},
		{http.MethodOptions, "/api/v1/wallet/submit-signed", "POST"},
	}
	for _, c := range cases {
		req := httptest.NewRequest(c.method, c.path, nil)
		req.Header.Set("Origin", "https://evil.example")
		if c.preflight != "" {
			req.Header.Set("Access-Control-Request-Method", c.preflight)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("%s %s (preflight %q): expected 403, got %d", c.method, c.path, c.preflight, w.Code)
		}
		if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Fatalf("%s %s: denied request echoed ACAO %q", c.method, c.path, got)
		}
	}
}

func TestCORS_PublicReadPathMatching(t *testing.T) {
	for path, want := range map[string]bool{
		"/api/v1/status":                 true,
		"/api/v1/wallet/balance":         true,
		"/api/v1/receipts":               true,
		"/api/v1/receipts/abc":           true,
		"/api/v1/statusx":                false,
		"/api/v1/status/":                false,
		"/api/v1/receipts/":              false,
		"/api/v1/receipts/abc/def":       false,
		"/api/v1/receipts/../auth/login": false,
		"//api/v1/status":                false,
		"/api/v1/transactions":           false,
		"/api/v1/wallet/send":            false,
		"/api/v1/wallet/submit-signed":   false,
		"/api/v1/auth/login":             false,
		"":                               false,
	} {
		if got := isPublicReadPath(path); got != want {
			t.Errorf("isPublicReadPath(%q) = %v, want %v", path, got, want)
		}
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
