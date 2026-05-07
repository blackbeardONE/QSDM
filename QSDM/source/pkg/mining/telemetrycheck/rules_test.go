package telemetrycheck

import (
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/telemetry"
)

func TestRuleArchVsComputeCap(t *testing.T) {
	cases := []struct {
		name       string
		claim      Claim
		wantField  string
		wantSeverity string
	}{
		{
			name: "ampere + 8.6 = match",
			claim: Claim{GPUArch: "ampere", ComputeCap: "8.6"},
		},
		{
			name: "hopper + 9.0 = match",
			claim: Claim{GPUArch: "hopper", ComputeCap: "9.0"},
		},
		{
			name: "ada-lovelace canonicalises across delimiters",
			claim: Claim{GPUArch: "Ada Lovelace", ComputeCap: "8.9"},
		},
		{
			name:         "ampere + 9.0 = mismatch (claim is hopper-cc)",
			claim:        Claim{GPUArch: "ampere", ComputeCap: "9.0"},
			wantField:    "arch",
			wantSeverity: "major",
		},
		{
			name:         "hopper + 8.6 = mismatch (claim is ampere-cc)",
			claim:        Claim{GPUArch: "hopper", ComputeCap: "8.6"},
			wantField:    "arch",
			wantSeverity: "major",
		},
		{
			name:  "empty arch = no fire",
			claim: Claim{ComputeCap: "8.6"},
		},
		{
			name:  "empty cc = no fire",
			claim: Claim{GPUArch: "ampere"},
		},
		{
			name:  "unknown future cc = no fire (don't punish a new generation)",
			claim: Claim{GPUArch: "blackwell", ComputeCap: "99.0"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ruleArchVsComputeCap(tc.claim)
			if tc.wantField == "" {
				if got != nil {
					t.Fatalf("want nil; got %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("want %s; got nil", tc.wantField)
			}
			if got.Field != tc.wantField {
				t.Errorf("Field = %q want %q", got.Field, tc.wantField)
			}
			if got.Severity != tc.wantSeverity {
				t.Errorf("Severity = %q want %q", got.Severity, tc.wantSeverity)
			}
		})
	}
}

func TestRuleComputeCapAgainstSKU(t *testing.T) {
	cands3050 := []telemetry.GPUObservation{
		{Name: "NVIDIA GeForce RTX 3050", ComputeCapability: "8.6"},
	}
	cases := []struct {
		name       string
		claim      Claim
		candidates []telemetry.GPUObservation
		wantField  string
	}{
		{
			name:       "match",
			claim:      Claim{GPUName: "NVIDIA GeForce RTX 3050", ComputeCap: "8.6"},
			candidates: cands3050,
		},
		{
			name:       "wrong cc on known SKU",
			claim:      Claim{GPUName: "NVIDIA GeForce RTX 3050", ComputeCap: "9.0"},
			candidates: cands3050,
			wantField:  "compute_cap",
		},
		{
			name:       "empty cc = no fire",
			claim:      Claim{GPUName: "NVIDIA GeForce RTX 3050", ComputeCap: ""},
			candidates: cands3050,
		},
		{
			name:  "candidates without CC = no fire",
			claim: Claim{GPUName: "NVIDIA GeForce RTX 3050", ComputeCap: "9.0"},
			candidates: []telemetry.GPUObservation{
				{Name: "NVIDIA GeForce RTX 3050", ComputeCapability: ""},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ruleComputeCapAgainstSKU(tc.claim, tc.candidates)
			if tc.wantField == "" {
				if got != nil {
					t.Fatalf("want nil; got %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("want fire; got nil")
			}
			if got.Field != tc.wantField {
				t.Errorf("Field = %q want %q", got.Field, tc.wantField)
			}
		})
	}
}

func TestRuleDriverVerFormat(t *testing.T) {
	cases := []struct {
		in   string
		fire bool
	}{
		{"576.28", false},
		{"535.104.05", false},
		{"535", false},
		{"", false},
		{"576.28-beta", true},
		{"foo", true},
		{".576", true},
		{"576.", true},
		{"5.7.6.2.8", true}, // too many dots
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := ruleDriverVerFormat(Claim{DriverVer: tc.in})
			if tc.fire {
				if got == nil {
					t.Fatalf("expected fire for %q", tc.in)
				}
				if got.Severity != "minor" {
					t.Errorf("severity = %q want minor", got.Severity)
				}
			} else if got != nil {
				t.Fatalf("unexpected fire for %q: %+v", tc.in, got)
			}
		})
	}
}

func TestEqualArchToken(t *testing.T) {
	cases := []struct {
		a, b string
		eq   bool
	}{
		{"ampere", "ampere", true},
		{"ampere", "AMPERE", true},
		{"ada-lovelace", "Ada Lovelace", true},
		{"ada_lovelace", "ada-lovelace", true},
		{"ampere", "hopper", false},
		{"", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.a+"_vs_"+tc.b, func(t *testing.T) {
			if got := equalArchToken(tc.a, tc.b); got != tc.eq {
				t.Errorf("got %v want %v", got, tc.eq)
			}
		})
	}
}

func TestInferArchitectureLocal_MirrorsTelemetryPackage(t *testing.T) {
	// Drift detector: any difference between the rules.go
	// table and the telemetry package's table is a bug.
	// We check the SKUs the baseline catalog cares about.
	cases := map[string]string{
		"5.2": "maxwell",
		"6.1": "pascal",
		"7.0": "volta",
		"7.5": "turing",
		"8.0": "ampere",
		"8.6": "ampere",
		"8.9": "ada-lovelace",
		"9.0": "hopper",
		"":    "",
		"x.y": "",
	}
	for in, want := range cases {
		if got := inferArchitectureLocal(in); got != want {
			t.Errorf("inferArchitectureLocal(%q) = %q want %q", in, got, want)
		}
	}
}
