package api

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// RoleTier defines rate limit parameters for a role.
type RoleTier struct {
	MaxRequests int           `json:"max_requests"` // per window
	Window      time.Duration `json:"window"`
}

// RoleRateLimiterConfig maps role names to their tier.
type RoleRateLimiterConfig struct {
	Admin     RoleTier `json:"admin"`
	User      RoleTier `json:"user"`
	Anonymous RoleTier `json:"anonymous"`
}

// DefaultRoleRateLimiterConfig returns sensible defaults for each tier.
func DefaultRoleRateLimiterConfig() RoleRateLimiterConfig {
	return RoleRateLimiterConfig{
		Admin:     RoleTier{MaxRequests: 600, Window: time.Minute},
		User:      RoleTier{MaxRequests: 120, Window: time.Minute},
		Anonymous: RoleTier{MaxRequests: 30, Window: time.Minute},
	}
}

type roleBucket struct {
	count     int
	windowEnd time.Time
}

// RoleRateLimiter applies per-role rate limits based on authenticated claims.
type RoleRateLimiter struct {
	config  RoleRateLimiterConfig
	buckets map[string]*roleBucket
	mu      sync.Mutex
}

// NewRoleRateLimiter creates a role-aware rate limiter.
func NewRoleRateLimiter(cfg RoleRateLimiterConfig) *RoleRateLimiter {
	rl := &RoleRateLimiter{
		config:  cfg,
		buckets: make(map[string]*roleBucket),
	}
	go rl.cleanup()
	return rl
}

func (rl *RoleRateLimiter) cleanup() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		rl.mu.Lock()
		now := time.Now()
		for key, b := range rl.buckets {
			if now.After(b.windowEnd) {
				delete(rl.buckets, key)
			}
		}
		rl.mu.Unlock()
	}
}

func (rl *RoleRateLimiter) tierFor(role string) RoleTier {
	switch role {
	case "admin":
		return rl.config.Admin
	case "user":
		return rl.config.User
	default:
		return rl.config.Anonymous
	}
}

// Allow checks whether a request from the given identifier+role is permitted.
func (rl *RoleRateLimiter) Allow(identifier, role string) bool {
	tier := rl.tierFor(role)
	key := fmt.Sprintf("%s:%s", role, identifier)

	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	b, exists := rl.buckets[key]
	if !exists || now.After(b.windowEnd) {
		rl.buckets[key] = &roleBucket{count: 1, windowEnd: now.Add(tier.Window)}
		return true
	}
	if b.count >= tier.MaxRequests {
		return false
	}
	b.count++
	return true
}

