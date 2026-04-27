package freshnesscheat_test

// Tests for the freshness-cheat evidence verifier. Strategy
// mirrors doublemining_test / forgedattest_test: build a
// known-good v2 proof + bundle fixture, then run the verifier
// under various witness postures and anchor configurations to
// assert:
//
//   - the verifier ACCEPTS (returns the configured cap) when:
//     · the proof is v2,
//     · the bundle binds to the slash payload's NodeID,
//     · the witness certifies the (anchor_height, anchor_time,
//       proof_id) tuple,
//     · anchor_time - bundle.IssuedAt > FreshnessWindow + Grace,
//     · the operator is in the registry;
//   - the verifier REJECTS (with ErrEvidenceVerification) when
//     ANY of those preconditions fails;
//   - the encoder + decoder round-trip a non-trivial Evidence;
//   - RejectAllWitness rejects every anchor (the production
//     posture today);
//   - FixedAnchorWitness accepts only the exact tuple it was
//     registered with.

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mining"
	"github.com/blackbeardONE/QSDM/pkg/mining/attest/hmac"
	"github.com/blackbeardONE/QSDM/pkg/mining/slashing"
	"github.com/blackbeardONE/QSDM/pkg/mining/slashing/freshnesscheat"
)

// ----- shared fixtures ---------------------------------------------------

const (
	fxNodeID  = "alice-rtx4090-01"
	fxGPUUUID = "GPU-01234567-89ab-cdef-0123-456789abcdef"
	fxGPUName = "NVIDIA GeForce RTX 4090"
	fxAddr    = "qsdm1testminer"

	// fxIssuedAt is the bundle issued_at time for fixtures.
	fxIssuedAt int64 = 1_700_000_000

	// fxAnchorTime is well past fxIssuedAt + freshness + grace
	// (60s + 30s = 90s), so the proof is provably stale at
	// anchor time.
	fxAnchorTime int64 = fxIssuedAt + 600 // 10 minutes stale
	fxAnchorH    uint64 = 12_345
)

var fxKey = []byte("test-key-do-not-use----32-bytes!")

// signedV2Proof produces a v2 proof + bundle signed with fxKey
// under fxNodeID/fxGPUUUID at the given Height. The bundle's
// IssuedAt is fxIssuedAt unless the test wants something else
// — callers can mutate p.Attestation.IssuedAt and re-sign.
func signedV2Proof(t *testing.T, height uint64) mining.Proof {
	t.Helper()
	return signedV2ProofAt(t, height, fxIssuedAt)
}

// signedV2ProofAt is signedV2Proof with an explicit issuedAt,
// used by tests that need to construct boundary-staleness
// fixtures.
func signedV2ProofAt(t *testing.T, height uint64, issuedAt int64) mining.Proof {
	t.Helper()

	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatalf("rand: %v", err)
	}
	var batchRoot, mix [32]byte
	for i := range batchRoot {
		batchRoot[i] = byte(i)
		mix[i] = byte(0xFF - i)
	}

	p := mining.Proof{
		Version:    mining.ProtocolVersionV2,
		Epoch:      1,
		Height:     height,
		HeaderHash: [32]byte{0xCC},
		BatchRoot:  batchRoot,
		BatchCount: 1,
		Nonce:      [16]byte{0x07},
		MixDigest:  mix,
		MinerAddr:  fxAddr,
		Attestation: mining.Attestation{
			Type:     mining.AttestationTypeHMAC,
			GPUArch:  "ada",
			Nonce:    nonce,
			IssuedAt: issuedAt,
		},
	}

	b := hmac.Bundle{
		ChallengeBind: hmac.HexChallengeBind(fxAddr, batchRoot, mix),
		ComputeCap:    "8.9",
		CUDAVersion:   "12.8",
		DriverVer:     "572.16",
		GPUName:       fxGPUName,
		GPUUUID:       fxGPUUUID,
		IssuedAt:      issuedAt,
		NodeID:        fxNodeID,
		Nonce:         hex.EncodeToString(nonce[:]),
	}
	signed, err := b.Sign(fxKey)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	bundleB64, err := signed.MarshalBase64()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	p.Attestation.BundleBase64 = bundleB64
	return p
}

