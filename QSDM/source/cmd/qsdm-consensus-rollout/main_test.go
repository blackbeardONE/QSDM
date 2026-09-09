package main

import (
	"strings"
	"testing"
)

const testValidatorSetFingerprint = "aa9c67f2d5f5a9b7e1c48b9827e09f929513e19fbd504124a17ce72de7720b3a"

func withMatchingValidatorSet(reports []nodeReport) []nodeReport {
	for i := range reports {
		reports[i].ValidatorSetActiveCount = minimumBFTValidatorSetSize
		reports[i].ValidatorSetFingerprint = testValidatorSetFingerprint
	}
	return reports
}

func TestEvaluateSuggestsFutureHeightForCompatibilityPosture(t *testing.T) {
	v := evaluate(withMatchingValidatorSet([]nodeReport{
		{
			URL:                              "https://a.example/api/v1/status",
			NodeID:                           "validator-a",
			ChainTip:                         1000,
			Peers:                            1,
			SignedConsensusSupported:         true,
			RequireSignedVotes:               false,
			SignedMessageActivationHeight:    0,
			UnsignedConsensusTrafficAccepted: true,
		},
		{
			URL:                              "https://b.example/api/v1/status",
			NodeID:                           "validator-b",
			ChainTip:                         1100,
			Peers:                            1,
			SignedConsensusSupported:         true,
			RequireSignedVotes:               false,
			SignedMessageActivationHeight:    0,
			UnsignedConsensusTrafficAccepted: true,
		},
	}), 50, 0, false)

	if !v.OK {
		t.Fatalf("expected OK verdict, got %#v", v)
	}
	if v.State != "ready_to_schedule" {
		t.Fatalf("state = %q, want ready_to_schedule", v.State)
	}
	if v.SuggestedActivationHeight != 1150 {
		t.Fatalf("suggested activation = %d, want 1150", v.SuggestedActivationHeight)
	}
}

func TestEvaluateRejectsActivationAtOrBelowCurrentTip(t *testing.T) {
	v := evaluate(withMatchingValidatorSet([]nodeReport{
		{
			URL:                              "https://a.example/api/v1/status",
			NodeID:                           "validator-a",
			ChainTip:                         1000,
			SignedConsensusSupported:         true,
			UnsignedConsensusTrafficAccepted: true,
		},
	}), 50, 1000, true)

	if v.OK {
		t.Fatalf("expected blocked verdict for past activation height, got %#v", v)
	}
}

func TestEvaluateRejectsMixedRolloutPosture(t *testing.T) {
	v := evaluate(withMatchingValidatorSet([]nodeReport{
		{
			URL:                              "https://a.example/api/v1/status",
			NodeID:                           "validator-a",
			ChainTip:                         1000,
			SignedConsensusSupported:         true,
			RequireSignedVotes:               true,
			SignedMessageActivationHeight:    1200,
			SignedConsensusActive:            false,
			UnsignedConsensusTrafficAccepted: true,
		},
		{
			URL:                              "https://b.example/api/v1/status",
			NodeID:                           "validator-b",
			ChainTip:                         999,
			SignedConsensusSupported:         true,
			RequireSignedVotes:               false,
			SignedMessageActivationHeight:    0,
			UnsignedConsensusTrafficAccepted: true,
		},
	}), 50, 0, false)

	if v.OK {
		t.Fatalf("expected blocked verdict for mixed posture, got %#v", v)
	}
}

func TestEvaluateAcceptsConsistentScheduledRollout(t *testing.T) {
	v := evaluate(withMatchingValidatorSet([]nodeReport{
		{
			URL:                              "https://a.example/api/v1/status",
			NodeID:                           "validator-a",
			ChainTip:                         1000,
			SignedConsensusSupported:         true,
			RequireSignedVotes:               true,
			SignedMessageActivationHeight:    1200,
			SignedConsensusActive:            false,
			UnsignedConsensusTrafficAccepted: true,
		},
		{
			URL:                              "https://b.example/api/v1/status",
			NodeID:                           "validator-b",
			ChainTip:                         1001,
			SignedConsensusSupported:         true,
			RequireSignedVotes:               true,
			SignedMessageActivationHeight:    1200,
			SignedConsensusActive:            false,
			UnsignedConsensusTrafficAccepted: true,
		},
	}), 50, 1200, false)

	if !v.OK {
		t.Fatalf("expected OK verdict, got %#v", v)
	}
	if v.State != "scheduled" {
		t.Fatalf("state = %q, want scheduled", v.State)
	}
}

