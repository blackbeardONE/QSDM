package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func ngcProbe(t *testing.T, method, path string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	// The sidecar reaches the API through the same TLS terminator as everyone
	// else, which is why it shares the anonymous bucket.
	req.RemoteAddr = "10.0.0.1:41000"
	return req
}

// The exemption is only safe because each exempted path keeps a purpose-sized
// cap in the pre-auth limiter. If one is ever removed, this fails rather than
// leaving the path with no rate limit at all.
func TestNGCIngestPathsHaveDedicatedPreAuthLimits(t *testing.T) {
	rl := NewRateLimiter(100, time.Minute)
	for path := range ngcIngestPaths {
		if limit := rl.getEndpointLimit(path, http.MethodPost); limit <= 0 {
			t.Errorf("%s is exempt from the role limiter but has no dedicated pre-auth cap", path)
		}
	}
}

func TestIsNGCMonitoringIngest_Classification(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/api/v1/monitoring/ngc-proof", true},
		{"/api/v1/monitoring/ngc-challenge", true},
		{"/api/v1/monitoring/ngc-proofs", true},

		// Exact match only: a route added under the prefix later must not
		// inherit the exemption.
		{"/api/v1/monitoring/ngc-proof/extra", false},
		{"/api/v1/monitoring/something-new", false},
		{"/api/v1/wallet/submit-signed", false},
		{"/api/v1/status", false},
	}
	for _, c := range cases {
		if got := isNGCMonitoringIngest(ngcProbe(t, http.MethodPost, c.path)); got != c.want {
			t.Errorf("%s: got %v, want %v", c.path, got, c.want)
		}
	}
	if isNGCMonitoringIngest(nil) {
		t.Error("a nil request must not be exempt")
	}
}

func TestRoleRateLimiter_NGCIngestBypassesAnonymousTier(t *testing.T) {
	// A 10-minute sidecar cycle cannot exhaust its own 30/min allowance; it was
	// being starved by unrelated anonymous traffic sharing one bucket.
	rl := NewRoleRateLimiter(DefaultRoleRateLimiterConfig())
	hits := 0
	h := rl.Middleware(countingHandler(&hits))

	const n = 120 // well past the 30/min anonymous tier
	for i := 0; i < n; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, ngcProbe(t, http.MethodPost, "/api/v1/monitoring/ngc-proof"))
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: got %d, want 200 -- attestation ingest must not be starved", i+1, rec.Code)
		}
	}
	if hits != n {
		t.Fatalf("expected all %d ingest posts through, got %d", n, hits)
	}
}

func TestRoleRateLimiter_NGCChallengeAndListBypass(t *testing.T) {
	for _, path := range []string{
		"/api/v1/monitoring/ngc-challenge",
		"/api/v1/monitoring/ngc-proofs",
	} {
		rl := NewRoleRateLimiter(DefaultRoleRateLimiterConfig())
		h := rl.Middleware(countingHandler(new(int)))
		for i := 0; i < 60; i++ {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, ngcProbe(t, http.MethodPost, path))
			if rec.Code != http.StatusOK {
				t.Fatalf("%s request %d: got %d, want 200", path, i+1, rec.Code)
			}
		}
	}
}

func TestRoleRateLimiter_UnlistedMonitoringPathStillLimited(t *testing.T) {
	// The bypass must not widen to the whole /monitoring/ prefix.
	rl := NewRoleRateLimiter(DefaultRoleRateLimiterConfig())
	h := rl.Middleware(countingHandler(new(int)))

	var limited bool
	for i := 0; i < 200; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, ngcProbe(t, http.MethodPost, "/api/v1/monitoring/something-new"))
		if rec.Code == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("an unlisted /monitoring/ path must still hit the anonymous tier")
	}
}

func TestRateLimiter_NGCIngestStillHasPreAuthCeiling(t *testing.T) {
	// The sized per-path cap is deliberately still in force: the role-limiter
	// bypass removes the blunt global tier, not the gate meant for this path.
	rl := NewRateLimiter(100, time.Minute)
	h := rl.RateLimitMiddleware(countingHandler(new(int)))

	var limited bool
	for i := 0; i < 200; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, ngcProbe(t, http.MethodPost, "/api/v1/monitoring/ngc-proof"))
		if rec.Code == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("ngc-proof must still be bounded by its dedicated pre-auth cap")
	}
}
