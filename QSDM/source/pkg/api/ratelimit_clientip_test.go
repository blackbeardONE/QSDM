package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// withTrustedProxy enables proxy-supplied identity for one test and restores
// the previous setting, since trustProxyHeaders is process-global.
func withTrustedProxy(t *testing.T, trust bool) {
	t.Helper()
	prev := TrustProxyHeaders()
	SetTrustProxyHeaders(trust)
	t.Cleanup(func() { SetTrustProxyHeaders(prev) })
}

func ipReq(t *testing.T, remoteAddr string, headers map[string]string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/validators", nil)
	r.RemoteAddr = remoteAddr
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	return r
}

const proxyAddr = "10.0.0.1:44444" // the TLS terminator

func TestClientIP_IgnoresProxyHeadersWhenNotTrusted(t *testing.T) {
	withTrustedProxy(t, false)
	got := clientIP(ipReq(t, proxyAddr, map[string]string{
		"X-Real-IP":       "9.9.9.9",
		"X-Forwarded-For": "8.8.8.8",
	}))
	if got != "10.0.0.1" {
		t.Fatalf("untrusted proxy headers must be ignored, got %q", got)
	}
}

func TestClientIP_PrefersProxySetRealIP(t *testing.T) {
	withTrustedProxy(t, true)
	got := clientIP(ipReq(t, proxyAddr, map[string]string{"X-Real-IP": "203.0.113.7"}))
	if got != "203.0.113.7" {
		t.Fatalf("expected the proxy-set client address, got %q", got)
	}
}

// The regression this change exists for. Previously clientIP returned the
// LEFTMOST X-Forwarded-For entry, which the client supplies -- so any caller
// could mint a fresh rate-limit bucket per request by rotating the header.
func TestClientIP_SpoofedForwardedForCannotChooseBucket(t *testing.T) {
	withTrustedProxy(t, true)

	// The client sent its own XFF values; the proxy appended the real peer.
	spoofed := ipReq(t, proxyAddr, map[string]string{
		"X-Forwarded-For": "1.1.1.1, 2.2.2.2, 203.0.113.7",
	})
	if got := clientIP(spoofed); got != "203.0.113.7" {
		t.Fatalf("must use the proxy-appended rightmost entry, got %q", got)
	}

	// Rotating the client-supplied prefix must not change the bucket.
	rotated := ipReq(t, proxyAddr, map[string]string{
		"X-Forwarded-For": "4.4.4.4, 5.5.5.5, 203.0.113.7",
	})
	if clientIP(spoofed) != clientIP(rotated) {
		t.Fatal("bucket changed when the caller rotated its own X-Forwarded-For prefix")
	}
}

func TestClientIP_RealIPWinsOverSpoofedForwardedFor(t *testing.T) {
	withTrustedProxy(t, true)
	got := clientIP(ipReq(t, proxyAddr, map[string]string{
		"X-Real-IP":       "203.0.113.7",
		"X-Forwarded-For": "1.1.1.1",
	}))
	if got != "203.0.113.7" {
		t.Fatalf("X-Real-IP is proxy-set and must win, got %q", got)
	}
}

func TestClientIP_GarbageHeadersFallBackToPeer(t *testing.T) {
	withTrustedProxy(t, true)
	for name, headers := range map[string]map[string]string{
		"unparseable real-ip": {"X-Real-IP": "not-an-ip"},
		"empty real-ip":       {"X-Real-IP": "   "},
		"garbage xff":         {"X-Forwarded-For": "nonsense, also-nonsense"},
		"both bad":            {"X-Real-IP": "???", "X-Forwarded-For": "???"},
	} {
		if got := clientIP(ipReq(t, proxyAddr, headers)); got != "10.0.0.1" {
			t.Errorf("%s: expected fallback to the peer address, got %q", name, got)
		}
	}
}

// Distinct spellings of one address must not be spendable as separate buckets.
func TestClientIP_NormalisesEquivalentForms(t *testing.T) {
	withTrustedProxy(t, true)
	forms := []string{"203.0.113.7", " 203.0.113.7 ", "::ffff:203.0.113.7"}
	first := clientIP(ipReq(t, proxyAddr, map[string]string{"X-Real-IP": forms[0]}))
	for _, f := range forms[1:] {
		if got := clientIP(ipReq(t, proxyAddr, map[string]string{"X-Real-IP": f})); got != first {
			t.Errorf("form %q produced bucket %q, want %q", f, got, first)
		}
	}
}

// The point of the whole change: separate clients get separate quotas instead
// of sharing one bucket keyed on the terminator.
func TestRoleRateLimiter_PerClientQuotasBehindTrustedProxy(t *testing.T) {
	withTrustedProxy(t, true)
	rl := NewRoleRateLimiter(DefaultRoleRateLimiterConfig())
	h := rl.Middleware(countingHandler(new(int)))

	exhaust := func(ip string) bool {
		for i := 0; i < 200; i++ {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, ipReq(t, proxyAddr, map[string]string{"X-Real-IP": ip}))
			if rec.Code == http.StatusTooManyRequests {
				return true
			}
		}
		return false
	}

	if !exhaust("203.0.113.7") {
		t.Fatal("precondition: one client should be able to exhaust its own quota")
	}
	// A different client must still be served.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, ipReq(t, proxyAddr, map[string]string{"X-Real-IP": "203.0.113.8"}))
	if rec.Code != http.StatusOK {
		t.Fatalf("a second client must have its own quota, got %d", rec.Code)
	}
}
