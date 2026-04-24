package hmac

// Tests for the nvidia-hmac-v1 acceptance flow. One test per
// branch of spec §3.2.2's 9-step flow (with step 8 deferred to
// Phase 2c-iv). Each test owns its own InMemoryRegistry /
// InMemoryNonceStore so the t.Parallel() runner doesn't cross-
// contaminate.
//
// Structure:
//
//   TestVerify_Accepts_Valid         - happy path (all 7 active steps green)
//   TestVerify_Rejects_WrongType     - dispatch guard
//   TestVerify_Rejects_Malformed_*   - bundle-malformed family (4 cases)
//   TestVerify_Rejects_ChallengeBind - step 2 failure
//   TestVerify_Rejects_Unregistered  - step 3 failure
//   TestVerify_Rejects_Revoked       - step 3 failure, revoked sentinel
//   TestVerify_Rejects_HMACMismatch  - step 4 failure
//   TestVerify_Rejects_WrongGPUUUID  - step 5 failure
//   TestVerify_Rejects_NonceMismatch - step 6a inner vs outer nonce
//   TestVerify_Rejects_StaleAge      - step 6b past the window
//   TestVerify_Rejects_FutureSkew    - step 6b impossibly-future
//   TestVerify_Rejects_NonceReplay   - step 6c replay detection
//   TestVerify_Rejects_DeniedGPU     - step 7 deny-list hit
//   TestVerify_NilRegistry           - misconfiguration guard

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mining"
)

// ----- fixtures ----------------------------------------------------

// fixtureNode is the registered GPU used across tests. A single
// constant tuple keeps the test code short; tests that need a
// different node enroll one locally.
const (
	fixtureNodeID  = "alice-rtx4090-01"
	fixtureGPUUUID = "GPU-01234567-89ab-cdef-0123-456789abcdef"
	fixtureGPUName = "NVIDIA GeForce RTX 4090"
)

// fixtureHMACKey is a deterministic 32-byte key so test output is
// reproducible. The value is NOT real key material; it's a literal
// "test key do not use" string padded to 32 bytes.
var fixtureHMACKey = []byte("test-key-do-not-use----32-bytes!")

// buildFixture returns a pre-enrolled registry, a fresh proof at
// the given time, and the matching bundle — all consistent with
// each other. Tests mutate copies of the returned values to
// exercise rejection branches.
func buildFixture(t *testing.T, now time.Time) (*InMemoryRegistry, mining.Proof, Bundle) {
	t.Helper()
	reg := NewInMemoryRegistry()
	if err := reg.Enroll(fixtureNodeID, fixtureGPUUUID, fixtureHMACKey); err != nil {
		t.Fatalf("enroll: %v", err)
	}

	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatalf("rand: %v", err)
	}
	var batchRoot [32]byte
	for i := range batchRoot {
		batchRoot[i] = byte(i)
	}
	var mix [32]byte
	for i := range mix {
		mix[i] = byte(0xFF - i)
	}
	minerAddr := "qsdm1testminer"

	p := mining.Proof{
		Version:    mining.ProtocolVersionV2,
		Epoch:      0,
		Height:     100,
		HeaderHash: [32]byte{0xAA},
		BatchRoot:  batchRoot,
		BatchCount: 1,
		Nonce:      [16]byte{0x03},
		MixDigest:  mix,
		MinerAddr:  minerAddr,
		Attestation: mining.Attestation{
			Type:         mining.AttestationTypeHMAC,
			GPUArch:      "ada",
			Nonce:        nonce,
			IssuedAt:     now.Unix(),
		},
	}

	bundle := Bundle{
		ChallengeBind: HexChallengeBind(minerAddr, batchRoot, mix),
		ComputeCap:    "8.9",
		CUDAVersion:   "12.8",
		DriverVer:     "572.16",
		GPUName:       fixtureGPUName,
		GPUUUID:       fixtureGPUUUID,
		IssuedAt:      now.Unix(),
		NodeID:        fixtureNodeID,
		Nonce:         hex.EncodeToString(nonce[:]),
	}
	signed, err := bundle.Sign(fixtureHMACKey)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	bundleB64, err := signed.MarshalBase64()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	p.Attestation.BundleBase64 = bundleB64
	return reg, p, signed
}

// reSign rebuilds the proof's bundle after a test mutates a bundle
// field that's covered by the HMAC — otherwise the HMAC check
// (step 4) fires before whatever step the test is actually
// targeting.
func reSign(t *testing.T, p *mining.Proof, b Bundle, key []byte) {
	t.Helper()
	signed, err := b.Sign(key)
	if err != nil {
		t.Fatalf("re-sign: %v", err)
	}
	b64, err := signed.MarshalBase64()
	if err != nil {
		t.Fatalf("re-marshal: %v", err)
	}
	p.Attestation.BundleBase64 = b64
}

