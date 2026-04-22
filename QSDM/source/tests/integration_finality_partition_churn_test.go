package tests

import (
	"fmt"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/chain"
)

// TestIntegration_FinalityReorgBoundariesAfterChurn exercises reorg limits after many tracked blocks
// (partition-style safety: deep forks rejected, shallow forks allowed).
func TestIntegration_FinalityReorgBoundariesAfterChurn(t *testing.T) {
	cfg := chain.DefaultFinalityConfig()
	cfg.ReorgLimit = 8
	// Keep confirmation/finality depths high so a single UpdateTip does not finalize mid-chain
	// (avoids coupling this test to lastFinalized vs shallow fork height).
	cfg.ConfirmationDepth = 99
	cfg.FinalityDepth = 99
	fg := chain.NewFinalityGadget(cfg)

	for h := uint64(0); h < 50; h++ {
		fg.TrackBlock(h, fmt.Sprintf("blk-%d", h))
	}
	fg.UpdateTip(49)

	if err := fg.CheckReorg(45); err != nil {
		t.Fatalf("shallow reorg should be allowed: %v", err)
	}
	if err := fg.CheckReorg(35); err == nil {
		t.Fatal("expected error for reorg deeper than limit")
	}
}