// fixtureRegistry returns a registry with fxNodeID enrolled.
func fixtureRegistry(t *testing.T) *hmac.InMemoryRegistry {
	t.Helper()
	reg := hmac.NewInMemoryRegistry()
	if err := reg.Enroll(fxNodeID, fxGPUUUID, fxKey); err != nil {
		t.Fatalf("enroll: %v", err)
	}
	return reg
}

// fixtureEvidence returns a happy-path Evidence: the proof is
// stale by 10 minutes against the anchor.
func fixtureEvidence(t *testing.T) freshnesscheat.Evidence {
	t.Helper()
	return freshnesscheat.Evidence{
		Proof:           signedV2Proof(t, 100),
		AnchorHeight:    fxAnchorH,
		AnchorBlockTime: fxAnchorTime,
		Memo:            "ten-minute stale, anchored at H=12345",
	}
}

// makePayload wraps an Evidence into a SlashPayload ready for
// the verifier. NodeID defaults to fxNodeID; tests override
// before calling Verify.
func makePayload(t *testing.T, ev freshnesscheat.Evidence) slashing.SlashPayload {
	t.Helper()
	blob, err := freshnesscheat.EncodeEvidence(ev)
	if err != nil {
		t.Fatalf("encode evidence: %v", err)
	}
	return slashing.SlashPayload{
		NodeID:          fxNodeID,
		EvidenceKind:    slashing.EvidenceKindFreshnessCheat,
		EvidenceBlob:    blob,
		SlashAmountDust: freshnesscheat.DefaultMaxSlashDust,
	}
}

// trustingVerifier returns a Verifier wired with
// TrustingTestWitness — every legitimate anchor is accepted, so
// staleness / structural rules are the only gates.
func trustingVerifier(t *testing.T) *freshnesscheat.Verifier {
	t.Helper()
	return &freshnesscheat.Verifier{
		Witness:  freshnesscheat.TrustingTestWitness{},
		Registry: fixtureRegistry(t),
	}
}

// ----- happy path --------------------------------------------------------

func TestVerify_Accepts_HappyPath(t *testing.T) {
	t.Parallel()
	v := trustingVerifier(t)
	ev := fixtureEvidence(t)
	payload := makePayload(t, ev)

	cap_, err := v.Verify(payload, 0)
	if err != nil {
		t.Fatalf("Verify rejected happy-path evidence: %v", err)
	}
	if cap_ != freshnesscheat.DefaultMaxSlashDust {
		t.Fatalf("cap=%d, want DefaultMaxSlashDust=%d",
			cap_, freshnesscheat.DefaultMaxSlashDust)
	}
}

func TestVerify_ReturnsConfiguredCap(t *testing.T) {
	t.Parallel()
	const customCap uint64 = 1_234_567_890
	v := &freshnesscheat.Verifier{
		Witness:      freshnesscheat.TrustingTestWitness{},
		Registry:     fixtureRegistry(t),
		MaxSlashDust: customCap,
	}
	cap_, err := v.Verify(makePayload(t, fixtureEvidence(t)), 0)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if cap_ != customCap {
		t.Fatalf("cap=%d, want %d", cap_, customCap)
	}
}

// ----- rejection: structural ---------------------------------------------

func TestVerify_Rejects_PreV2Proof(t *testing.T) {
	t.Parallel()
	v := trustingVerifier(t)
	ev := fixtureEvidence(t)
	ev.Proof.Version = 1 // pre-v2

	// Re-encode evidence with pre-v2 proof — but the canonical
	// encoder will object before we even get to Verify. Build
	// the wire bytes manually to bypass EncodeEvidence and
	// land directly in Verify.
	blob := manualEncodeEvidenceBypassingV2Check(t, ev)
	payload := slashing.SlashPayload{
		NodeID:       fxNodeID,
		EvidenceKind: slashing.EvidenceKindFreshnessCheat,
		EvidenceBlob: blob,
	}

	_, err := v.Verify(payload, 0)
	if err == nil {
		t.Fatal("Verify accepted pre-v2 proof")
	}
	if !errors.Is(err, freshnesscheat.ErrProofNotV2) {
		t.Errorf("error %v does not wrap ErrProofNotV2", err)
	}
	if !errors.Is(err, slashing.ErrEvidenceVerification) {
		t.Errorf("error %v does not wrap ErrEvidenceVerification", err)
	}
}

