package api

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/crypto"
	"github.com/blackbeardONE/QSDM/pkg/monitoring"
)

// SecurityHeaders adds the canonical set of HTTP security headers required
// by the audit baseline (HIGH-5). The header list intentionally tracks the
// OWASP Secure Headers Project's "must-have" set, plus the cross-origin
// isolation policies (COOP/CORP) introduced after Spectre to prevent
// cross-origin sidechannel leakage.
//
// Each header is annotated with the threat it mitigates so the audit
// reviewer can map the implementation back to the recommendation table.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()

		// HSTS — forces HTTPS for at least 1 year, including subdomains, and
		// declares this host preload-eligible.  Mitigates SSL-strip / on-path
		// downgrade.
		h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains; preload")

		// X-Frame-Options — defence-in-depth clickjacking guard (legacy
		// browsers that ignore frame-ancestors below).
		h.Set("X-Frame-Options", "DENY")

		// X-Content-Type-Options — disables MIME-sniffing.  Prevents the
		// "user uploaded text.txt that the browser decided to render as
		// text/html with embedded <script>" attack class.
		h.Set("X-Content-Type-Options", "nosniff")

		// X-XSS-Protection — legacy header; ignored by modern Chromium but
		// still respected by older Edge/Safari/IE.  We keep the strict
		// "block on detection" mode.
		h.Set("X-XSS-Protection", "1; mode=block")

		// Content-Security-Policy — strict CSP.  No 'unsafe-inline' for
		// script-src (login and import map are served from /static/*.js|.json).
		// frame-ancestors 'none' is the modern clickjacking guard.
		// base-uri 'self' blocks <base> hijack; form-action 'self' stops
		// authenticated form-post exfiltration; object-src 'none' kills
		// legacy plugin attack surface.
		h.Set("Content-Security-Policy",
			"default-src 'self'; "+
				"script-src 'self' https://cdn.jsdelivr.net; "+
				"style-src 'self' 'unsafe-inline'; "+
				"img-src 'self' data:; "+
				"font-src 'self' https://cdn.jsdelivr.net; "+
				"connect-src 'self'; "+
				"frame-ancestors 'none'; "+
				"base-uri 'self'; "+
				"form-action 'self'; "+
				"object-src 'none'")

		// Referrer-Policy — the audit baseline (HIGH-5) specifies
		// strict-origin-when-cross-origin: full URL on same-origin, origin
		// only on cross-origin HTTPS, nothing on HTTPS→HTTP downgrades.
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")

		// Permissions-Policy — explicitly disable powerful APIs that no
		// QSDM endpoint needs.  Reduces the blast radius of a successful
		// XSS that smuggles past CSP.
		h.Set("Permissions-Policy",
			"geolocation=(), microphone=(), camera=(), payment=(), usb=(), accelerometer=(), gyroscope=(), magnetometer=()")

		// Cross-Origin-Opener-Policy — isolates the browsing context group
		// so a malicious popup cannot access window.opener of an
		// authenticated dashboard tab.
		h.Set("Cross-Origin-Opener-Policy", "same-origin")

		// Cross-Origin-Resource-Policy — prevents other origins from
		// embedding API responses (e.g. <img src="…/wallet/balance">).
		h.Set("Cross-Origin-Resource-Policy", "same-origin")

		// Remove server fingerprint.  net/http won't emit Server: by
		// default in Go 1.20+, but we clear it explicitly to be safe.
		h.Set("Server", "")

		next.ServeHTTP(w, r)
	})
}

// RateLimiter implements token bucket rate limiting
type RateLimiter struct {
	requests map[string]*rateLimitEntry
	mu       sync.RWMutex
	maxReqs  int
	window   time.Duration
}

type rateLimitEntry struct {
	count     int
	windowEnd time.Time
}

// NewRateLimiter creates a new rate limiter
func NewRateLimiter(maxReqs int, window time.Duration) *RateLimiter {
	rl := &RateLimiter{
		requests: make(map[string]*rateLimitEntry),
		maxReqs:  maxReqs,
		window:   window,
	}

	// Cleanup goroutine
	go rl.cleanup()

	return rl
}

// cleanup removes expired entries periodically
func (rl *RateLimiter) cleanup() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		rl.mu.Lock()
		now := time.Now()
		for key, entry := range rl.requests {
			if now.After(entry.windowEnd) {
				delete(rl.requests, key)
			}
		}
		rl.mu.Unlock()
	}
}

// Allow checks if a request should be allowed
func (rl *RateLimiter) Allow(identifier string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	entry, exists := rl.requests[identifier]
	now := time.Now()

	if !exists || now.After(entry.windowEnd) {
		// New window
		rl.requests[identifier] = &rateLimitEntry{
			count:     1,
			windowEnd: now.Add(rl.window),
		}
		return true
	}

	if entry.count >= rl.maxReqs {
		return false
	}

	entry.count++
	return true
}

