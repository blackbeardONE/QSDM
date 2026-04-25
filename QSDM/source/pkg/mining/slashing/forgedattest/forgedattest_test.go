package forgedattest_test

// Tests for the forged-attestation evidence verifier. The
// strategy is to build a known-good v2 proof + bundle (the same
// fixture the hmac.Verifier tests use), then mutate exactly one
// invariant per test case and assert that:
//
//   - the verifier rejects (with ErrEvidenceVerification) when
//     the mutation makes the HMAC verifier accept (i.e. evidence
//     is bogus);
//   - the verifier accepts (returning the configured cap) when
//     the mutation makes the HMAC verifier reject (i.e. real
//     forged attestation);
//   - structural invariants are enforced (NodeID binding,
//     malformed evidence, oversize memo, unknown fault class).

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mining"
	"github.com/blackbeardONE/QSDM/pkg/mining/attest/hmac"
	"github.com/blackbeardONE/QSDM/pkg/mining/slashing"
	"github.com/blackbeardONE/QSDM/pkg/mining/slashing/forgedattest"
)

// ----- shared fixtures ----------------------------------------------------

const (
	fxNodeID  = "alice-rtx4090-01"
	fxGPUUUID = "GPU-01234567-89ab-cdef-0123-456789abcdef"
	fxGPUName = "NVIDIA GeForce RTX 4090"
	fxAddr    = "qsdm1testminer"
)

var fxKey = []byte("test-key-do-not-use----32-bytes!")

// buildSignedProof produces a v2 proof + the matching bundle
// (signed with fxKey under fxNodeID/fxGPUUUID). Tests mutate the
// returned values to introduce specific faults.
func buildSignedProof(t *testing.T) (*hmac.InMemoryRegistry, mining.Proof, hmac.Bundle) {
	t.Helper()
	reg := hmac.NewInMemoryRegistry()
	if err := reg.Enroll(fxNodeID, fxGPUUUID, fxKey); err != nil {
		t.Fatalf("enroll: %v", err)
	}

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
		Epoch:      0,
		Height:     200,
		HeaderHash: [32]byte{0xBB},
		BatchRoot:  batchRoot,
		BatchCount: 1,
		Nonce:      [16]byte{0x07},
		MixDigest:  mix,
		MinerAddr:  fxAddr,
		Attestation: mining.Attestation{
			Type:     mining.AttestationTypeHMAC,
			GPUArch:  "ada",
			Nonce:    nonce,
			IssuedAt: 1_700_000_000,
		},
	}

	b := hmac.Bundle{
		ChallengeBind: hmac.HexChallengeBind(fxAddr, batchRoot, mix),
		ComputeCap:    "8.9",
		CUDAVersion:   "12.8",
		DriverVer:     "572.16",
		GPUName:       fxGPUName,
		GPUUUID:       fxGPUUUID,
		IssuedAt:      1_700_000_000,
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
	return reg, p, signed
}

// reSign rebuilds a proof's bundle after a test mutates a field
// covered by the HMAC. This is the "the attacker has the key
// and signs whatever they want" path.
func reSign(t *testing.T, p *mining.Proof, b hmac.Bundle, key []byte) {
	t.Helper()
	signed, err := b.Sign(key)
	if err != nil {
		t.Fatalf("re-sign: %v", err)
	}
	encoded, err := signed.MarshalBase64()
	if err != nil {
		t.Fatalf("re-marshal: %v", err)
	}
	p.Attestation.BundleBase64 = encoded
}

// makePayload wraps an Evidence into a SlashPayload ready for
// the verifier. Tests can override fields on the returned
// payload before calling Verify.
func makePayload(t *testing.T, ev forgedattest.Evidence) slashing.SlashPayload {
	t.Helper()
	blob, err := forgedattest.EncodeEvidence(ev)
	if err != nil {
		t.Fatalf("encode evidence: %v", err)
	}
	return slashing.SlashPayload{
		NodeID:          fxNodeID,
		EvidenceKind:    slashing.EvidenceKindForgedAttestation,
		EvidenceBlob:    blob,
		SlashAmountDust: forgedattest.DefaultMaxSlashDust,
	}
}

// ----- happy/sad path -----------------------------------------------------

// TestVerify_BogusEvidence_ValidProof asserts that submitting a
// pristine, validly-signed proof as forged-attestation evidence
// is REJECTED — there's no forgery to slash. This is the
// "slasher tried to grief an honest miner" defence.
func TestVerify_BogusEvidence_ValidProof(t *testing.T) {
	t.Parallel()
	reg, p, _ := buildSignedProof(t)
	v := forgedattest.NewVerifier(reg, 0)

	payload := makePayload(t, forgedattest.Evidence{
		Proof:      p,
		FaultClass: forgedattest.FaultHMACMismatch,
	})

	cap_, err := v.Verify(payload, 0)
	if err == nil {
		t.Fatalf("expected ErrEvidenceVerification for honest proof, got cap=%d", cap_)
	}
	if !errors.Is(err, slashing.ErrEvidenceVerification) {
		t.Fatalf("error %v does not wrap ErrEvidenceVerification", err)
	}
}