func TestVerify_Rejects_AnchorBeforeIssuedAt(t *testing.T) {
	t.Parallel()
	v := trustingVerifier(t)
	ev := fixtureEvidence(t)
	ev.AnchorBlockTime = fxIssuedAt - 1 // anchor predates the proof

	_, err := v.Verify(makePayload(t, ev), 0)
	if err == nil {
		t.Fatal("Verify accepted anchor that predates proof")
	}
	if !errors.Is(err, freshnesscheat.ErrAnchorBeforeIssuedAt) {
		t.Errorf("error %v does not wrap ErrAnchorBeforeIssuedAt", err)
	}
}

func TestVerify_Rejects_AnchorEqualsIssuedAt(t *testing.T) {
	t.Parallel()
	// Boundary case: anchor == issuedAt. Non-physical (block
	// can't include a proof signed at the same exact second
	// the block was sealed) AND staleness=0, so two reasons
	// to reject. Test asserts we trip the anchor-pre-issuedat
	// rule first (which is more specific).
	v := trustingVerifier(t)
	ev := fixtureEvidence(t)
	ev.AnchorBlockTime = fxIssuedAt

	_, err := v.Verify(makePayload(t, ev), 0)
	if err == nil {
		t.Fatal("Verify accepted anchor == issuedAt")
	}
	if !errors.Is(err, freshnesscheat.ErrAnchorBeforeIssuedAt) {
		t.Errorf("error %v does not wrap ErrAnchorBeforeIssuedAt", err)
	}
}

func TestVerify_Rejects_AnchorTooOld(t *testing.T) {
	t.Parallel()
	v := trustingVerifier(t)
	ev := fixtureEvidence(t)
	// Two years past issuedAt — well past the 1-year sanity
	// limit.
	ev.AnchorBlockTime = fxIssuedAt + 2*freshnesscheat.MaxAnchorAgeSeconds

	_, err := v.Verify(makePayload(t, ev), 0)
	if err == nil {
		t.Fatal("Verify accepted 2-year-old anchor")
	}
	if !errors.Is(err, freshnesscheat.ErrAnchorTooOld) {
		t.Errorf("error %v does not wrap ErrAnchorTooOld", err)
	}
}

func TestVerify_Rejects_NotStaleEnough(t *testing.T) {
	t.Parallel()
	v := trustingVerifier(t)
	ev := fixtureEvidence(t)
	// Just at the threshold: 60s window + 30s grace = 90s, so
	// 90s exactly is rejected (we use strict >).
	ev.AnchorBlockTime = fxIssuedAt + 90

	_, err := v.Verify(makePayload(t, ev), 0)
	if err == nil {
		t.Fatal("Verify accepted boundary case (staleness == window+grace)")
	}
	if !errors.Is(err, freshnesscheat.ErrNotStaleEnough) {
		t.Errorf("error %v does not wrap ErrNotStaleEnough", err)
	}
}

func TestVerify_Accepts_OneSecondPastBoundary(t *testing.T) {
	t.Parallel()
	v := trustingVerifier(t)
	ev := fixtureEvidence(t)
	// 91s = window(60) + grace(30) + 1
	ev.AnchorBlockTime = fxIssuedAt + 91

	if _, err := v.Verify(makePayload(t, ev), 0); err != nil {
		t.Fatalf("Verify rejected staleness=91s (window+grace+1): %v", err)
	}
}

