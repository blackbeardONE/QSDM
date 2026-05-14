package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/blackbeardONE/QSDM/internal/logging"
)

// AuthMiddleware validates JWT tokens
func AuthMiddleware(authManager *AuthManager, logger *logging.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip auth for public endpoints
			if isPublicEndpoint(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}

			// Extract token from Authorization header
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				writeErrorResponse(w, http.StatusUnauthorized, "missing authorization header")
				return
			}

			// Parse "Bearer <token>"
			parts := strings.Split(authHeader, " ")
			if len(parts) != 2 || parts[0] != "Bearer" {
				writeErrorResponse(w, http.StatusUnauthorized, "invalid authorization format")
				return
			}

			token := parts[1]

			// Validate token
			claims, err := authManager.ValidateToken(token)
			if err != nil {
				logger.Warn("Token validation failed", "error", err, "path", r.URL.Path)
				writeErrorResponse(w, http.StatusUnauthorized, "invalid or expired token")
				return
			}

			// Add claims to request context
			ctx := context.WithValue(r.Context(), "claims", claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RoleMiddleware enforces role-based access control
func RoleMiddleware(allowedRoles []string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, ok := r.Context().Value("claims").(*Claims)
			if !ok {
				writeErrorResponse(w, http.StatusUnauthorized, "missing authentication")
				return
			}

			// Check if user's role is allowed
			allowed := false
			for _, role := range allowedRoles {
				if claims.Role == role {
					allowed = true
					break
				}
			}

			if !allowed {
				writeErrorResponse(w, http.StatusForbidden, "insufficient permissions")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// RequestSigningMiddleware validates request signatures
func RequestSigningMiddleware(signer *RequestSigner, logger *logging.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip signing for GET requests
			if r.Method == "GET" {
				next.ServeHTTP(w, r)
				return
			}

			// Skip signing for public endpoints
			if isPublicEndpoint(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}

			// Extract signature headers
			timestampStr := r.Header.Get("X-Timestamp")
			nonce := r.Header.Get("X-Nonce")
			signature := r.Header.Get("X-Signature")

			if timestampStr == "" || nonce == "" || signature == "" {
				writeErrorResponse(w, http.StatusBadRequest, "missing request signature headers")
				return
			}

			// Parse timestamp
			var timestamp int64
			if _, err := fmt.Sscanf(timestampStr, "%d", &timestamp); err != nil {
				writeErrorResponse(w, http.StatusBadRequest, "invalid timestamp format")
				return
			}

			// Read request body
			body, err := readRequestBody(r)
			if err != nil {
				writeErrorResponse(w, http.StatusBadRequest, "failed to read request body")
				return
			}

			// Verify signature
			if err := signer.VerifyRequest(body, timestamp, nonce, signature); err != nil {
				logger.Warn("Request signature verification failed", "error", err, "path", r.URL.Path)
				writeErrorResponse(w, http.StatusUnauthorized, "invalid request signature")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// AuditLogMiddleware logs all API requests for security auditing
func AuditLogMiddleware(logger *logging.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Create response writer wrapper to capture status code
			rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

			// Extract user info from context if available
			var userID, role string
			if claims, ok := r.Context().Value("claims").(*Claims); ok {
				userID = claims.UserID
				role = claims.Role
			}

			// Log request
			logger.Info("API request",
				"method", r.Method,
				"path", r.URL.Path,
				"remote_addr", r.RemoteAddr,
				"user_id", userID,
				"role", role,
				"user_agent", r.UserAgent(),
			)

			next.ServeHTTP(rw, r)

			// Log response
			duration := time.Since(start)
			logger.Info("API response",
				"method", r.Method,
				"path", r.URL.Path,
				"status", rw.statusCode,
				"duration_ms", duration.Milliseconds(),
				"user_id", userID,
			)
		})
	}
}

// responseWriter wraps http.ResponseWriter to capture status code
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// Helper functions

func isPublicEndpoint(path string) bool {
	publicPaths := []string{
		"/api/v1/health",
		"/api/v1/health/live",
		"/api/v1/health/ready",
		"/api/v1/status",
		"/api/v1/auth/login",
		"/api/v1/auth/register",
		"/api/v1/wallet/create", // Public for game server integration
		"/api/v1/wallet/balance", // Public for game server to check balances (address required in query)
		"/api/v1/wallet/mint",    // Removed in v0.3.3 (Session 91): always returns 410 Gone with a migration message. Kept in publicPaths so external callers that still hit the path receive a structured 410 instead of a confusing 401 redirect to /api/auth/login.
		// /api/v1/wallet/submit-signed is the v0.4.0 (Session 95)
		// self-custody send endpoint. Intentionally public because the
		// cryptographic identity IS the envelope's `public_key` field
		// (sender = hex(sha256(public_key)), verified inside the
		// handler) — demanding a JWT on top would force the user to
		// reveal a server-side session identity that has no bearing on
		// the on-chain debit. Per-IP rate-limit (security.go) handles
		// abuse; `result=signature_invalid` / `result=sender_mismatch`
		// counters surface a misbehaving caller.
		"/api/v1/wallet/submit-signed",
		// /api/v1/wallet/nonce is the v0.4.1 (Session 100) public-read
		// helper that lets a self-custody client fetch the next
		// envelope nonce without an authenticated session. Symmetric
		// with /wallet/balance: read-only, address-required-in-query,
		// no JWT. See V041_REPLAY_PROTECTION_DESIGN.md §5.2.
		"/api/v1/wallet/nonce",
		"/api/v1/monitoring/ngc-proof",
		"/api/v1/monitoring/ngc-challenge",
		"/api/v1/monitoring/ngc-proofs",
		// Mining endpoints are public so home miners can subscribe to a
		// validator without provisioning long-lived API tokens. Both
		// endpoints are deterministic-reject: the /work endpoint is
		// side-effect-free, and /submit is protected by the PoW cost
		// plus per-address quarantine (MINING_PROTOCOL.md §8.3).
		"/api/v1/mining/work",
		"/api/v1/mining/submit",
		// /mining/account is a read-only solo-mode probe;
		// reachable without auth so operators can curl
		// balances during testnet bring-up. Outside solo
		// mode it returns 503 because no probe is wired.
		"/api/v1/mining/account",
		// /mining/emission is a read-only schedule probe;
		// no AccountStore peek so it's safe outside solo
		// mode. SDKs render tokenomics widgets from it.
		"/api/v1/mining/emission",
		// /mining/blocks is a read-only block-header probe
		// for the public chain dashboard. No AccountStore
		// peek; returns the last N block headers in
		// height-ascending order. Same threat model as
		// /status: pure transparency, no secret leakage.
		"/api/v1/mining/blocks",
		// /mining/spec-anomalies surfaces the Tier-2
		// telemetry advisory checker output: the most-
		// recent N proofs whose claimed GPU specs
		// disagreed with the catalog. Read-only,
		// non-consensus, deliberately public so independent
		// observers can cross-check a validator's advisory
		// state without an operator-granted session. Same
		// trust-transparency posture as /mining/blocks
		// and /receipts.
		"/api/v1/mining/spec-anomalies",
		// /mining/penalty surfaces the Tier-3 reward-
		// downgrade engine output: per-miner sliding-
		// window state + the current reward multiplier.
		// Read-only, non-secret, deliberately public so
		// a flagged miner can self-serve "why did my
		// rewards drop?" without an operator-granted
		// session. Same trust posture as /spec-anomalies.
		"/api/v1/mining/penalty",
		// /receipts (no tx_id, exact-match) is the
		// height-range list endpoint that powers the
		// chain dashboard's "recent transactions" tile.
		// Same transparency posture as the per-tx
		// /receipts/{tx_id} probe — anyone observing the
		// chain can independently render the recent tx
		// stream without an operator-granted session.
		"/api/v1/receipts",
		// /receipts/{tx_id} is a read-only per-tx outcome
		// probe (TxApplied / TxFailed log + status code).
		// Receipts live on-chain implicitly via the
		// state-root hash, so exposing them publicly is the
		// canonical "trust transparency" posture: anyone
		// who saw a tx submission can independently confirm
		// it landed and at what status. The handler does
		// path-prefix-matching itself (because the URL has
		// the tx_id baked in), but the public-allowlist
		// check uses HasPrefix elsewhere — we keep the
		// trailing slash here to match exactly.
		"/api/v1/receipts/",
		// /mining/challenge mints a fresh per-call nonce and MUST be
		// publicly reachable — if miners had to authenticate to fetch
		// a challenge, the validator's identity gating would leak out
		// of the attestation path into session management and make
		// bring-up fragile. The endpoint is rate-limited the same way
		// as /mining/work.
		"/api/v1/mining/challenge",
		// Mining enrollment / unenrollment endpoints (Phase 2c-x).
		// Public for the same reason as /mining/challenge: the
		// per-tx signature is the cryptographic identity, not an
		// API session. Stateless validation in the mempool gate
		// rejects malformed traffic before it reaches block apply.
		"/api/v1/mining/enroll",
		"/api/v1/mining/unenroll",
		// /mining/enrollments is the paged read of the on-chain
		// enrollment registry. Public for the same reason as the
		// transparency endpoints below: anyone observing the chain
		// can independently verify "this NodeID is enrolled with
		// this stake/phase" without needing an operator-granted
		// session. The list handler itself caps page size and is
		// read-only, so leakage risk is bounded.
		"/api/v1/mining/enrollments",
		// Trust transparency endpoints (Major Update §8.5). Intentionally
		// public so third parties can independently scrape and verify
		// "X of Y attested" without operator-granted API tokens. The
		// handlers themselves gate behaviour on aggregator state.
		"/api/v1/trust/attestations/summary",
		"/api/v1/trust/attestations/recent",
		// Audit-checklist transparency endpoints (Session 77).
		// Same justification as the trust block above: third
		// parties (SDK consumers, landing page widget, external
		// audit aggregators) need to read the runtime-verified
		// audit score without an operator-granted session. The
		// underlying ChecklistItem text is already public from
		// the open-source repo, so the only thing being exposed
		// is the per-item Status/ReviewedBy/ReviewedAt — which
		// is exactly the transparency signal we want to advertise.
		"/api/v1/audit/summary",
		"/api/v1/audit/items",
	}
	for _, publicPath := range publicPaths {
		if path == publicPath {
			return true
		}
	}
	// Prefix-matched public paths: GET /api/v1/mining/enrollment/{node_id}
	// is the per-rig variant of /mining/enrollments above and shares the
	// same transparency justification. The handler validates the suffix
	// itself, so a request like /api/v1/mining/enrollment/ (empty node)
	// reaches the handler and gets a clean 400 — exactly the same path
	// it takes for an authenticated caller.
	if strings.HasPrefix(path, "/api/v1/mining/enrollment/") {
		return true
	}
	// /api/v1/receipts/{tx_id} is the per-tx-outcome probe.
	// Keep public for the same transparency reason as
	// /mining/enrollments — anyone with a tx-id should be
	// able to verify on-chain inclusion without an operator
	// session. The handler itself bounds tx_id length.
	if strings.HasPrefix(path, "/api/v1/receipts/") {
		return true
	}
	return false
}

func readRequestBody(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return []byte{}, nil
	}

	body, err := io.ReadAll(r.Body)
	r.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("failed to read request body: %w", err)
	}

	// Restore the body so downstream handlers can read it again
	r.Body = io.NopCloser(bytes.NewReader(body))
	return body, nil
}

func writeErrorResponse(w http.ResponseWriter, statusCode int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"error":   http.StatusText(statusCode),
		"message": message,
		"status":  statusCode,
	})
}

func writeJSONResponse(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(data)
}

// RequestSizeLimitMiddleware limits the size of request bodies
func RequestSizeLimitMiddleware(maxSize int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Limit request body size
			r.Body = http.MaxBytesReader(w, r.Body, maxSize)
			
			next.ServeHTTP(w, r)
		})
	}
}