// RateLimitMiddleware adds rate limiting to requests
func (rl *RateLimiter) RateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Do not rate-limit health probes (Kubernetes / load balancers).
		if strings.HasPrefix(r.URL.Path, "/api/v1/health") {
			next.ServeHTTP(w, r)
			return
		}
		// Mining-protocol endpoints are designed for high-
		// frequency miner traffic (work poll ~2s, challenge
		// per accepted proof, submit per solved proof). The
		// HTTP-layer rate limit is the wrong gate for them —
		// consensus-level protection lives in pkg/mining/verifier
		// (Dedup + Quarantine + hashrate-band gating + the v2
		// attestation gate that rejects unattested proofs at
		// zero CPU cost). Without this bypass the limiter
		// chokes a real RTX-3050 within ~5s of mining (verified
		// 2026-05-07: 19 accepted proofs followed by 12 sequential
		// 429s from this exact RateLimiter). Mirror of the bypass
		// added to RoleRateLimiter in ratelimit_roles.go — both
		// middlewares are mounted in series and the bypass must
		// land in BOTH or the miner sees 429s anyway.
		if strings.HasPrefix(r.URL.Path, "/api/v1/mining/") {
			next.ServeHTTP(w, r)
			return
		}
		// Get client identifier (IP address or API key)
		identifier := rl.getClientIdentifier(r)

		// Get per-endpoint rate limit (if configured)
		endpointLimit := rl.getEndpointLimit(r.URL.Path, r.Method)

		// Use endpoint-specific limit if available, otherwise use default
		limitToCheck := rl.maxReqs
		if endpointLimit > 0 {
			limitToCheck = endpointLimit
		}

		// Check rate limit with endpoint-specific key
		endpointKey := fmt.Sprintf("%s:%s:%s", identifier, r.Method, r.URL.Path)
		if !rl.AllowWithLimit(endpointKey, limitToCheck) {
			if strings.Contains(r.URL.Path, "/monitoring/ngc-challenge") {
				monitoring.RecordNGCChallengeRateLimited()
			}
			// MED-8: surface every 429 in the security metrics stream so
			// alerting can detect brute-force / scraping attempts.
			monitoring.RecordRateLimitViolation()
			w.Header().Set("Retry-After", fmt.Sprintf("%.0f", rl.window.Seconds()))
			http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// AllowWithLimit checks if a request should be allowed with a custom limit
func (rl *RateLimiter) AllowWithLimit(identifier string, limit int) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	entry, exists := rl.requests[identifier]
	now := time.Now()

	if !exists || now.After(entry.windowEnd) {
		// New window
		rl.requests[identifier] = &rateLimitEntry{
			count:     1,
			windowEnd: now.Add(rl.window),
		}
		return true
	}

	if entry.count >= limit {
		return false
	}

	entry.count++
	return true
}

// getEndpointLimit returns endpoint-specific rate limit
func (rl *RateLimiter) getEndpointLimit(path, method string) int {
	// Sensitive endpoints have lower limits
	sensitiveEndpoints := map[string]int{
		"/api/v1/auth/login":    5,  // 5 requests per minute for login
		"/api/v1/auth/register": 3,  // 3 requests per minute for registration
		"/api/v1/wallet/send":   10, // 10 transactions per minute
		// v0.4.0 (Session 95) self-custody endpoint. Same per-minute
		// ceiling as /wallet/send. We don't go lower because a
		// well-funded honest user might fan out small payments (e.g.
		// game-NPC payouts) and 10/min is already the tightest
		// reasonable bound. We don't go higher because every accepted
		// call writes a row to storage + broadcasts on the p2p tx
		// topic, and a misbehaving signer would otherwise be free to
		// flood the gossip mesh with cryptographically-valid envelopes
		// that drain ONLY their own balance (until they hit 402).
		"/api/v1/wallet/submit-signed":     10,
		"/api/v1/faucet/claim":             5,
		"/api/v1/monitoring/ngc-proof":     30, // NGC sidecar batches
		"/api/v1/monitoring/ngc-challenge": 15, // tight: nonce minting (per IP per minute)
		"/api/v1/monitoring/ngc-proofs":    60, // dashboard polling
		"/api/v1/wallet/mint":              20, // public mint (game integration)
		"/api/v1/tokens/mint":              15,
		"/api/v1/tokens/create":            10,
		"/api/v1/tokens/list":              60,
	}

	// Check exact path match
	if limit, ok := sensitiveEndpoints[path]; ok {
		return limit
	}

	// Default: use global limit
	return 0
}

