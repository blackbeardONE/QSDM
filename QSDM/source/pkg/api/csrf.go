package api

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/monitoring"
)

// Public names for the CSRF handshake. Clients (browsers, SDKs) fetch a token
// from CSRFTokenEndpoint, persist it (e.g. in a hidden form field or JS
// variable), and send it back in CSRFHeader on every state-changing request.
//
// The server stores the same token in a Secure, SameSite=Strict cookie
// (CSRFCookie). Middleware enforces BOTH:
//   1) the header value matches the cookie value (double-submit pattern), AND
//   2) the value is present in the in-memory store and not expired
//      (synchronizer token pattern, scoped to the issuing user if any).
//
// Either pattern alone is acceptable per OWASP, but combining them is cheap
// and defends against attackers who can:
//   - inject arbitrary cookies via a sibling subdomain (defeats raw
//     double-submit) — caught by (2),
//   - read the in-memory store via a separate vulnerability (e.g. log leak) —
//     caught by (1),
//   - replay a token issued to another user — caught by user binding.
const (
	CSRFTokenEndpoint = "/api/v1/csrf-token"
	CSRFHeader        = "X-CSRF-Token"
	CSRFCookie        = "qsdm_csrf"

	defaultCSRFTokenTTL        = 1 * time.Hour
	defaultCSRFTokenSize       = 32 // 256 bits
	defaultCSRFCleanupInterval = 5 * time.Minute
)

// CSRFManager manages CSRF token generation and validation.
//
// Tokens are stored in-memory keyed by the token string itself. A single
// background goroutine started by NewCSRFManager evicts expired entries on a
// fixed interval; callers MUST invoke Stop() to release the goroutine when
// the manager is no longer needed (typically in tests — production servers
// keep the manager alive for the process lifetime).
type CSRFManager struct {
	tokens          map[string]*csrfToken
	mu              sync.RWMutex
	tokenTTL        time.Duration
	tokenSize       int
	cleanupInterval time.Duration

	stopCh   chan struct{}
	stopOnce sync.Once
}

type csrfToken struct {
	token     string
	userID    string // optional binding; empty for anonymous tokens
	expiresAt time.Time
}

// NewCSRFManager creates a new CSRF manager and starts its background
// cleanup goroutine. Call Stop() to terminate the goroutine.
func NewCSRFManager() *CSRFManager {
	return newCSRFManagerWithIntervals(defaultCSRFTokenTTL, defaultCSRFCleanupInterval)
}

// newCSRFManagerWithIntervals constructs a CSRFManager with caller-supplied
// tokenTTL and cleanupInterval and starts its background cleanup goroutine
// AFTER the fields are populated. Package-private so tests can build a
// fast-cycling manager without racing against the production constructor.
//
// History: TestCSRF_BackgroundCleanupEvictsExpired used to mutate
// cm.cleanupInterval AFTER calling NewCSRFManager(), which had already
// `go cm.runCleanup()`'d a goroutine that captured the field via
// time.NewTicker(cm.cleanupInterval) at goroutine entry. The mutation was
// a data race against the goroutine's read AND a logical race against
// the ticker construction: depending on scheduler ordering, the ticker
// could end up using the 5-minute default and the eviction test would
// time out in CI. Routing all construction through this helper makes the
// field assignment happens-before the goroutine start, eliminating both
// hazards in one constructor call.
func newCSRFManagerWithIntervals(tokenTTL, cleanupInterval time.Duration) *CSRFManager {
	cm := &CSRFManager{
		tokens:          make(map[string]*csrfToken),
		tokenTTL:        tokenTTL,
		tokenSize:       defaultCSRFTokenSize,
		cleanupInterval: cleanupInterval,
		stopCh:          make(chan struct{}),
	}
	go cm.runCleanup()
	return cm
}

// Stop terminates the background cleanup goroutine. Safe to call multiple
// times; only the first call has an effect.
func (cm *CSRFManager) Stop() {
	cm.stopOnce.Do(func() { close(cm.stopCh) })
}

// runCleanup is the periodic eviction loop. Mirrors the pattern used by
// RateLimiter.cleanup() in security.go: one goroutine, ticker-driven, locks
// only briefly to scan and delete.
func (cm *CSRFManager) runCleanup() {
	ticker := time.NewTicker(cm.cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-cm.stopCh:
			return
		case <-ticker.C:
			cm.evictExpired()
		}
	}
}

func (cm *CSRFManager) evictExpired() {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	now := time.Now()
	for token, entry := range cm.tokens {
		if now.After(entry.expiresAt) {
			delete(cm.tokens, token)
		}
	}
}

// GenerateToken generates a new anonymous CSRF token. Equivalent to
// GenerateTokenForUser("").
func (cm *CSRFManager) GenerateToken() (string, error) {
	return cm.GenerateTokenForUser("")
}

// GenerateTokenForUser generates a new CSRF token optionally bound to a
// user ID. When userID is non-empty, ValidateToken will require the
// validating request to match (defends against cross-user token replay if
// an attacker steals one token but not the matching session).
func (cm *CSRFManager) GenerateTokenForUser(userID string) (string, error) {
	tokenBytes := make([]byte, cm.tokenSize)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", fmt.Errorf("failed to generate CSRF token: %w", err)
	}
	token := base64.URLEncoding.EncodeToString(tokenBytes)

	cm.mu.Lock()
	cm.tokens[token] = &csrfToken{
		token:     token,
		userID:    userID,
		expiresAt: time.Now().Add(cm.tokenTTL),
	}
	cm.mu.Unlock()

	return token, nil
}

