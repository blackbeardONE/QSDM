package telemetry

import (
	"strings"
	"testing"
)

// nvidia-smi output captured from a real RTX 3050 (Ampere
// CC 8.6, Manila box, driver 576.28). One row, twelve
// fields, comma-separated. This fixture pins the parser
// against drift in real-world output formatting — every
// field exercised below comes from this single row.
const fixtureRTX3050CSV = "" +
	"GPU-39925fa6-82f0-0e13-dd28-aa4be2048287, NVIDIA GeForce RTX 3050, " +
	"8.6, 8188, 576.28, 4, 16, 130.00, 94.06.31.00.20, " +
	"Disabled, 1755, 7000\n"

// Same row but with several "[N/A]" placeholders — common
// on consumer cards for ECC, unsupported clock domains, etc.
const fixtureRTX3050WithNAs = "" +
	"GPU-39925fa6-82f0-0e13-dd28-aa4be2048287, NVIDIA GeForce RTX 3050, " +
	"8.6, 8188, 576.28, [N/A], [Not Supported], 130.00, 94.06.31.00.20, " +
	"[N/A], 1755, 7000\n"

// Two-GPU box (RTX 3050 + an A100 added for variety). Pins
// the parser against multi-row CSVs.
const fixtureMultiGPUCSV = "" +
	"GPU-39925fa6-82f0-0e13-dd28-aa4be2048287, NVIDIA GeForce RTX 3050, 8.6, 8188, 576.28, 4, 16, 130.00, 94.06.31.00.20, Disabled, 1755, 7000\n" +
	"GPU-aaaaaaaa-bbbb-cccc-dddd-000000000001, NVIDIA A100 80GB PCIe, 8.0, 81920, 535.104.05, 4, 16, 300.00, 92.00.61.00.0F, Enabled, 1410, 1593\n"

func TestParseNVIDIASMICSV_Fixture3050(t *testing.T) {
	out, err := parseNVIDIASMICSV([]byte(fixtureRTX3050CSV))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("rows = %d", len(out))
	}
	g := out[0]
	if g.UUID != "GPU-39925fa6-82f0-0e13-dd28-aa4be2048287" {
		t.Errorf("UUID = %q", g.UUID)
	}
	if g.Name != "NVIDIA GeForce RTX 3050" {
		t.Errorf("Name = %q", g.Name)
	}
	if g.Vendor != "NVIDIA" {
		t.Errorf("Vendor = %q", g.Vendor)
	}
	if g.ComputeCapability != "8.6" {
		t.Errorf("CC = %q", g.ComputeCapability)
	}
	if g.Architecture != "ampere" {
		t.Errorf("Architecture = %q (want ampere)", g.Architecture)
	}
	if g.MemoryTotalMB != 8188 {
		t.Errorf("Memory = %d", g.MemoryTotalMB)
	}
	if g.PCIeGen != 4 || g.PCIeWidth != 16 {
		t.Errorf("PCIe = %d/%d", g.PCIeGen, g.PCIeWidth)
	}
	if g.PowerMaxW != 130 {
		t.Errorf("Power = %f", g.PowerMaxW)
	}
	if !g.ECCSupported {
		t.Errorf("ECCSupported = false (Disabled means present-but-disabled, which is supported)")
	}
	if len(g.DriverVersionsSeen) != 1 || g.DriverVersionsSeen[0] != "576.28" {
		t.Errorf("DriverVersionsSeen = %v", g.DriverVersionsSeen)
	}
	if len(g.VBIOSVersionsSeen) != 1 || g.VBIOSVersionsSeen[0] != "94.06.31.00.20" {
		t.Errorf("VBIOSVersionsSeen = %v", g.VBIOSVersionsSeen)
	}
	if g.ClockGraphicsBoostMHz != 1755 || g.ClockMemoryMHz != 7000 {
		t.Errorf("Clocks = %d/%d", g.ClockGraphicsBoostMHz, g.ClockMemoryMHz)
	}
}