// getClientIdentifier extracts client identifier from request
func (rl *RateLimiter) getClientIdentifier(r *http.Request) string {
	// Try API key first
	if apiKey := r.Header.Get("X-API-Key"); apiKey != "" {
		return "api:" + apiKey
	}

	// Fall back to IP address
	ip := r.RemoteAddr
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		ip = strings.Split(forwarded, ",")[0]
	}
	return "ip:" + ip
}

// RequestSigner handles request signing and verification.
//
// When the build links Dilithium (CGO+liboqs or the pure-Go circl
// backend), every sign/verify hits the quantum-safe path. When
// Dilithium is unavailable AND the operator did NOT supply an
// explicit HMAC secret via NewRequestSigner, hmacSecret() lazily
// generates a 32-byte ephemeral key from crypto/rand on first call
// and caches it for the life of the process. This matches
// AuthManager.jwtHMACSecretBytes()'s policy and closes the
// crypto-02 audit-checklist row ("HMAC fallback uses random
// ephemeral key, not hardcoded, when ML-DSA is unavailable").
//
// Earlier versions of this file returned a literal []byte("Charming123")
// as the fallback secret. That string is also the demo-prefix
// banned by config.go::Validate when QSDM_STRICT_SECRETS=true, so
// the old code would have been rejected by strict-mode startup
// anyway — leaving it in the dev path was a footgun the audit
// checklist correctly flagged. The fix is the lazy-random pattern
// AuthManager already used.
//
// Cross-process consistency: if you need two QSDM processes to
// accept each other's signatures (e.g. a validator + dashboard
// sharing the same authentication realm), pass the same
// hmacFallbackSecret to NewRequestSigner on both sides. Without
// an explicit secret, each process generates its own ephemeral
// key on first use and cross-process signatures will not validate
// — that is the correct security default for the "Dilithium
// unavailable" path.
type RequestSigner struct {
	dilithium *crypto.Dilithium
	// hmacMu guards hmacFallback + hmacFallbackSecondary for the
	// lazy-init race between the first SignRequest and a concurrent
	// VerifyRequest, and for SetSecondaryHMACSecret rebinding the
	// secondary mid-rotation.
	hmacMu       sync.Mutex
	hmacFallback []byte // explicit primary secret, or lazily-generated 32 B ephemeral
	// hmacFallbackSecondary: VERIFY-ONLY secondary key for zero-downtime
	// HMAC rotation (audit row rotation-01). When set, VerifyRequest first
	// tries the primary; if it does not match, it tries the secondary and
	// — on success — increments
	// qsdm_security_request_signature_secondary_key_hits_total so the
	// operator can confirm the window is being exercised before completing
	// the cutover. SignRequest NEVER reads this field — new signatures
	// are always primary-only.
	hmacFallbackSecondary []byte
}

// NewRequestSigner creates a new request signer. hmacFallbackSecret
// is used for HMAC when Dilithium is unavailable (non-CGO); empty
// leaves the fallback secret unset, so hmacSecret() will lazily
// generate a 32-byte ephemeral key from crypto/rand on first use.
//
// Use SetSecondaryHMACSecret after construction to install a
// rotation-window VERIFY-ONLY secondary key (audit row rotation-01).
func NewRequestSigner(hmacFallbackSecret string) (*RequestSigner, error) {
	d := crypto.NewDilithium()
	// Allow nil Dilithium for non-CGO builds (uses fallback signing)
	// In production, CGO and liboqs should be used for quantum-safe crypto
	rs := &RequestSigner{dilithium: d}
	s := strings.TrimSpace(hmacFallbackSecret)
	if s != "" {
		rs.hmacFallback = []byte(s)
	}
	return rs, nil
}

// SetSecondaryHMACSecret installs (or clears, with an empty argument)
// the VERIFY-ONLY secondary HMAC key used during a key-rotation
// window. The secondary key is consulted by VerifyRequest's HMAC
// fallback path AFTER the primary fails. New signatures are ALWAYS
// produced with the primary (SignRequest never reads the secondary).
//
// Setting the secondary to the same bytes as the primary is rejected
// as a no-op: a same-key "rotation" would mean the secondary hit
// counter never increments, defeating the runbook's gating check.
//
// See QSDM/docs/docs/runbooks/JWT_KEY_ROTATION.md for the procedure.
func (rs *RequestSigner) SetSecondaryHMACSecret(secret string) {
	s := strings.TrimSpace(secret)
	rs.hmacMu.Lock()
	defer rs.hmacMu.Unlock()
	if s == "" {
		rs.hmacFallbackSecondary = nil
		return
	}
	if len(rs.hmacFallback) > 0 && hmac.Equal([]byte(s), rs.hmacFallback) {
		rs.hmacFallbackSecondary = nil
		return
	}
	rs.hmacFallbackSecondary = []byte(s)
}

