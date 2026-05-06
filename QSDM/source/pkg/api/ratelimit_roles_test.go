package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRoleRateLimiter_AdminHigherLimit(t *testing.T) {
	cfg := RoleRateLimiterConfig{
		Admin:     RoleTier{MaxRequests: 10, Window: time.Minute},
		User:      RoleTier{MaxRequests: 5, Window: time.Minute},
		Anonymous: RoleTier{MaxRequests: 2, Window: time.Minute},
	}
	rl := NewRoleRateLimiter(cfg)

	for i := 0; i < 10; i++ {
		if !rl.Allow("addr1", "admin") {
			t.Fatalf("admin blocked at request %d", i+1)
		}
	}
	if rl.Allow("addr1", "admin") {
		t.Fatal("admin should be blocked after 10 requests")
	}
}

func TestRoleRateLimiter_UserLimit(t *testing.T) {
	cfg := RoleRateLimiterConfig{
		Admin:     RoleTier{MaxRequests: 100, Window: time.Minute},
		User:      RoleTier{MaxRequests: 5, Window: time.Minute},
		Anonymous: RoleTier{MaxRequests: 2, Window: time.Minute},
	}
	rl := NewRoleRateLimiter(cfg)

	for i := 0; i < 5; i++ {
		if !rl.Allow("user1", "user") {
			t.Fatalf("user blocked at request %d", i+1)
		}
	}
	if rl.Allow("user1", "user") {
		t.Fatal("user should be blocked after 5 requests")
	}
}

func TestRoleRateLimiter_AnonymousLimit(t *testing.T) {
	cfg := RoleRateLimiterConfig{
		Admin:     RoleTier{MaxRequests: 100, Window: time.Minute},
		User:      RoleTier{MaxRequests: 50, Window: time.Minute},
		Anonymous: RoleTier{MaxRequests: 3, Window: time.Minute},
	}
	rl := NewRoleRateLimiter(cfg)

	for i := 0; i < 3; i++ {
		if !rl.Allow("1.2.3.4", "anonymous") {
			t.Fatalf("anon blocked at request %d", i+1)
		}
	}
	if rl.Allow("1.2.3.4", "anonymous") {
		t.Fatal("anon should be blocked after 3 requests")
	}
}

func TestRoleRateLimiter_SeparateIdentifiers(t *testing.T) {
	cfg := RoleRateLimiterConfig{
		Admin:     RoleTier{MaxRequests: 100, Window: time.Minute},
		User:      RoleTier{MaxRequests: 2, Window: time.Minute},
		Anonymous: RoleTier{MaxRequests: 2, Window: time.Minute},
	}
	rl := NewRoleRateLimiter(cfg)

	rl.Allow("alice", "user")
	rl.Allow("alice", "user")
	if rl.Allow("alice", "user") {
		t.Fatal("alice should be blocked")
	}
	if !rl.Allow("bob", "user") {
		t.Fatal("bob should not be blocked (different identifier)")
	}
}

func TestRoleRateLimiter_Middleware_Anonymous(t *testing.T) {
	cfg := RoleRateLimiterConfig{
		Admin:     RoleTier{MaxRequests: 100, Window: time.Minute},
		User:      RoleTier{MaxRequests: 50, Window: time.Minute},
		Anonymous: RoleTier{MaxRequests: 2, Window: time.Minute},
	}
	rl := NewRoleRateLimiter(cfg)

	handler := rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("GET", "/api/v1/test", nil)
		req.RemoteAddr = "10.0.0.1:1234"
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d", i+1, rr.Code)
		}
	}

	req := httptest.NewRequest("GET", "/api/v1/test", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rr.Code)
	}
}

