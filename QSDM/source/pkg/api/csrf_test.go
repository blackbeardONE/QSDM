package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newTestCSRFManager builds a CSRFManager whose cleanup interval is tiny
// (so an eviction smoke-test does not block CI) and registers Cleanup to
// release the background goroutine.
func newTestCSRFManager(t *testing.T) *CSRFManager {
	t.Helper()
	cm := NewCSRFManager()
	// Tighten cleanup so TestCSRF_BackgroundCleanup completes quickly.
	cm.cleanupInterval = 50 * time.Millisecond
	t.Cleanup(cm.Stop)
	return cm
}

// noopNext is the next handler used by middleware tests: it asserts the
// chain reached it and writes 200.
func noopNext(t *testing.T) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func TestCSRF_GenerateAndValidate(t *testing.T) {
	cm := newTestCSRFManager(t)

	tok, err := cm.GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	if tok == "" {
		t.Fatal("expected non-empty token")
	}
	if err := cm.ValidateToken(tok); err != nil {
		t.Fatalf("ValidateToken: %v", err)
	}
}

func TestCSRF_ValidateRejectsUnknownAndEmpty(t *testing.T) {
	cm := newTestCSRFManager(t)

	if err := cm.ValidateToken(""); err == nil {
		t.Fatal("expected error on empty token")
	}
	if err := cm.ValidateToken("definitely-not-a-real-token"); err == nil {
		t.Fatal("expected error on unknown token")
	}
}

func TestCSRF_TokenExpires(t *testing.T) {
	cm := newTestCSRFManager(t)
	cm.tokenTTL = 10 * time.Millisecond

	tok, err := cm.GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}

	time.Sleep(25 * time.Millisecond)

	if err := cm.ValidateToken(tok); err == nil {
		t.Fatal("expected expired token to be rejected")
	}
}

func TestCSRF_UserBinding(t *testing.T) {
	cm := newTestCSRFManager(t)

	tok, err := cm.GenerateTokenForUser("alice")
	if err != nil {
		t.Fatalf("GenerateTokenForUser: %v", err)
	}

	if err := cm.ValidateTokenForUser(tok, "alice"); err != nil {
		t.Fatalf("validate alice->alice: %v", err)
	}
	if err := cm.ValidateTokenForUser(tok, "bob"); err == nil {
		t.Fatal("expected cross-user token replay to be rejected")
	}
	// Anonymous expectation against a bound token should also fail.
	if err := cm.ValidateTokenForUser(tok, ""); err == nil {
		t.Fatal("expected empty expectedUserID to fail against a bound token")
	}
}

func TestCSRF_AnonymousTokenAcceptsAnyCaller(t *testing.T) {
	cm := newTestCSRFManager(t)

	tok, err := cm.GenerateTokenForUser("")
	if err != nil {
		t.Fatalf("GenerateTokenForUser: %v", err)
	}

	// Anonymous tokens (no binding) are allowed regardless of caller — they
	// exist to protect pre-login forms.
	if err := cm.ValidateTokenForUser(tok, ""); err != nil {
		t.Fatalf("anonymous->anonymous: %v", err)
	}
	if err := cm.ValidateTokenForUser(tok, "alice"); err != nil {
		t.Fatalf("anonymous->alice: %v", err)
	}
}

func TestCSRF_IssueTokenSetsSecureCookie(t *testing.T) {
	cm := newTestCSRFManager(t)

	w := httptest.NewRecorder()
	tok, err := cm.IssueToken(w, "alice", true)
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}

	resp := w.Result()
	defer resp.Body.Close()
	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == CSRFCookie {
			cookie = c
			break
		}
	}
	if cookie == nil {
		t.Fatal("CSRF cookie not set")
	}
	if cookie.Value != tok {
		t.Fatalf("cookie value %q != token %q", cookie.Value, tok)
	}
	if !cookie.Secure {
		t.Error("expected Secure flag on cookie")
	}
	if cookie.SameSite != http.SameSiteStrictMode {
		t.Errorf("expected SameSite=Strict, got %v", cookie.SameSite)
	}
	if cookie.Path != "/" {
		t.Errorf("expected Path=/, got %q", cookie.Path)
	}
}