// mustReject asserts that VerifyAttestation returned an error
// wrapping `want`. On mismatch it fails with the full error chain
// so diagnostics are useful.
func mustReject(t *testing.T, err error, want error) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected rejection wrapping %v, got nil", want)
	}
	if !errors.Is(err, want) {
		t.Fatalf("expected error to wrap %v, got %v", want, err)
	}
}

// ----- happy path --------------------------------------------------

func TestVerify_Accepts_Valid(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	reg, p, _ := buildFixture(t, now)
	v := NewVerifier(reg)
	if err := v.VerifyAttestation(p, now); err != nil {
		t.Fatalf("happy-path rejected: %v", err)
	}
}

// ----- dispatch guard ----------------------------------------------

func TestVerify_Rejects_WrongType(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	reg, p, _ := buildFixture(t, now)
	p.Attestation.Type = mining.AttestationTypeCC // wrong dispatch target
	v := NewVerifier(reg)
	mustReject(t, v.VerifyAttestation(p, now), mining.ErrAttestationTypeUnknown)
}

// ----- malformed bundles (step 1) ----------------------------------

func TestVerify_Rejects_MalformedBase64(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	reg, p, _ := buildFixture(t, now)
	p.Attestation.BundleBase64 = "!!!not valid base64!!!"
	v := NewVerifier(reg)
	mustReject(t, v.VerifyAttestation(p, now), mining.ErrAttestationBundleMalformed)
}

func TestVerify_Rejects_MalformedJSON(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	reg, p, _ := buildFixture(t, now)
	p.Attestation.BundleBase64 = base64.StdEncoding.EncodeToString([]byte("{not json"))
	v := NewVerifier(reg)
	mustReject(t, v.VerifyAttestation(p, now), mining.ErrAttestationBundleMalformed)
}

func TestVerify_Rejects_TrailingBytes(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	reg, p, b := buildFixture(t, now)
	// Produce valid JSON followed by extra bytes. ParseBundle must
	// catch this via dec.More().
	raw, err := b.MarshalBase64()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	decoded, _ := base64.StdEncoding.DecodeString(raw)
	poisoned := append([]byte{}, decoded...)
	poisoned = append(poisoned, []byte(`{"garbage":true}`)...)
	p.Attestation.BundleBase64 = base64.StdEncoding.EncodeToString(poisoned)
	v := NewVerifier(reg)
	mustReject(t, v.VerifyAttestation(p, now), mining.ErrAttestationBundleMalformed)
}

func TestVerify_Rejects_UnknownBundleField(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	reg, p, _ := buildFixture(t, now)
	// A bundle with an extra field — DisallowUnknownFields must
	// trip. This protects against attackers who stuff replay
	// material into a bundle via unrecognised keys.
	poisoned := []byte(`{"node_id":"x","gpu_uuid":"y","gpu_name":"z","driver_ver":"","cuda_version":"","compute_cap":"","nonce":"","issued_at":0,"challenge_bind":"","hmac":"","extra_field":true}`)
	p.Attestation.BundleBase64 = base64.StdEncoding.EncodeToString(poisoned)
	v := NewVerifier(reg)
	mustReject(t, v.VerifyAttestation(p, now), mining.ErrAttestationBundleMalformed)
}

// ----- step 2: challenge-bind --------------------------------------

func TestVerify_Rejects_ChallengeBindMismatch(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	reg, p, b := buildFixture(t, now)
	// Flip one byte of challenge_bind. The HMAC must be recomputed
	// because challenge_bind is covered by it — otherwise step 4
	// fires first and the test becomes useless.
	var bogus [32]byte
	for i := range bogus {
		bogus[i] = byte(0xAB)
	}
	b.ChallengeBind = hex.EncodeToString(bogus[:])
	reSign(t, &p, b, fixtureHMACKey)
	v := NewVerifier(reg)
	mustReject(t, v.VerifyAttestation(p, now), mining.ErrAttestationSignatureInvalid)
}

// ----- step 3: registry --------------------------------------------

func TestVerify_Rejects_Unregistered(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	_, p, b := buildFixture(t, now)
	// Empty registry. The bundle still refers to fixtureNodeID
	// which nothing enrolled.
	emptyReg := NewInMemoryRegistry()
	// Re-sign not needed — node_id is the lookup key, not part of
	// what the caller can lie about.
	_ = b
	v := NewVerifier(emptyReg)
	mustReject(t, v.VerifyAttestation(p, now), mining.ErrAttestationSignatureInvalid)
}

