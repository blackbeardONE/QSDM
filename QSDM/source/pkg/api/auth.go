package api

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/crypto"
)

// TokenType represents the type of authentication token
type TokenType string

const (
	TokenTypeAccess  TokenType = "access"
	TokenTypeRefresh TokenType = "refresh"
)

// Claims represents JWT claims with quantum-safe signature
type Claims struct {
	UserID    string    `json:"user_id"`
	Address   string    `json:"address"`
	Role      string    `json:"role"`
	TokenType TokenType `json:"token_type"`
	IssuedAt  int64     `json:"iat"`
	ExpiresAt int64     `json:"exp"`
	Nonce     string    `json:"nonce"`
}

// AuthManager handles authentication and authorization
type AuthManager struct {
	dilithium      *crypto.Dilithium
	dilithiumMu    sync.Mutex // liboqs sign/verify may not be safe concurrent on one context; API + dashboard share one AuthManager
	nonces         map[string]time.Time // nonce -> timestamp for replay protection
	mu             sync.RWMutex
	nonceTTL       time.Duration
	lockoutManager *AccountLockoutManager
	// jwtHMACFallback: when Dilithium is nil (non-CGO), used for JWT HMAC instead of the hardcoded dev key.
	jwtHMACFallback []byte
}

// NewAuthManager creates a new authentication manager
func NewAuthManager() (*AuthManager, error) {
	d := crypto.NewDilithium()
	// Allow nil Dilithium for non-CGO builds (uses fallback authentication)
	// In production, CGO and liboqs should be used for quantum-safe crypto

	return &AuthManager{
		dilithium:      d, // May be nil in non-CGO builds
		nonces:         make(map[string]time.Time),
		nonceTTL:       5 * time.Minute, // Nonces expire after 5 minutes
		lockoutManager: NewAccountLockoutManager(),
	}, nil
}

// SetJWTHMACFallbackSecret sets the HMAC key for JWT signing/verification when Dilithium is unavailable.
// Empty leaves the built-in development default (not for production).
func (am *AuthManager) SetJWTHMACFallbackSecret(secret string) {
	s := strings.TrimSpace(secret)
	if s != "" {
		am.jwtHMACFallback = []byte(s)
	}
}

func (am *AuthManager) jwtHMACSecretBytes() []byte {
	am.mu.Lock()
	defer am.mu.Unlock()
	if len(am.jwtHMACFallback) > 0 {
		return am.jwtHMACFallback
	}
	// Auto-generate a random 32-byte key so the node never runs with a known default.
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return []byte("Charming123") // last resort; should never happen
	}
	am.jwtHMACFallback = b
	fmt.Println("WARNING: No JWT HMAC secret configured; generated an ephemeral random key. Set QSDM_JWT_HMAC_SECRET for stable sessions across restarts.")
	return am.jwtHMACFallback
}

// GenerateNonce generates a cryptographically secure nonce
func (am *AuthManager) GenerateNonce() (string, error) {
	nonceBytes := make([]byte, 32)
	if _, err := rand.Read(nonceBytes); err != nil {
		return "", fmt.Errorf("failed to generate nonce: %w", err)
	}
	return base64.URLEncoding.EncodeToString(nonceBytes), nil
}

// ValidateNonce validates a nonce and prevents replay attacks
func (am *AuthManager) ValidateNonce(nonce string) error {
	am.mu.Lock()
	defer am.mu.Unlock()

	// Check if nonce was already used
	if _, exists := am.nonces[nonce]; exists {
		return errors.New("nonce already used (replay attack detected)")
	}

	// Store nonce with current timestamp
	am.nonces[nonce] = time.Now()

	// Clean up expired nonces
	am.cleanupExpiredNonces()

	return nil
}

// cleanupExpiredNonces removes expired nonces
func (am *AuthManager) cleanupExpiredNonces() {
	now := time.Now()
	for nonce, timestamp := range am.nonces {
		if now.Sub(timestamp) > am.nonceTTL {
			delete(am.nonces, nonce)
		}
	}
}

