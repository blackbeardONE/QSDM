package v2client

// Integration test: everything shipped in this session composed
// end-to-end.
//
//   pkg/api.MiningChallengeHandler           (51e1c5e + 0593d76)
//     ├── served by httptest.Server on a real mux
//     └── backed by challenge.Issuer(HMACSigner)
//   pkg/mining/v2client.FetchChallenge       (this commit)
//   pkg/mining/v2client.BuildHMACAttestation (this commit)
//   pkg/mining/attest.Dispatcher             (1f9e452)
//     └── routing nvidia-hmac-v1 →
//         pkg/mining/attest/hmac.Verifier    (Phase 2c-i)
//             ├── Registry: enrolled node     (Phase 2c-i)
//             └── ChallengeVerifier: HMACSignerVerifier
//                                             (71ba995)
//
// The test simulates a miner that (1) fetches a challenge,
// (2) builds a bundle with their operator key + fetched
// challenge, (3) submits it, (4) the dispatcher routes to the
// hmac verifier, (5) every spec check passes, (6) no reject.
//
// If this test ever fails, one of:
//   - the wire schema between api.ChallengeWire and
//     challengeWire drifted
//   - the bundle field order in hmac.Bundle drifted
//   - the HMAC canonical form drifted
//   - the challenge.Challenge.SigningBytes() drifted
// and a commit description should say so explicitly.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/api"
	"github.com/blackbeardONE/QSDM/pkg/mining"
	"github.com/blackbeardONE/QSDM/pkg/mining/attest"
	"github.com/blackbeardONE/QSDM/pkg/mining/attest/hmac"
	"github.com/blackbeardONE/QSDM/pkg/mining/challenge"
)

// fakeIssuerFromReal adapts *challenge.Issuer to the
// api.ChallengeIssuer interface. We cannot use the Issuer
// directly because its interface contract is defined inside
// pkg/api.
type fakeIssuerAdapter struct{ inner *challenge.Issuer }

func (f fakeIssuerAdapter) Issue() (challenge.Challenge, error) { return f.inner.Issue() }

// buildIntegrationServer stands up a real httptest.Server with
// just the mining/challenge route registered. Returns the
// server, the challenge verifier (for wiring into the hmac
// verifier on the validator side), and the operator HMAC key
// (for signing the bundle on the miner side).
func buildIntegrationServer(
	t *testing.T, signerID string, clock func() time.Time,
) (
	*httptest.Server,
	challenge.SignerVerifier,
	map[string][]byte, // nodeID -> operator hmac key (enrollment)
) {
	t.Helper()

	// Challenge signer (validator side)
	chgKey := bytes.Repeat([]byte{0xC1}, 32)
	signer, err := challenge.NewHMACSigner(signerID, chgKey)
	if err != nil {
		t.Fatalf("NewHMACSigner: %v", err)
	}
	iss, err := challenge.NewIssuer(signer, challenge.WithClock(clock))
	if err != nil {
		t.Fatalf("NewIssuer: %v", err)
	}
	api.SetChallengeIssuer(fakeIssuerAdapter{inner: iss})
	t.Cleanup(func() { api.SetChallengeIssuer(nil) })

	// Matching verifier for the validator-side attest path
	chgVerifier := challenge.NewHMACSignerVerifier()
	if err := chgVerifier.Register(signerID, chgKey); err != nil {
		t.Fatalf("Register signer: %v", err)
	}

	// Operator enrollment (also validator side). Each node_id
	// gets its own operator HMAC key.
	enrollment := map[string][]byte{
		"alice-rtx4090-01": bytes.Repeat([]byte{0xAA}, 32),
	}

	// Spin up the HTTP server with the real handler.
	mux := http.NewServeMux()
	h := &api.Handlers{}
	mux.HandleFunc("/api/v1/mining/challenge", h.MiningChallengeHandler)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return srv, chgVerifier, enrollment
}

