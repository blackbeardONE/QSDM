package api

import (
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/monitoring"
)

// CORSConfig drives the CORSMiddleware behaviour. All fields default to
// secure (deny-by-default) values; an operator who wants to allow browser
// callers MUST set AllowedOrigins explicitly.
type CORSConfig struct {
	// AllowedOrigins lists exact origins (scheme://host[:port]) that are
	// allowed to call the API from a browser. The single wildcard "*" is
	// honoured but ONLY when AllowCredentials is false — combining "*"
	// with credentialed requests is forbidden by the Fetch spec and is
	// silently ignored by every modern browser.
	AllowedOrigins []string

	// AllowedMethods lists CORS-permitted HTTP methods. Defaults to the
	// minimum useful set if empty.
	AllowedMethods []string

	// AllowedHeaders lists request headers that may appear in the
	// preflight Access-Control-Request-Headers list.
	AllowedHeaders []string

	// ExposedHeaders is the list of response headers a browser script
	// is allowed to read (in addition to the safelisted defaults).
	ExposedHeaders []string

	// AllowCredentials toggles Access-Control-Allow-Credentials. Only
	// set to true when the API actually authenticates browsers via
	// cookies — Bearer-token API clients do not need this.
	AllowCredentials bool

	// MaxAge bounds how long a browser may cache the preflight result.
	// Default 600s (10 minutes) — long enough to amortise OPTIONS, short
	// enough that an origin-allowlist change propagates quickly.
	MaxAge time.Duration
}

// NormalizedAllowedOrigins lower-cases and de-duplicates the allowlist
// once at config-load time so the hot path is an O(N) string match
// instead of an O(N) ToLower+match.
func (c *CORSConfig) NormalizedAllowedOrigins() []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(c.AllowedOrigins))
	for _, o := range c.AllowedOrigins {
		o = strings.TrimSpace(strings.ToLower(o))
		if o == "" {
			continue
		}
		if _, ok := seen[o]; ok {
			continue
		}
		seen[o] = struct{}{}
		out = append(out, o)
	}
	return out
}

// CORSMiddleware enforces the Cross-Origin Resource Sharing protocol.
//
// Threat model: a browser script at https://evil.example tries to call the
// authenticated API at https://api.qsdm.local using the victim's session.
// Without CORS, the browser sends the request and only blocks the script
// from reading the response — but a state-changing POST still executes.
// With this middleware, the server simply refuses requests whose Origin is
// not on the allowlist BEFORE any handler runs.
//
// Requests with no Origin header (i.e. server-to-server, CLI tools) are
// passed through unchanged: CORS is a browser-only concern.
func CORSMiddleware(cfg *CORSConfig) func(http.Handler) http.Handler {
	if cfg == nil {
		cfg = &CORSConfig{}
	}

	allowedOrigins := cfg.NormalizedAllowedOrigins()
	allowedMethods := cfg.AllowedMethods
	if len(allowedMethods) == 0 {
		allowedMethods = []string{"GET", "POST", "PUT", "DELETE", "PATCH", "OPTIONS"}
	}
	allowedHeaders := cfg.AllowedHeaders
	if len(allowedHeaders) == 0 {
		allowedHeaders = []string{"Authorization", "Content-Type", CSRFHeader, "X-Timestamp", "X-Nonce", "X-Signature"}
	}
	maxAge := cfg.MaxAge
	if maxAge <= 0 {
		maxAge = 10 * time.Minute
	}

	allowWildcard := false
	for _, o := range allowedOrigins {
		if o == "*" {
			allowWildcard = true
			break
		}
	}

	originAllowed := func(origin string) bool {
		if origin == "" {
			return false
		}
		lc := strings.ToLower(origin)
		for _, o := range allowedOrigins {
			if o == lc {
				return true
			}
		}
		return false
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")

			// No Origin header → not a browser request. Pass through.
			if origin == "" {
				next.ServeHTTP(w, r)
				return
			}

			matched := originAllowed(origin)
			useWildcard := false
			if !matched && allowWildcard && !cfg.AllowCredentials {
				useWildcard = true
				matched = true
			}

			if !matched {
				monitoring.RecordCORSRejection()
				// Per the Fetch spec we MUST NOT emit any Access-Control-*
				// header on a denied preflight — the browser will surface
				// the failure to the script via a network error.
				if r.Method == http.MethodOptions {
					w.WriteHeader(http.StatusForbidden)
					return
				}
				writeErrorResponse(w, http.StatusForbidden, "origin not allowed")
				return
			}

			if useWildcard {
				w.Header().Set("Access-Control-Allow-Origin", "*")
			} else {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				// Vary: Origin is mandatory whenever the response varies on
				// Origin — without it, downstream caches happily serve the
				// wrong origin's ACAO header to the next caller.
				w.Header().Add("Vary", "Origin")
			}
			if cfg.AllowCredentials && !useWildcard {
				w.Header().Set("Access-Control-Allow-Credentials", "true")
			}
			if len(cfg.ExposedHeaders) > 0 {
				w.Header().Set("Access-Control-Expose-Headers", strings.Join(cfg.ExposedHeaders, ", "))
			}

			if r.Method == http.MethodOptions {
				w.Header().Set("Access-Control-Allow-Methods", strings.Join(allowedMethods, ", "))
				w.Header().Set("Access-Control-Allow-Headers", strings.Join(allowedHeaders, ", "))
				w.Header().Set("Access-Control-Max-Age", strconv.Itoa(int(maxAge.Seconds())))
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// corsConfigCacheKey is the once.Do-style guard that prevents repeated
// reparsing of the same config string on every request hot path.
type corsConfigCache struct {
	once sync.Once
	cfg  *CORSConfig
}

var defaultCORSCache corsConfigCache

// LoadCORSConfigFromEnv parses the QSDM_CORS_ALLOWED_ORIGINS env var (and
// optional companions) into a CORSConfig. Empty environment → empty
// allowlist → all browser requests rejected (the safe default).
//
//	QSDM_CORS_ALLOWED_ORIGINS — comma-separated list of allowed origins
//	QSDM_CORS_ALLOW_CREDENTIALS — "true" enables credentialed CORS
func LoadCORSConfigFromEnv(getenv func(string) string) *CORSConfig {
	if getenv == nil {
		return &CORSConfig{}
	}
	cfg := &CORSConfig{}
	if raw := strings.TrimSpace(getenv("QSDM_CORS_ALLOWED_ORIGINS")); raw != "" {
		for _, part := range strings.Split(raw, ",") {
			if v := strings.TrimSpace(part); v != "" {
				cfg.AllowedOrigins = append(cfg.AllowedOrigins, v)
			}
		}
	}
	if v := strings.ToLower(strings.TrimSpace(getenv("QSDM_CORS_ALLOW_CREDENTIALS"))); v == "1" || v == "true" || v == "yes" || v == "on" {
		cfg.AllowCredentials = true
	}
	return cfg
}