func TestVerify_Rejects_BundleNodeIDMismatch(t *testing.T) {
	t.Parallel()
	v := trustingVerifier(t)
	ev := fixtureEvidence(t)
	payload := makePayload(t, ev)
	payload.NodeID = "other-rig" // bundle says fxNodeID

	_, err := v.Verify(payload, 0)
	if err == nil {
		t.Fatal("Verify accepted node_id-mismatch")
	}
	if !errors.Is(err, freshnesscheat.ErrBundleNodeIDMismatch) {
		t.Errorf("error %v does not wrap ErrBundleNodeIDMismatch", err)
	}
}

func TestVerify_Rejects_RegistryNotEnrolled(t *testing.T) {
	t.Parallel()
	// Empty registry — node not enrolled.
	v := &freshnesscheat.Verifier{
		Witness:  freshnesscheat.TrustingTestWitness{},
		Registry: hmac.NewInMemoryRegistry(),
	}
	_, err := v.Verify(makePayload(t, fixtureEvidence(t)), 0)
	if err == nil {
		t.Fatal("Verify accepted slash for non-enrolled node")
	}
	if !errors.Is(err, slashing.ErrEvidenceVerification) {
		t.Errorf("error %v does not wrap ErrEvidenceVerification", err)
	}
}

func TestVerify_Rejects_EmptyAttestation(t *testing.T) {
	t.Parallel()
	v := trustingVerifier(t)
	ev := fixtureEvidence(t)
	ev.Proof.Attestation.Type = ""
	ev.Proof.Attestation.BundleBase64 = ""

	// EncodeEvidence will reject (canonical proof rejects
	// empty attestation) before we get to Verify, so build
	// the wire by hand. Empty attestation type means the
	// canonical encoder also won't run — we hand-encode.
	blob := manualEncodeEvidenceBypassingV2Check(t, ev)
	payload := slashing.SlashPayload{
		NodeID:       fxNodeID,
		EvidenceKind: slashing.EvidenceKindFreshnessCheat,
		EvidenceBlob: blob,
	}
	_, err := v.Verify(payload, 0)
	if err == nil {
		t.Fatal("Verify accepted empty-attestation proof")
	}
	if !errors.Is(err, slashing.ErrEvidenceVerification) {
		t.Errorf("error %v does not wrap ErrEvidenceVerification", err)
	}
}

func TestVerify_Rejects_MalformedBundle(t *testing.T) {
	t.Parallel()
	v := trustingVerifier(t)
	ev := fixtureEvidence(t)
	ev.Proof.Attestation.BundleBase64 = "not-valid-base64-bundle!!!"

	blob := manualEncodeEvidenceBypassingV2Check(t, ev)
	payload := slashing.SlashPayload{
		NodeID:       fxNodeID,
		EvidenceKind: slashing.EvidenceKindFreshnessCheat,
		EvidenceBlob: blob,
	}

	_, err := v.Verify(payload, 0)
	if err == nil {
		t.Fatal("Verify accepted malformed-bundle proof")
	}
	if !errors.Is(err, slashing.ErrEvidenceVerification) {
		t.Errorf("error %v does not wrap ErrEvidenceVerification", err)
	}
	if !strings.Contains(err.Error(), "forged-attestation") {
		t.Errorf("malformed-bundle error %v should mention forged-attestation routing hint", err)
	}
}

func TestVerify_Rejects_WrongEvidenceKind(t *testing.T) {
	t.Parallel()
	v := trustingVerifier(t)
	payload := makePayload(t, fixtureEvidence(t))
	payload.EvidenceKind = slashing.EvidenceKindForgedAttestation

	_, err := v.Verify(payload, 0)
	if err == nil {
		t.Fatal("Verify accepted wrong EvidenceKind")
	}
	if !errors.Is(err, slashing.ErrUnknownEvidenceKind) {
		t.Errorf("error %v does not wrap ErrUnknownEvidenceKind", err)
	}
}

// ----- witness postures --------------------------------------------------

