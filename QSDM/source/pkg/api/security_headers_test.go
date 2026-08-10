package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestSecurityHeaders_Baseline pins the audit-required header set so a
// future refactor that drops a header (or weakens Referrer-Policy back to
// "no-referrer") fails CI loudly. The required list mirrors HIGH-5.
func TestSecurityHeaders_Baseline(t *testing.T) {
	h := SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	required := map[string]string{
		"X-Content-Type-Options":       "nosniff",
		"X-Frame-Options":              "DENY",
		"X-XSS-Protection":             "1; mode=block",
		"Referrer-Policy":              "strict-origin-when-cross-origin",
		"Cross-Origin-Opener-Policy":   "same-origin",
		"Cross-Origin-Resource-Policy": "same-origin",
	}
	for name, want := range required {
		if got := w.Header().Get(name); got != want {
			t.Errorf("%s: got %q, want %q", name, got, want)
		}
	}

	// Sticky substring checks (these headers carry multiple directives).
	checks := map[string]string{
		"Strict-Transport-Security": "max-age=31536000",
		"Content-Security-Policy":   "frame-ancestors 'none'",
		"Permissions-Policy":        "geolocation=()",
	}
	for name, want := range checks {
		if got := w.Header().Get(name); !strings.Contains(got, want) {
			t.Errorf("%s: %q does not contain %q", name, got, want)
		}
	}

	// script-src and font-src must stay first-party. The dashboard briefly
	// allowed https://cdn.jsdelivr.net so an import map could pull three.js
	// from a CDN; that is now vendored under /static/vendor, and re-adding a
	// third-party origin would hand an external host script execution inside
	// an authenticated operator session.
	csp := w.Header().Get("Content-Security-Policy")
	for _, directive := range []string{"script-src 'self';", "font-src 'self';"} {
		if !strings.Contains(csp, directive) {
			t.Errorf("CSP %q does not contain first-party directive %q", csp, directive)
		}
	}
	if strings.Contains(csp, "cdn.jsdelivr.net") {
		t.Errorf("CSP re-introduced a third-party CDN origin: %q", csp)
	}
}
