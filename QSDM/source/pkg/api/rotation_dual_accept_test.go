package api

// Audit row rotation-01: JWT / API key rotation — dual-accept window.
// Pins the contract that AuthManager.ValidateToken and
// RequestSigner.VerifyRequest will accept signatures produced under
// BOTH the primary and a configured VERIFY-ONLY secondary key during
// a rotation window, while always producing new signatures under the
// primary only.

import (
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/monitoring"
)

// withTestAuthManager builds an AuthManager that exercises the HMAC
// fallback path (the rotation-01 surface) regardless of which
// Dilithium backend the build links against. The pure-Go circl
// backend makes crypto.NewDilithium() non-nil even on non-CGO
// builds, so we explicitly nil the dilithium field after construction
// to keep the test deterministic across CGO / non-CGO / circl
// matrices. Returns the manager and a cleanup that resets the
// global security counters so a failing test does not leak counter
// state into the next test.
func withTestAuthManager(t *testing.T, primary string) (*AuthManager, func()) {
	t.Helper()
	am, err := NewAuthManager()
	if err != nil {
		t.Fatalf("NewAuthManager: %v", err)
	}
	// Force HMAC path. The dilithium field is package-private so this
	// is safe to do from within pkg/api tests; out-of-package callers
	// cannot reach it (which is the whole point of keeping it private).
	am.dilithium = nil
	am.SetJWTHMACFallbackSecret(primary)
	monitoring.ResetSecurityMetricsForTest()
	return am, monitoring.ResetSecurityMetricsForTest
}

// signTokenWith creates a token by temporarily installing `key` as the
// primary on `am`, calling CreateToken, then restoring the original
// primary. Lets a test mint a "pre-rotation" token without standing up
// a second AuthManager.
func signTokenWith(t *testing.T, am *AuthManager, key, userID, role string) string {
	t.Helper()
	orig := am.jwtHMACFallback
	am.jwtHMACFallback = []byte(key)
	tok, err := am.CreateToken(userID, "addr-"+userID, role, TokenTypeAccess, 5*time.Minute)
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	am.jwtHMACFallback = orig
	return tok
}

// TestJWT_PrimaryOnly_VerifiesAndDoesNotBumpSecondaryCounter pins
// the steady-state contract: with NO secondary set, a primary-signed
// token verifies AND the secondary-hit counter stays zero.
func TestJWT_PrimaryOnly_VerifiesAndDoesNotBumpSecondaryCounter(t *testing.T) {
	am, reset := withTestAuthManager(t, "primary-key-v1-32bytes-of-entropy-here")
	defer reset()

	tok, err := am.CreateToken("alice", "addr1", "user", TokenTypeAccess, 5*time.Minute)
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	claims, err := am.ValidateToken(tok)
	if err != nil {
		t.Fatalf("ValidateToken (primary-only): %v", err)
	}
	if claims.UserID != "alice" {
		t.Fatalf("UserID round-trip: got %q want %q", claims.UserID, "alice")
	}
	if hits := monitoring.JWTSecondaryKeyHitsCount(); hits != 0 {
		t.Fatalf("secondary-hit counter must be 0 when no rotation is in flight; got %d", hits)
	}
}

// TestJWT_DualAccept_PrimaryAndSecondary_BothVerify pins the
// rotation-window contract: a token signed under the OLD key (now
// the secondary) verifies AND the secondary counter increments;
// a token signed under the NEW key (now the primary) ALSO verifies
// AND the counter does NOT increment.
func TestJWT_DualAccept_PrimaryAndSecondary_BothVerify(t *testing.T) {
	const oldKey = "old-rotation-key-v1-with-enough-entropy"
	const newKey = "new-rotation-key-v2-with-enough-entropy"

	am, reset := withTestAuthManager(t, newKey)
	defer reset()
	am.SetJWTHMACFallbackSecondarySecret(oldKey)

	preToken := signTokenWith(t, am, oldKey, "bob", "user")
	postToken := signTokenWith(t, am, newKey, "carol", "user")

	// Primary-signed token: must verify, secondary counter unchanged.
	if _, err := am.ValidateToken(postToken); err != nil {
		t.Fatalf("primary-signed token must verify: %v", err)
	}
	if hits := monitoring.JWTSecondaryKeyHitsCount(); hits != 0 {
		t.Fatalf("primary-signed verify must NOT bump secondary counter; got %d", hits)
	}

	// Secondary-signed token: must verify, secondary counter += 1.
	if _, err := am.ValidateToken(preToken); err != nil {
		t.Fatalf("secondary-signed token must verify during rotation window: %v", err)
	}
	if hits := monitoring.JWTSecondaryKeyHitsCount(); hits != 1 {
		t.Fatalf("secondary-signed verify must bump counter to 1; got %d", hits)
	}

	// Repeat the secondary-signed verify: counter must now be 2.
	if _, err := am.ValidateToken(preToken); err != nil {
		t.Fatalf("secondary-signed token must verify on repeat: %v", err)
	}
	if hits := monitoring.JWTSecondaryKeyHitsCount(); hits != 2 {
		t.Fatalf("secondary-signed verify must bump counter to 2 on repeat; got %d", hits)
	}
}

