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
		"/api/v1/wallet/mint",    // Public for game server to mint $CELL (main coin)
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
		// /mining/challenge mints a fresh per-call nonce and MUST be
		// publicly reachable — if miners had to authenticate to fetch
		// a challenge, the validator's identity gating would leak out
		// of the attestation path into session management and make
		// bring-up fragile. The endpoint is rate-limited the same way
		// as /mining/work.
		"/api/v1/mining/challenge",
		// Trust transparency endpoints (Major Update §8.5). Intentionally
		// public so third parties can independently scrape and verify
		// "X of Y attested" without operator-granted API tokens. The
		// handlers themselves gate behaviour on aggregator state.
		"/api/v1/trust/attestations/summary",
		"/api/v1/trust/attestations/recent",
	}
	for _, publicPath := range publicPaths {
		if path == publicPath {
			return true
		}
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