func TestVerify_RejectAllWitness_RejectsAllAnchors(t *testing.T) {
	t.Parallel()
	v := &freshnesscheat.Verifier{
		Witness:  freshnesscheat.RejectAllWitness{},
		Registry: fixtureRegistry(t),
	}
	_, err := v.Verify(makePayload(t, fixtureEvidence(t)), 0)
	if err == nil {
		t.Fatal("RejectAllWitness accepted an anchor")
	}
	if !errors.Is(err, freshnesscheat.ErrAnchorUnverified) {
		t.Errorf("error %v does not wrap ErrAnchorUnverified", err)
	}
	if !errors.Is(err, slashing.ErrEvidenceVerification) {
		t.Errorf("error %v does not wrap ErrEvidenceVerification", err)
	}
	// Operator-diagnostic hint: RejectAllWitness explains the
	// missing dependency.
	if !strings.Contains(err.Error(), "BFT finality") {
		t.Errorf("error %v should mention the BFT-finality dependency", err)
	}
}

func TestVerify_FixedAnchorWitness_AcceptsExactMatch(t *testing.T) {
	t.Parallel()
	ev := fixtureEvidence(t)
	pid, err := ev.Proof.ID()
	if err != nil {
		t.Fatalf("proof.ID: %v", err)
	}
	v := &freshnesscheat.Verifier{
		Witness: freshnesscheat.FixedAnchorWitness{
			Height:    ev.AnchorHeight,
			BlockTime: ev.AnchorBlockTime,
			ProofID:   pid,
		},
		Registry: fixtureRegistry(t),
	}
	if _, err := v.Verify(makePayload(t, ev), 0); err != nil {
		t.Fatalf("FixedAnchorWitness rejected its own anchor: %v", err)
	}
}

func TestVerify_FixedAnchorWitness_RejectsOnHeightMismatch(t *testing.T) {
	t.Parallel()
	ev := fixtureEvidence(t)
	pid, _ := ev.Proof.ID()
	v := &freshnesscheat.Verifier{
		Witness: freshnesscheat.FixedAnchorWitness{
			Height:    ev.AnchorHeight + 1, // mismatch
			BlockTime: ev.AnchorBlockTime,
			ProofID:   pid,
		},
		Registry: fixtureRegistry(t),
	}
	_, err := v.Verify(makePayload(t, ev), 0)
	if err == nil || !errors.Is(err, freshnesscheat.ErrAnchorUnverified) {
		t.Errorf("FixedAnchorWitness should have rejected height-mismatch; got %v", err)
	}
}

func TestVerify_FixedAnchorWitness_RejectsOnProofIDMismatch(t *testing.T) {
	t.Parallel()
	ev := fixtureEvidence(t)
	v := &freshnesscheat.Verifier{
		Witness: freshnesscheat.FixedAnchorWitness{
			Height:    ev.AnchorHeight,
			BlockTime: ev.AnchorBlockTime,
			ProofID:   [32]byte{0xDE, 0xAD},
		},
		Registry: fixtureRegistry(t),
	}
	_, err := v.Verify(makePayload(t, ev), 0)
	if err == nil || !errors.Is(err, freshnesscheat.ErrAnchorUnverified) {
		t.Errorf("FixedAnchorWitness should have rejected proof_id-mismatch; got %v", err)
	}
}

// ----- nil-collaborator guards ------------------------------------------

func TestVerify_Rejects_NilWitness(t *testing.T) {
	t.Parallel()
	v := &freshnesscheat.Verifier{
		Witness:  nil,
		Registry: fixtureRegistry(t),
	}
	_, err := v.Verify(makePayload(t, fixtureEvidence(t)), 0)
	if err == nil {
		t.Fatal("Verify accepted nil-Witness verifier")
	}
}

func TestVerify_Rejects_NilRegistry(t *testing.T) {
	t.Parallel()
	v := &freshnesscheat.Verifier{
		Witness:  freshnesscheat.TrustingTestWitness{},
		Registry: nil,
	}
	_, err := v.Verify(makePayload(t, fixtureEvidence(t)), 0)
	if err == nil {
		t.Fatal("Verify accepted nil-Registry verifier")
	}
}