// TestJWT_AfterCutover_OldKeyTokenRejected pins the post-cutover
// contract: once the operator clears the secondary, tokens signed
// under the OLD key MUST stop verifying.
func TestJWT_AfterCutover_OldKeyTokenRejected(t *testing.T) {
	const oldKey = "old-key-being-decommissioned"
	const newKey = "new-key-after-cutover"

	am, reset := withTestAuthManager(t, newKey)
	defer reset()
	am.SetJWTHMACFallbackSecondarySecret(oldKey)
	preToken := signTokenWith(t, am, oldKey, "dave", "user")

	// Cutover: clear the secondary.
	am.SetJWTHMACFallbackSecondarySecret("")

	if _, err := am.ValidateToken(preToken); err == nil {
		t.Fatal("post-cutover: token signed under old key must be REJECTED, got nil error")
	}
	if hits := monitoring.JWTSecondaryKeyHitsCount(); hits != 0 {
		t.Fatalf("post-cutover rejected verify must NOT bump secondary counter; got %d", hits)
	}
}

// TestJWT_SameKeySecondary_TreatedAsNoOp pins the foot-gun guard:
// setting the secondary to the same bytes as the primary clears
// the secondary instead of being silently "active" — otherwise the
// secondary-hit counter would never increment and the runbook's
// "metric must go above zero before completing cutover" check
// would become a no-signal trap.
func TestJWT_SameKeySecondary_TreatedAsNoOp(t *testing.T) {
	const key = "same-key-everywhere"
	am, reset := withTestAuthManager(t, key)
	defer reset()

	am.SetJWTHMACFallbackSecondarySecret(key)
	if got := am.jwtHMACSecondaryBytes(); got != nil {
		t.Fatalf("same-as-primary secondary must be cleared; got %q", got)
	}
}

// TestJWT_ForgedToken_StillRejected_DuringRotation pins the security
// contract: a token signed under a key that is NEITHER the primary
// NOR the secondary MUST be rejected during a rotation window. The
// secondary fallback is the only relaxation; everything else still
// gets the strict gate.
func TestJWT_ForgedToken_StillRejected_DuringRotation(t *testing.T) {
	am, reset := withTestAuthManager(t, "real-primary")
	defer reset()
	am.SetJWTHMACFallbackSecondarySecret("real-secondary")

	forged := signTokenWith(t, am, "attacker-controlled-key", "eve", "admin")
	if _, err := am.ValidateToken(forged); err == nil {
		t.Fatal("forged token signed under unknown key must be REJECTED, got nil error")
	}
	if hits := monitoring.JWTSecondaryKeyHitsCount(); hits != 0 {
		t.Fatalf("forged-token reject must NOT bump secondary counter; got %d", hits)
	}
}

// TestRequestSigner_DualAccept_PrimaryAndSecondary_BothVerify mirrors
// the JWT contract for the per-request HMAC path
// (RequestSigner.VerifyRequest). A signature produced under the old
// key (now secondary) verifies AND the request-secondary counter
// increments; the new-key signature verifies WITHOUT bumping the
// counter.
func TestRequestSigner_DualAccept_PrimaryAndSecondary_BothVerify(t *testing.T) {
	const oldKey = "old-request-key-v1"
	const newKey = "new-request-key-v2"

	monitoring.ResetSecurityMetricsForTest()

	rsNew, err := NewRequestSigner(newKey)
	if err != nil {
		t.Fatalf("NewRequestSigner(new): %v", err)
	}
	rsNew.dilithium = nil // force HMAC path; see withTestAuthManager rationale
	rsNew.SetSecondaryHMACSecret(oldKey)

	rsOld, err := NewRequestSigner(oldKey)
	if err != nil {
		t.Fatalf("NewRequestSigner(old): %v", err)
	}
	rsOld.dilithium = nil

	body := []byte(`{"op":"transfer","amount":1}`)
	nonce := "deterministic-nonce-for-test"
	ts := time.Now().Unix()

	// Sign one payload under the OLD key, verify against the rotation-
	// window verifier (which carries the NEW key as primary).
	sigOld, err := rsOld.SignRequest(body, ts, nonce)
	if err != nil {
		t.Fatalf("SignRequest(old): %v", err)
	}
	if err := rsNew.VerifyRequest(body, ts, nonce, sigOld); err != nil {
		t.Fatalf("VerifyRequest with old-key signature must succeed during rotation; got %v", err)
	}
	if hits := monitoring.RequestSignatureSecondaryKeyHitsCount(); hits != 1 {
		t.Fatalf("secondary counter must be 1 after one old-key verify; got %d", hits)
	}

	// Sign + verify under the NEW key — counter MUST NOT bump.
	sigNew, err := rsNew.SignRequest(body, ts, nonce)
	if err != nil {
		t.Fatalf("SignRequest(new): %v", err)
	}
	if err := rsNew.VerifyRequest(body, ts, nonce, sigNew); err != nil {
		t.Fatalf("VerifyRequest with new-key signature must succeed: %v", err)
	}
	if hits := monitoring.RequestSignatureSecondaryKeyHitsCount(); hits != 1 {
		t.Fatalf("primary verify must NOT bump secondary counter; got %d", hits)
	}

	// Clear the secondary (cutover) — old-key signatures MUST be rejected.
	rsNew.SetSecondaryHMACSecret("")
	if err := rsNew.VerifyRequest(body, ts, nonce, sigOld); err == nil {
		t.Fatal("post-cutover: old-key signature must be REJECTED, got nil error")
	}
}

// TestRequestSigner_SameKeySecondary_TreatedAsNoOp mirrors the JWT
// foot-gun guard for the request-signing path.
func TestRequestSigner_SameKeySecondary_TreatedAsNoOp(t *testing.T) {
	rs, err := NewRequestSigner("same-key")
	if err != nil {
		t.Fatalf("NewRequestSigner: %v", err)
	}
	rs.dilithium = nil
	rs.SetSecondaryHMACSecret("same-key")
	if got := rs.secondaryHMACSecret(); got != nil {
		t.Fatalf("same-as-primary secondary must be cleared; got %q", got)
	}
}
