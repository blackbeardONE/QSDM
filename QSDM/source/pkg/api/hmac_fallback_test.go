package api

import (
	"bytes"
	"testing"
	"time"
)

// HMAC-fallback entropy tests — closes the crypto-02 audit-checklist
// row ("Confirm HMAC fallback uses random ephemeral key (not
// hardcoded) when ML-DSA is unavailable"). The historical bug
// these tests are guarding against:
//
//   - RequestSigner.hmacSecret() used to return a literal
//     []byte("Charming123") when the operator had not supplied an
//     explicit secret. That string is also the demo-prefix banned
//     by config.go::Validate when QSDM_STRICT_SECRETS=true, so the
//     old code was simultaneously (a) hardcoded, (b) low-entropy,
//     and (c) explicitly forbidden by the strict-mode gate the
//     operator-facing deploy docs already recommended. Audit row
//     crypto-02 correctly flagged the gap.
//
//   - AuthManager.jwtHMACSecretBytes() always did the right thing
//     (32 B from crypto/rand cached for the life of the AuthManager),
//     but the parallel signer in pkg/api/security.go had drifted.
//     These tests cover both paths in one suite so a future
//     regression on either side fails CI.

// TestRequestSigner_HMACFallback_NotHardcoded is the headline test
// for crypto-02. We construct a RequestSigner with an empty
// fallback secret (the operator-unconfigured path), call the
// internal hmacSecret() helper twice, and assert:
//
//  1. The returned key is exactly 32 bytes (matches the
//     AuthManager policy and is enough entropy for HMAC-SHA256).
//  2. The key is NOT the historical "Charming123" literal.
//  3. The key is stable across calls within the same instance
//     (otherwise Sign and Verify would not round-trip).
//  4. Two independent RequestSigner instances with no explicit
//     fallback produce DIFFERENT keys (the fallback is per-
//     process ephemeral, not a global compile-time constant).
//  5. The key has the minimum-entropy structure we expect from
//     crypto/rand: at least 20 distinct byte values across the
//     32-byte buffer (a true CSPRNG draw averages ~22 distinct
//     values; a hardcoded ASCII string like "Charming123" has
//     only 8 distinct values).
func TestRequestSigner_HMACFallback_NotHardcoded(t *testing.T) {
	rs1, err := NewRequestSigner("")
	if err != nil {
		t.Fatalf("NewRequestSigner: %v", err)
	}

	secret1a := rs1.hmacSecret()
	secret1b := rs1.hmacSecret()

	if len(secret1a) != 32 {
		t.Errorf("ephemeral HMAC key length: got %d, want 32", len(secret1a))
	}

	if bytes.Equal(secret1a, []byte("Charming123")) {
		t.Error("HMAC fallback returned the historical hardcoded 'Charming123' literal; crypto-02 regression")
	}

	if !bytes.Equal(secret1a, secret1b) {
		t.Error("HMAC fallback returned different keys on consecutive calls within the same RequestSigner; Sign/Verify will not round-trip")
	}

	rs2, err := NewRequestSigner("")
	if err != nil {
		t.Fatalf("NewRequestSigner #2: %v", err)
	}
	secret2 := rs2.hmacSecret()

	if bytes.Equal(secret1a, secret2) {
		t.Error("Two independent RequestSigner instances generated the same ephemeral HMAC key; that would only happen if the fallback were a compile-time constant or the CSPRNG were broken")
	}

	distinct := countDistinctBytes(secret1a)
	if distinct < 20 {
		t.Errorf("HMAC fallback key has only %d distinct byte values (expected >=20 for a 32-byte crypto/rand draw); this suggests low-entropy / hardcoded source", distinct)
	}
}

// TestRequestSigner_HMACFallback_ExplicitOverride verifies that
// when the operator DOES configure an explicit secret via the
// constructor, the lazy-random path stays dormant and the explicit
// secret is used verbatim. This is the cross-process consistency
// path documented in deploy/README.md (operators that need two
// QSDM processes to validate each other's signatures must share
// the same QSDM_JWT_HMAC_SECRET).
func TestRequestSigner_HMACFallback_ExplicitOverride(t *testing.T) {
	explicit := "operator-supplied-secret-with-enough-entropy-1234567890"
	rs, err := NewRequestSigner(explicit)
	if err != nil {
		t.Fatalf("NewRequestSigner: %v", err)
	}
	got := rs.hmacSecret()
	if string(got) != explicit {
		t.Errorf("explicit secret was overridden by lazy-random path; got %q, want %q", string(got), explicit)
	}
}

