package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRoleRateLimiter_HighFrequencyPublicReadsBypassAnonymousTier(t *testing.T) {
	rl := NewRoleRateLimiter(DefaultRoleRateLimiterConfig())
	h := rl.Middleware(countingHandler(new(int)))

	paths := []string{
		"/api/v1/status",
		"/api/v1/chain/blocks",
		"/api/v1/tasks",
		"/api/v1/tasks/state",
		"/api/v1/tasks/actions",
		"/api/v1/tasks/qsdm-system-miner",
	}
	for _, path := range paths {
		for i := 0; i < 50; i++ {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.RemoteAddr = "127.0.0.1:4567"
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("%s request %d should not spend the anonymous role bucket, got %d", path, i+1, rec.Code)
			}
		}
	}
}

func TestRoleRateLimiter_TaskActionSubmitStillLimited(t *testing.T) {
	rl := NewRoleRateLimiter(DefaultRoleRateLimiterConfig())
	h := rl.Middleware(countingHandler(new(int)))

	for i := 0; i < 30; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/actions/submit-signed", nil)
		req.RemoteAddr = "127.0.0.1:4567"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("submit request %d below anonymous cap should pass, got %d", i+1, rec.Code)
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/actions/submit-signed", nil)
	req.RemoteAddr = "127.0.0.1:4567"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("task-action submit must still be role-rate-limited, got %d", rec.Code)
	}
}

func TestRateLimiter_HighFrequencyPublicReadsHaveSizedPreAuthCeiling(t *testing.T) {
	rl := NewRateLimiter(2, time.Minute)
	h := rl.RateLimitMiddleware(countingHandler(new(int)))

	for i := 0; i < highFrequencyPublicReadLimitPerMinute; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/chain/blocks", nil)
		req.RemoteAddr = "127.0.0.1:4567"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("chain read request %d should pass below sized pre-auth ceiling, got %d", i+1, rec.Code)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/chain/blocks", nil)
	req.RemoteAddr = "127.0.0.1:4567"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("high-frequency public read must still have a pre-auth ceiling, got %d", rec.Code)
	}
}

func TestRateLimiter_TaskActionSubmitDoesNotUsePublicReadCeiling(t *testing.T) {
	rl := NewRateLimiter(2, time.Minute)
	h := rl.RateLimitMiddleware(countingHandler(new(int)))

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/actions/submit-signed", nil)
		req.RemoteAddr = "127.0.0.1:4567"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("submit request %d below default pre-auth cap should pass, got %d", i+1, rec.Code)
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/actions/submit-signed", nil)
	req.RemoteAddr = "127.0.0.1:4567"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("POST task-action submit must not inherit the public-read ceiling, got %d", rec.Code)
	}
}