// TestVerify_HMACMismatch is the canonical fault: the bundle's
// HMAC field does not match the canonical-MAC of the bundle's
// other fields under the registered key. The proof was signed
// with the wrong key (or no key). This is the offence the
// verifier was built to catch.
func TestVerify_HMACMismatch(t *testing.T) {
	t.Parallel()
	reg, p, _ := buildSignedProof(t)
	// Corrupt the HMAC field after signing — the bundle is now
	// internally inconsistent.
	bundle, err := hmac.ParseBundle(p.Attestation.BundleBase64)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// Flip the last hex character to break the MAC without
	// breaking length validation.
	tampered := []byte(bundle.HMAC)
	if tampered[len(tampered)-1] == '0' {
		tampered[len(tampered)-1] = '1'
	} else {
		tampered[len(tampered)-1] = '0'
	}
	bundle.HMAC = string(tampered)
	reEncoded, err := bundle.MarshalBase64()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	p.Attestation.BundleBase64 = reEncoded

	v := forgedattest.NewVerifier(reg, 0)
	payload := makePayload(t, forgedattest.Evidence{
		Proof:      p,
		FaultClass: forgedattest.FaultHMACMismatch,
	})

	cap_, err := v.Verify(payload, 1234)
	if err != nil {
		t.Fatalf("expected slash to land, got error: %v", err)
	}
	if cap_ != forgedattest.DefaultMaxSlashDust {
		t.Fatalf("cap = %d, want %d", cap_, forgedattest.DefaultMaxSlashDust)
	}
}

// TestVerify_GPUUUIDMismatch: attacker has the operator's HMAC
// key but mines from a different physical card whose UUID does
// not match the enrolled one. They re-sign the bundle so the MAC
// is valid, but step 5 of the HMAC flow rejects on UUID
// mismatch. This is the "lent my key out" / "stolen key" abuse
// path.
func TestVerify_GPUUUIDMismatch(t *testing.T) {
	t.Parallel()
	reg, p, b := buildSignedProof(t)
	b.GPUUUID = "GPU-DEADBEEF-0000-0000-0000-000000000000"
	reSign(t, &p, b, fxKey) // attacker has the key

	v := forgedattest.NewVerifier(reg, 0)
	payload := makePayload(t, forgedattest.Evidence{
		Proof:      p,
		FaultClass: forgedattest.FaultGPUUUIDMismatch,
	})

	cap_, err := v.Verify(payload, 0)
	if err != nil {
		t.Fatalf("expected slash to land, got %v", err)
	}
	if cap_ == 0 {
		t.Fatalf("cap = 0, want non-zero")
	}
}

// TestVerify_ChallengeBindMismatch: bundle's challenge_bind
// commits to a different (miner_addr, batch_root, mix_digest)
// than the proof carries. This is a bundle-replay attempt: the
// attacker re-uses one valid bundle across two different proofs.
func TestVerify_ChallengeBindMismatch(t *testing.T) {
	t.Parallel()
	reg, p, b := buildSignedProof(t)
	// Re-bind to a different mix_digest. The bundle now
	// commits to a proof we did NOT submit.
	var rogueMix [32]byte
	for i := range rogueMix {
		rogueMix[i] = 0x42
	}
	b.ChallengeBind = hmac.HexChallengeBind(fxAddr, p.BatchRoot, rogueMix)
	reSign(t, &p, b, fxKey)

	v := forgedattest.NewVerifier(reg, 0)
	payload := makePayload(t, forgedattest.Evidence{
		Proof:      p,
		FaultClass: forgedattest.FaultChallengeBindMismat,
	})
	if _, err := v.Verify(payload, 0); err != nil {
		t.Fatalf("expected slash to land, got %v", err)
	}
}

// TestVerify_DeniedGPU: the bundle's gpu_name matches a
// substring on the verifier's deny-list. This is the governance
// kill-switch path (spec §5.3) — flag-it-on, slash everyone with
// the affected card.
func TestVerify_DeniedGPU(t *testing.T) {
	t.Parallel()
	reg, p, _ := buildSignedProof(t)

	denied := hmac.SubstringDenyList{Substrings: []string{"RTX 4090"}}
	v := &forgedattest.Verifier{
		Registry:     reg,
		DenyList:     denied,
		MaxSlashDust: forgedattest.DefaultMaxSlashDust,
	}

	payload := makePayload(t, forgedattest.Evidence{
		Proof:      p,
		FaultClass: forgedattest.FaultDenyListedGPU,
	})

	if _, err := v.Verify(payload, 0); err != nil {
		t.Fatalf("expected slash to land, got %v", err)
	}
}