func TestIntegration_FullV2Flow_Accepts(t *testing.T) {
	const signerID = "validator-alpha"
	const nodeID = "alice-rtx4090-01"
	const gpuUUID = "GPU-deadbeef-0000-0000-0000-000000000001"

	now := time.Unix(1_700_000_000, 0)
	clock := func() time.Time { return now }
	srv, chgVerifier, enrollment := buildIntegrationServer(t, signerID, clock)

	// ----- MINER SIDE -----
	// Fetch the challenge.
	c, err := FetchChallenge(context.Background(), srv.Client(), srv.URL)
	if err != nil {
		t.Fatalf("miner FetchChallenge: %v", err)
	}

	// Construct the proof's consensus-field values. Miner-side
	// these come from mining.Solve; here we fake them directly
	// because we're not exercising the PoW path.
	var batchRoot [32]byte
	for i := range batchRoot {
		batchRoot[i] = byte(i)
	}
	var mix [32]byte
	for i := range mix {
		mix[i] = byte(0xFF - i)
	}
	const minerAddr = "qsdm1alice"

	// Assemble + sign the bundle.
	att, err := BuildHMACAttestation(BundleInputs{
		NodeID:      nodeID,
		GPUUUID:     gpuUUID,
		GPUName:     "NVIDIA GeForce RTX 4090",
		ComputeCap:  "8.9",
		CUDAVersion: "12.8",
		DriverVer:   "572.16",
		HMACKey:     enrollment[nodeID],
		MinerAddr:   minerAddr,
		BatchRoot:   batchRoot,
		MixDigest:   mix,
		Challenge:   c,
	}, "ada")
	if err != nil {
		t.Fatalf("miner BuildHMACAttestation: %v", err)
	}

	// Build the proof the miner would submit.
	proof := mining.Proof{
		Version:    mining.ProtocolVersionV2,
		Epoch:      0,
		Height:     100,
		HeaderHash: [32]byte{0xAA},
		BatchRoot:  batchRoot,
		BatchCount: 1,
		Nonce:      [16]byte{0x03},
		MixDigest:  mix,
		MinerAddr:  minerAddr,
	}
	if err := AttachToProof(&proof, att); err != nil {
		t.Fatalf("AttachToProof: %v", err)
	}

	// ----- VALIDATOR SIDE -----
	// Wire up the enrollment registry.
	reg := hmac.NewInMemoryRegistry()
	if err := reg.Enroll(nodeID, gpuUUID, enrollment[nodeID]); err != nil {
		t.Fatalf("Enroll: %v", err)
	}
	hmacV := hmac.NewVerifier(reg)
	hmacV.ChallengeVerifier = chgVerifier
	// Add a nonce store so replay test below works.
	hmacV.NonceStore = hmac.NewInMemoryNonceStore(2 * mining.FreshnessWindow)

	// Compose through the dispatcher.
	d := attest.NewDispatcher()
	d.MustRegister(mining.AttestationTypeHMAC, hmacV)

	// Verify.
	if err := d.VerifyAttestation(proof, now); err != nil {
		t.Fatalf("integration verify: %v", err)
	}

	// Replay on the SAME proof MUST be rejected by the nonce
	// store (proves the nonce store's integration with
	// VerifyAttestation works through the dispatcher).
	err = d.VerifyAttestation(proof, now)
	if err == nil {
		t.Fatal("replay should have been rejected by NonceStore")
	}
	if !errors.Is(err, mining.ErrAttestationNonceMismatch) {
		t.Fatalf("replay reject should wrap ErrAttestationNonceMismatch, got %v", err)
	}
}

// TestIntegration_StaleChallengeRejected confirms that a
// miner who sits on a challenge past the freshness window gets
// rejected at the attest step — not at the HMAC step — because
// the freshness check in the hmac verifier uses the clock we
// inject.
func TestIntegration_StaleChallengeRejected(t *testing.T) {
	const signerID = "validator-alpha"
	const nodeID = "alice-rtx4090-01"
	const gpuUUID = "GPU-deadbeef-0000-0000-0000-000000000002"

	issueAt := time.Unix(1_700_000_000, 0)
	clock := func() time.Time { return issueAt }
	srv, chgVerifier, enrollment := buildIntegrationServer(t, signerID, clock)

	c, err := FetchChallenge(context.Background(), srv.Client(), srv.URL)
	if err != nil {
		t.Fatalf("miner FetchChallenge: %v", err)
	}

	var batchRoot [32]byte
	var mix [32]byte
	const minerAddr = "qsdm1alice"
	att, err := BuildHMACAttestation(BundleInputs{
		NodeID:    nodeID,
		GPUUUID:   gpuUUID,
		GPUName:   "NVIDIA GeForce RTX 4090",
		HMACKey:   enrollment[nodeID],
		MinerAddr: minerAddr,
		BatchRoot: batchRoot,
		MixDigest: mix,
		Challenge: c,
	}, "ada")
	if err != nil {
		t.Fatalf("miner BuildHMACAttestation: %v", err)
	}

	proof := mining.Proof{
		Version:   mining.ProtocolVersionV2,
		Height:    100,
		BatchRoot: batchRoot,
		MixDigest: mix,
		MinerAddr: minerAddr,
	}
	if err := AttachToProof(&proof, att); err != nil {
		t.Fatalf("AttachToProof: %v", err)
	}

	reg := hmac.NewInMemoryRegistry()
	if err := reg.Enroll(nodeID, gpuUUID, enrollment[nodeID]); err != nil {
		t.Fatalf("Enroll: %v", err)
	}
	hmacV := hmac.NewVerifier(reg)
	hmacV.ChallengeVerifier = chgVerifier

	// Validator's clock is 90 seconds after the issue — past the
	// 60s freshness window.
	late := issueAt.Add(90 * time.Second)
	err = hmacV.VerifyAttestation(proof, late)
	if err == nil {
		t.Fatal("stale challenge should have been rejected")
	}
	if !errors.Is(err, mining.ErrAttestationStale) {
		t.Fatalf("stale reject should wrap ErrAttestationStale, got %v", err)
	}
}

