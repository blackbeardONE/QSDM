package api

import (
	"context"
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
	"github.com/blackbeardONE/QSDM/pkg/monitoring"
)

// claimsCtxKey is the typed context key used to publish *Claims onto a
// request context from AuthMiddleware. Using a private struct type
// (rather than a built-in string) satisfies staticcheck SA1029 and
// prevents third-party middleware that publishes under the string
// "claims" from accidentally colliding with our authentication state.
type claimsCtxKey struct{}

// ContextWithClaims returns a copy of ctx that carries the provided
// claims pointer. Used by AuthMiddleware (production) and the API test
// suite (where tests fabricate a Claims pointer to exercise authenticated
// handlers without round-tripping a real JWT).
func ContextWithClaims(ctx context.Context, c *Claims) context.Context {
	return context.WithValue(ctx, claimsCtxKey{}, c)
}

// ClaimsFromContext extracts the *Claims attached by AuthMiddleware
// from ctx, returning (nil,false) if no claims have been published.
// All handlers that need to know who the caller is should use this
// helper rather than reaching into the context value table directly.
func ClaimsFromContext(ctx context.Context) (*Claims, bool) {
	c, ok := ctx.Value(claimsCtxKey{}).(*Claims)
	if !ok || c == nil {
		return nil, false
	}
	return c, true
}

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
	// jwtHMACFallbackSecondary: VERIFY-ONLY secondary key for zero-downtime
	// key rotation (audit row rotation-01). When set, ValidateToken first
	// tries the primary key and falls back to the secondary on mismatch.
	// New tokens are always signed with the PRIMARY key — the secondary
	// is decommissioning-only. Empty (nil) when no rotation is in flight.
	//
	// Rotation procedure (runbook: JWT_KEY_ROTATION.md):
	//
	//  T0 (steady state):   primary=A   secondary=unset
	//  T1 (window opens):   primary=B   secondary=A   <- A-signed tokens still accepted
	//  T2 (window closes):  primary=B   secondary=unset
	//
	// The window must be at least as long as the longest in-flight token
	// lifetime (default access-token TTL = 24h; refresh = 7d). The metric
	// qsdm_security_jwt_secondary_key_hits_total goes flat once every
	// A-signed token has expired; at that point the operator deploys T2.
	jwtHMACFallbackSecondary []byte
	// revocations stores tokens revoked by /auth/logout (or admin action).
	// nil until SetRevocationStore wires one — leaving it nil keeps the
	// legacy "tokens are valid until they expire" behaviour for embedded
	// callers / tests that do not want the background sweeper.
	revocations *TokenRevocationStore
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
//
// Also records the wall-clock SET time for the rotation-monitoring
// gauge (audit row rotation-05). The gauge emits a NEGATIVE age-in-
// days under kind=jwt_primary so the operator's alert rule can fire
// when the secret has been in place beyond the rotation policy.
// Subject is fixed at "jwt-hmac-primary" so subsequent
// SetJWTHMACFallbackSecret calls UPDATE the entry rather than
// appending duplicate series.
func (am *AuthManager) SetJWTHMACFallbackSecret(secret string) {
	s := strings.TrimSpace(secret)
	if s != "" {
		am.mu.Lock()
		am.jwtHMACFallback = []byte(s)
		am.mu.Unlock()
		monitoring.RecordSecretSetTime(monitoring.SecretExpiryKindJWTPrimary, "jwt-hmac-primary", time.Now())
	}
}

// SetJWTHMACFallbackSecondarySecret installs (or, with an empty
// argument, removes) the VERIFY-ONLY secondary HMAC key used during
// a key-rotation window. Audit row rotation-01.
//
// Contract:
//
//   - The secondary key is consulted by ValidateToken's HMAC fallback
//     path AFTER the primary key fails, ONLY when len(secondary) > 0.
//   - The secondary key is NEVER used to sign new tokens — CreateToken
//     always reads jwtHMACFallback (the primary).
//   - Setting the secondary to the same bytes as the primary is rejected
//     as a no-op (no rotation in flight; nothing to verify against the
//     primary that the primary itself would not already match).
//
// Empty input clears the secondary (cutover complete). See the runbook
// at QSDM/docs/docs/runbooks/JWT_KEY_ROTATION.md.
func (am *AuthManager) SetJWTHMACFallbackSecondarySecret(secret string) {
	s := strings.TrimSpace(secret)
	am.mu.Lock()
	if s == "" {
		am.jwtHMACFallbackSecondary = nil
		am.mu.Unlock()
		// rotation-05: secondary cleared at cutover. Stop emitting
		// the age-in-days gauge so a stale series doesn't keep
		// firing the alert after the rotation window closes.
		monitoring.ClearSecretExpiry(monitoring.SecretExpiryKindJWTSecondary, "jwt-hmac-secondary")
		return
	}
	if len(am.jwtHMACFallback) > 0 && hmac.Equal([]byte(s), am.jwtHMACFallback) {
		am.jwtHMACFallbackSecondary = nil
		am.mu.Unlock()
		monitoring.ClearSecretExpiry(monitoring.SecretExpiryKindJWTSecondary, "jwt-hmac-secondary")
		return
	}
	am.jwtHMACFallbackSecondary = []byte(s)
	am.mu.Unlock()
	// rotation-05: register the secondary's wall-clock install time.
	// During rotation the alert rule is mostly informational (a
	// per-kind threshold can be wired to fire if the secondary is
	// left in place too long after the rotation window should have
	// closed).
	monitoring.RecordSecretSetTime(monitoring.SecretExpiryKindJWTSecondary, "jwt-hmac-secondary", time.Now())
}

