package challenge

// HMACSigner is the reference Signer+SignerVerifier pair used for
// Phase 2c ship — it is a self-contained, auditable, unit-
// testable implementation with no external dependencies. It is
// NOT the final production crypto: the v2 protocol documents
// that the issuer signature ultimately anchors in the validator
// consensus signing key (ML-DSA via pkg/crypto). That upgrade
// re-uses the Signer / SignerVerifier interfaces exactly, so
// replacing HMACSigner is an O(1) wiring change.
//
// Why HMAC is defensible as a reference:
//
//   - A single validator can already prove its own issuance to
//     itself without any key exchange (HMAC key is local).
//   - For a federated validator set, operators agree on a shared
//     HMAC key at genesis — feasible during initial testnet while
//     the full ML-DSA registry is being built.
//   - The registry-of-keys shape is identical for HMAC and any
//     asymmetric scheme, so downstream code (pkg/api handler,
//     hmac.Verifier wiring) does not change when we upgrade.

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"sync"
)

// HMACSigner implements Signer with HMAC-SHA256 over a
// per-validator key. Output is a constant 32-byte slice.
type HMACSigner struct {
	id  string
	key []byte
}

// NewHMACSigner builds an HMACSigner. The signerID must be
// non-empty; key must be at least 16 bytes (the keyspace only
// protects against brute force if the key has adequate entropy).
func NewHMACSigner(signerID string, key []byte) (*HMACSigner, error) {
	if signerID == "" {
		return nil, errors.New("challenge: signerID must be non-empty")
	}
	if len(key) < 16 {
		return nil, errors.New("challenge: HMAC key must be >= 16 bytes")
	}
	// Copy to insulate caller from subsequent mutation.
	k := make([]byte, len(key))
	copy(k, key)
	return &HMACSigner{id: signerID, key: k}, nil
}

// Sign implements Signer.
func (s *HMACSigner) Sign(payload []byte) ([]byte, error) {
	m := hmac.New(sha256.New, s.key)
	m.Write(payload)
	return m.Sum(nil), nil
}

// SignerID implements Signer.
func (s *HMACSigner) SignerID() string { return s.id }

// HMACSignerVerifier implements SignerVerifier by holding a
// registry of (signer_id → HMAC key) mappings. One validator's
// verifier may know many signer_ids (itself + its peers).
//
// Concurrency: Register is not safe concurrent with
// VerifySignature. Populate at startup.
type HMACSignerVerifier struct {
	mu   sync.RWMutex
	keys map[string][]byte
}

// NewHMACSignerVerifier constructs an empty verifier registry.
func NewHMACSignerVerifier() *HMACSignerVerifier {
	return &HMACSignerVerifier{keys: make(map[string][]byte)}
}

// Register associates signerID with an HMAC key. Duplicate
// registration is an error; use Rotate when rolling keys.
func (v *HMACSignerVerifier) Register(signerID string, key []byte) error {
	if signerID == "" {
		return errors.New("challenge: signerID must be non-empty")
	}
	if len(key) < 16 {
		return errors.New("challenge: HMAC key must be >= 16 bytes")
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if _, exists := v.keys[signerID]; exists {
		return fmt.Errorf("challenge: signer_id %q already registered", signerID)
	}
	k := make([]byte, len(key))
	copy(k, key)
	v.keys[signerID] = k
	return nil
}

// Rotate replaces the key for signerID. If signerID is not yet
// registered, this acts as Register.
func (v *HMACSignerVerifier) Rotate(signerID string, key []byte) error {
	if signerID == "" {
		return errors.New("challenge: signerID must be non-empty")
	}
	if len(key) < 16 {
		return errors.New("challenge: HMAC key must be >= 16 bytes")
	}
	k := make([]byte, len(key))
	copy(k, key)
	v.mu.Lock()
	v.keys[signerID] = k
	v.mu.Unlock()
	return nil
}

// VerifySignature implements SignerVerifier.
func (v *HMACSignerVerifier) VerifySignature(signerID string, payload, signature []byte) error {
	v.mu.RLock()
	key, ok := v.keys[signerID]
	v.mu.RUnlock()
	if !ok {
		return fmt.Errorf("challenge: signer_id %q: %w", signerID, ErrChallengeUnknownSigner)
	}
	m := hmac.New(sha256.New, key)
	m.Write(payload)
	want := m.Sum(nil)
	if subtle.ConstantTimeCompare(want, signature) != 1 {
		return ErrChallengeSignatureBad
	}
	return nil
}

// compile-time interface checks.
var (
	_ Signer         = (*HMACSigner)(nil)
	_ SignerVerifier = (*HMACSignerVerifier)(nil)
)