func TestRoleRateLimiter_Middleware_WithClaims(t *testing.T) {
	cfg := RoleRateLimiterConfig{
		Admin:     RoleTier{MaxRequests: 100, Window: time.Minute},
		User:      RoleTier{MaxRequests: 3, Window: time.Minute},
		Anonymous: RoleTier{MaxRequests: 1, Window: time.Minute},
	}
	rl := NewRoleRateLimiter(cfg)

	handler := rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for i := 0; i < 3; i++ {
		req := httptest.NewRequest("GET", "/api/v1/test", nil)
		ctx := context.WithValue(req.Context(), "claims", &Claims{Address: "user_addr", Role: "user"})
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req.WithContext(ctx))
		if rr.Code != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d", i+1, rr.Code)
		}
	}

	req := httptest.NewRequest("GET", "/api/v1/test", nil)
	ctx := context.WithValue(req.Context(), "claims", &Claims{Address: "user_addr", Role: "user"})
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req.WithContext(ctx))
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rr.Code)
	}
}

func TestRoleRateLimiter_HealthBypass(t *testing.T) {
	cfg := RoleRateLimiterConfig{
		Admin:     RoleTier{MaxRequests: 1, Window: time.Minute},
		User:      RoleTier{MaxRequests: 1, Window: time.Minute},
		Anonymous: RoleTier{MaxRequests: 1, Window: time.Minute},
	}
	rl := NewRoleRateLimiter(cfg)

	handler := rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for i := 0; i < 5; i++ {
		req := httptest.NewRequest("GET", "/api/v1/health", nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("health request %d should never be rate-limited, got %d", i+1, rr.Code)
		}
	}
}

func TestRoleRateLimiter_MiningBypass(t *testing.T) {
	// Mining endpoints are designed for high-frequency miner
	// traffic and have their own consensus-level abuse
	// protection (Dedup + Quarantine + hashrate-band gating
	// + v2 attestation gate). The HTTP-layer rate limit MUST
	// NOT engage on these paths or it will choke any real
	// miner within a single minute. This test pins the
	// bypass against accidental regression.
	cfg := RoleRateLimiterConfig{
		Admin:     RoleTier{MaxRequests: 1, Window: time.Minute},
		User:      RoleTier{MaxRequests: 1, Window: time.Minute},
		Anonymous: RoleTier{MaxRequests: 1, Window: time.Minute},
	}
	rl := NewRoleRateLimiter(cfg)

	handler := rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	miningPaths := []string{
		"/api/v1/mining/work",
		"/api/v1/mining/submit",
		"/api/v1/mining/challenge",
		"/api/v1/mining/account",
		"/api/v1/mining/emission",
		"/api/v1/mining/enroll",
	}
	for _, path := range miningPaths {
		for i := 0; i < 200; i++ {
			req := httptest.NewRequest("GET", path, nil)
			req.RemoteAddr = "10.0.0.99:1234"
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			if rr.Code != http.StatusOK {
				t.Fatalf("%s request %d should never be rate-limited at HTTP layer, got %d",
					path, i+1, rr.Code)
			}
		}
	}

	// Sanity: a non-mining path under /api/v1 STILL gets
	// rate-limited (so we know the bypass is path-scoped, not
	// a wholesale disable).
	req := httptest.NewRequest("GET", "/api/v1/wallet/balance", nil)
	req.RemoteAddr = "10.0.0.99:1234"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("first /wallet request should pass; got %d", rr.Code)
	}
	req2 := httptest.NewRequest("GET", "/api/v1/wallet/balance", nil)
	req2.RemoteAddr = "10.0.0.99:1234"
	rr2 := httptest.NewRecorder()
	handler.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusTooManyRequests {
		t.Fatalf("second /wallet request should 429 (bypass must be /mining-only); got %d", rr2.Code)
	}
}

func TestRoleRateLimiter_DefaultConfig(t *testing.T) {
	cfg := DefaultRoleRateLimiterConfig()
	if cfg.Admin.MaxRequests <= cfg.User.MaxRequests {
		t.Fatal("admin should have higher limit than user")
	}
	if cfg.User.MaxRequests <= cfg.Anonymous.MaxRequests {
		t.Fatal("user should have higher limit than anonymous")
	}
}