// jwtHMACSecondaryBytes returns the secondary HMAC key bytes (or nil
// when no rotation is in flight). VERIFY-ONLY accessor — never call
// this from a sign path. Returns a fresh slice so a concurrent
// SetJWTHMACFallbackSecondarySecret rebinding the field cannot mutate
// the returned slice mid-Verify.
func (am *AuthManager) jwtHMACSecondaryBytes() []byte {
	am.mu.RLock()
	defer am.mu.RUnlock()
	if len(am.jwtHMACFallbackSecondary) == 0 {
		return nil
	}
	out := make([]byte, len(am.jwtHMACFallbackSecondary))
	copy(out, am.jwtHMACFallbackSecondary)
	return out
}

// SetRevocationStore attaches a TokenRevocationStore. When set,
// ValidateToken consults it BEFORE returning success, and the /auth/logout
// handler can call Revoke to invalidate the caller's token. Server.Start
// wires a default store; tests can leave it nil to keep behaviour
// identical to the pre-revocation baseline.
func (am *AuthManager) SetRevocationStore(s *TokenRevocationStore) {
	am.mu.Lock()
	am.revocations = s
	am.mu.Unlock()
}

// RevocationStore returns the attached store (nil if none).
func (am *AuthManager) RevocationStore() *TokenRevocationStore {
	am.mu.RLock()
	defer am.mu.RUnlock()
	return am.revocations
}

// RevokeToken adds the claims' nonce to the revocation store, if one is
// attached. No-op when no store is wired so embedded callers keep the
// legacy behaviour.
func (am *AuthManager) RevokeToken(claims *Claims) {
	if claims == nil {
		return
	}
	store := am.RevocationStore()
	if store == nil {
		return
	}
	store.Revoke(claims.Nonce, time.Unix(claims.ExpiresAt, 0))
}

func (am *AuthManager) jwtHMACSecretBytes() []byte {
	am.mu.Lock()
	defer am.mu.Unlock()
	if len(am.jwtHMACFallback) > 0 {
		return am.jwtHMACFallback
	}
	// Auto-generate a random 32-byte key so the node never runs with a
	// known default. crypto/rand failure is fatal for any signing path;
	// the best we can do is surface a non-empty key that is stable for
	// this process so Sign/Verify still round-trip. In practice
	// rand.Read on a healthy system never fails; this branch exists so
	// the function is total. The placeholder string is deliberately not
	// the historical "Charming123" literal (which is also banned by
	// config.go::Validate strict-mode); same shape as
	// security.go::RequestSigner.hmacSecret's unreachable-error fallback
	// so the two HMAC paths are consistent.
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		b = []byte("qsdm-jwt-rand-fallback-unreachable-in-pra")
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
		// Fallback: Verify HMAC-SHA256 for non-CGO builds.
		//
		// Dual-key verify (audit row rotation-01): try the primary
		// key first; if it does not match AND a secondary key is
		// configured (rotation window in flight), try the secondary.
		// A secondary hit increments
		// qsdm_security_jwt_secondary_key_hits_total so the operator
		// can confirm the window is being exercised before completing
		// the cutover by clearing the secondary. The primary remains
		// the only key used to SIGN new tokens (see CreateToken).
		primary := am.jwtHMACSecretBytes()
		h := hmac.New(sha256.New, primary)
		h.Write(claimsJSON)
		expectedSignature := h.Sum(nil)
		if !hmac.Equal(signature, expectedSignature) {
			secondary := am.jwtHMACSecondaryBytes()
			if len(secondary) == 0 {
				return nil, errors.New("invalid token signature")
			}
			h2 := hmac.New(sha256.New, secondary)
			h2.Write(claimsJSON)
			expectedSecondary := h2.Sum(nil)
			if !hmac.Equal(signature, expectedSecondary) {
				return nil, errors.New("invalid token signature")
			}
			// Token verified against the rotation-window secondary.
			// Increment the operator-visible counter — the rotation
			// runbook gates "cutover complete" on this counter going
			// flat (i.e., no in-flight tokens still rely on the old
			// key). The hit is not a security event in itself: a
			// pre-rotation token IS still valid until its exp, by design.
			monitoring.RecordJWTSecondaryKeyHit()
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

	// Revocation check (MED-7): a token whose nonce is on the revocation
	// list — typically because /auth/logout was hit — is treated as
	// expired. The RevocationStore self-evicts entries past their natural
	// expiry so this lookup is bounded.
	if store := am.RevocationStore(); store != nil && store.IsRevoked(claims.Nonce) {
		return nil, errors.New("token revoked")
	}

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

