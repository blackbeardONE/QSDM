package main

import (
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/config"
)

// The replay tool applies stream_ed25519_retirement_height exactly like
// main() (TestHL1ReplayConsensusSettingsMatchMain pins the call list), so a
// boot replay of a block at or above H enforces the same rule as the live
// node, and 0 leaves history unchanged.
func TestPQOnlyReplaySettingsApplyStreamRetirementHeight(t *testing.T) {
	prev := chain.StreamEd25519RetirementHeight()
	t.Cleanup(func() { chain.SetStreamEd25519RetirementHeight(prev) })
	// The other consensus settings stay at their zero values, which are the
	// package defaults.
	settings := &config.Config{}
	settings.StreamEd25519RetirementHeight = 900_000
	hl1ReplayConsensusSettings(settings)
	if chain.StreamEd25519RetirementHeight() != 900_000 {
		t.Fatal("hl1ReplayConsensusSettings did not apply stream_ed25519_retirement_height")
	}
	settings.StreamEd25519RetirementHeight = 0
	hl1ReplayConsensusSettings(settings)
	if chain.StreamEd25519RetirementHeight() != 0 || chain.StreamEd25519SessionKeysRetiredAt(1<<62) {
		t.Fatal("hl1ReplayConsensusSettings did not apply 0")
	}
}

func TestPQOnlyMetricsCollector(t *testing.T) {
	prev := chain.StreamEd25519RetirementHeight()
	t.Cleanup(func() { chain.SetStreamEd25519RetirementHeight(prev) })
	chain.SetStreamEd25519RetirementHeight(321)
	got := map[string]float64{}
	for _, m := range pqOnlyMetricsCollector()() {
		got[m.Name] = m.Value
	}
	if got["qsdm_stream_ed25519_retirement_height"] != 321 {
		t.Fatalf("metrics %v", got)
	}
	if _, ok := got["qsdm_tx_gossip_ed25519_rejected_total"]; !ok {
		t.Fatalf("qsdm_tx_gossip_ed25519_rejected_total missing: metrics %v", got)
	}
}