// TestVerify_NodeNotEnrolled: the attacker's bundle.node_id
// matches the slash payload's NodeID, but the registry has no
// such enrollment. This means either (a) the chain accepted a
// proof for an unenrolled node (a serious validator bug), or
// (b) the operator was revoked between acceptance and slashing.
// Either way, this is the offence the verifier slashes.
//
// Note: the chain-side applier ALSO independently rejects slash
// txs for non-enrolled nodes via slashing.ErrNodeNotEnrolled.
// That belt-and-braces is intentional — the verifier here treats
// the absence purely as a forged-attestation rejection cause.
func TestVerify_NodeNotEnrolled(t *testing.T) {
	t.Parallel()
	// Build a registry with a DIFFERENT node enrolled — the
	// fixture node is intentionally absent.
	reg := hmac.NewInMemoryRegistry()
	if err := reg.Enroll("someone-else", fxGPUUUID, fxKey); err != nil {
		t.Fatalf("enroll: %v", err)
	}
	// Use buildSignedProof to get a valid proof shape, but
	// with a registry that does not contain fxNodeID.
	_, p, _ := buildSignedProof(t)

	v := forgedattest.NewVerifier(reg, 0)
	payload := makePayload(t, forgedattest.Evidence{
		Proof:      p,
		FaultClass: forgedattest.FaultNodeNotEnrolled,
	})
	if _, err := v.Verify(payload, 0); err != nil {
		t.Fatalf("expected slash to land, got %v", err)
	}
}

// ----- structural rejections ---------------------------------------------

// TestVerify_NodeIDBindingMismatch: the slash payload claims
// NodeID X but the embedded bundle's node_id is Y. This is the
// "slasher tried to attribute someone else's faulty proof to me"
// defence.
func TestVerify_NodeIDBindingMismatch(t *testing.T) {
	t.Parallel()
	reg, p, b := buildSignedProof(t)
	b.NodeID = "evil-impostor"
	reSign(t, &p, b, fxKey)

	v := forgedattest.NewVerifier(reg, 0)
	payload := makePayload(t, forgedattest.Evidence{
		Proof: p,
	})
	// Override NodeID so the bundle.NodeID != payload.NodeID.
	payload.NodeID = fxNodeID

	if _, err := v.Verify(payload, 0); err == nil {
		t.Fatalf("expected ErrEvidenceVerification on NodeID mismatch")
	} else if !errors.Is(err, slashing.ErrEvidenceVerification) {
		t.Fatalf("error %v does not wrap ErrEvidenceVerification", err)
	}
}

// TestVerify_BadEvidenceJSON: the EvidenceBlob is not parseable
// JSON. Reject as ErrEvidenceVerification (with ErrPayloadDecode-
// style wrapping inside the slasher path is the chain applier's
// concern; here the verifier just refuses).
func TestVerify_BadEvidenceJSON(t *testing.T) {
	t.Parallel()
	reg, _, _ := buildSignedProof(t)
	v := forgedattest.NewVerifier(reg, 0)

	payload := slashing.SlashPayload{
		NodeID:       fxNodeID,
		EvidenceKind: slashing.EvidenceKindForgedAttestation,
		EvidenceBlob: []byte("not-json"),
	}
	_, err := v.Verify(payload, 0)
	if err == nil || !errors.Is(err, slashing.ErrEvidenceVerification) {
		t.Fatalf("want ErrEvidenceVerification, got %v", err)
	}
}

// TestVerify_WrongKind: the dispatcher should never route the
// wrong kind to this verifier, but defence-in-depth the
// verifier rejects on its own.
func TestVerify_WrongKind(t *testing.T) {
	t.Parallel()
	reg, _, _ := buildSignedProof(t)
	v := forgedattest.NewVerifier(reg, 0)

	payload := slashing.SlashPayload{
		NodeID:       fxNodeID,
		EvidenceKind: slashing.EvidenceKindDoubleMining, // wrong
		EvidenceBlob: []byte("{}"),
	}
	_, err := v.Verify(payload, 0)
	if err == nil || !errors.Is(err, slashing.ErrUnknownEvidenceKind) {
		t.Fatalf("want ErrUnknownEvidenceKind, got %v", err)
	}
}

// TestVerify_NilRegistry: a misconfigured verifier with no
// registry must refuse to verify rather than panic.
func TestVerify_NilRegistry(t *testing.T) {
	t.Parallel()
	v := &forgedattest.Verifier{} // no Registry
	payload := slashing.SlashPayload{
		NodeID:       fxNodeID,
		EvidenceKind: slashing.EvidenceKindForgedAttestation,
		EvidenceBlob: []byte(`{"proof":{}}`),
	}
	if _, err := v.Verify(payload, 0); err == nil {
		t.Fatalf("expected error on nil Registry")
	}
}

