package api

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// CSRFManager manages CSRF token generation and validation
type CSRFManager struct {
	tokens    map[string]*csrfToken
	mu        sync.RWMutex
	tokenTTL  time.Duration
	tokenSize int // Token size in bytes
}

type csrfToken struct {
	token     string
	expiresAt time.Time
}

// NewCSRFManager creates a new CSRF manager
func NewCSRFManager() *CSRFManager {
	return &CSRFManager{
		tokens:    make(map[string]*csrfToken),
		tokenTTL:  1 * time.Hour, // Tokens expire after 1 hour
		tokenSize: 32,             // 32 bytes = 256 bits
	}
}

// GenerateToken generates a new CSRF token
func (cm *CSRFManager) GenerateToken() (string, error) {
	tokenBytes := make([]byte, cm.tokenSize)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", fmt.Errorf("failed to generate CSRF token: %w", err)
	}

	token := base64.URLEncoding.EncodeToString(tokenBytes)

	// Store token
	cm.mu.Lock()
	cm.tokens[token] = &csrfToken{
		token:     token,
		expiresAt: time.Now().Add(cm.tokenTTL),
	}
	cm.mu.Unlock()

	// Cleanup expired tokens
	go cm.cleanupExpiredTokens()

	return token, nil
}

// ValidateToken validates a CSRF token
func (cm *CSRFManager) ValidateToken(token string) error {
	if token == "" {
		return errors.New("CSRF token is required")
	}

	cm.mu.RLock()
	storedToken, exists := cm.tokens[token]
	cm.mu.RUnlock()

	if !exists {
		return errors.New("invalid CSRF token")
	}

	// Check expiration
	if time.Now().After(storedToken.expiresAt) {
		// Remove expired token
		cm.mu.Lock()
		delete(cm.tokens, token)
		cm.mu.Unlock()
		return errors.New("CSRF token expired")
	}

	return nil
}

// cleanupExpiredTokens removes expired tokens
func (cm *CSRFManager) cleanupExpiredTokens() {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	now := time.Now()
	for token, csrfToken := range cm.tokens {
		if now.After(csrfToken.expiresAt) {
			delete(cm.tokens, token)
		}
	}
}

// CSRFMiddleware validates CSRF tokens for state-changing requests
func CSRFMiddleware(csrfManager *CSRFManager) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip CSRF check for safe methods
			if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
				next.ServeHTTP(w, r)
				return
			}

			// Skip CSRF check for public endpoints
			if isPublicEndpoint(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}

			// Skip CSRF for Bearer-token authenticated API requests.
			// CSRF attacks only apply to cookie-based sessions; API clients
			// using Authorization headers are not vulnerable.
			if authHeader := r.Header.Get("Authorization"); len(authHeader) > 7 && authHeader[:7] == "Bearer " {
				next.ServeHTTP(w, r)
				return
			}

			// Extract token from header or form
			token := r.Header.Get("X-CSRF-Token")
			if token == "" {
				// Try form value
				token = r.FormValue("csrf_token")
			}

			// Validate token
			if err := csrfManager.ValidateToken(token); err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"error":   "Forbidden",
					"message": fmt.Sprintf("CSRF validation failed: %v", err),
					"status":  http.StatusForbidden,
				})
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// GetCSRFToken returns a CSRF token for the current request
// This should be called from handlers that need to include CSRF tokens in responses
func GetCSRFToken(csrfManager *CSRFManager) (string, error) {
	return csrfManager.GenerateToken()
}