func TestVerify_Rejects_Revoked(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	reg, p, _ := buildFixture(t, now)
	reg.Revoke(fixtureNodeID)
	v := NewVerifier(reg)
	err := v.VerifyAttestation(p, now)
	mustReject(t, err, mining.ErrAttestationSignatureInvalid)
	// Additionally check the revocation sentinel surfaces somewhere
	// in the chain — this is what dashboards key off for the
	// "operator misbehaved" bucket vs "random unknown".
	if !errors.Is(err, ErrNodeRevoked) {
		t.Fatalf("revoked rejection should wrap ErrNodeRevoked, got %v", err)
	}
}

// ----- step 4: HMAC ------------------------------------------------

func TestVerify_Rejects_HMACMismatch(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	reg, p, b := buildFixture(t, now)
	// Overwrite the HMAC field with a syntactically-valid but
	// wrong 32-byte hex value. Do NOT re-sign — that would
	// overwrite our tamper.
	b.HMAC = hex.EncodeToString(bytes.Repeat([]byte{0xCC}, 32))
	b64, err := b.MarshalBase64()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	p.Attestation.BundleBase64 = b64
	v := NewVerifier(reg)
	mustReject(t, v.VerifyAttestation(p, now), mining.ErrAttestationSignatureInvalid)
}

func TestVerify_Rejects_HMACNotHex(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	reg, p, b := buildFixture(t, now)
	b.HMAC = "not-hex"
	b64, _ := b.MarshalBase64()
	p.Attestation.BundleBase64 = b64
	v := NewVerifier(reg)
	mustReject(t, v.VerifyAttestation(p, now), mining.ErrAttestationBundleMalformed)
}

// ----- step 5: GPU UUID --------------------------------------------

func TestVerify_Rejects_WrongGPUUUID(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	reg, p, b := buildFixture(t, now)
	b.GPUUUID = "GPU-different-uuid-0000-0000-000000000000"
	reSign(t, &p, b, fixtureHMACKey)
	v := NewVerifier(reg)
	mustReject(t, v.VerifyAttestation(p, now), mining.ErrAttestationSignatureInvalid)
}

func TestVerify_Accepts_CaseInsensitiveGPUUUID(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	reg, p, b := buildFixture(t, now)
	b.GPUUUID = strings.ToUpper(fixtureGPUUUID)
	reSign(t, &p, b, fixtureHMACKey)
	v := NewVerifier(reg)
	if err := v.VerifyAttestation(p, now); err != nil {
		t.Fatalf("uppercase UUID should still match: %v", err)
	}
}

// ----- step 6a: inner vs outer nonce -------------------------------

func TestVerify_Rejects_NonceMismatch(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	reg, p, _ := buildFixture(t, now)
	// Rewrite the outer Attestation.Nonce to something other than
	// what the bundle says (and is HMAC'd over).
	var different [32]byte
	for i := range different {
		different[i] = byte(0x55)
	}
	p.Attestation.Nonce = different
	// Do NOT re-sign; the bundle still carries the original nonce.
	v := NewVerifier(reg)
	mustReject(t, v.VerifyAttestation(p, now), mining.ErrAttestationNonceMismatch)
}

func TestVerify_Rejects_IssuedAtMismatch(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	reg, p, _ := buildFixture(t, now)
	p.Attestation.IssuedAt++ // bundle signed one, outer claims another
	v := NewVerifier(reg)
	mustReject(t, v.VerifyAttestation(p, now), mining.ErrAttestationNonceMismatch)
}

// ----- step 6b: freshness ------------------------------------------

func TestVerify_Rejects_StaleAge(t *testing.T) {
	issuedAt := time.Unix(1_700_000_000, 0)
	reg, p, _ := buildFixture(t, issuedAt)
	// Validator's wall clock is 120s later, window is 60s default.
	later := issuedAt.Add(mining.FreshnessWindow + 30*time.Second)
	v := NewVerifier(reg)
	mustReject(t, v.VerifyAttestation(p, later), mining.ErrAttestationStale)
}

func TestVerify_Rejects_FutureBeyondSkew(t *testing.T) {
	future := time.Unix(1_700_000_100, 0)
	reg, p, _ := buildFixture(t, future)
	// Validator's wall clock is 30s before the bundle's issued_at.
	earlier := future.Add(-30 * time.Second)
	v := NewVerifier(reg)
	mustReject(t, v.VerifyAttestation(p, earlier), mining.ErrAttestationStale)
}