func TestEvaluateRejectsDuplicateNodeIDs(t *testing.T) {
	v := evaluate(withMatchingValidatorSet([]nodeReport{
		{
			URL:                              "https://a.example/api/v1/status",
			NodeID:                           "validator-a",
			ChainTip:                         1000,
			SignedConsensusSupported:         true,
			UnsignedConsensusTrafficAccepted: true,
		},
		{
			URL:                              "https://b.example/api/v1/status",
			NodeID:                           "validator-a",
			ChainTip:                         1000,
			SignedConsensusSupported:         true,
			UnsignedConsensusTrafficAccepted: true,
		},
	}), 50, 0, false)

	if v.OK {
		t.Fatalf("expected blocked verdict for duplicate node IDs, got %#v", v)
	}
}

func TestEvaluateRejectsSingleNodeByDefault(t *testing.T) {
	v := evaluate(withMatchingValidatorSet([]nodeReport{
		{
			URL:                              "https://a.example/api/v1/status",
			NodeID:                           "validator-a",
			ChainTip:                         1000,
			SignedConsensusSupported:         true,
			UnsignedConsensusTrafficAccepted: true,
		},
	}), 50, 0, false)

	if v.OK || v.State != "blocked" {
		t.Fatalf("expected blocked verdict for a single node, got %#v", v)
	}
	if !hasText(v.Errors, "at least two distinct node reports") {
		t.Fatalf("expected multi-validator evidence error, got %#v", v.Errors)
	}
}

func TestEvaluateAllowsSingleNodeDiagnosticWithoutActivationHeight(t *testing.T) {
	v := evaluate(withMatchingValidatorSet([]nodeReport{
		{
			URL:                              "https://a.example/api/v1/status",
			NodeID:                           "validator-a",
			ChainTip:                         1000,
			SignedConsensusSupported:         true,
			UnsignedConsensusTrafficAccepted: true,
		},
	}), 50, 0, true)

	if !v.OK || v.State != "single_node_diagnostic" {
		t.Fatalf("expected diagnostic-only single-node verdict, got %#v", v)
	}
	if v.SuggestedActivationHeight != 0 {
		t.Fatalf("single-node diagnostic suggested activation height %d", v.SuggestedActivationHeight)
	}
	if !hasText(v.Warnings, "no shared activation height") {
		t.Fatalf("expected diagnostic warning, got %#v", v.Warnings)
	}
}

func TestEvaluateRejectsSmallValidatorSetByDefault(t *testing.T) {
	reports := withMatchingValidatorSet([]nodeReport{
		{URL: "https://a.example/api/v1/status", NodeID: "validator-a", ChainTip: 1000, SignedConsensusSupported: true, UnsignedConsensusTrafficAccepted: true},
		{URL: "https://b.example/api/v1/status", NodeID: "validator-b", ChainTip: 1001, SignedConsensusSupported: true, UnsignedConsensusTrafficAccepted: true},
	})
	for i := range reports {
		reports[i].ValidatorSetActiveCount = 2
	}
	v := evaluate(reports, 50, 0, false)
	if v.OK || v.State != "blocked" {
		t.Fatalf("expected two-validator set to be blocked for BFT readiness, got %#v", v)
	}
	if !hasText(v.Errors, "at least 4 active validators") {
		t.Fatalf("expected one-fault BFT size error, got %#v", v.Errors)
	}
}

func TestEvaluateAllowsSmallValidatorSetDiagnostic(t *testing.T) {
	reports := withMatchingValidatorSet([]nodeReport{
		{URL: "https://a.example/api/v1/status", NodeID: "validator-a", ChainTip: 1000, SignedConsensusSupported: true, UnsignedConsensusTrafficAccepted: true},
		{URL: "https://b.example/api/v1/status", NodeID: "validator-b", ChainTip: 1001, SignedConsensusSupported: true, UnsignedConsensusTrafficAccepted: true},
	})
	for i := range reports {
		reports[i].ValidatorSetActiveCount = 2
	}
	v := evaluateWithOptions(reports, 50, 0, false, true)
	if !v.OK || v.State != "small_validator_set_diagnostic" {
		t.Fatalf("expected small-set diagnostic verdict, got %#v", v)
	}
	if v.SuggestedActivationHeight != 0 {
		t.Fatalf("small-set diagnostic suggested activation height %d", v.SuggestedActivationHeight)
	}
	if !hasText(v.Warnings, "diagnostic-only") {
		t.Fatalf("expected diagnostic-only warning, got %#v", v.Warnings)
	}
}