// Middleware returns HTTP middleware that applies role-based rate limiting.
// Claims are expected in context under key "claims" (set by auth middleware).
func (rl *RoleRateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/health") || r.URL.Path == "/api/v1/status" {
			next.ServeHTTP(w, r)
			return
		}
		// Mining-protocol endpoints are designed for high-
		// frequency miner traffic (the canonical /work poll
		// is every ~2s, /challenge is every accepted proof,
		// /submit is one per solved proof). The 30/min
		// anonymous tier was set for occasional UI browsing
		// and chokes any real miner within a single minute.
		// Consensus-level abuse protection for these paths
		// lives where it belongs — pkg/mining/verifier
		// Dedup + Quarantine + hashrate-band gating, plus
		// the v2 attestation gate that rejects unattested
		// proofs at zero CPU cost. The HTTP-layer rate
		// limit is redundant here and operationally
		// harmful, so bypass it.
		if strings.HasPrefix(r.URL.Path, "/api/v1/mining/") {
			next.ServeHTTP(w, r)
			return
		}
		// Public read-only transparency surface. The anonymous tier is 30/min
		// and every unauthenticated caller shares one bucket keyed on the TLS
		// terminator's address, so this tier -- not the pre-auth limiter -- is
		// what returns 429 to the external probe first. See
		// ratelimit_transparency.go. Must stay mirrored in RateLimiter.
		if isPublicTransparencyRead(r) {
			next.ServeHTTP(w, r)
			return
		}
		// High-frequency public reads are polled by Hive, validators, explorers,
		// and status widgets. Do not spend the small anonymous role bucket here;
		// the pre-auth RateLimiter keeps a sized per-path ceiling.
		if isHighFrequencyPublicRead(r) {
			next.ServeHTTP(w, r)
			return
		}
		// NGC attestation ingest. These carry a shared-secret header rather
		// than claims, so they are "anonymous" here and were being starved out
		// of the single 30/min bucket by unrelated traffic -- which is what
		// dropped the trust surface to 0 fresh attestations. Their sized
		// per-path caps in RateLimiter are the correct gate and still apply.
		// See ratelimit_ngc.go.
		if isNGCMonitoringIngest(r) {
			next.ServeHTTP(w, r)
			return
		}

		role := "anonymous"
		identifier := clientIP(r)

		if claims, ok := ClaimsFromContext(r.Context()); ok {
			if claims.Role != "" {
				role = claims.Role
			}
			if claims.Address != "" {
				identifier = claims.Address
			}
		}

		if !rl.Allow(identifier, role) {
			tier := rl.tierFor(role)
			w.Header().Set("Retry-After", fmt.Sprintf("%.0f", tier.Window.Seconds()))
			w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%d", tier.MaxRequests))
			http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// trustProxyHeaders controls whether proxy-supplied client identity is
// honoured. It defaults to false, because a caller-supplied header used as a
// bucket key is a key the caller can rotate -- the same defect that made the
// removed X-API-Key path unbounded.
//
// When enabled, identity is taken from X-Real-IP first. That header is SET by
// the trusted proxy (Caddy: `header_up X-Real-IP {remote_host}` on every
// route), so a client-supplied value is overwritten and cannot survive.
// X-Forwarded-For is only a fallback, and only its RIGHTMOST entry is used,
// because a reverse proxy APPENDS the peer address: everything to the left is
// whatever the client sent. Reading the leftmost entry -- as this function
// previously did -- would have let any caller mint a fresh bucket per request
// simply by sending its own X-Forwarded-For.
//
// Enable only where the API port is unreachable except through that proxy.
var trustProxyHeaders atomic.Bool

// SetTrustProxyHeaders declares whether this node sits behind a trusted
// reverse proxy that sets X-Real-IP. Enable it ONLY when the API port is
// unreachable except through that proxy; otherwise a direct caller supplies
// its own identity and rate limiting becomes bypassable by header rotation.
func SetTrustProxyHeaders(trust bool) { trustProxyHeaders.Store(trust) }

// TrustProxyHeaders reports the current setting.
func TrustProxyHeaders() bool { return trustProxyHeaders.Load() }

// parseClientIP normalises one candidate address, rejecting anything that is
// not a valid IP. Normalising through net.IP means "1.2.3.4", " 1.2.3.4" and
// "::ffff:1.2.3.4" cannot be spent as three separate buckets.
func parseClientIP(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if ip := net.ParseIP(raw); ip != nil {
		return ip.String()
	}
	if host, _, err := net.SplitHostPort(raw); err == nil {
		if ip := net.ParseIP(strings.TrimSpace(host)); ip != nil {
			return ip.String()
		}
	}
	return ""
}

func clientIP(r *http.Request) string {
	if trustProxyHeaders.Load() {
		// Set, not appended, by the trusted proxy.
		if ip := parseClientIP(r.Header.Get("X-Real-IP")); ip != "" {
			return ip
		}
		// Fallback. The rightmost entry is the one our own proxy appended;
		// everything left of it is client-controlled.
		if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
			parts := strings.Split(forwarded, ",")
			if ip := parseClientIP(parts[len(parts)-1]); ip != "" {
				return ip
			}
		}
		// Unparseable proxy headers fall through to RemoteAddr, which is the
		// proxy itself: one shared bucket, i.e. today's behaviour.
	}
	// RemoteAddr is host:port; strip the port so a client cannot mint a
	// new bucket per source port.
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil && host != "" {
		return host
	}
	return r.RemoteAddr
}