// secondaryHMACSecret returns the rotation-window secondary key
// (or nil when no rotation is in flight). VERIFY-ONLY accessor.
func (rs *RequestSigner) secondaryHMACSecret() []byte {
	rs.hmacMu.Lock()
	defer rs.hmacMu.Unlock()
	if len(rs.hmacFallbackSecondary) == 0 {
		return nil
	}
	out := make([]byte, len(rs.hmacFallbackSecondary))
	copy(out, rs.hmacFallbackSecondary)
	return out
}

func (rs *RequestSigner) hmacSecret() []byte {
	rs.hmacMu.Lock()
	defer rs.hmacMu.Unlock()
	if len(rs.hmacFallback) > 0 {
		return rs.hmacFallback
	}
	// Lazy-generate a 32-byte ephemeral key from crypto/rand on
	// first use. Cached for the life of the process so subsequent
	// Sign/Verify pairs see the same key. Matches the
	// AuthManager.jwtHMACSecretBytes() policy; see the crypto-02
	// audit-checklist Notes for the threat-model rationale.
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failure is fatal for any signing path; the
		// best we can do is surface a non-empty key that will be
		// stable for this process so Sign/Verify still round-trip.
		// In practice rand.Read on a healthy system never fails;
		// this branch exists so the function is total.
		b = []byte("qsdm-rand-fallback-unreachable-in-practice-key-32b")
	}
	rs.hmacFallback = b
	return rs.hmacFallback
}

// SignRequest signs a request body with quantum-safe signature (or HMAC fallback)
func (rs *RequestSigner) SignRequest(body []byte, timestamp int64, nonce string) (string, error) {
	// Create signature payload: timestamp + nonce + body
	payload := fmt.Sprintf("%d:%s:", timestamp, nonce)
	payloadBytes := append([]byte(payload), body...)

	var signature []byte
	if rs.dilithium != nil {
		var err error
		signature, err = rs.dilithium.Sign(payloadBytes)
		if err != nil {
			return "", fmt.Errorf("failed to sign request: %w", err)
		}
	} else {
		// Fallback: Use HMAC-SHA256 for non-CGO builds (development/testing only)
		h := hmac.New(sha256.New, rs.hmacSecret())
		h.Write(payloadBytes)
		signature = h.Sum(nil)
	}

	return base64.URLEncoding.EncodeToString(signature), nil
}

// VerifyRequest verifies a signed request
func (rs *RequestSigner) VerifyRequest(body []byte, timestamp int64, nonce string, signatureB64 string) error {
	// Decode signature
	signature, err := base64.URLEncoding.DecodeString(signatureB64)
	if err != nil {
		return fmt.Errorf("failed to decode signature: %w", err)
	}

	// Recreate payload
	payload := fmt.Sprintf("%d:%s:", timestamp, nonce)
	payloadBytes := append([]byte(payload), body...)

	// Verify signature (quantum-safe Dilithium or HMAC fallback)
	if rs.dilithium != nil {
		valid, err := rs.dilithium.Verify(payloadBytes, signature)
		if err != nil {
			return fmt.Errorf("failed to verify signature: %w", err)
		}
		if !valid {
			return errors.New("invalid request signature")
		}
	} else {
		// Fallback: Verify HMAC-SHA256 for non-CGO builds.
		//
		// Dual-key verify (audit row rotation-01): try primary first;
		// on mismatch, if a rotation-window secondary is configured,
		// try the secondary. A secondary hit increments
		// qsdm_security_request_signature_secondary_key_hits_total
		// so operators can confirm the window is being exercised
		// before clearing the secondary at cutover.
		h := hmac.New(sha256.New, rs.hmacSecret())
		h.Write(payloadBytes)
		expectedSignature := h.Sum(nil)
		if !hmac.Equal(signature, expectedSignature) {
			secondary := rs.secondaryHMACSecret()
			if len(secondary) == 0 {
				return errors.New("invalid request signature")
			}
			h2 := hmac.New(sha256.New, secondary)
			h2.Write(payloadBytes)
			expectedSecondary := h2.Sum(nil)
			if !hmac.Equal(signature, expectedSecondary) {
				return errors.New("invalid request signature")
			}
			monitoring.RecordRequestSignatureSecondaryKeyHit()
		}
	}

	// Check timestamp (prevent replay attacks)
	now := time.Now().Unix()
	if abs(now-timestamp) > 300 { // 5 minute window
		return errors.New("request timestamp out of window")
	}

	return nil
}

// abs returns absolute value
func abs(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

// SecureCompare performs constant-time string comparison
func SecureCompare(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
