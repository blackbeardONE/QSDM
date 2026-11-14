package main

import (
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/config"
)

// The replay tool applies edge_relay_only_activation_height exactly like
// main() (TestHL1ReplayConsensusSettingsMatchMain pins the call list), so a
// boot replay of a block at or above the height enforces the same rule as the
// running node, and 0 leaves history unchanged.
func TestEdgeRelayOnlyReplaySettingsApplyHeight(t *testing.T) {
	prev := chain.EdgeRelayOnlyActivationHeight()
	t.Cleanup(func() { chain.SetEdgeRelayOnlyActivationHeight(prev) })
	settings := &config.Config{EdgeRelayOnlyActivationHeight: 900_000}
	hl1ReplayConsensusSettings(settings)
	if chain.EdgeRelayOnlyActivationHeight() != 900_000 {
		t.Fatal("hl1ReplayConsensusSettings did not apply edge_relay_only_activation_height")
	}
	settings.EdgeRelayOnlyActivationHeight = 0
	hl1ReplayConsensusSettings(settings)
	if chain.EdgeRelayOnlyActivationHeight() != 0 || chain.EdgeRelayOnlyActiveAt(1<<62) {
		t.Fatal("hl1ReplayConsensusSettings did not apply 0")
	}
}

func TestEdgeRelayOnlyMetricsCollector(t *testing.T) {
	prev := chain.EdgeRelayOnlyActivationHeight()
	t.Cleanup(func() { chain.SetEdgeRelayOnlyActivationHeight(prev) })
	chain.SetEdgeRelayOnlyActivationHeight(123)
	got := map[string]float64{}
	for _, m := range edgeRelayOnlyMetricsCollector()() {
		got[m.Name] = m.Value
	}
	if got["qsdm_edge_relay_only_activation_height"] != 123 {
		t.Fatalf("metrics %v", got)
	}
	for _, name := range []string{"qsdm_edge_relay_only_admission_rejected_total", "qsdm_task_catalog_reserved_admission_rejected_total"} {
		if _, ok := got[name]; !ok {
			t.Fatalf("%s missing: metrics %v", name, got)
		}
	}
}