func TestVerify_Accepts_WithinSkew(t *testing.T) {
	future := time.Unix(1_700_000_100, 0)
	reg, p, _ := buildFixture(t, future)
	// 3s in the future — inside default 5s allowed skew.
	almost := future.Add(-3 * time.Second)
	v := NewVerifier(reg)
	if err := v.VerifyAttestation(p, almost); err != nil {
		t.Fatalf("within-skew proof rejected: %v", err)
	}
}

// ----- step 6c: replay ---------------------------------------------

func TestVerify_Rejects_NonceReplay(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	reg, p, _ := buildFixture(t, now)
	store := NewInMemoryNonceStore(2 * mining.FreshnessWindow)
	v := NewVerifier(reg)
	v.NonceStore = store
	if err := v.VerifyAttestation(p, now); err != nil {
		t.Fatalf("first use should accept: %v", err)
	}
	// Second use with the same nonce must be flagged as replay
	// regardless of clock — the store records it, so the second
	// call finds it.
	mustReject(t, v.VerifyAttestation(p, now), mining.ErrAttestationNonceMismatch)
}

// ----- step 7: deny-list -------------------------------------------

func TestVerify_Rejects_DeniedGPU(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	reg, p, _ := buildFixture(t, now)
	v := NewVerifier(reg)
	v.DenyList = SubstringDenyList{Substrings: []string{"RTX 4090"}}
	mustReject(t, v.VerifyAttestation(p, now), mining.ErrAttestationSignatureInvalid)
}

func TestVerify_EmptyDenyList_Accepts(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	reg, p, _ := buildFixture(t, now)
	v := NewVerifier(reg)
	v.DenyList = EmptyDenyList{}
	if err := v.VerifyAttestation(p, now); err != nil {
		t.Fatalf("empty deny-list should accept: %v", err)
	}
}

// ----- misconfiguration --------------------------------------------

func TestVerify_NilRegistry(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	_, p, _ := buildFixture(t, now)
	v := &Verifier{Registry: nil}
	mustReject(t, v.VerifyAttestation(p, now), mining.ErrAttestationSignatureInvalid)
}

// ----- round-trip & canonical-form stability -----------------------

// TestBundle_CanonicalForMAC_Stable locks the byte output of
// CanonicalForMAC against a known-good reference. If this test
// breaks, a cross-implementation test vector has been invalidated
// and every non-Go miner must re-sign.
func TestBundle_CanonicalForMAC_Stable(t *testing.T) {
	b := Bundle{
		ChallengeBind: "00112233",
		ComputeCap:    "8.9",
		CUDAVersion:   "12.8",
		DriverVer:     "572.16",
		GPUName:       "NVIDIA GeForce RTX 4090",
		GPUUUID:       "GPU-abc",
		HMAC:          "ignored-by-canonical-form",
		IssuedAt:      1_700_000_000,
		NodeID:        "alice-rtx4090-01",
		Nonce:         "deadbeef",
	}
	got, err := b.CanonicalForMAC()
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	want := `{"challenge_bind":"00112233","compute_cap":"8.9","cuda_version":"12.8","driver_ver":"572.16","gpu_name":"NVIDIA GeForce RTX 4090","gpu_uuid":"GPU-abc","issued_at":1700000000,"node_id":"alice-rtx4090-01","nonce":"deadbeef"}`
	if string(got) != want {
		t.Fatalf("canonical form drifted:\n got: %s\nwant: %s", got, want)
	}
}

// TestBundle_SignVerifyRoundTrip confirms a freshly signed bundle
// verifies under the same key and fails under a wrong key, giving
// us confidence the Sign helper matches the verifier's MAC
// recomputation.
func TestBundle_SignVerifyRoundTrip(t *testing.T) {
	b := Bundle{
		ChallengeBind: "cafe",
		ComputeCap:    "8.9",
		CUDAVersion:   "12.8",
		DriverVer:     "572.16",
		GPUName:       "TEST GPU",
		GPUUUID:       "GPU-test",
		IssuedAt:      1_700_000_000,
		NodeID:        "test-node",
		Nonce:         "00",
	}
	key := []byte("test-key-do-not-use----32-bytes!")
	signed, err := b.Sign(key)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if signed.HMAC == "" {
		t.Fatal("signed HMAC field empty")
	}
	canonical, _ := signed.CanonicalForMAC()
	mac := ComputeMAC(key, canonical)
	if hex.EncodeToString(mac) != signed.HMAC {
		t.Fatal("signed HMAC does not match recomputed MAC")
	}
	// Wrong key must fail.
	wrongMAC := ComputeMAC([]byte("wrong-key-wrong-wrong-wrong-wrong"), canonical)
	if hex.EncodeToString(wrongMAC) == signed.HMAC {
		t.Fatal("different keys produced identical MAC — impossible")
	}
}
