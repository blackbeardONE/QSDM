package api

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
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

		role := "anonymous"
		identifier := clientIP(r)

		if claims, ok := r.Context().Value("claims").(*Claims); ok && claims != nil {
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

func clientIP(r *http.Request) string {
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		return strings.Split(forwarded, ",")[0]
	}
	return r.RemoteAddr
}
