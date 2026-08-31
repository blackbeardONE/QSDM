package main

import "testing"

func TestEvaluateSuggestsFutureHeightForCompatibilityPosture(t *testing.T) {
	v := evaluate([]nodeReport{
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
	}, 50, 0)

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
	v := evaluate([]nodeReport{
		{
			URL:                              "https://a.example/api/v1/status",
			NodeID:                           "validator-a",
			ChainTip:                         1000,
			SignedConsensusSupported:         true,
			UnsignedConsensusTrafficAccepted: true,
		},
	}, 50, 1000)

	if v.OK {
		t.Fatalf("expected blocked verdict for past activation height, got %#v", v)
	}
}

func TestEvaluateRejectsMixedRolloutPosture(t *testing.T) {
	v := evaluate([]nodeReport{
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
	}, 50, 0)

	if v.OK {
		t.Fatalf("expected blocked verdict for mixed posture, got %#v", v)
	}
}

func TestEvaluateAcceptsConsistentScheduledRollout(t *testing.T) {
	v := evaluate([]nodeReport{
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
	}, 50, 1200)

	if !v.OK {
		t.Fatalf("expected OK verdict, got %#v", v)
	}
	if v.State != "scheduled" {
		t.Fatalf("state = %q, want scheduled", v.State)
	}
}

func TestEvaluateRejectsDuplicateNodeIDs(t *testing.T) {
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
			NodeID:                           "validator-a",
			ChainTip:                         1000,
			SignedConsensusSupported:         true,
			UnsignedConsensusTrafficAccepted: true,
		},
	}, 50, 0)

	if v.OK {
		t.Fatalf("expected blocked verdict for duplicate node IDs, got %#v", v)
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
