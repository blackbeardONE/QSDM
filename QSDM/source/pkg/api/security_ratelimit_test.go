package api

// security_ratelimit_test.go — pin the mining-endpoint bypass on
// the older RateLimitMiddleware (security.go) against regression.
//
// This test exists because there are TWO independently-mounted
// rate limiters in front of /api/v1: the legacy RateLimiter
// (security.go, this file) and the role-aware RoleRateLimiter
// (ratelimit_roles.go). The miner only needs ONE of them to
// engage to start seeing 429s, so the bypass must live in BOTH.
// On 2026-05-07 a real RTX-3050 hit this exact failure mode
// (19 accepted v2 proofs followed by 12 sequential 429s) because
// the bypass was only on the role-aware limiter. The fix is the
// strings.HasPrefix(/api/v1/mining/) bypass added to
// RateLimitMiddleware; this test makes sure that bypass stays.

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRateLimitMiddleware_MiningBypass(t *testing.T) {
	// Configure an extremely tight limit so a single miner-style
	// burst would trip it within a handful of requests if the
	// bypass were absent.
	rl := NewRateLimiter(2, time.Minute)

	handler := rl.RateLimitMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	miningPaths := []string{
		"/api/v1/mining/work",
		"/api/v1/mining/submit",
		"/api/v1/mining/challenge",
		"/api/v1/mining/account",
		"/api/v1/mining/emission",
		"/api/v1/mining/blocks",
		"/api/v1/mining/enroll",
		"/api/v1/mining/unenroll",
		"/api/v1/mining/enrollments",
	}
	for _, path := range miningPaths {
		// 50 requests is well past the maxReqs=2 ceiling but
		// well under any reasonable per-test wall-clock budget.
		// If the bypass regresses, the third request 429s and
		// the test fails immediately.
		for i := 0; i < 50; i++ {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.RemoteAddr = "10.0.0.99:1234"
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("%s request %d should never be rate-limited at HTTP layer (mining bypass), got %d",
					path, i+1, rec.Code)
			}
		}
	}

	// Sanity: a non-mining /api/v1 path STILL gets rate-limited
	// after maxReqs=2 hits, so we know the bypass is path-scoped
	// and not a wholesale disable of the limiter.
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/wallet/balance", nil)
		req.RemoteAddr = "10.0.0.42:1234"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("non-mining /api/v1 request %d below cap should pass, got %d", i+1, rec.Code)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/wallet/balance", nil)
	req.RemoteAddr = "10.0.0.42:1234"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("3rd /wallet request should 429 (bypass must be /mining-only), got %d", rec.Code)
	}
}

func TestRateLimitMiddleware_HealthBypassStillWorks(t *testing.T) {
	// Pin the original /api/v1/health bypass — the change that
	// added the mining bypass is two prefix checks back-to-back,
	// so it would be easy to accidentally swap or remove the
	// health check while editing.
	rl := NewRateLimiter(1, time.Minute)
	handler := rl.RateLimitMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	for i := 0; i < 20; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
		req.RemoteAddr = "10.0.0.7:1"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("/api/v1/health request %d should not be rate-limited, got %d", i+1, rec.Code)
		}
	}
}