// CreateToken creates a quantum-safe signed token
func (am *AuthManager) CreateToken(userID, address, role string, tokenType TokenType, expiresIn time.Duration) (string, error) {
	nonce, err := am.GenerateNonce()
	if err != nil {
		return "", err
	}

	now := time.Now()
	claims := Claims{
		UserID:    userID,
		Address:   address,
		Role:      role,
		TokenType: tokenType,
		IssuedAt:  now.Unix(),
		ExpiresAt: now.Add(expiresIn).Unix(),
		Nonce:     nonce,
	}

	// Marshal claims to JSON
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("failed to marshal claims: %w", err)
	}

	// Sign with quantum-safe Dilithium (or fallback to HMAC for non-CGO builds)
	var signature []byte
	if am.dilithium != nil {
		am.dilithiumMu.Lock()
		signature, err = am.dilithium.Sign(claimsJSON)
		am.dilithiumMu.Unlock()
		if err != nil {
			return "", fmt.Errorf("failed to sign token: %w", err)
		}
	} else {
		// Fallback: Use HMAC-SHA256 for non-CGO builds (development/testing only)
		// In production, CGO and liboqs should be used
		h := hmac.New(sha256.New, am.jwtHMACSecretBytes())
		h.Write(claimsJSON)
		signature = h.Sum(nil)
	}

	// Encode token: base64(claims) + "." + base64(signature)
	token := fmt.Sprintf("%s.%s",
		base64.URLEncoding.EncodeToString(claimsJSON),
		base64.URLEncoding.EncodeToString(signature),
	)

	return token, nil
}

// ValidateToken validates a token and returns claims
func (am *AuthManager) ValidateToken(token string) (*Claims, error) {
	// Split token into claims and signature
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return nil, errors.New("invalid token format")
	}

	claimsB64, sigB64 := parts[0], parts[1]

	// Decode claims
	claimsJSON, err := base64.URLEncoding.DecodeString(claimsB64)
	if err != nil {
		return nil, fmt.Errorf("failed to decode claims: %w", err)
	}

	// Decode signature
	signature, err := base64.URLEncoding.DecodeString(sigB64)
	if err != nil {
		return nil, fmt.Errorf("failed to decode signature: %w", err)
	}

	// Verify signature (quantum-safe Dilithium or HMAC fallback)
	if am.dilithium != nil {
		am.dilithiumMu.Lock()
		valid, err := am.dilithium.Verify(claimsJSON, signature)
		am.dilithiumMu.Unlock()
		if err != nil {
			return nil, fmt.Errorf("failed to verify signature: %w", err)
		}
		if !valid {
			return nil, errors.New("invalid token signature")
		}
	} else {
		// Fallback: Verify HMAC-SHA256 for non-CGO builds
		h := hmac.New(sha256.New, am.jwtHMACSecretBytes())
		h.Write(claimsJSON)
		expectedSignature := h.Sum(nil)
		if !hmac.Equal(signature, expectedSignature) {
			return nil, errors.New("invalid token signature")
		}
	}

	// Unmarshal claims
	var claims Claims
	if err := json.Unmarshal(claimsJSON, &claims); err != nil {
		return nil, fmt.Errorf("failed to unmarshal claims: %w", err)
	}

	// Check expiration
	if time.Now().Unix() > claims.ExpiresAt {
		return nil, errors.New("token expired")
	}

	// Do not consume claims.Nonce here: access tokens are sent on every request (cookie / Bearer)
	// until expiry; single-use nonce tracking would reject the second request with the same token.

	return &claims, nil
}

// IsAccountLocked checks if an account is locked
func (am *AuthManager) IsAccountLocked(identifier string) (bool, error) {
	return am.lockoutManager.IsLocked(identifier)
}

// RecordFailedAttempt records a failed login attempt
func (am *AuthManager) RecordFailedAttempt(identifier string) {
	am.lockoutManager.RecordFailedAttempt(identifier)
}

// RecordSuccessfulAttempt clears failed attempts after successful login
func (am *AuthManager) RecordSuccessfulAttempt(identifier string) {
	am.lockoutManager.RecordSuccessfulAttempt(identifier)
}

// GetRemainingAttempts returns remaining login attempts before lockout
func (am *AuthManager) GetRemainingAttempts(identifier string) int {
	return am.lockoutManager.GetRemainingAttempts(identifier)
}