// ----- NewVerifier constructor defaults ---------------------------------

func TestNewVerifier_NilWitness_DefaultsToRejectAll(t *testing.T) {
	t.Parallel()
	v := freshnesscheat.NewVerifier(nil, fixtureRegistry(t), 0)
	_, err := v.Verify(makePayload(t, fixtureEvidence(t)), 0)
	if err == nil {
		t.Fatal("default-witness verifier accepted an anchor")
	}
	if !errors.Is(err, freshnesscheat.ErrAnchorUnverified) {
		t.Errorf("error %v does not wrap ErrAnchorUnverified", err)
	}
}

func TestNewVerifier_KindIsCorrect(t *testing.T) {
	t.Parallel()
	v := freshnesscheat.NewVerifier(nil, fixtureRegistry(t), 0)
	if got := v.Kind(); got != slashing.EvidenceKindFreshnessCheat {
		t.Errorf("Kind()=%q, want %q", got, slashing.EvidenceKindFreshnessCheat)
	}
}

// ----- encode/decode round-trip -----------------------------------------

func TestEncodeDecode_RoundTrip(t *testing.T) {
	t.Parallel()
	ev := fixtureEvidence(t)
	blob, err := freshnesscheat.EncodeEvidence(ev)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := freshnesscheat.DecodeEvidence(blob)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got.AnchorHeight != ev.AnchorHeight {
		t.Errorf("AnchorHeight: got %d want %d", got.AnchorHeight, ev.AnchorHeight)
	}
	if got.AnchorBlockTime != ev.AnchorBlockTime {
		t.Errorf("AnchorBlockTime: got %d want %d", got.AnchorBlockTime, ev.AnchorBlockTime)
	}
	if got.Memo != ev.Memo {
		t.Errorf("Memo: got %q want %q", got.Memo, ev.Memo)
	}
	if got.Proof.Height != ev.Proof.Height {
		t.Errorf("Proof.Height: got %d want %d", got.Proof.Height, ev.Proof.Height)
	}
	if got.Proof.Attestation.IssuedAt != ev.Proof.Attestation.IssuedAt {
		t.Errorf("Attestation.IssuedAt: got %d want %d",
			got.Proof.Attestation.IssuedAt, ev.Proof.Attestation.IssuedAt)
	}
}

func TestEncode_RejectsOversizeMemo(t *testing.T) {
	t.Parallel()
	ev := fixtureEvidence(t)
	ev.Memo = strings.Repeat("x", freshnesscheat.MaxMemoLen+1)
	if _, err := freshnesscheat.EncodeEvidence(ev); err == nil {
		t.Fatal("Encode accepted oversize memo")
	}
}

func TestDecode_RejectsTrailingBytes(t *testing.T) {
	t.Parallel()
	ev := fixtureEvidence(t)
	blob, err := freshnesscheat.EncodeEvidence(ev)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	withTrailing := append(append([]byte{}, blob...), []byte(`{"x":1}`)...)
	if _, err := freshnesscheat.DecodeEvidence(withTrailing); err == nil {
		t.Fatal("Decode accepted bytes with trailing JSON")
	}
}

func TestDecode_RejectsUnknownField(t *testing.T) {
	t.Parallel()
	ev := fixtureEvidence(t)
	blob, err := freshnesscheat.EncodeEvidence(ev)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	// Drop the trailing '}' and inject an unknown field.
	tampered := append([]byte{}, blob[:len(blob)-1]...)
	tampered = append(tampered, []byte(`,"unexpected_field":42}`)...)
	if _, err := freshnesscheat.DecodeEvidence(tampered); err == nil {
		t.Fatal("Decode accepted unknown field")
	}
}

func TestDecode_RejectsNonPositiveAnchorTime(t *testing.T) {
	t.Parallel()
	wire := map[string]interface{}{
		"proof":             json.RawMessage(canonicalProofJSON(t, signedV2Proof(t, 100))),
		"anchor_height":     "1",
		"anchor_block_time": 0,
	}
	blob, err := json.Marshal(wire)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, err := freshnesscheat.DecodeEvidence(blob); err == nil {
		t.Fatal("Decode accepted zero anchor_block_time")
	}
}

