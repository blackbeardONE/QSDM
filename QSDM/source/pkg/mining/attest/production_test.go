package attest

import (
	"bytes"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mining"
	"github.com/blackbeardONE/QSDM/pkg/mining/attest/cc"
	"github.com/blackbeardONE/QSDM/pkg/mining/attest/hmac"
	"github.com/blackbeardONE/QSDM/pkg/mining/challenge"
)

// ----- ProductionConfig.Validate -----------------------------------------

func TestProductionConfig_Validate_Accept(t *testing.T) {
	cfg := validProdConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

func TestProductionConfig_Validate_RejectsMissingRegistry(t *testing.T) {
	cfg := validProdConfig()
	cfg.Registry = nil
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for missing Registry")
	}
}

func TestProductionConfig_Validate_RejectsMissingChallengeVerifier(t *testing.T) {
	cfg := validProdConfig()
	cfg.ChallengeVerifier = nil
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for missing ChallengeVerifier")
	}
	// Make sure the error message calls out the security consequence.
	if !contains(err.Error(), "mint their own nonces") {
		t.Fatalf("error should explain the attack, got %q", err.Error())
	}
}

func TestProductionConfig_Validate_RejectsMissingNonceStore(t *testing.T) {
	cfg := validProdConfig()
	cfg.NonceStore = nil
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for missing NonceStore")
	}
}

// ----- NewProductionDispatcher ------------------------------------------

func TestNewProductionDispatcher_RegistersBothTypes(t *testing.T) {
	d, err := NewProductionDispatcher(validProdConfig())
	if err != nil {
		t.Fatalf("NewProductionDispatcher: %v", err)
	}
	types := d.RegisteredTypes()
	if len(types) != 2 {
		t.Fatalf("expected 2 types registered, got %d: %v", len(types), types)
	}
	// Types should be sorted (Dispatcher contract).
	if types[0] != mining.AttestationTypeCC ||
		types[1] != mining.AttestationTypeHMAC {
		t.Fatalf("unexpected types: %v", types)
	}
}

func TestNewProductionDispatcher_AssertAllRegisteredPasses(t *testing.T) {
	d, err := NewProductionDispatcher(validProdConfig())
	if err != nil {
		t.Fatalf("NewProductionDispatcher: %v", err)
	}
	if err := d.AssertAllRegistered(
		mining.AttestationTypeHMAC, mining.AttestationTypeCC,
	); err != nil {
		t.Fatalf("AssertAllRegistered: %v", err)
	}
}

func TestNewProductionDispatcher_PropagatesValidateErrors(t *testing.T) {
	_, err := NewProductionDispatcher(ProductionConfig{})
	if err == nil {
		t.Fatal("expected error for zero config")
	}
}

// TestNewProductionDispatcher_CCStub_RoutedAndRejects confirms
// the cc.StubVerifier is what the dispatcher routes to when no
// override is given, and that attempting to verify an
// nvidia-cc-v1 proof fails with the "not yet available" error.
func TestNewProductionDispatcher_CCStub_RoutedAndRejects(t *testing.T) {
	d, err := NewProductionDispatcher(validProdConfig())
	if err != nil {
		t.Fatalf("NewProductionDispatcher: %v", err)
	}
	proof := mining.Proof{
		Version: mining.ProtocolVersionV2,
		Attestation: mining.Attestation{
			Type: mining.AttestationTypeCC,
		},
	}
	err = d.VerifyAttestation(proof, time.Now())
	if err == nil {
		t.Fatal("stub should reject every nvidia-cc-v1 proof")
	}
	if !errors.Is(err, cc.ErrNotYetAvailable) {
		t.Fatalf("expected wrapping cc.ErrNotYetAvailable, got %v", err)
	}
}

// TestNewProductionDispatcher_CCOverride_Honored: operators can
// inject a real CC verifier once Phase 2c-iv is done.
func TestNewProductionDispatcher_CCOverride_Honored(t *testing.T) {
	stub := &countingVerifier{}
	cfg := validProdConfig()
	cfg.CCVerifier = stub
	d, err := NewProductionDispatcher(cfg)
	if err != nil {
		t.Fatalf("NewProductionDispatcher: %v", err)
	}
	proof := mining.Proof{
		Version:     mining.ProtocolVersionV2,
		Attestation: mining.Attestation{Type: mining.AttestationTypeCC},
	}
	_ = d.VerifyAttestation(proof, time.Now())
	if stub.calls != 1 {
		t.Fatalf("expected override to be invoked once, got %d", stub.calls)
	}
}