// ValidateToken validates a CSRF token. expectedUserID is matched against
// the token's recorded binding when the token was issued with a non-empty
// userID; pass "" when no session context is available.
func (cm *CSRFManager) ValidateToken(token string) error {
	return cm.ValidateTokenForUser(token, "")
}

// ValidateTokenForUser is the user-aware companion to ValidateToken.
func (cm *CSRFManager) ValidateTokenForUser(token, expectedUserID string) error {
	if token == "" {
		return errors.New("CSRF token is required")
	}

	cm.mu.RLock()
	stored, exists := cm.tokens[token]
	cm.mu.RUnlock()

	if !exists {
		return errors.New("invalid CSRF token")
	}

	// Expiration check: a freshly-expired token is a soft-fail so the
	// caller sees a deterministic "expired" message instead of a
	// timing-dependent "invalid" once the cleanup goroutine wins.
	if time.Now().After(stored.expiresAt) {
		cm.mu.Lock()
		delete(cm.tokens, token)
		cm.mu.Unlock()
		return errors.New("CSRF token expired")
	}

	// User binding: only enforced when the token was issued with one.
	// Anonymous tokens (userID=="") accept any caller — they exist so
	// pre-login pages can carry a CSRF value for login form POSTs.
	if stored.userID != "" {
		if subtle.ConstantTimeCompare([]byte(stored.userID), []byte(expectedUserID)) != 1 {
			return errors.New("CSRF token user mismatch")
		}
	}

	return nil
}

// IssueToken mints a fresh CSRF token, writes it to the response as a
// Secure/SameSite=Strict cookie (CSRFCookie), and returns the raw value so
// the caller can embed it in the response body. The cookie's MaxAge matches
// the in-memory TTL so a stale cookie cannot outlive its server-side record.
//
// secure should be true on HTTPS responses (set Cookie.Secure). The handler
// passes r.TLS != nil so the cookie is hardened in production but still
// usable for local HTTP development.
func (cm *CSRFManager) IssueToken(w http.ResponseWriter, userID string, secure bool) (string, error) {
	token, err := cm.GenerateTokenForUser(userID)
	if err != nil {
		return "", err
	}
	// #nosec G124 -- HttpOnly intentionally false: the double-submit cookie
	// pattern requires the client SDK to read this cookie and echo its value
	// into CSRFHeader on every state-changing request. The token is bound to
	// the authenticated user (claims.UserID) and validated by
	// ValidateTokenForUser, so JS-readability does not expand the attack
	// surface against the synchronizer-token check. Secure+SameSite=Strict
	// + CSRF middleware together neutralise the standard CSRF threat model.
	http.SetCookie(w, &http.Cookie{
		Name:     CSRFCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: false,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(cm.tokenTTL.Seconds()),
	})
	return token, nil
}

// CSRFMiddleware validates CSRF tokens for state-changing requests.
//
// The middleware applies the following rules in order:
//  1. Safe methods (GET / HEAD / OPTIONS) bypass the check entirely.
//  2. Public endpoints (isPublicEndpoint) bypass — these are read-only or
//     cryptographically self-authenticating (e.g. /wallet/submit-signed).
//  3. Requests carrying an Authorization: Bearer <jwt> header bypass.
//     CSRF only applies to ambient credentials (cookies, HTTP Basic). API
//     clients that explicitly attach a Bearer token are immune by
//     construction.
//  4. Remaining requests must present a non-empty X-CSRF-Token. If a
//     qsdm_csrf cookie is present, header value MUST equal cookie value
//     (double-submit). The token MUST also exist in the manager's store
//     and (if user-bound) match the authenticated claims.UserID.
func CSRFMiddleware(csrfManager *CSRFManager) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
				next.ServeHTTP(w, r)
				return
			}

			if isPublicEndpoint(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}

			if authHeader := r.Header.Get("Authorization"); strings.HasPrefix(authHeader, "Bearer ") {
				next.ServeHTTP(w, r)
				return
			}

			// Extract token from header (preferred) or form value (legacy form posts).
			headerToken := r.Header.Get(CSRFHeader)
			if headerToken == "" {
				headerToken = r.FormValue("csrf_token")
			}
			if headerToken == "" {
				writeCSRFError(w, "CSRF token missing from request")
				return
			}

			// Double-submit check: if a cookie is present, header MUST equal
			// cookie. Using constant-time compare so an attacker cannot
			// timing-side-channel partial matches when forging cookies via a
			// sibling subdomain.
			if cookie, err := r.Cookie(CSRFCookie); err == nil && cookie.Value != "" {
				if subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(headerToken)) != 1 {
					writeCSRFError(w, "CSRF token mismatch between header and cookie")
					return
				}
			}

			// Synchronizer-token check (server-side store + optional user binding).
			var userID string
			if claims, ok := ClaimsFromContext(r.Context()); ok {
				userID = claims.UserID
			}
			if err := csrfManager.ValidateTokenForUser(headerToken, userID); err != nil {
				writeCSRFError(w, fmt.Sprintf("CSRF validation failed: %v", err))
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// writeCSRFError emits the standard 403 JSON body used by CSRFMiddleware
// and increments the security counter so SOC alerting can detect bursts
// of CSRF rejections (typically a probing attacker).
func writeCSRFError(w http.ResponseWriter, message string) {
	monitoring.RecordCSRFFailure()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"error":   "Forbidden",
		"message": message,
		"status":  http.StatusForbidden,
	})
}

// GetCSRFToken returns a CSRF token for the current request. Kept for
// backwards compatibility with callers that mint tokens outside of an HTTP
// response (e.g. test scaffolding); production handlers should use
// CSRFManager.IssueToken so the cookie is set in lockstep.
func GetCSRFToken(csrfManager *CSRFManager) (string, error) {
	return csrfManager.GenerateToken()
}