func TestDecode_RejectsMissingFields(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"missing-proof":         `{"anchor_height":"1","anchor_block_time":2}`,
		"missing-anchor-height": `{"proof":` + string(canonicalProofJSON(t, signedV2Proof(t, 100))) + `,"anchor_block_time":2}`,
	}
	for name, blob := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := freshnesscheat.DecodeEvidence([]byte(blob)); err == nil {
				t.Fatal("Decode accepted blob with missing required field")
			}
		})
	}
}

// ----- production wiring -------------------------------------------------

func TestNewProductionSlashingDispatcher_AllKindsRegistered(t *testing.T) {
	t.Parallel()
	d, err := freshnesscheat.NewProductionSlashingDispatcher(
		fixtureRegistry(t),
		nil,                                  // empty deny-list
		freshnesscheat.TrustingTestWitness{}, // testnet posture
		0, 0, 0,
	)
	if err != nil {
		t.Fatalf("NewProductionSlashingDispatcher: %v", err)
	}
	got := d.Kinds()
	if len(got) != len(slashing.AllEvidenceKinds) {
		t.Fatalf("kinds count = %d, want %d", len(got), len(slashing.AllEvidenceKinds))
	}
	// All three should accept their respective happy-path
	// payloads. We exercise freshness-cheat here to confirm
	// the verifier wired through the dispatcher reaches
	// TrustingTestWitness rather than the StubVerifier
	// fallback.
	cap_, err := d.Verify(makePayload(t, fixtureEvidence(t)), 0)
	if err != nil {
		t.Fatalf("dispatcher rejected freshness-cheat: %v", err)
	}
	if cap_ != freshnesscheat.DefaultMaxSlashDust {
		t.Errorf("cap=%d, want DefaultMaxSlashDust=%d",
			cap_, freshnesscheat.DefaultMaxSlashDust)
	}
}

func TestNewProductionSlashingDispatcher_NilWitness_RejectsFreshnessCheat(t *testing.T) {
	t.Parallel()
	d, err := freshnesscheat.NewProductionSlashingDispatcher(
		fixtureRegistry(t),
		nil,
		nil, // → RejectAllWitness
		0, 0, 0,
	)
	if err != nil {
		t.Fatalf("NewProductionSlashingDispatcher: %v", err)
	}
	_, err = d.Verify(makePayload(t, fixtureEvidence(t)), 0)
	if err == nil {
		t.Fatal("RejectAllWitness path accepted an anchor")
	}
	if !errors.Is(err, slashing.ErrEvidenceVerification) {
		t.Errorf("error %v does not wrap ErrEvidenceVerification", err)
	}
}

// ----- helpers -----------------------------------------------------------

// canonicalProofJSON returns the canonical-JSON serialisation of
// a proof. Used by hand-crafted wire fixtures that need to embed
// a real proof without going through EncodeEvidence (which
// enforces v2-bundle preconditions).
func canonicalProofJSON(t *testing.T, p mining.Proof) []byte {
	t.Helper()
	canon, err := p.CanonicalJSON()
	if err != nil {
		t.Fatalf("CanonicalJSON: %v", err)
	}
	return canon
}

