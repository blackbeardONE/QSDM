package api

import (
	"crypto/hmac"
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

// SecurityHeaders adds military-grade security headers
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// HSTS - Force HTTPS
		w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains; preload")
		
		// X-Frame-Options - Prevent clickjacking
		w.Header().Set("X-Frame-Options", "DENY")
		
		// X-Content-Type-Options - Prevent MIME sniffing
		w.Header().Set("X-Content-Type-Options", "nosniff")
		
		// X-XSS-Protection
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		
		// Content-Security-Policy - Strict CSP (no script-src unsafe-inline: login + import map use /static/*.js|.json)
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self' https://cdn.jsdelivr.net; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self' https://cdn.jsdelivr.net; connect-src 'self'; frame-ancestors 'none';")
		
		// Referrer-Policy
		w.Header().Set("Referrer-Policy", "no-referrer")
		
		// Permissions-Policy
		w.Header().Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
		
		// Remove server information
		w.Header().Set("Server", "")
		
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
		"/api/v1/auth/register": 3, // 3 requests per minute for registration
		"/api/v1/wallet/send":  10, // 10 transactions per minute
		"/api/v1/monitoring/ngc-proof":    30, // NGC sidecar batches
		"/api/v1/monitoring/ngc-challenge": 15, // tight: nonce minting (per IP per minute)
		"/api/v1/monitoring/ngc-proofs":   60, // dashboard polling
		"/api/v1/wallet/mint":           20, // public mint (game integration)
		"/api/v1/tokens/mint":           15,
		"/api/v1/tokens/create":         10,
		"/api/v1/tokens/list":           60,
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

// RequestSigner handles request signing and verification
type RequestSigner struct {
	dilithium     *crypto.Dilithium
	hmacFallback  []byte // used when dilithium is nil; defaults in hmacSecret() if unset
}

// NewRequestSigner creates a new request signer. hmacFallbackSecret is used for HMAC when Dilithium is unavailable (non-CGO); empty keeps the dev default.
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

func (rs *RequestSigner) hmacSecret() []byte {
	if len(rs.hmacFallback) > 0 {
		return rs.hmacFallback
	}
	return []byte("Charming123")
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
		// Fallback: Verify HMAC-SHA256 for non-CGO builds
		h := hmac.New(sha256.New, rs.hmacSecret())
		h.Write(payloadBytes)
		expectedSignature := h.Sum(nil)
		if !hmac.Equal(signature, expectedSignature) {
			return errors.New("invalid request signature")
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

