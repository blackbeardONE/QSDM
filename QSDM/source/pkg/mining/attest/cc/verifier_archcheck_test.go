package cc

// verifier_archcheck_test.go: integration tests for the §4.6.5
// "leaf cert subject ↔ gpu_arch" consistency check (Step 9 of
// the cc.Verifier flow).
//
// What's covered:
//
//   - LeafSubjectCN containing positive product evidence
//     consistent with claimed gpu_arch -> accept.
//   - LeafSubjectCN containing positive product evidence
//     CONTRADICTING claimed gpu_arch -> reject with
//     ErrAttestationSignatureInvalid wrapping
//     archcheck.ErrArchCertSubjectMismatch.
//   - LeafSubjectCN with NO product evidence (test fixture
//     default, corporate AIK label) + any gpu_arch -> accept
//     (evidence-based pass-through).
//   - GPUArch empty -> step 9 skipped (standalone-call path,
//     pre-fork bring-up vectors).
//   - Alias (`ada`) accepted alongside canonical
//     (`ada-lovelace`).

import (
	"errors"
	mathrand "math/rand"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mining"
	"github.com/blackbeardONE/QSDM/pkg/mining/attest/archcheck"
)

// buildArchcheckProof constructs a happy-path bundle with a
// caller-controlled LeafSubjectCN and stamps Attestation.GPUArch
// onto the outer Proof. The verifier is configured with
// MinFirmware floors disabled so step 9 is the discriminating
// check; everything else mirrors buildHappyPath.
func buildArchcheckProof(
	t *testing.T,
	leafCN string,
	gpuArch string,
) (mining.Proof, *Verifier, BuildOpts) {
	t.Helper()
	opts := BuildOpts{
		Reader:        mathrand.New(mathrand.NewSource(testSeed)),
		LeafSubjectCN: leafCN,
	}
	b64, root, _, err := BuildTestBundle(opts)
	if err != nil {
		t.Fatalf("BuildTestBundle: %v", err)
	}
	o := normaliseOpts(opts)
	p := mining.Proof{
		MinerAddr: o.MinerAddr,
		BatchRoot: o.BatchRoot,
		MixDigest: o.MixDigest,
		Attestation: mining.Attestation{
			Type:         mining.AttestationTypeCC,
			BundleBase64: b64,
			Nonce:        o.Nonce,
			IssuedAt:     o.IssuedAt,
			GPUArch:      gpuArch,
		},
	}
	v, err := NewVerifier(VerifierConfig{
		PinnedRoots: []PinnedRoot{{Subject: "test-root", DER: root.DER}},
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	return p, v, o
}

// TestVerifier_ArchCheck_HappyPath_HopperLeaf: a leaf minted
// with CN="NVIDIA H100 80GB HBM3" and gpu_arch="hopper" passes
// all the way through.
func TestVerifier_ArchCheck_HappyPath_HopperLeaf(t *testing.T) {
	p, v, o := buildArchcheckProof(t, "NVIDIA H100 80GB HBM3", "hopper")
	if err := v.VerifyAttestation(p, o.Now); err != nil {
		t.Fatalf("expected acceptance for matching arch+subject, got %v", err)
	}
}

// TestVerifier_ArchCheck_HappyPath_AdaAlias: alias
// gpu_arch="ada" canonicalises to ArchAdaLovelace and accepts
// against an Ada leaf.
func TestVerifier_ArchCheck_HappyPath_AdaAlias(t *testing.T) {
	p, v, o := buildArchcheckProof(t,
		"NVIDIA RTX 6000 Ada Generation", "ada")
	if err := v.VerifyAttestation(p, o.Now); err != nil {
		t.Fatalf("expected acceptance for ada alias, got %v", err)
	}
}

// TestVerifier_ArchCheck_RejectsContradiction_HopperCertAdaArch:
// the load-bearing test. A cert subject claiming "H100" with a
// claimed_arch="ada-lovelace" must reject with the canonical
// archcheck.ErrArchCertSubjectMismatch wrapped under
// ErrAttestationSignatureInvalid.
func TestVerifier_ArchCheck_RejectsContradiction_HopperCertAdaArch(t *testing.T) {
	p, v, o := buildArchcheckProof(t, "NVIDIA H100 80GB HBM3", "ada-lovelace")
	err := v.VerifyAttestation(p, o.Now)
	if err == nil {
		t.Fatal("expected rejection for arch contradiction; got nil")
	}
	if !errors.Is(err, mining.ErrAttestationSignatureInvalid) {
		t.Errorf("error %v does not wrap ErrAttestationSignatureInvalid", err)
	}
	if !errors.Is(err, archcheck.ErrArchCertSubjectMismatch) {
		t.Errorf("error %v does not wrap ErrArchCertSubjectMismatch", err)
	}
}

// TestVerifier_ArchCheck_RejectsContradiction_AdaCertHopperArch:
// the inverse — an Ada-evidence cert claiming Hopper.
func TestVerifier_ArchCheck_RejectsContradiction_AdaCertHopperArch(t *testing.T) {
	p, v, o := buildArchcheckProof(t,
		"NVIDIA GeForce RTX 4090", "hopper")
	err := v.VerifyAttestation(p, o.Now)
	if err == nil {
		t.Fatal("expected rejection; got nil")
	}
	if !errors.Is(err, archcheck.ErrArchCertSubjectMismatch) {
		t.Errorf("error %v does not wrap ErrArchCertSubjectMismatch", err)
	}
}

// TestVerifier_ArchCheck_NoEvidencePassesThrough_TestFixtureCN:
// the existing default CN "qsdm-test-nvidia-aik" (used by every
// test fixture in the package) MUST pass through with any
// claimed arch — this is what protects the existing test suite
// from breaking when the §4.6.5 check turned on.
func TestVerifier_ArchCheck_NoEvidencePassesThrough_TestFixtureCN(t *testing.T) {
	cases := []string{"hopper", "blackwell", "ada-lovelace", "ampere", "turing"}
	for _, arch := range cases {
		p, v, o := buildArchcheckProof(t, "qsdm-test-nvidia-aik", arch)
		if err := v.VerifyAttestation(p, o.Now); err != nil {
			t.Errorf("test-fixture CN with arch=%q should pass through; got %v",
				arch, err)
		}
	}
}

// TestVerifier_ArchCheck_NoEvidencePassesThrough_CorporateCN:
// a more realistic NVIDIA-themed CN that doesn't carry a
// product token (e.g. "NVIDIA Confidential Computing AIK") also
// passes through. The cert-chain pin remains the cryptographic
// lock.
func TestVerifier_ArchCheck_NoEvidencePassesThrough_CorporateCN(t *testing.T) {
	p, v, o := buildArchcheckProof(t,
		"NVIDIA Confidential Computing AIK", "hopper")
	if err := v.VerifyAttestation(p, o.Now); err != nil {
		t.Fatalf("corporate CN with no product token should pass; got %v", err)
	}
}

// TestVerifier_ArchCheck_GPUArchEmpty_SkipsStep9 covers the
// standalone-call path and pre-fork bring-up vectors: when
// Attestation.GPUArch is empty the verifier MUST skip step 9
// and accept (assuming everything else passes). This is the
// behaviour the existing TestVerifier_HappyPath relies on.
func TestVerifier_ArchCheck_GPUArchEmpty_SkipsStep9(t *testing.T) {
	// Even a contradiction-shaped leaf passes when the outer
	// proof doesn't claim an arch.
	p, v, o := buildArchcheckProof(t, "NVIDIA H100 80GB HBM3", "")
	if err := v.VerifyAttestation(p, o.Now); err != nil {
		t.Fatalf("empty gpu_arch should skip step 9 (standalone path); got %v", err)
	}
}

// TestVerifier_ArchCheck_LongestPatternWins is the CC-side
// counterpart to the archcheck unit test of the same name. A
// leaf CN "RTX 6000 Ada Generation" must match Ada (longest
// pattern) and reject Turing claims even though "rtx 6000" is
// also a Turing pattern.
func TestVerifier_ArchCheck_LongestPatternWins(t *testing.T) {
	subject := "NVIDIA RTX 6000 Ada Generation"

	// Ada claim accepts.
	p, v, o := buildArchcheckProof(t, subject, "ada-lovelace")
	if err := v.VerifyAttestation(p, o.Now); err != nil {
		t.Fatalf("ada-lovelace claim on Ada cert should pass; got %v", err)
	}

	// Turing claim rejects.
	p2, v2, o2 := buildArchcheckProof(t, subject, "turing")
	err := v2.VerifyAttestation(p2, o2.Now)
	if err == nil {
		t.Fatal("turing claim on Ada cert should reject; got nil")
	}
	if !errors.Is(err, archcheck.ErrArchCertSubjectMismatch) {
		t.Errorf("error %v does not wrap ErrArchCertSubjectMismatch", err)
	}
}