// TestAuthManager_JWTHMACFallback_NotHardcoded covers the same
// invariant on the AuthManager side. The implementation has been
// correct for a while (see pkg/api/auth.go::jwtHMACSecretBytes),
// but pinning the behaviour here makes a future refactor that
// reintroduces a hardcoded fallback fail CI.
//
// We construct an AuthManager via the public constructor, leave
// jwtHMACFallback unset (operator-unconfigured), and read the
// secret. Same assertions as the RequestSigner test.
func TestAuthManager_JWTHMACFallback_NotHardcoded(t *testing.T) {
	am1 := newAuthManagerForHMACTest(t)
	secret1a := am1.jwtHMACSecretBytes()
	secret1b := am1.jwtHMACSecretBytes()

	if len(secret1a) != 32 {
		t.Errorf("ephemeral JWT HMAC key length: got %d, want 32", len(secret1a))
	}

	if bytes.Equal(secret1a, []byte("Charming123")) {
		t.Error("JWT HMAC fallback returned the historical hardcoded 'Charming123' literal; crypto-02 regression")
	}

	if !bytes.Equal(secret1a, secret1b) {
		t.Error("JWT HMAC fallback returned different keys on consecutive calls within the same AuthManager; bearer-token verify would silently fail")
	}

	am2 := newAuthManagerForHMACTest(t)
	secret2 := am2.jwtHMACSecretBytes()
	if bytes.Equal(secret1a, secret2) {
		t.Error("Two independent AuthManager instances generated the same ephemeral JWT HMAC key; CSPRNG broken")
	}

	distinct := countDistinctBytes(secret1a)
	if distinct < 20 {
		t.Errorf("JWT HMAC fallback key has only %d distinct byte values (expected >=20 for a 32-byte crypto/rand draw)", distinct)
	}
}

// TestRequestSigner_SignVerify_RoundTrip_UnderEphemeralHMAC
// exercises the operational invariant the previous tests guard
// at the structural level: SignRequest and VerifyRequest must
// round-trip when the fallback secret is the lazy-random key.
// If the lazy-init had a race or returned a different buffer per
// call, this test would surface the bug as a verify-false at
// runtime.
func TestRequestSigner_SignVerify_RoundTrip_UnderEphemeralHMAC(t *testing.T) {
	rs, err := NewRequestSigner("")
	if err != nil {
		t.Fatalf("NewRequestSigner: %v", err)
	}
	if rs.dilithium != nil {
		// A Dilithium backend is available on this build host
		// (CGO+liboqs OR the pure-Go circl backend). The
		// HMAC fallback path is dead code here. This test
		// only runs on builds where Dilithium init returned
		// nil — i.e. a CGO-enabled build whose liboqs.dll
		// failed to load. The entropy + non-hardcoded
		// assertions in the other tests in this file already
		// cover crypto-02; this round-trip case exists for
		// the emergency-fallback build only.
		t.Skip("Dilithium backend present; HMAC fallback round-trip is exercised only when liboqs is unavailable on a CGO build")
	}

	body := []byte(`{"hello":"crypto-02"}`)
	ts := time.Now().Unix()
	nonce := "test-nonce-fixed-for-determinism-1234567890"

	sig, err := rs.SignRequest(body, ts, nonce)
	if err != nil {
		t.Fatalf("SignRequest: %v", err)
	}
	if sig == "" {
		t.Fatal("SignRequest returned empty signature")
	}

	if err := rs.VerifyRequest(body, ts, nonce, sig); err != nil {
		t.Fatalf("VerifyRequest on a freshly-signed request failed: %v", err)
	}

	bad := []byte(`{"hello":"tampered"}`)
	if err := rs.VerifyRequest(bad, ts, nonce, sig); err == nil {
		t.Fatal("VerifyRequest accepted a mutated body; HMAC verifier is broken")
	}
}

// countDistinctBytes returns the number of distinct byte values
// in b. Used as a coarse entropy floor for the HMAC fallback
// tests; a 32 B crypto/rand draw averages ~22 distinct values
// and almost always hits at least 20, while a hardcoded ASCII
// string like "Charming123" hits only 8.
func countDistinctBytes(b []byte) int {
	var seen [256]bool
	n := 0
	for _, x := range b {
		if !seen[x] {
			seen[x] = true
			n++
		}
	}
	return n
}

// newAuthManagerForHMACTest constructs an AuthManager via the
// same path the HTTP server uses, but does NOT call
// SetJWTHMACFallbackSecret. The point of these tests is the
// operator-unconfigured path.
func newAuthManagerForHMACTest(t *testing.T) *AuthManager {
	t.Helper()
	am, err := NewAuthManager()
	if err != nil {
		t.Fatalf("NewAuthManager: %v", err)
	}
	return am
}