// TestNewProductionDispatcher_HMACVerifier_WiredThrough builds a
// minimal valid nvidia-hmac-v1 proof and confirms the
// dispatcher routes it through the real hmac.Verifier with all
// the injected collaborators (Registry, NonceStore,
// ChallengeVerifier). This is the closest-to-production
// integration test we can run without spinning up pkg/api.
func TestNewProductionDispatcher_HMACVerifier_WiredThrough(t *testing.T) {
	const nodeID = "alice-rtx4090-01"
	const gpuUUID = "GPU-deadbeef-0000-0000-0000-000000000042"
	const minerAddr = "qsdm1alice"
	const signerID = "validator-01"

	operatorKey := bytes.Repeat([]byte{0xAA}, 32)
	chgKey := bytes.Repeat([]byte{0xC1}, 32)

	reg := hmac.NewInMemoryRegistry()
	if err := reg.Enroll(nodeID, gpuUUID, operatorKey); err != nil {
		t.Fatalf("Enroll: %v", err)
	}

	chgSV := challenge.NewHMACSignerVerifier()
	if err := chgSV.Register(signerID, chgKey); err != nil {
		t.Fatalf("Register: %v", err)
	}

	// Build a real challenge + bundle + proof.
	chgSigner, err := challenge.NewHMACSigner(signerID, chgKey)
	if err != nil {
		t.Fatalf("NewHMACSigner: %v", err)
	}
	issueAt := time.Unix(1_700_000_000, 0)
	iss, err := challenge.NewIssuer(chgSigner, challenge.WithClock(func() time.Time { return issueAt }))
	if err != nil {
		t.Fatalf("NewIssuer: %v", err)
	}
	chg, err := iss.Issue()
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	var batchRoot [32]byte
	for i := range batchRoot {
		batchRoot[i] = byte(i)
	}
	var mix [32]byte
	for i := range mix {
		mix[i] = byte(0xFF - i)
	}
	bundle := hmac.Bundle{
		ChallengeBind:     hmac.HexChallengeBind(minerAddr, batchRoot, mix),
		ChallengeSig:      hex.EncodeToString(chg.Signature),
		ChallengeSignerID: chg.SignerID,
		ComputeCap:        "8.9",
		CUDAVersion:       "12.8",
		DriverVer:         "572.16",
		GPUName:           "NVIDIA GeForce RTX 4090",
		GPUUUID:           gpuUUID,
		IssuedAt:          chg.IssuedAt,
		NodeID:            nodeID,
		Nonce:             hex.EncodeToString(chg.Nonce[:]),
	}
	signed, err := bundle.Sign(operatorKey)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	b64, err := signed.MarshalBase64()
	if err != nil {
		t.Fatalf("MarshalBase64: %v", err)
	}

	proof := mining.Proof{
		Version:    mining.ProtocolVersionV2,
		Height:     100,
		BatchRoot:  batchRoot,
		MixDigest:  mix,
		MinerAddr:  minerAddr,
		Attestation: mining.Attestation{
			Type:         mining.AttestationTypeHMAC,
			BundleBase64: b64,
			GPUArch:      "ada",
			Nonce:        chg.Nonce,
			IssuedAt:     chg.IssuedAt,
		},
	}

	cfg := ProductionConfig{
		Registry:          reg,
		ChallengeVerifier: chgSV,
		NonceStore:        hmac.NewInMemoryNonceStore(2 * mining.FreshnessWindow),
	}
	d, err := NewProductionDispatcher(cfg)
	if err != nil {
		t.Fatalf("NewProductionDispatcher: %v", err)
	}

	if err := d.VerifyAttestation(proof, issueAt); err != nil {
		t.Fatalf("accept: %v", err)
	}

	// Replay MUST be caught by the wired NonceStore — this
	// proves the NonceStore collaborator actually reached the
	// hmac verifier through the factory.
	if err := d.VerifyAttestation(proof, issueAt); err == nil {
		t.Fatal("replay should be rejected by NonceStore")
	}
}

// TestNewProductionDispatcher_DenyListOverride: confirm the
// optional DenyList field is plumbed into the hmac verifier.
func TestNewProductionDispatcher_DenyListOverride(t *testing.T) {
	cfg := validProdConfig()
	cfg.DenyList = hmac.SubstringDenyList{Substrings: []string{"rtx 4090"}}
	d, err := NewProductionDispatcher(cfg)
	if err != nil {
		t.Fatalf("NewProductionDispatcher: %v", err)
	}
	// Smoke: dispatcher still registers both types.
	if len(d.RegisteredTypes()) != 2 {
		t.Fatalf("expected 2 registered types")
	}
}

// ----- helpers -----------------------------------------------------------

func validProdConfig() ProductionConfig {
	reg := hmac.NewInMemoryRegistry()
	chgSV := challenge.NewHMACSignerVerifier()
	return ProductionConfig{
		Registry:          reg,
		ChallengeVerifier: chgSV,
		NonceStore:        hmac.NewInMemoryNonceStore(60 * time.Second),
	}
}

// countingVerifier is a test double that records invocation
// count so we can confirm the override is the one that got
// called (vs the default stub).
type countingVerifier struct{ calls int }

func (c *countingVerifier) VerifyAttestation(_ mining.Proof, _ time.Time) error {
	c.calls++
	return errors.New("stub override")
}