// TestVerify_CapOverride: a verifier constructed with a
// non-default MaxSlashDust returns that cap on success.
func TestVerify_CapOverride(t *testing.T) {
	t.Parallel()
	reg, p, b := buildSignedProof(t)
	// Force a real fault (HMAC mismatch by signing with a
	// rogue key after we already have the registered key
	// installed for the node).
	rogue := []byte("rogue-key-rogue-key-rogue-key-32")
	reSign(t, &p, b, rogue)

	customCap := uint64(7_777_777)
	v := forgedattest.NewVerifier(reg, customCap)
	payload := makePayload(t, forgedattest.Evidence{
		Proof:      p,
		FaultClass: forgedattest.FaultHMACMismatch,
	})

	got, err := v.Verify(payload, 0)
	if err != nil {
		t.Fatalf("expected slash to land, got %v", err)
	}
	if got != customCap {
		t.Fatalf("cap = %d, want %d", got, customCap)
	}
}

// ----- encoder/decoder edge cases ----------------------------------------

// TestEncodeEvidence_RejectsLongMemo guards the MaxMemoLen cap
// at encode time so a slasher cannot accidentally ship oversize
// evidence.
func TestEncodeEvidence_RejectsLongMemo(t *testing.T) {
	t.Parallel()
	ev := forgedattest.Evidence{
		Memo: strings.Repeat("x", forgedattest.MaxMemoLen+1),
	}
	if _, err := forgedattest.EncodeEvidence(ev); err == nil {
		t.Fatalf("expected error on oversize memo")
	}
}

// TestEncodeEvidence_RejectsUnknownFaultClass guards the
// permitted FaultClass set at encode time.
func TestEncodeEvidence_RejectsUnknownFaultClass(t *testing.T) {
	t.Parallel()
	ev := forgedattest.Evidence{FaultClass: "made-up-class"}
	if _, err := forgedattest.EncodeEvidence(ev); err == nil {
		t.Fatalf("expected error on unknown fault_class")
	}
}

// TestDecodeEvidence_RejectsTrailingBytes is a defensive check
// against attackers stuffing replay material after the JSON
// object.
func TestDecodeEvidence_RejectsTrailingBytes(t *testing.T) {
	t.Parallel()
	_, p, _ := buildSignedProof(t)
	good, err := forgedattest.EncodeEvidence(forgedattest.Evidence{
		Proof:      p,
		FaultClass: forgedattest.FaultHMACMismatch,
	})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	tampered := append(append([]byte{}, good...), []byte(" garbage")...)
	if _, err := forgedattest.DecodeEvidence(tampered); err == nil {
		t.Fatalf("expected decode rejection on trailing bytes")
	}
}

// TestDecodeEvidence_RejectsUnknownFields ensures the verifier
// uses DisallowUnknownFields under the hood. A forward-compat
// extension would have to bump the wire by adding a new
// EvidenceKind, not by smuggling a field into this one.
func TestDecodeEvidence_RejectsUnknownFields(t *testing.T) {
	t.Parallel()
	// Hand-craft JSON with an unknown field.
	bad := []byte(`{"proof":{"version":2,"miner_addr":"x","batch_count":1},"unknown_field":1}`)
	if _, err := forgedattest.DecodeEvidence(bad); err == nil {
		t.Fatalf("expected decode rejection on unknown field")
	}
}

// TestEncodeEvidence_NoHTMLEscape verifies the encoder emits
// raw <, >, & (no \u003c-style escaping) so cross-implementation
// byte-stability holds.
func TestEncodeEvidence_NoHTMLEscape(t *testing.T) {
	t.Parallel()
	_, p, _ := buildSignedProof(t)
	ev := forgedattest.Evidence{Proof: p, Memo: "<a&b>"}
	out, err := forgedattest.EncodeEvidence(ev)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if !strings.Contains(string(out), "<a&b>") {
		t.Fatalf("memo HTML-escaped in output: %s", out)
	}
	// Round-trip stays equal via the package's own decoder.
	rt, err := forgedattest.DecodeEvidence(out)
	if err != nil {
		t.Fatalf("round-trip decode: %v", err)
	}
	if rt.Memo != ev.Memo {
		t.Fatalf("memo round-trip lost data: got %q want %q", rt.Memo, ev.Memo)
	}
}

// ----- compile-time ------------------------------------------------------

// TestVerifier_Kind locks the EvidenceKind so a future rename
// does not silently break dispatcher routing.
func TestVerifier_Kind(t *testing.T) {
	t.Parallel()
	v := forgedattest.NewVerifier(nil, 0)
	if got, want := v.Kind(), slashing.EvidenceKindForgedAttestation; got != want {
		t.Fatalf("Kind() = %q, want %q", got, want)
	}
}
