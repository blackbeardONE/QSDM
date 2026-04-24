package enrollment

// Integration test: the full enrollment path end-to-end.
//
//   InMemoryState  (simulated chain state)
//       ⬇
//   ValidateEnrollFields + ValidateEnrollAgainstState  (stateless + stateful)
//       ⬇
//   InMemoryState.ApplyEnroll  (simulated state transition)
//       ⬇
//   StateBackedRegistry.Lookup  (adapter: enrollment → hmac.Registry)
//       ⬇
//   attest.NewProductionDispatcher  (wiring factory, shipped in 109b787)
//       ⬇
//   VerifyAttestation on a real bundle signed with the
//   enrolled operator key  → accept
//
// Proves that everything Phase 2c-i shipped (the
// NewProductionDispatcher factory) accepts a miner that enrolled
// through the Phase 2c-ii surface — without going through the
// chain's tx-application pathway, which is a separate future
// commit.

import (
	"bytes"
	"encoding/hex"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mining"
	"github.com/blackbeardONE/QSDM/pkg/mining/attest"
	"github.com/blackbeardONE/QSDM/pkg/mining/attest/hmac"
	"github.com/blackbeardONE/QSDM/pkg/mining/challenge"
)

func TestIntegration_EnrollThenMine(t *testing.T) {
	// --- 1. Miner prepares an enrollment payload.
	const (
		nodeID    = "alice-rtx4090-01"
		gpuUUID   = "GPU-deadbeef-0000-0000-0000-000000000042"
		minerAddr = "qsdm1alice"
		owner     = minerAddr
	)
	operatorKey := bytes.Repeat([]byte{0xAA}, 32)
	payload := EnrollPayload{
		Kind:      PayloadKindEnroll,
		NodeID:    nodeID,
		GPUUUID:   gpuUUID,
		HMACKey:   operatorKey,
		StakeDust: mining.MinEnrollStakeDust,
		Memo:      "integration test rig",
	}

	// --- 2. Stateless validation.
	if err := ValidateEnrollFields(payload, owner); err != nil {
		t.Fatalf("ValidateEnrollFields: %v", err)
	}

	// --- 3. Chain-state validation + apply.
	state := NewInMemoryState()
	// Owner has enough balance to cover the stake.
	if err := ValidateEnrollAgainstState(payload, mining.MinEnrollStakeDust+1000, state); err != nil {
		t.Fatalf("ValidateEnrollAgainstState: %v", err)
	}
	if err := state.ApplyEnroll(EnrollmentRecord{
		NodeID:           payload.NodeID,
		Owner:            owner,
		GPUUUID:          payload.GPUUUID,
		HMACKey:          payload.HMACKey,
		StakeDust:        payload.StakeDust,
		EnrolledAtHeight: 100,
		Memo:             payload.Memo,
	}); err != nil {
		t.Fatalf("ApplyEnroll: %v", err)
	}

	// --- 4. Build the validator's attestation stack, backed by
	// the enrollment state.
	reg := NewStateBackedRegistry(state)

	const signerID = "validator-01"
	chgKey := bytes.Repeat([]byte{0xC1}, 32)
	chgSigner, err := challenge.NewHMACSigner(signerID, chgKey)
	if err != nil {
		t.Fatalf("NewHMACSigner: %v", err)
	}
	chgVerifier := challenge.NewHMACSignerVerifier()
	if err := chgVerifier.Register(signerID, chgKey); err != nil {
		t.Fatalf("Register signer: %v", err)
	}

	now := time.Unix(1_700_000_000, 0)
	issuer, err := challenge.NewIssuer(chgSigner, challenge.WithClock(func() time.Time { return now }))
	if err != nil {
		t.Fatalf("NewIssuer: %v", err)
	}
	chg, err := issuer.Issue()
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	nonceStore := hmac.NewInMemoryNonceStore(2 * mining.FreshnessWindow)
	disp, err := attest.NewProductionDispatcher(attest.ProductionConfig{
		Registry:          reg, // <-- the adapter, backed by on-chain state
		ChallengeVerifier: chgVerifier,
		NonceStore:        nonceStore,
	})
	if err != nil {
		t.Fatalf("NewProductionDispatcher: %v", err)
	}

	// --- 5. Miner assembles a bundle + proof using the
	// just-enrolled key.
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
		Version:   mining.ProtocolVersionV2,
		Height:    100,
		BatchRoot: batchRoot,
		MixDigest: mix,
		MinerAddr: minerAddr,
		Attestation: mining.Attestation{
			Type:         mining.AttestationTypeHMAC,
			BundleBase64: b64,
			GPUArch:      "ada",
			Nonce:        chg.Nonce,
			IssuedAt:     chg.IssuedAt,
		},
	}

	// --- 6. Validator accepts.
	if err := disp.VerifyAttestation(proof, now); err != nil {
		t.Fatalf("accept after enrollment: %v", err)
	}

	// --- 7. Miner unenrolls; subsequent proof attempts fail
	// with "revoked".
	if err := state.ApplyUnenroll(nodeID, 200); err != nil {
		t.Fatalf("ApplyUnenroll: %v", err)
	}

	// Get a fresh challenge (new nonce so we don't trip the
	// replay cache instead of the revoked-node check).
	chg2, err := issuer.Issue()
	if err != nil {
		t.Fatalf("Issue 2: %v", err)
	}
	bundle2 := hmac.Bundle{
		ChallengeBind:     hmac.HexChallengeBind(minerAddr, batchRoot, mix),
		ChallengeSig:      hex.EncodeToString(chg2.Signature),
		ChallengeSignerID: chg2.SignerID,
		GPUName:           "NVIDIA GeForce RTX 4090",
		GPUUUID:           gpuUUID,
		IssuedAt:          chg2.IssuedAt,
		NodeID:            nodeID,
		Nonce:             hex.EncodeToString(chg2.Nonce[:]),
	}
	signed2, _ := bundle2.Sign(operatorKey)
	b64_2, _ := signed2.MarshalBase64()
	proof2 := mining.Proof{
		Version:   mining.ProtocolVersionV2,
		Height:    200,
		BatchRoot: batchRoot,
		MixDigest: mix,
		MinerAddr: minerAddr,
		Attestation: mining.Attestation{
			Type:         mining.AttestationTypeHMAC,
			BundleBase64: b64_2,
			GPUArch:      "ada",
			Nonce:        chg2.Nonce,
			IssuedAt:     chg2.IssuedAt,
		},
	}
	if err := disp.VerifyAttestation(proof2, now); err == nil {
		t.Fatal("revoked node should not be able to mine")
	}
}
