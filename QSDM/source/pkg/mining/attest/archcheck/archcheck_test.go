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
// ValidateBundleArchConsistencyCC (placeholder)
// -----------------------------------------------------------------------------

func TestValidateBundleArchConsistencyCC_AlwaysNilForNow(t *testing.T) {
	// Reservation point — explicit acknowledgement that the CC
	// path is currently a no-op. When this stops being a no-op,
	// this test must be replaced with cert-subject coverage.
	if err := ValidateBundleArchConsistencyCC(
		ArchHopper, "CN=NVIDIA H100",
	); err != nil {
		t.Errorf("CC consistency check is currently a no-op; got %v", err)
	}
}