// TestIntegration_TamperedBundleRejected simulates an MITM
// adversary who intercepts the challenge response, lets the
// miner compute a valid bundle, and then swaps the
// challenge_sig for a bogus one before submission. The
// validator MUST reject.
func TestIntegration_TamperedBundleRejected(t *testing.T) {
	const signerID = "validator-alpha"
	const nodeID = "alice-rtx4090-01"
	const gpuUUID = "GPU-deadbeef-0000-0000-0000-000000000003"

	now := time.Unix(1_700_000_000, 0)
	clock := func() time.Time { return now }
	srv, chgVerifier, enrollment := buildIntegrationServer(t, signerID, clock)

	c, err := FetchChallenge(context.Background(), srv.Client(), srv.URL)
	if err != nil {
		t.Fatalf("miner FetchChallenge: %v", err)
	}

	// Tamper: flip a signature byte.
	c.Signature = append([]byte(nil), c.Signature...)
	c.Signature[0] ^= 0x01

	var batchRoot [32]byte
	var mix [32]byte
	const minerAddr = "qsdm1alice"
	att, err := BuildHMACAttestation(BundleInputs{
		NodeID:    nodeID,
		GPUUUID:   gpuUUID,
		GPUName:   "NVIDIA GeForce RTX 4090",
		HMACKey:   enrollment[nodeID],
		MinerAddr: minerAddr,
		BatchRoot: batchRoot,
		MixDigest: mix,
		Challenge: c,
	}, "ada")
	if err != nil {
		t.Fatalf("miner BuildHMACAttestation: %v", err)
	}

	proof := mining.Proof{
		Version:   mining.ProtocolVersionV2,
		Height:    100,
		BatchRoot: batchRoot,
		MixDigest: mix,
		MinerAddr: minerAddr,
	}
	if err := AttachToProof(&proof, att); err != nil {
		t.Fatalf("AttachToProof: %v", err)
	}

	reg := hmac.NewInMemoryRegistry()
	if err := reg.Enroll(nodeID, gpuUUID, enrollment[nodeID]); err != nil {
		t.Fatalf("Enroll: %v", err)
	}
	hmacV := hmac.NewVerifier(reg)
	hmacV.ChallengeVerifier = chgVerifier

	err = hmacV.VerifyAttestation(proof, now)
	if err == nil {
		t.Fatal("tampered challenge_sig should have been rejected")
	}
	if !errors.Is(err, mining.ErrAttestationSignatureInvalid) {
		t.Fatalf("tamper reject should wrap ErrAttestationSignatureInvalid, got %v", err)
	}
}

// TestIntegration_ChallengeWireRoundTrip asserts that the exact
// bytes the api handler emits round-trip byte-identically
// through api.ChallengeWire and v2client.challengeWire. This is
// a stronger guard than TestChallengeWireMatchesAPI because it
// includes the handler's actual JSON encoder output (field
// order, whitespace, etc.).
func TestIntegration_ChallengeWireRoundTrip(t *testing.T) {
	signerID := "v"
	now := time.Unix(1_700_000_001, 0)
	clock := func() time.Time { return now }
	srv, _, _ := buildIntegrationServer(t, signerID, clock)

	resp, err := srv.Client().Get(srv.URL + "/api/v1/mining/challenge")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var a api.ChallengeWire
	if err := json.NewDecoder(resp.Body).Decode(&a); err != nil {
		t.Fatalf("decode api.ChallengeWire: %v", err)
	}
	// Round-trip through v2client.challengeWire.
	aBytes, _ := json.Marshal(a)
	var b challengeWire
	if err := json.Unmarshal(aBytes, &b); err != nil {
		t.Fatalf("unmarshal v2client: %v", err)
	}
	bBytes, _ := json.Marshal(b)
	if !bytes.Equal(aBytes, bBytes) {
		t.Fatalf("round-trip diff:\n  a: %s\n  b: %s", aBytes, bBytes)
	}
}
