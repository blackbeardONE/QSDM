package api

// Integration tests for the mining challenge endpoint. Use a real
// challenge.Issuer backed by the HMAC reference signer so the
// tests cover the full pipeline: HTTP handler -> Issuer -> Signer
// -> wire JSON -> Verifier round-trip.

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mining/challenge"
)

// withHMACIssuer installs a fresh HMAC-backed issuer for the test
// and returns the verifier that knows the matching key. Cleanup
// removes the process-wide registration so tests run in any
// order without leaking state.
func withHMACIssuer(t *testing.T, signerID string, clock func() time.Time) *challenge.HMACSignerVerifier {
	t.Helper()
	key := bytes.Repeat([]byte{0x42}, 32)
	signer, err := challenge.NewHMACSigner(signerID, key)
	if err != nil {
		t.Fatalf("NewHMACSigner: %v", err)
	}
	iss, err := challenge.NewIssuer(signer, challenge.WithClock(clock))
	if err != nil {
		t.Fatalf("NewIssuer: %v", err)
	}
	SetChallengeIssuer(iss)
	t.Cleanup(func() { SetChallengeIssuer(nil) })

	verifier := challenge.NewHMACSignerVerifier()
	if err := verifier.Register(signerID, key); err != nil {
		t.Fatalf("Register: %v", err)
	}
	return verifier
}

func TestMiningChallengeHandler_HappyPath(t *testing.T) {
	fixed := time.Unix(1_700_000_000, 0)
	clock := func() time.Time { return fixed }
	verifier := withHMACIssuer(t, "validator-01", clock)

	h := &Handlers{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/mining/challenge", nil)

	h.MiningChallengeHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	if cc := rr.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store (replay risk!)", cc)
	}

	var wire ChallengeWire
	if err := json.Unmarshal(rr.Body.Bytes(), &wire); err != nil {
		t.Fatalf("unmarshal body: %v; body=%s", err, rr.Body.String())
	}
	if wire.SignerID != "validator-01" {
		t.Fatalf("SignerID = %q, want validator-01", wire.SignerID)
	}
	if wire.IssuedAt != fixed.Unix() {
		t.Fatalf("IssuedAt = %d, want %d", wire.IssuedAt, fixed.Unix())
	}
	if len(wire.Nonce) != 64 { // 32 bytes -> 64 hex chars
		t.Fatalf("Nonce len = %d, want 64 hex chars", len(wire.Nonce))
	}
	if len(wire.Signature) != 64 { // HMAC-SHA256 -> 32 bytes -> 64 hex
		t.Fatalf("Signature len = %d, want 64 hex chars", len(wire.Signature))
	}

	// Reconstruct the core Challenge and verify end-to-end.
	nonceBytes, err := hex.DecodeString(wire.Nonce)
	if err != nil {
		t.Fatalf("decode nonce: %v", err)
	}
	sigBytes, err := hex.DecodeString(wire.Signature)
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}
	var nonce [32]byte
	copy(nonce[:], nonceBytes)
	c := challenge.Challenge{
		Nonce:     nonce,
		IssuedAt:  wire.IssuedAt,
		SignerID:  wire.SignerID,
		Signature: sigBytes,
	}
	if err := c.Verify(fixed.Unix(), c.IssuedAt, 60, 5, verifier); err != nil {
		t.Fatalf("end-to-end Verify: %v", err)
	}
}

func TestMiningChallengeHandler_NoIssuer_Returns503(t *testing.T) {
	SetChallengeIssuer(nil)
	t.Cleanup(func() { SetChallengeIssuer(nil) })

	h := &Handlers{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/mining/challenge", nil)
	h.MiningChallengeHandler(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rr.Code)
	}
	if rr.Header().Get("Retry-After") == "" {
		t.Fatal("503 must include Retry-After")
	}
}

func TestMiningChallengeHandler_WrongMethod(t *testing.T) {
	fixed := time.Unix(1_700_000_000, 0)
	_ = withHMACIssuer(t, "validator-01", func() time.Time { return fixed })

	h := &Handlers{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/mining/challenge", nil)
	h.MiningChallengeHandler(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rr.Code)
	}
	if rr.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("Allow header = %q, want GET", rr.Header().Get("Allow"))
	}
}

// failingIssuer always returns an error — used to exercise the
// 500 branch.
type failingIssuer struct{}

func (failingIssuer) Issue() (challenge.Challenge, error) {
	return challenge.Challenge{}, &issuerErr{}
}

type issuerErr struct{}

func (issuerErr) Error() string { return "simulated issuer failure" }

func TestMiningChallengeHandler_IssuerError_Returns500(t *testing.T) {
	SetChallengeIssuer(failingIssuer{})
	t.Cleanup(func() { SetChallengeIssuer(nil) })

	h := &Handlers{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/mining/challenge", nil)
	h.MiningChallengeHandler(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rr.Code)
	}
	if !bytes.Contains(rr.Body.Bytes(), []byte("simulated issuer failure")) {
		t.Fatalf("body should surface issuer error, got %s", rr.Body.String())
	}
}

func TestMiningChallengeHandler_MintsDistinctNonces(t *testing.T) {
	fixed := time.Unix(1_700_000_000, 0)
	_ = withHMACIssuer(t, "validator-01", func() time.Time { return fixed })
	h := &Handlers{}

	seen := make(map[string]struct{})
	for i := 0; i < 8; i++ {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/mining/challenge", nil)
		h.MiningChallengeHandler(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("req %d: status %d", i, rr.Code)
		}
		var wire ChallengeWire
		if err := json.Unmarshal(rr.Body.Bytes(), &wire); err != nil {
			t.Fatalf("req %d: decode: %v", i, err)
		}
		if _, dup := seen[wire.Nonce]; dup {
			t.Fatalf("req %d: duplicate nonce %s", i, wire.Nonce)
		}
		seen[wire.Nonce] = struct{}{}
	}
}

func TestChallengeFromCore_RoundTrip(t *testing.T) {
	var n [32]byte
	for i := range n {
		n[i] = byte(i)
	}
	c := challenge.Challenge{
		Nonce:     n,
		IssuedAt:  1_700_000_000,
		SignerID:  "v1",
		Signature: []byte{0xde, 0xad, 0xbe, 0xef},
	}
	wire := ChallengeFromCore(c)
	if wire.SignerID != c.SignerID {
		t.Fatalf("SignerID roundtrip: got %q, want %q", wire.SignerID, c.SignerID)
	}
	if wire.IssuedAt != c.IssuedAt {
		t.Fatalf("IssuedAt roundtrip: got %d, want %d", wire.IssuedAt, c.IssuedAt)
	}
	if wire.Nonce != hex.EncodeToString(n[:]) {
		t.Fatalf("Nonce roundtrip: got %q", wire.Nonce)
	}
	if wire.Signature != "deadbeef" {
		t.Fatalf("Signature roundtrip: got %q", wire.Signature)
	}
}
