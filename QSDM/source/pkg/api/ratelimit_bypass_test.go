package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestRateLimiter_apiKeyRotationCannotMintBuckets is the regression test for
// the pre-auth rate-limit bypass. getClientIdentifier used to key the bucket
// on the caller-supplied X-API-Key header, and RateLimitMiddleware runs
// BEFORE AuthMiddleware, so nothing had validated that header. Sending a
// fresh random X-API-Key per request produced a fresh bucket with a full
// quota, making the nominal 10/min limit on /wallet/send unbounded.
func TestRateLimiter_apiKeyRotationCannotMintBuckets(t *testing.T) {
	rl := NewRateLimiter(3, time.Minute)

	req := func(apiKey string) *http.Request {
		// A path with no per-endpoint override, so the global limit applies.
		r := httptest.NewRequest(http.MethodGet, "/api/v1/wallet/balance", nil)
		r.RemoteAddr = "203.0.113.7:51000"
		if apiKey != "" {
			r.Header.Set("X-API-Key", apiKey)
		}
		return r
	}

	// Each request carries a different API key. All share one source IP, so
	// all must share one bucket and the 4th must be refused.
	keys := []string{"k1", "k2", "k3", "k4"}
	statuses := make([]int, 0, len(keys))
	handler := rl.RateLimitMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	for _, k := range keys {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req(k))
		statuses = append(statuses, rec.Code)
	}

	if statuses[3] != http.StatusTooManyRequests {
		t.Fatalf("rotating X-API-Key must not mint a fresh bucket; statuses=%v", statuses)
	}
}

// X-Forwarded-For is caller-supplied too, so it must not be trusted unless
// the operator has declared a reverse proxy in front of the node.
func TestRateLimiter_forwardedForRotationCannotMintBuckets(t *testing.T) {
	SetTrustProxyHeaders(false)
	rl := NewRateLimiter(2, time.Minute)

	handler := rl.RateLimitMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	last := 0
	for i, xff := range []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"} {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/wallet/balance", nil)
		r.RemoteAddr = "203.0.113.9:52000"
		r.Header.Set("X-Forwarded-For", xff)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, r)
		last = rec.Code
		_ = i
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("rotating X-Forwarded-For must not mint a fresh bucket; last status=%d", last)
	}
}

// With a trusted proxy declared, X-Forwarded-For becomes the bucket key
// again so distinct real clients behind one proxy are limited separately.
func TestRateLimiter_trustedProxyHonoursForwardedFor(t *testing.T) {
	SetTrustProxyHeaders(true)
	t.Cleanup(func() { SetTrustProxyHeaders(false) })

	rl := NewRateLimiter(1, time.Minute)
	handler := rl.RateLimitMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for _, xff := range []string{"1.1.1.1", "2.2.2.2"} {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/wallet/balance", nil)
		r.RemoteAddr = "203.0.113.9:52000"
		r.Header.Set("X-Forwarded-For", xff)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, r)
		if rec.Code != http.StatusOK {
			t.Fatalf("distinct clients behind a trusted proxy must get separate buckets, got %d for %s", rec.Code, xff)
		}
	}
}

// Source-port variation must not mint buckets either.
func TestRateLimiter_sourcePortDoesNotMintBuckets(t *testing.T) {
	SetTrustProxyHeaders(false)
	rl := NewRateLimiter(2, time.Minute)
	handler := rl.RateLimitMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	last := 0
	for _, port := range []string{"51000", "51001", "51002"} {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/wallet/balance", nil)
		r.RemoteAddr = "203.0.113.11:" + port
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, r)
		last = rec.Code
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("varying source port must not mint a fresh bucket; last status=%d", last)
	}
}
