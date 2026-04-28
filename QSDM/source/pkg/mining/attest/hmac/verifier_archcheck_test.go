package hmac

// verifier_archcheck_test.go: step-8 (arch-spoof rejection)
// integration tests that exercise the new
// pkg/mining/attest/archcheck wiring inside the HMAC verifier.
//
// What's tested here that archcheck/archcheck_test.go cannot:
//
//   - The wiring path: Bundle.HMAC must STILL pass before step 8
//     fires, because step 4 (HMAC check) is upstream. So a spoof
//     with a re-signed bundle is the realistic attacker — they
//     already paid the cost of forging a valid HMAC and are now
//     hoping the arch-spoof check doesn't catch them.
//
//   - The error wrapping: every step-8 rejection is expected to
//     wrap mining.ErrAttestationSignatureInvalid so the validator's
//     reason-grouping metric (REASON_ATTESTATION) tags it
//     consistently with the rest of the HMAC failures.
//
// The tests deliberately reuse the fixture from verifier_test.go
// (buildFixture / reSign / mustReject) so the regression bar
// stays consistent with the rest of the §3.3 step coverage.

import (
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mining"
	"github.com/blackbeardONE/QSDM/pkg/mining/attest/archcheck"
)

// TestVerify_Rejects_LazySpoof_OuterArch is THE archetypal
// step-8 attack: an Ada Lovelace card (RTX 4090, the fixture's
// honest gpu_name) trying to claim gpu_arch=hopper to look like
// a datacenter card. The bundle is fully consistent and the HMAC
// is valid — only the outer Attestation.GPUArch is the lie.
//
// This is a "spoofer who didn't bother to also flip gpu_name in
// the HMAC bundle" scenario — the simplest, cheapest spoof an
// attacker could attempt. Step 8 catches it.
func TestVerify_Rejects_LazySpoof_OuterArch(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	reg, p, _ := buildFixture(t, now)
	// Outer arch is NOT covered by the HMAC, so we don't reSign.
	// The attacker can flip this field at will.
	p.Attestation.GPUArch = "hopper"
	v := NewVerifier(reg)

	err := v.VerifyAttestation(p, now)
	mustReject(t, err, archcheck.ErrArchGPUNameMismatch)
	mustReject(t, err, mining.ErrAttestationSignatureInvalid)
}

// TestVerify_Rejects_LazySpoof_AcrossArchFamilies covers the
// inverse spoof — a downgrade (claiming consumer arch when
// running datacenter hardware, or vice-versa). All cases use
// the standard fixture's gpu_name = "NVIDIA GeForce RTX 4090"
// and only flip Attestation.GPUArch.
func TestVerify_Rejects_LazySpoof_AcrossArchFamilies(t *testing.T) {
	cases := []struct {
		name string
		arch string
	}{
		{"hopper-spoof", "hopper"},
		{"blackwell-spoof", "blackwell"},
		{"ampere-spoof", "ampere"},
		{"turing-spoof", "turing"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			now := time.Unix(1_700_000_000, 0)
			reg, p, _ := buildFixture(t, now)
			p.Attestation.GPUArch = c.arch
			v := NewVerifier(reg)
			mustReject(t, v.VerifyAttestation(p, now), archcheck.ErrArchGPUNameMismatch)
		})
	}
}

// TestVerify_Rejects_DeterminedSpoof_BundleGPUName is the
// determined attacker: they own a working HMAC key (e.g.
// they're the legitimate operator colluding with a non-NVIDIA
// or wrong-arch card) and have re-signed the bundle with a
// fake gpu_name. The outer Attestation.GPUArch and the
// re-signed inner gpu_name agree — but the inner gpu_name is
// nonsense ("FakeGPU 9000").
//
// Today this rejection happens on the deny-list (which the
// fixture leaves empty) only if the operator's adversary chose
// a denied substring; otherwise step 8 must catch it via the
// arch <-> gpu_name pattern table. This test confirms step 8
// is the actual catcher when the deny-list is empty.
func TestVerify_Rejects_DeterminedSpoof_BundleGPUName(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	reg, p, b := buildFixture(t, now)
	// Both the outer arch and inner gpu_name are flipped, and we
	// re-sign so step 4 stays green. No pattern in archcheck
	// matches "FakeGPU 9000".
	p.Attestation.GPUArch = "hopper"
	b.GPUName = "FakeGPU 9000"
	reSign(t, &p, b, fixtureHMACKey)
	v := NewVerifier(reg)
	mustReject(t, v.VerifyAttestation(p, now), archcheck.ErrArchGPUNameMismatch)
}

// TestVerify_Accepts_AdaAlias confirms the alias path: the
// fixture ships GPUArch="ada" (the qsdmminer-console-emitted
// short form). The HMAC verifier MUST canonicalise to
// ArchAdaLovelace and pass the bundle's "RTX 4090" gpu_name
// through.
func TestVerify_Accepts_AdaAlias(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	reg, p, _ := buildFixture(t, now)
	if p.Attestation.GPUArch != "ada" {
		t.Fatalf("fixture should ship the ada alias; got %q",
			p.Attestation.GPUArch)
	}
	v := NewVerifier(reg)
	if err := v.VerifyAttestation(p, now); err != nil {
		t.Errorf("ada alias should be accepted; got %v", err)
	}
}

// TestVerify_Rejects_UnknownArch covers the closed-enum
// allowlist edge: an attacker trying to sneak an unknown arch
// string ("voltA", "pascal", a future arch the validator hasn't
// been upgraded to support) past the verifier. These all
// reject in archcheck.Canonicalise; the HMAC verifier wraps
// the failure with mining.ErrAttestationSignatureInvalid so
// the metrics path tags it correctly.
func TestVerify_Rejects_UnknownArch(t *testing.T) {
	cases := []string{
		"volta",
		"pascal",
		"future-arch-2099",
		"",
	}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			now := time.Unix(1_700_000_000, 0)
			reg, p, _ := buildFixture(t, now)
			p.Attestation.GPUArch = c
			v := NewVerifier(reg)
			err := v.VerifyAttestation(p, now)
			mustReject(t, err, mining.ErrAttestationSignatureInvalid)
		})
	}
}
