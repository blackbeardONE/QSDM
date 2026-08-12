package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func transparencyProbe(t *testing.T, method, path string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	// Every caller arrives from the TLS terminator, which is the whole reason
	// one bucket is shared by the internet.
	req.RemoteAddr = "10.0.0.1:54321"
	return req
}

func TestIsPublicTransparencyRead_Classification(t *testing.T) {
	cases := []struct {
		method, path string
		want         bool
	}{
		{http.MethodGet, "/api/v1/trust/attestations/summary", true},
		{http.MethodGet, "/api/v1/trust/attestations/recent", true},
		{http.MethodGet, "/api/v1/audit/summary", true},
		{http.MethodGet, "/api/v1/audit/items", true},
		{http.MethodGet, "/api/v1/audit/badge.svg", true},
		{http.MethodHead, "/api/v1/audit/summary", true},

		// A mutating method must not inherit the bypass, so a write route
		// added under these prefixes later is still rate limited.
		{http.MethodPost, "/api/v1/trust/attestations/summary", false},
		{http.MethodPut, "/api/v1/audit/items", false},
		{http.MethodDelete, "/api/v1/audit/items", false},

		// Neighbouring paths must not be swept in by a loose prefix.
		{http.MethodGet, "/api/v1/wallet/submit-signed", false},
		{http.MethodGet, "/api/v1/auth/login", false},
		{http.MethodGet, "/api/v1/status", false},
	}
	for _, c := range cases {
		got := isPublicTransparencyRead(transparencyProbe(t, c.method, c.path))
		if got != c.want {
			t.Errorf("%s %s: got %v, want %v", c.method, c.path, got, c.want)
		}
	}
}

func TestIsPublicTransparencyRead_NilRequest(t *testing.T) {
	if isPublicTransparencyRead(nil) {
		t.Fatal("a nil request must not be treated as exempt")
	}
}

// countingHandler records how many requests reached the wrapped handler.
func countingHandler(hits *int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*hits++
		w.WriteHeader(http.StatusOK)
	})
}

func TestRateLimiter_TransparencyReadsBypassPreAuthLimit(t *testing.T) {
	// A tiny quota stands in for "the internet already spent it".
	rl := NewRateLimiter(2, time.Minute)
	hits := 0
	h := rl.RateLimitMiddleware(countingHandler(&hits))

	const n = 25
	for i := 0; i < n; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, transparencyProbe(t, http.MethodGet, "/api/v1/trust/attestations/summary"))
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: got %d, want 200 -- the probe must not be starved by page traffic", i+1, rec.Code)
		}
	}
	if hits != n {
		t.Fatalf("expected all %d transparency reads to pass through, got %d", n, hits)
	}
}

func TestRateLimiter_NonTransparencyPathStillLimited(t *testing.T) {
	// The bypass must not become a general hole.
	rl := NewRateLimiter(2, time.Minute)
	h := rl.RateLimitMiddleware(countingHandler(new(int)))

	var limited bool
	for i := 0; i < 10; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, transparencyProbe(t, http.MethodGet, "/api/v1/validators"))
		if rec.Code == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("a non-transparency path must still hit the pre-auth limiter")
	}
}

func TestRateLimiter_TransparencyWriteStillLimited(t *testing.T) {
	rl := NewRateLimiter(2, time.Minute)
	h := rl.RateLimitMiddleware(countingHandler(new(int)))

	var limited bool
	for i := 0; i < 10; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, transparencyProbe(t, http.MethodPost, "/api/v1/audit/items"))
		if rec.Code == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("a mutating method under a transparency prefix must still be limited")
	}
}

func TestRoleRateLimiter_TransparencyReadsBypassAnonymousTier(t *testing.T) {
	// The anonymous tier is the binding constraint in production: it is
	// tighter than the pre-auth limit, so a bypass that landed only in
	// RateLimiter would still return 429 from here.
	rl := NewRoleRateLimiter(DefaultRoleRateLimiterConfig())
	hits := 0
	h := rl.Middleware(countingHandler(&hits))

	const n = 120 // comfortably past the 30/min anonymous tier
	for i := 0; i < n; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, transparencyProbe(t, http.MethodGet, "/api/v1/audit/summary"))
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: got %d, want 200 from the role limiter", i+1, rec.Code)
		}
	}
	if hits != n {
		t.Fatalf("expected all %d reads through the role limiter, got %d", n, hits)
	}
}

func TestRoleRateLimiter_NonTransparencyPathStillLimited(t *testing.T) {
	rl := NewRoleRateLimiter(DefaultRoleRateLimiterConfig())
	h := rl.Middleware(countingHandler(new(int)))

	var limited bool
	for i := 0; i < 200; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, transparencyProbe(t, http.MethodGet, fmt.Sprintf("/api/v1/validators?i=%d", i)))
		if rec.Code == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("a non-transparency path must still hit the anonymous tier")
	}
}