// manualEncodeEvidenceBypassingV2Check builds a wire-format
// blob for an Evidence whose proof MAY violate the v2 / shape
// preconditions that EncodeEvidence enforces. Used by tests
// that want to land such evidence at the Verifier so the
// Verifier's own structural checks fire (rather than the
// encoder's).
func manualEncodeEvidenceBypassingV2Check(t *testing.T, ev freshnesscheat.Evidence) []byte {
	t.Helper()
	// We can't always go through Proof.CanonicalJSON because
	// validateShape rejects e.g. version=0 / missing
	// attestation. Drop straight to json.Marshal (which is
	// not byte-canonical, but Verify's DecodeEvidence path
	// only requires the wire shape, not canonicality).
	type attWire struct {
		Type               string `json:"type"`
		Bundle             string `json:"bundle"`
		GPUArch            string `json:"gpu_arch"`
		ClaimedHashrateHPS uint64 `json:"claimed_hashrate_hps"`
		Nonce              string `json:"nonce"`
		IssuedAt           int64  `json:"issued_at"`
	}
	type proofWire struct {
		Version     uint32  `json:"version"`
		Epoch       string  `json:"epoch"`
		Height      string  `json:"height"`
		HeaderHash  string  `json:"header_hash"`
		MinerAddr   string  `json:"miner_addr"`
		BatchRoot   string  `json:"batch_root"`
		BatchCount  uint32  `json:"batch_count"`
		Nonce       string  `json:"nonce"`
		MixDigest   string  `json:"mix_digest"`
		Attestation attWire `json:"attestation"`
	}
	pw := proofWire{
		Version:    ev.Proof.Version,
		Epoch:      uintToStr(ev.Proof.Epoch),
		Height:     uintToStr(ev.Proof.Height),
		HeaderHash: hex.EncodeToString(ev.Proof.HeaderHash[:]),
		MinerAddr:  ev.Proof.MinerAddr,
		BatchRoot:  hex.EncodeToString(ev.Proof.BatchRoot[:]),
		BatchCount: ev.Proof.BatchCount,
		Nonce:      hex.EncodeToString(ev.Proof.Nonce[:]),
		MixDigest:  hex.EncodeToString(ev.Proof.MixDigest[:]),
		Attestation: attWire{
			Type:               string(ev.Proof.Attestation.Type),
			Bundle:             ev.Proof.Attestation.BundleBase64,
			GPUArch:            ev.Proof.Attestation.GPUArch,
			ClaimedHashrateHPS: ev.Proof.Attestation.ClaimedHashrateHPS,
			Nonce:              hex.EncodeToString(ev.Proof.Attestation.Nonce[:]),
			IssuedAt:           ev.Proof.Attestation.IssuedAt,
		},
	}
	proofBytes, err := json.Marshal(pw)
	if err != nil {
		t.Fatalf("marshal proof: %v", err)
	}
	wire := struct {
		ProofJSON       json.RawMessage `json:"proof"`
		AnchorHeight    string          `json:"anchor_height"`
		AnchorBlockTime int64           `json:"anchor_block_time"`
		Memo            string          `json:"memo,omitempty"`
	}{
		ProofJSON:       proofBytes,
		AnchorHeight:    uintToStr(ev.AnchorHeight),
		AnchorBlockTime: ev.AnchorBlockTime,
		Memo:            ev.Memo,
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(wire); err != nil {
		t.Fatalf("manual encode: %v", err)
	}
	out := buf.Bytes()
	if n := len(out); n > 0 && out[n-1] == '\n' {
		out = out[:n-1]
	}
	return out
}

func uintToStr[T ~uint64 | ~uint32](u T) string {
	const digits = "0123456789"
	if u == 0 {
		return "0"
	}
	buf := make([]byte, 0, 20)
	for u > 0 {
		buf = append([]byte{digits[u%10]}, buf...)
		u /= 10
	}
	return string(buf)
}

// ----- benchmark guard ---------------------------------------------------

// Sanity: a happy-path Verify under TrustingTestWitness should
// be cheap. This guards against an accidental introduction of
// expensive work in the hot path. Not a benchmark — just a
// rough wall-clock smoke check that runs in CI.
func TestVerify_HappyPath_CompletesQuickly(t *testing.T) {
	t.Parallel()
	v := trustingVerifier(t)
	payload := makePayload(t, fixtureEvidence(t))
	const N = 1000
	deadline := time.Now().Add(2 * time.Second)
	for i := 0; i < N; i++ {
		if _, err := v.Verify(payload, 0); err != nil {
			t.Fatalf("Verify on iteration %d: %v", i, err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("Verify x%d exceeded 2s wall-clock budget", i+1)
		}
	}
}