func TestParseNVIDIASMICSV_NAFieldsZero(t *testing.T) {
	out, err := parseNVIDIASMICSV([]byte(fixtureRTX3050WithNAs))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("rows = %d", len(out))
	}
	g := out[0]
	if g.PCIeGen != 0 || g.PCIeWidth != 0 {
		t.Errorf("[N/A] should map to zero, got PCIe=%d/%d", g.PCIeGen, g.PCIeWidth)
	}
	if g.ECCSupported {
		t.Errorf("[N/A] ECC should not register as supported")
	}
}

func TestParseNVIDIASMICSV_MultiGPU(t *testing.T) {
	out, err := parseNVIDIASMICSV([]byte(fixtureMultiGPUCSV))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("rows = %d", len(out))
	}
	if out[0].Architecture != "ampere" || out[1].Architecture != "ampere" {
		t.Errorf("archs = %q,%q", out[0].Architecture, out[1].Architecture)
	}
	if out[1].MemoryTotalMB != 81920 {
		t.Errorf("A100 memory = %d, want 81920", out[1].MemoryTotalMB)
	}
	if !out[1].ECCSupported {
		t.Errorf("A100 ECC = false, want true")
	}
}

func TestParseNVIDIASMICSV_EmptyOutput(t *testing.T) {
	if _, err := parseNVIDIASMICSV([]byte("")); err == nil {
		t.Fatalf("expected error on empty output")
	}
	if _, err := parseNVIDIASMICSV([]byte("   \n")); err == nil {
		t.Fatalf("expected error on whitespace-only output")
	}
}

func TestParseNVIDIASMICSV_TooFewColumns(t *testing.T) {
	bad := "GPU-x, NVIDIA Foo, 8.6\n"
	if _, err := parseNVIDIASMICSV([]byte(bad)); err == nil {
		t.Fatalf("expected error on short row")
	} else if !strings.Contains(err.Error(), "columns") {
		t.Errorf("error wording: %v", err)
	}
}

func TestInferArchitecture(t *testing.T) {
	cases := map[string]string{
		"5.2":  "maxwell",
		"6.1":  "pascal",
		"7.0":  "volta",
		"7.5":  "turing",
		"8.0":  "ampere",
		"8.6":  "ampere",
		"8.9":  "ada-lovelace",
		"9.0":  "hopper",
		"10.0": "blackwell",
		"12.0": "blackwell",
		"":     "",
		"x.y":  "",
		"99.0": "", // unknown future generation; conservative empty
	}
	for in, want := range cases {
		if got := inferArchitecture(in); got != want {
			t.Errorf("inferArchitecture(%q) = %q want %q", in, got, want)
		}
	}
}

func TestCleanField(t *testing.T) {
	cases := map[string]string{
		"  576.28  ":      "576.28",
		"[N/A]":           "",
		"[Not Available]": "",
		"[Not Supported]": "",
		"N/A":             "",
		"":                "",
		"576.28":          "576.28",
	}
	for in, want := range cases {
		if got := cleanField(in); got != want {
			t.Errorf("cleanField(%q) = %q want %q", in, got, want)
		}
	}
}

func TestParseHelpers_AcceptDecimalsRejectGarbage(t *testing.T) {
	if v := parseUint64("1234"); v != 1234 {
		t.Errorf("parseUint64: %d", v)
	}
	if v := parseUint64("not-a-number"); v != 0 {
		t.Errorf("parseUint64 garbage: %d", v)
	}
	if v := parseFloat("130.5"); v != 130.5 {
		t.Errorf("parseFloat: %f", v)
	}
	if v := parseFloat("[N/A]"); v != 0 {
		t.Errorf("parseFloat NA: %f", v)
	}
	if v := parseUint8("16"); v != 16 {
		t.Errorf("parseUint8: %d", v)
	}
	if v := parseUint32("1755"); v != 1755 {
		t.Errorf("parseUint32: %d", v)
	}
	// Overflow → zero.
	if v := parseUint8("999"); v != 0 {
		t.Errorf("parseUint8 overflow: %d", v)
	}
}

func TestNVIDIASMICollector_Kind(t *testing.T) {
	c := &NVIDIASMICollector{}
	if c.Kind() != "nvidia-smi" {
		t.Fatalf("Kind = %q", c.Kind())
	}
}