func TestCSRFMiddleware_SkipsSafeMethods(t *testing.T) {
	cm := newTestCSRFManager(t)
	h := CSRFMiddleware(cm)(noopNext(t))

	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		req := httptest.NewRequest(method, "/api/v1/wallet/send", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: expected 200, got %d", method, w.Code)
		}
	}
}

func TestCSRFMiddleware_SkipsPublicEndpoints(t *testing.T) {
	cm := newTestCSRFManager(t)
	h := CSRFMiddleware(cm)(noopNext(t))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader("{}"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("public endpoint POST expected 200, got %d", w.Code)
	}
}

func TestCSRFMiddleware_SkipsBearerAuth(t *testing.T) {
	cm := newTestCSRFManager(t)
	h := CSRFMiddleware(cm)(noopNext(t))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/wallet/send", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer eyJ.fake.jwt")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("Bearer auth expected to bypass CSRF (200), got %d", w.Code)
	}
}

func TestCSRFMiddleware_RejectsMissingToken(t *testing.T) {
	cm := newTestCSRFManager(t)
	h := CSRFMiddleware(cm)(noopNext(t))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/wallet/send", strings.NewReader("{}"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("missing token expected 403, got %d", w.Code)
	}
}

func TestCSRFMiddleware_RejectsInvalidToken(t *testing.T) {
	cm := newTestCSRFManager(t)
	h := CSRFMiddleware(cm)(noopNext(t))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/wallet/send", strings.NewReader("{}"))
	req.Header.Set(CSRFHeader, "not-a-real-token")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("invalid token expected 403, got %d", w.Code)
	}
}

func TestCSRFMiddleware_AcceptsValidToken(t *testing.T) {
	cm := newTestCSRFManager(t)
	tok, err := cm.GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}

	h := CSRFMiddleware(cm)(noopNext(t))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/wallet/send", strings.NewReader("{}"))
	req.Header.Set(CSRFHeader, tok)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("valid token expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}
}

func TestCSRFMiddleware_DoubleSubmit_MatchPasses(t *testing.T) {
	cm := newTestCSRFManager(t)
	tok, err := cm.GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}

	h := CSRFMiddleware(cm)(noopNext(t))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/wallet/send", strings.NewReader("{}"))
	req.Header.Set(CSRFHeader, tok)
	req.AddCookie(&http.Cookie{Name: CSRFCookie, Value: tok})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("matched cookie+header expected 200, got %d", w.Code)
	}
}

func TestCSRFMiddleware_DoubleSubmit_MismatchFails(t *testing.T) {
	cm := newTestCSRFManager(t)
	tok, err := cm.GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}

	h := CSRFMiddleware(cm)(noopNext(t))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/wallet/send", strings.NewReader("{}"))
	req.Header.Set(CSRFHeader, tok)
	req.AddCookie(&http.Cookie{Name: CSRFCookie, Value: "tampered-by-sibling-subdomain"})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("cookie/header mismatch expected 403, got %d", w.Code)
	}
}

func TestCSRFMiddleware_UserBindingEnforced(t *testing.T) {
	cm := newTestCSRFManager(t)
	tok, err := cm.GenerateTokenForUser("alice")
	if err != nil {
		t.Fatalf("GenerateTokenForUser: %v", err)
	}

	h := CSRFMiddleware(cm)(noopNext(t))

	// Bob attempts to replay Alice's token while authenticated as Bob.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/wallet/send", strings.NewReader("{}"))
	req.Header.Set(CSRFHeader, tok)
	ctx := ContextWithClaims(req.Context(), &Claims{UserID: "bob"})
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("cross-user token replay expected 403, got %d", w.Code)
	}

	// Alice presents her own token while authenticated as Alice — passes.
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/wallet/send", strings.NewReader("{}"))
	req2.Header.Set(CSRFHeader, tok)
	ctx2 := ContextWithClaims(req2.Context(), &Claims{UserID: "alice"})
	req2 = req2.WithContext(ctx2)
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("same-user token expected 200, got %d", w2.Code)
	}
}

