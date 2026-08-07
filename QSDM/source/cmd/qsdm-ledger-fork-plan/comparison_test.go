package main

import (
	"path/filepath"
	"testing"
	"time"
)

func TestAnalyzeAttachesBaselineComparison(t *testing.T) {
	dir := t.TempDir()
	paths := writeFixture(t, dir, false, false)
	baseline, err := analyze(analyzeOptions{
		AccountsPath: paths.accounts, ChainPath: paths.chain,
		EnrollmentPath: paths.enrollment, StakingPath: paths.staking,
		Now: time.Unix(1_700_000_000, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	baselinePath := filepath.Join(dir, "baseline.json")
	writeJSON(t, baselinePath, baseline)

	current, err := analyze(analyzeOptions{
		AccountsPath: paths.accounts, ChainPath: paths.chain,
		EnrollmentPath: paths.enrollment, StakingPath: paths.staking,
		BaselinePath: baselinePath,
		Now:          time.Unix(1_700_000_100, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	if current.Comparison == nil {
		t.Fatal("baseline comparison was not attached")
	}
	if !current.LedgerChecksPass || !current.Comparison.CurrentLedgerChecksPassed {
		t.Fatalf("comparison unexpectedly failed readiness checks: %+v", current.Checks)
	}
	if current.Comparison.HeightDelta != "0" || current.Comparison.AccountedExcessTrend != "unchanged" {
		t.Fatalf("unexpected comparison: %+v", current.Comparison)
	}
	if current.Comparison.BaselineManifest.SHA256 == "" {
		t.Fatal("baseline evidence hash is missing")
	}
}

func TestCompareManifestReportsSignedDrift(t *testing.T) {
	path := filepath.Join(t.TempDir(), "baseline.json")
	baseline := manifest{
		SchemaVersion:    manifestSchemaVersion,
		Mode:             "read_only_analysis",
		GeneratedAt:      "2026-08-07T01:00:00Z",
		Chain:            chainSummary{GenesisHash: "genesis", TipHeight: 100},
		Supply:           supplySummary{ChainIssuedDust: "1000", AccountedCirculatingDust: "1100", AccountedExcessDust: "100"},
		LedgerChecksPass: false,
	}
	writeJSON(t, path, baseline)

	current := baseline
	current.GeneratedAt = "2026-08-07T02:00:00Z"
	current.Chain.TipHeight = 125
	current.Supply.ChainIssuedDust = "1450"
	current.Supply.AccountedCirculatingDust = "1525"
	current.Supply.AccountedExcessDust = "75"
	current.LedgerChecksPass = true

	got, checks, err := compareManifest(path, current)
	if err != nil {
		t.Fatal(err)
	}
	if got.HeightDelta != "25" || got.ChainIssuedDeltaDust != "450" || got.AccountedSupplyDeltaDust != "425" {
		t.Fatalf("unexpected deltas: %+v", got)
	}
	if got.AccountedExcessDeltaDust != "-25" || got.AccountedExcessTrend != "decreased" {
		t.Fatalf("unexpected excess drift: %+v", got)
	}
	if got.BaselineLedgerChecksPassed {
		t.Fatal("baseline check status was not preserved")
	}
	for _, check := range checks {
		if !check.Passed {
			t.Fatalf("expected comparison check %q to pass: %s", check.Name, check.Detail)
		}
	}
}

func TestCompareManifestRejectsDifferentGenesis(t *testing.T) {
	path := filepath.Join(t.TempDir(), "baseline.json")
	baseline := manifest{
		SchemaVersion: manifestSchemaVersion,
		Mode:          "read_only_analysis",
		GeneratedAt:   "2026-08-07T01:00:00Z",
		Chain:         chainSummary{GenesisHash: "genesis-a", TipHeight: 100},
		Supply:        supplySummary{ChainIssuedDust: "1", AccountedCirculatingDust: "1", AccountedExcessDust: "0"},
	}
	writeJSON(t, path, baseline)
	current := baseline
	current.GeneratedAt = "2026-08-07T02:00:00Z"
	current.Chain.GenesisHash = "genesis-b"

	_, checks, err := compareManifest(path, current)
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range checks {
		if check.Name == "baseline-same-genesis" {
			if check.Passed {
				t.Fatal("different genesis hashes must fail comparison readiness")
			}
			return
		}
	}
	t.Fatal("baseline-same-genesis check not found")
}

func TestReadinessExitFailsClosed(t *testing.T) {
	tests := []struct {
		name string
		in   manifest
		want int
	}{
		{name: "ledger checks failed", in: manifest{}, want: 2},
		{name: "activation blocked", in: manifest{LedgerChecksPass: true}, want: 3},
		{name: "ready", in: manifest{LedgerChecksPass: true, Activation: activationSummary{Ready: true}}, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := readinessExit(tt.in)
			if got != tt.want {
				t.Fatalf("exit code=%d, want %d", got, tt.want)
			}
		})
	}
}
