package archcheck

// archcheck_test.go: unit tests for the closed-enum allowlist,
// the alias map, and the arch <-> gpu_name consistency check.
//
// These tests are the regression bar for the §4.6 / §3.3 step-8
// arch-spoof rejection logic. A new Architecture entering the
// canonical set (or a new alias) MUST land alongside an entry
// here so the tightening is intentional.

import (
	"errors"
	"testing"
)

// -----------------------------------------------------------------------------
// Canonicalise / KnownArchitectures
// -----------------------------------------------------------------------------

func TestKnownArchitectures_StableOrder(t *testing.T) {
	got := KnownArchitectures()
	want := []Architecture{
		ArchHopper, ArchBlackwell, ArchAdaLovelace,
		ArchAmpere, ArchTuring,
	}
	if len(got) != len(want) {
		t.Fatalf("KnownArchitectures len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("KnownArchitectures[%d] = %q, want %q",
				i, got[i], want[i])
		}
	}
}

func TestCanonicalise_AcceptsCanonical(t *testing.T) {
	cases := []struct {
		in   string
		want Architecture
	}{
		{"hopper", ArchHopper},
		{"blackwell", ArchBlackwell},
		{"ada-lovelace", ArchAdaLovelace},
		{"ampere", ArchAmpere},
		{"turing", ArchTuring},
	}
	for _, c := range cases {
		got, ok := Canonicalise(c.in)
		if !ok {
			t.Errorf("Canonicalise(%q): ok=false; want true", c.in)
			continue
		}
		if got != c.want {
			t.Errorf("Canonicalise(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCanonicalise_AcceptsAliases(t *testing.T) {
	got, ok := Canonicalise("ada")
	if !ok || got != ArchAdaLovelace {
		t.Errorf(`Canonicalise("ada") = (%q, %v); want (%q, true)`,
			got, ok, ArchAdaLovelace)
	}
}

func TestCanonicalise_CaseInsensitive(t *testing.T) {
	cases := []string{"HOPPER", "Ada", "ada-LOVELACE", "  Ampere  ", "ADA"}
	for _, in := range cases {
		if _, ok := Canonicalise(in); !ok {
			t.Errorf("Canonicalise(%q): ok=false; want true (case-insensitive)", in)
		}
	}
}

func TestCanonicalise_RejectsUnknown(t *testing.T) {
	cases := []string{
		"",
		"  ",
		"voltA",   // Volta — intentionally OFF the allowlist
		"pascal",  // also OFF
		"maxwell", // also OFF
		"kepler",  // also OFF
		"future-arch-9000",
		"ada-lovely", // typo
		"hopper2",    // sneak attempt
	}
	for _, in := range cases {
		if _, ok := Canonicalise(in); ok {
			t.Errorf("Canonicalise(%q): ok=true; want false (unknown)", in)
		}
	}
}

// -----------------------------------------------------------------------------
// ValidateOuterArch
// -----------------------------------------------------------------------------

func TestValidateOuterArch_AcceptsKnown(t *testing.T) {
	for _, a := range KnownArchitectures() {
		got, err := ValidateOuterArch(string(a))
		if err != nil {
			t.Errorf("ValidateOuterArch(%q): %v", a, err)
		}
		if got != a {
			t.Errorf("ValidateOuterArch(%q) returned %q; want %q",
				a, got, a)
		}
	}
}

func TestValidateOuterArch_AcceptsAlias(t *testing.T) {
	got, err := ValidateOuterArch("ada")
	if err != nil {
		t.Fatalf(`ValidateOuterArch("ada"): %v`, err)
	}
	if got != ArchAdaLovelace {
		t.Errorf(`ValidateOuterArch("ada") = %q; want %q (canonical form)`,
			got, ArchAdaLovelace)
	}
}

func TestValidateOuterArch_RejectsUnknownWithSentinel(t *testing.T) {
	_, err := ValidateOuterArch("RTX-superduper-2099")
	if err == nil {
		t.Fatal("expected error for unknown gpu_arch")
	}
	if !errors.Is(err, ErrArchUnknown) {
		t.Errorf("error %v does not wrap ErrArchUnknown", err)
	}
}

func TestValidateOuterArch_RejectsEmpty(t *testing.T) {
	if _, err := ValidateOuterArch(""); err == nil {
		t.Error("expected error for empty gpu_arch")
	}
}

// -----------------------------------------------------------------------------
// ValidateBundleArchConsistencyHMAC
// -----------------------------------------------------------------------------

func TestValidateBundleArchConsistencyHMAC_HappyPath(t *testing.T) {
	// (arch, gpu_name) pairs an honest miner would emit. Each
	// MUST pass.
	cases := []struct {
		arch Architecture
		name string
	}{
		{ArchHopper, "NVIDIA H100 80GB HBM3"},
		{ArchHopper, "Tesla H200"},
		{ArchHopper, "NVIDIA H800"},
		{ArchBlackwell, "NVIDIA B200 192GB"},
		{ArchBlackwell, "NVIDIA GB200 NVL72"},
		{ArchBlackwell, "NVIDIA GeForce RTX 5090"},
		{ArchAdaLovelace, "NVIDIA GeForce RTX 4090"},
		{ArchAdaLovelace, "NVIDIA GeForce RTX 4070 Ti"},
		{ArchAdaLovelace, "NVIDIA L40S"},
		{ArchAdaLovelace, "NVIDIA RTX 6000 Ada Generation"},
		{ArchAmpere, "NVIDIA A100-SXM4-80GB"},
		{ArchAmpere, "NVIDIA GeForce RTX 3090 Ti"},
		{ArchAmpere, "NVIDIA RTX A6000"},
		{ArchTuring, "NVIDIA GeForce RTX 2080 Ti"},
		{ArchTuring, "NVIDIA GeForce GTX 1660 SUPER"},
		{ArchTuring, "Tesla T4"},
	}
	for _, c := range cases {
		if err := ValidateBundleArchConsistencyHMAC(c.arch, c.name); err != nil {
			t.Errorf("(%q, %q) should be consistent; got %v",
				c.arch, c.name, err)
		}
	}
}

// TestValidateBundleArchConsistencyHMAC_RejectsLazySpoof is THE
// load-bearing test for this whole feature: an attacker on an
// RTX 4090 (Ada Lovelace) who claims gpu_arch=hopper but
// forgot to flip gpu_name. Bundle gpu_name is HMAC-bound, so
// they cannot post-hoc swap it; they're trapped at this check.
func TestValidateBundleArchConsistencyHMAC_RejectsLazySpoof(t *testing.T) {
	cases := []struct {
		arch Architecture
		name string
		desc string
	}{
		{ArchHopper, "NVIDIA GeForce RTX 4090",
			"RTX 4090 lying about being Hopper"},
		{ArchHopper, "NVIDIA GeForce RTX 5090",
			"RTX 5090 (Blackwell consumer) lying about being Hopper"},
		{ArchBlackwell, "NVIDIA H100 80GB HBM3",
			"H100 (Hopper) lying about being Blackwell"},
		{ArchAdaLovelace, "NVIDIA GeForce RTX 3090",
			"RTX 3090 (Ampere) lying about being Ada"},
		{ArchAmpere, "NVIDIA GeForce RTX 4090",
			"RTX 4090 (Ada) lying about being Ampere"},
		{ArchTuring, "NVIDIA H100",
			"H100 lying about being Turing (downgrade spoof)"},
		{ArchHopper, "AMD Radeon Instinct MI300X",
			"AMD card pretending to be NVIDIA Hopper"},
	}
	for _, c := range cases {
		err := ValidateBundleArchConsistencyHMAC(c.arch, c.name)
		if err == nil {
			t.Errorf("(%q, %q) should reject [%s]; got nil",
				c.arch, c.name, c.desc)
			continue
		}
		if !errors.Is(err, ErrArchGPUNameMismatch) {
			t.Errorf("(%q, %q) error %v does not wrap ErrArchGPUNameMismatch [%s]",
				c.arch, c.name, err, c.desc)
		}
	}
}

func TestValidateBundleArchConsistencyHMAC_RejectsEmpty(t *testing.T) {
	err := ValidateBundleArchConsistencyHMAC(ArchHopper, "")
	if err == nil {
		t.Fatal("expected error for empty gpu_name")
	}
	if !errors.Is(err, ErrArchGPUNameMismatch) {
		t.Errorf("error %v does not wrap ErrArchGPUNameMismatch", err)
	}
}

func TestValidateBundleArchConsistencyHMAC_CaseInsensitive(t *testing.T) {
	if err := ValidateBundleArchConsistencyHMAC(
		ArchHopper, "nvidia h100",
	); err != nil {
		t.Errorf(`lowercased "nvidia h100" should match Hopper; got %v`, err)
	}
	if err := ValidateBundleArchConsistencyHMAC(
		ArchAdaLovelace, "  NVIDIA  GeForce  RTX  4090  ",
	); err != nil {
		t.Errorf("padded gpu_name should match Ada-Lovelace; got %v", err)
	}
}

func TestValidateBundleArchConsistencyHMAC_RejectsUnknownArch(t *testing.T) {
	err := ValidateBundleArchConsistencyHMAC(
		Architecture("not-an-arch"), "NVIDIA H100",
	)
	if err == nil {
		t.Fatal("expected error for non-canonical arch")
	}
	if !errors.Is(err, ErrArchUnknown) {
		t.Errorf("error %v does not wrap ErrArchUnknown", err)
	}
}

// -----------------------------------------------------------------------------
// ValidateClaimedHashrate
// -----------------------------------------------------------------------------

// TestValidateClaimedHashrate_ZeroIsNotAsserted locks the
// "not asserted" sentinel: a literal 0 passes through for every
// canonical arch. This is critical for backward compatibility
// with the existing test fixtures and miners that don't
// populate the field.
func TestValidateClaimedHashrate_ZeroIsNotAsserted(t *testing.T) {
	for _, a := range KnownArchitectures() {
		if err := ValidateClaimedHashrate(a, 0); err != nil {
			t.Errorf("(%q, 0) should be a no-op (sentinel); got %v", a, err)
		}
	}
}

// TestValidateClaimedHashrate_HappyPath confirms each per-arch
// band accepts plausible mid-range values. Numbers are pinned
// to the §4.4 "Miner cost" estimates so a future loosening of
// the bands lands as a deliberate test diff, not silent drift.
func TestValidateClaimedHashrate_HappyPath(t *testing.T) {
	cases := []struct {
		arch    Architecture
		claimed uint64
		desc    string
	}{
		{ArchTuring, 500_000, "T4 ~0.5 MH/s"},
		{ArchAmpere, 1_500_000, "RTX 3090 ~1.5 MH/s"},
		{ArchAmpere, 5_000_000, "A100 ~5 MH/s"},
		{ArchAdaLovelace, 5_000_000, "RTX 4090 ~5 MH/s"},
		{ArchAdaLovelace, 7_000_000, "L40S ~7 MH/s"},
		{ArchHopper, 30_000_000, "H100 ~30 MH/s"},
		{ArchBlackwell, 60_000_000, "B200 ~60 MH/s"},
	}
	for _, c := range cases {
		if err := ValidateClaimedHashrate(c.arch, c.claimed); err != nil {
			t.Errorf("(%q, %d) [%s] should be in band; got %v",
				c.arch, c.claimed, c.desc, err)
		}
	}
}

// TestValidateClaimedHashrate_BoundsInclusive verifies both
// endpoints of every band accept exactly. A naive `< Min` or
// `> Max` regression would break this; the inclusive-on-both-
// ends rule is consensus-relevant.
func TestValidateClaimedHashrate_BoundsInclusive(t *testing.T) {
	for _, a := range KnownArchitectures() {
		band, ok := HashrateBandFor(a)
		if !ok {
			t.Fatalf("HashrateBandFor(%q) returned false", a)
		}
		if err := ValidateClaimedHashrate(a, band.Min); err != nil {
			t.Errorf("(%q, Min=%d) inclusive lower bound rejected: %v",
				a, band.Min, err)
		}
		if err := ValidateClaimedHashrate(a, band.Max); err != nil {
			t.Errorf("(%q, Max=%d) inclusive upper bound rejected: %v",
				a, band.Max, err)
		}
	}
}

// TestValidateClaimedHashrate_RejectsLazyHashrateSpoof is THE
// load-bearing test for this feature: an obvious downgrade or
// upgrade lie. Each case is a real-world spoof shape an
// attacker would attempt — claiming H100 throughput on a
// consumer card, claiming MH-scale hashrate on a CPU, etc.
func TestValidateClaimedHashrate_RejectsLazyHashrateSpoof(t *testing.T) {
	cases := []struct {
		arch    Architecture
		claimed uint64
		desc    string
	}{
		{ArchTuring, 100_000_000, "T4 claiming 100 MH/s (200x peak)"},
		{ArchAdaLovelace, 200_000_000, "RTX 4090 claiming 200 MH/s (40x peak)"},
		{ArchAmpere, 500_000_000, "A100 claiming 500 MH/s (100x peak)"},
		{ArchHopper, 100, "H100 claiming 100 H/s (CPU territory)"},
		{ArchBlackwell, 1, "GB200 claiming 1 H/s (typo)"},
		{ArchHopper, 18_000_000_000_000_000,
			"obvious typo: 18 PB/s units confusion"},
	}
	for _, c := range cases {
		err := ValidateClaimedHashrate(c.arch, c.claimed)
		if err == nil {
			t.Errorf("(%q, %d) [%s] should reject; got nil",
				c.arch, c.claimed, c.desc)
			continue
		}
		if !errors.Is(err, ErrHashrateOutOfBand) {
			t.Errorf("(%q, %d) [%s] error %v does not wrap ErrHashrateOutOfBand",
				c.arch, c.claimed, c.desc, err)
		}
	}
}

// TestValidateClaimedHashrate_RejectsUnknownArch covers the
// programmer-error path: caller passes an Architecture that's
// not in the canonical set. Returns ErrArchUnknown rather than
// panicking on the consensus path.
func TestValidateClaimedHashrate_RejectsUnknownArch(t *testing.T) {
	err := ValidateClaimedHashrate(Architecture("not-an-arch"), 1_000_000)
	if err == nil {
		t.Fatal("expected error for non-canonical arch")
	}
	if !errors.Is(err, ErrArchUnknown) {
		t.Errorf("error %v does not wrap ErrArchUnknown", err)
	}
}

// TestHashrateBandFor_KnownAndUnknown spot-checks the lookup
// API: every canonical arch returns a band; an arbitrary
// non-canonical name returns ok=false.
func TestHashrateBandFor_KnownAndUnknown(t *testing.T) {
	for _, a := range KnownArchitectures() {
		if _, ok := HashrateBandFor(a); !ok {
			t.Errorf("HashrateBandFor(%q): ok=false; want true", a)
		}
	}
	if _, ok := HashrateBandFor(Architecture("voltA")); ok {
		t.Error(`HashrateBandFor("voltA"): ok=true; want false`)
	}
}

// -----------------------------------------------------------------------------
// ValidateBundleArchConsistencyCC
// -----------------------------------------------------------------------------

// TestValidateBundleArchConsistencyCC_HappyPath: cert subjects
// emitted by an NVIDIA-issued AIK leaf for each canonical arch.
// Each MUST pass — the subject contains positive product
// evidence that aligns with the claimed arch.
func TestValidateBundleArchConsistencyCC_HappyPath(t *testing.T) {
	cases := []struct {
		arch    Architecture
		subject string
	}{
		{ArchHopper, "NVIDIA H100 80GB HBM3"},
		{ArchHopper, "Tesla H200 SXM"},
		{ArchHopper, "CN=NVIDIA H800,O=NVIDIA Corporation"},
		{ArchBlackwell, "NVIDIA B200 192GB HBM3e"},
		{ArchBlackwell, "CN=NVIDIA GB200 NVL72,O=NVIDIA"},
		{ArchAdaLovelace, "NVIDIA L40S"},
		{ArchAdaLovelace, "NVIDIA RTX 6000 Ada Generation"},
		{ArchAmpere, "NVIDIA A100-SXM4-80GB"},
		{ArchTuring, "NVIDIA Tesla T4"},
	}
	for _, c := range cases {
		if err := ValidateBundleArchConsistencyCC(c.arch, c.subject); err != nil {
			t.Errorf("(%q, %q) should pass; got %v",
				c.arch, c.subject, err)
		}
	}
}

// TestValidateBundleArchConsistencyCC_NoEvidencePassesThrough:
// a leaf cert whose subject contains NO known NVIDIA product
// substring (test fixtures, generic AIK CN's, OID-based model
// encodings) must pass through. This is the explicit
// "evidence-based, not strict" semantic — see the function doc.
func TestValidateBundleArchConsistencyCC_NoEvidencePassesThrough(t *testing.T) {
	cases := []struct {
		arch    Architecture
		subject string
		desc    string
	}{
		{ArchHopper, "qsdm-test-nvidia-aik",
			"existing test fixture default CN"},
		{ArchHopper, "CN=NVIDIA Confidential Computing AIK",
			"corporate CN with no model token"},
		{ArchAdaLovelace, "",
			"empty subject (no CN at all)"},
		{ArchAmpere, "   ",
			"whitespace-only"},
		{ArchHopper, "CN=Quadro Reserve",
			"NVIDIA-themed but no model token"},
		{ArchBlackwell, "CN=qsdm-test-nvidia-root",
			"root-style label without product"},
	}
	for _, c := range cases {
		if err := ValidateBundleArchConsistencyCC(c.arch, c.subject); err != nil {
			t.Errorf("(%q, %q) [%s] should pass through; got %v",
				c.arch, c.subject, c.desc, err)
		}
	}
}

// TestValidateBundleArchConsistencyCC_RejectsContradiction is
// THE load-bearing test: the cert subject contains positive
// product evidence that contradicts the claimed Architecture.
// This catches the "fabricated AIK" attacker (assuming they
// somehow got past the cert-chain pin) AND honest miners who
// misconfigured `gpu_arch` after a hardware swap.
func TestValidateBundleArchConsistencyCC_RejectsContradiction(t *testing.T) {
	cases := []struct {
		arch    Architecture
		subject string
		desc    string
	}{
		{ArchHopper, "NVIDIA GeForce RTX 4090",
			"4090 cert claiming Hopper"},
		{ArchAdaLovelace, "NVIDIA H100 80GB",
			"H100 cert claiming Ada"},
		{ArchTuring, "NVIDIA B200",
			"B200 cert claiming Turing"},
		{ArchAmpere, "NVIDIA L40S",
			"L40S (Ada) cert claiming Ampere"},
		{ArchBlackwell, "NVIDIA Tesla T4",
			"T4 (Turing) cert claiming Blackwell"},
	}
	for _, c := range cases {
		err := ValidateBundleArchConsistencyCC(c.arch, c.subject)
		if err == nil {
			t.Errorf("(%q, %q) [%s] should reject; got nil",
				c.arch, c.subject, c.desc)
			continue
		}
		if !errors.Is(err, ErrArchCertSubjectMismatch) {
			t.Errorf("(%q, %q) [%s] error %v does not wrap ErrArchCertSubjectMismatch",
				c.arch, c.subject, c.desc, err)
		}
	}
}

// TestValidateBundleArchConsistencyCC_LongestPatternWins locks
// the overlap-resolution rule: the substring "rtx 6000" (Quadro
// RTX 6000, Turing) is contained in "rtx 6000 ada" (RTX 6000
// Ada Generation, Ada). A claimed_arch=turing on an
// "RTX 6000 Ada" cert MUST reject because the longer-pattern
// ("rtx 6000 ada") attribution wins. Conversely the same cert
// with claimed_arch=ada-lovelace MUST pass.
func TestValidateBundleArchConsistencyCC_LongestPatternWins(t *testing.T) {
	subject := "NVIDIA RTX 6000 Ada Generation"

	if err := ValidateBundleArchConsistencyCC(ArchAdaLovelace, subject); err != nil {
		t.Errorf("Ada cert with Ada arch should pass; got %v", err)
	}
	err := ValidateBundleArchConsistencyCC(ArchTuring, subject)
	if err == nil {
		t.Fatal("Ada cert with Turing arch should reject; got nil")
	}
	if !errors.Is(err, ErrArchCertSubjectMismatch) {
		t.Errorf("error %v does not wrap ErrArchCertSubjectMismatch", err)
	}

	// And the genuinely Turing-era card SHOULD match Turing.
	if err := ValidateBundleArchConsistencyCC(
		ArchTuring, "NVIDIA Quadro RTX 6000",
	); err != nil {
		t.Errorf("Quadro RTX 6000 (Turing) with Turing arch should pass; got %v", err)
	}
}

// TestValidateBundleArchConsistencyCC_CaseInsensitive is the
// counterpart to the HMAC case-insensitivity test: real cert
// subjects come back as `pkix.Name.String()` which preserves
// whatever case the issuer used. We canonicalise on read.
func TestValidateBundleArchConsistencyCC_CaseInsensitive(t *testing.T) {
	if err := ValidateBundleArchConsistencyCC(
		ArchHopper, "nvidia h100",
	); err != nil {
		t.Errorf(`lowercased "nvidia h100" should match Hopper; got %v`, err)
	}
	if err := ValidateBundleArchConsistencyCC(
		ArchAdaLovelace, "  NVIDIA  GeForce  RTX  4090  ",
	); err != nil {
		t.Errorf("padded subject should match Ada-Lovelace; got %v", err)
	}
}

// TestValidateBundleArchConsistencyCC_RejectsUnknownArch covers
// the programmer-error path. Returns ErrArchUnknown not
// ErrArchCertSubjectMismatch so the call site can distinguish
// "you misconfigured the verifier" from "the bundle lied".
func TestValidateBundleArchConsistencyCC_RejectsUnknownArch(t *testing.T) {
	err := ValidateBundleArchConsistencyCC(
		Architecture("not-an-arch"), "NVIDIA H100",
	)
	if err == nil {
		t.Fatal("expected error for non-canonical arch")
	}
	if !errors.Is(err, ErrArchUnknown) {
		t.Errorf("error %v does not wrap ErrArchUnknown", err)
	}
}
