package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

func compareManifest(path string, current manifest) (comparisonSummary, []checkResult, error) {
	raw, evidence, stable, err := readStableFile(path)
	if err != nil {
		return comparisonSummary{}, nil, err
	}
	var baseline manifest
	if err := json.Unmarshal(raw, &baseline); err != nil {
		return comparisonSummary{}, nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if baseline.SchemaVersion != manifestSchemaVersion {
		return comparisonSummary{}, nil, fmt.Errorf(
			"unsupported baseline schema %d (want %d)",
			baseline.SchemaVersion,
			manifestSchemaVersion,
		)
	}
	if baseline.Mode != "read_only_analysis" {
		return comparisonSummary{}, nil, fmt.Errorf("unsupported baseline mode %q", baseline.Mode)
	}
	baselineGenerated, err := time.Parse(time.RFC3339Nano, baseline.GeneratedAt)
	if err != nil {
		return comparisonSummary{}, nil, fmt.Errorf("baseline generated_at is invalid: %w", err)
	}
	currentGenerated, err := time.Parse(time.RFC3339Nano, current.GeneratedAt)
	if err != nil {
		return comparisonSummary{}, nil, fmt.Errorf("current generated_at is invalid: %w", err)
	}

	baselineIssued, err := parseDust("baseline chain issued", baseline.Supply.ChainIssuedDust)
	if err != nil {
		return comparisonSummary{}, nil, err
	}
	currentIssued, err := parseDust("current chain issued", current.Supply.ChainIssuedDust)
	if err != nil {
		return comparisonSummary{}, nil, err
	}
	baselineAccounted, err := parseDust("baseline accounted supply", baseline.Supply.AccountedCirculatingDust)
	if err != nil {
		return comparisonSummary{}, nil, err
	}
	currentAccounted, err := parseDust("current accounted supply", current.Supply.AccountedCirculatingDust)
	if err != nil {
		return comparisonSummary{}, nil, err
	}
	baselineExcess, err := parseDust("baseline accounted excess", baseline.Supply.AccountedExcessDust)
	if err != nil {
		return comparisonSummary{}, nil, err
	}
	currentExcess, err := parseDust("current accounted excess", current.Supply.AccountedExcessDust)
	if err != nil {
		return comparisonSummary{}, nil, err
	}

	sameGenesis := baseline.Chain.GenesisHash != "" && baseline.Chain.GenesisHash == current.Chain.GenesisHash
	tipNotRegressed := current.Chain.TipHeight >= baseline.Chain.TipHeight
	generatedNotRegressed := !currentGenerated.Before(baselineGenerated)
	checks := []checkResult{
		{Name: "baseline-manifest-stable", Passed: stable, Detail: "baseline manifest did not change while it was read"},
		{Name: "baseline-same-genesis", Passed: sameGenesis, Detail: "baseline and current manifests describe the same genesis block"},
		{Name: "baseline-tip-not-newer", Passed: tipNotRegressed, Detail: fmt.Sprintf("baseline tip=%d current tip=%d", baseline.Chain.TipHeight, current.Chain.TipHeight)},
		{Name: "baseline-generated-not-newer", Passed: generatedNotRegressed, Detail: fmt.Sprintf("baseline generated=%s current generated=%s", baseline.GeneratedAt, current.GeneratedAt)},
	}

	return comparisonSummary{
		BaselineManifest:           evidence,
		BaselineGeneratedAt:        baseline.GeneratedAt,
		BaselineTipHeight:          baseline.Chain.TipHeight,
		HeightDelta:                signedDelta(current.Chain.TipHeight, baseline.Chain.TipHeight),
		ChainIssuedDeltaDust:       signedDelta(currentIssued, baselineIssued),
		AccountedSupplyDeltaDust:   signedDelta(currentAccounted, baselineAccounted),
		AccountedExcessDeltaDust:   signedDelta(currentExcess, baselineExcess),
		AccountedExcessTrend:       deltaTrend(currentExcess, baselineExcess),
		BaselineLedgerChecksPassed: baseline.LedgerChecksPass,
	}, checks, nil
}

func parseDust(name, value string) (uint64, error) {
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s is invalid: %q", name, value)
	}
	return parsed, nil
}

func signedDelta(current, baseline uint64) string {
	if current >= baseline {
		return strconv.FormatUint(current-baseline, 10)
	}
	return "-" + strconv.FormatUint(baseline-current, 10)
}

func deltaTrend(current, baseline uint64) string {
	switch {
	case current > baseline:
		return "increased"
	case current < baseline:
		return "decreased"
	default:
		return "unchanged"
	}
}