func hasText(values []string, want string) bool {
	for _, value := range values {
		if strings.Contains(value, want) {
			return true
		}
	}
	return false
}

func TestParseFlagsAllowsSingleNodeDiagnostic(t *testing.T) {
	opts := parseFlags([]string{
		"--node", "https://a.example/api/v1",
		"--allow-single-node",
		"--allow-small-validator-set",
	})
	if !opts.allowSingleNode {
		t.Fatal("allow-single-node was not parsed")
	}
	if !opts.allowSmallSet {
		t.Fatal("allow-small-validator-set was not parsed")
	}
}
func TestStatusEndpointNormalizesCommonInputs(t *testing.T) {
	tests := map[string]string{
		"https://api.qsdm.tech":               "https://api.qsdm.tech/api/v1/status",
		"https://api.qsdm.tech/api/v1":        "https://api.qsdm.tech/api/v1/status",
		"https://api.qsdm.tech/api/v1/status": "https://api.qsdm.tech/api/v1/status",
	}
	for in, want := range tests {
		got, err := statusEndpoint(in)
		if err != nil {
			t.Fatalf("statusEndpoint(%q): %v", in, err)
		}
		if got != want {
			t.Fatalf("statusEndpoint(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStatusEndpointRejectsUnusableURLs(t *testing.T) {
	for _, input := range []string{
		"ftp://validator.example",
		"https://operator:secret@validator.example",
		"https://validator.example?probe=1",
		"https://validator.example#status",
	} {
		if _, err := statusEndpoint(input); err == nil {
			t.Fatalf("statusEndpoint(%q) succeeded; expected URL to be rejected", input)
		}
	}
}

func TestEvaluateRejectsMissingValidatorSetPosture(t *testing.T) {
	v := evaluate([]nodeReport{
		{
			URL:                              "https://a.example/api/v1/status",
			NodeID:                           "validator-a",
			ChainTip:                         1000,
			SignedConsensusSupported:         true,
			UnsignedConsensusTrafficAccepted: true,
		},
		{
			URL:                              "https://b.example/api/v1/status",
			NodeID:                           "validator-b",
			ChainTip:                         1001,
			SignedConsensusSupported:         true,
			UnsignedConsensusTrafficAccepted: true,
		},
	}, 50, 0, false)

	if v.OK || v.State != "blocked" {
		t.Fatalf("expected missing validator-set posture to block, got %#v", v)
	}
	if !hasText(v.Errors, "validator_set.active_count") || !hasText(v.Errors, "validator_set.fingerprint") {
		t.Fatalf("expected validator-set posture errors, got %#v", v.Errors)
	}
}

func TestEvaluateRejectsMismatchedValidatorSetSnapshot(t *testing.T) {
	reports := withMatchingValidatorSet([]nodeReport{
		{
			URL:                              "https://a.example/api/v1/status",
			NodeID:                           "validator-a",
			ChainTip:                         1000,
			SignedConsensusSupported:         true,
			UnsignedConsensusTrafficAccepted: true,
		},
		{
			URL:                              "https://b.example/api/v1/status",
			NodeID:                           "validator-b",
			ChainTip:                         1001,
			SignedConsensusSupported:         true,
			UnsignedConsensusTrafficAccepted: true,
		},
	})
	reports[1].ValidatorSetActiveCount = 3
	reports[1].ValidatorSetFingerprint = "d06e5e333087ce4eb7eec48c19ca33ceab473012bac64190d1ad2d0da1e6f017"

	v := evaluate(reports, 50, 0, false)
	if v.OK || v.State != "blocked" {
		t.Fatalf("expected mismatched validator-set snapshot to block, got %#v", v)
	}
	if !hasText(v.Errors, "different validator_set.fingerprint") || !hasText(v.Errors, "validator_set.active_count=3") {
		t.Fatalf("expected validator-set mismatch errors, got %#v", v.Errors)
	}
}

func TestReportFromStatusIncludesValidatorSet(t *testing.T) {
	report := reportFromStatus("https://a.example/api/v1/status", statusResponse{
		ValidatorSet: validatorSetInfo{
			ActiveCount: 2,
			Fingerprint: "  " + testValidatorSetFingerprint + "  ",
		},
	})
	if report.ValidatorSetActiveCount != 2 || report.ValidatorSetFingerprint != testValidatorSetFingerprint {
		t.Fatalf("validator set report = %#v", report)
	}
}