func TestCSRFMiddleware_FormValueFallback(t *testing.T) {
	cm := newTestCSRFManager(t)
	tok, err := cm.GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}

	h := CSRFMiddleware(cm)(noopNext(t))

	body := strings.NewReader("csrf_token=" + tok)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/wallet/send", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("form-value token expected 200, got %d", w.Code)
	}
}

func TestCSRF_BackgroundCleanupEvictsExpired(t *testing.T) {
	cm := newTestCSRFManager(t)
	cm.tokenTTL = 10 * time.Millisecond

	tok, err := cm.GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}

	// Wait for at least one cleanup tick after expiry.
	time.Sleep(200 * time.Millisecond)

	cm.mu.RLock()
	_, present := cm.tokens[tok]
	cm.mu.RUnlock()
	if present {
		t.Fatal("expected background cleanup to have evicted expired token")
	}
}

func TestCSRFTokenHandler_IssuesTokenAndCookie(t *testing.T) {
	cm := newTestCSRFManager(t)
	h := &Handlers{csrfManager: cm}

	req := httptest.NewRequest(http.MethodGet, CSRFTokenEndpoint, nil)
	w := httptest.NewRecorder()
	h.CSRFTokenHandler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}

	var body struct {
		CSRFToken        string `json:"csrf_token"`
		ExpiresInSeconds int    `json:"expires_in_seconds"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("response not JSON: %v (body=%s)", err, w.Body.String())
	}
	if body.CSRFToken == "" {
		t.Fatal("response missing csrf_token")
	}
	if body.ExpiresInSeconds <= 0 {
		t.Fatalf("expected positive expires_in_seconds, got %d", body.ExpiresInSeconds)
	}

	// Token from the body must be present in the cookie and valid against
	// the manager.
	resp := w.Result()
	defer resp.Body.Close()
	var cookieValue string
	for _, c := range resp.Cookies() {
		if c.Name == CSRFCookie {
			cookieValue = c.Value
			break
		}
	}
	if cookieValue != body.CSRFToken {
		t.Fatalf("cookie %q != body.csrf_token %q", cookieValue, body.CSRFToken)
	}
	if err := cm.ValidateToken(body.CSRFToken); err != nil {
		t.Fatalf("validate issued token: %v", err)
	}
}

func TestCSRFTokenHandler_RejectsNonGET(t *testing.T) {
	cm := newTestCSRFManager(t)
	h := &Handlers{csrfManager: cm}

	req := httptest.NewRequest(http.MethodPost, CSRFTokenEndpoint, nil)
	w := httptest.NewRecorder()
	h.CSRFTokenHandler(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestCSRFTokenHandler_503WhenManagerNil(t *testing.T) {
	h := &Handlers{csrfManager: nil}

	req := httptest.NewRequest(http.MethodGet, CSRFTokenEndpoint, nil)
	w := httptest.NewRecorder()
	h.CSRFTokenHandler(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestCSRFTokenHandler_BindsToClaims(t *testing.T) {
	cm := newTestCSRFManager(t)
	h := &Handlers{csrfManager: cm}

	req := httptest.NewRequest(http.MethodGet, CSRFTokenEndpoint, nil)
	ctx := ContextWithClaims(req.Context(), &Claims{UserID: "alice"})
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	h.CSRFTokenHandler(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var body struct {
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("bad json: %v", err)
	}

	// Issued token is bound to alice — bob cannot use it.
	if err := cm.ValidateTokenForUser(body.CSRFToken, "alice"); err != nil {
		t.Fatalf("alice should validate own token: %v", err)
	}
	if err := cm.ValidateTokenForUser(body.CSRFToken, "bob"); err == nil {
		t.Fatal("bob should not be able to use alice's token")
	}
}

func TestCSRFManager_StopIsIdempotent(t *testing.T) {
	cm := NewCSRFManager()
	cm.Stop()
	// Second Stop must not panic.
	cm.Stop()
}

func TestCSRFTokenEndpoint_IsPublic(t *testing.T) {
	if !isPublicEndpoint(CSRFTokenEndpoint) {
		t.Fatalf("%s must be public so unauthenticated clients can fetch a token", CSRFTokenEndpoint)
	}
}
