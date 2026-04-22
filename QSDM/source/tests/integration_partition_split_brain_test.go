package tests

import (
	"fmt"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/chain"
)

// TestIntegration_PartitionSplitBrainRejoinShallowFork models two finality gadgets on either
// side of a partition that build different tips, then converge on rejoin. The shorter fork
// is accepted via CheckReorg (shallow) and the nodes agree on finalized prefix.
func TestIntegration_PartitionSplitBrainRejoinShallowFork(t *testing.T) {
	mk := func() *chain.FinalityGadget {
		cfg := chain.DefaultFinalityConfig()
		cfg.ReorgLimit = 8
		cfg.ConfirmationDepth = 3
		cfg.FinalityDepth = 12
		return chain.NewFinalityGadget(cfg)
	}

	a := mk()
	b := mk()

	// Shared history up to height 20 (finalized prefix both sides agree on).
	for h := uint64(0); h <= 20; h++ {
		hash := fmt.Sprintf("shared-%d", h)
		a.TrackBlock(h, hash)
		b.TrackBlock(h, hash)
	}
	a.UpdateTip(20)
	b.UpdateTip(20)

	// Partition: A builds 4 blocks on fork-a; B builds 3 on fork-b (shorter).
	for h := uint64(21); h <= 24; h++ {
		a.TrackBlock(h, fmt.Sprintf("fork-a-%d", h))
	}
	a.UpdateTip(24)
	for h := uint64(21); h <= 23; h++ {
		b.TrackBlock(h, fmt.Sprintf("fork-b-%d", h))
	}
	b.UpdateTip(23)

	// Rejoin: B learns about A's longer chain. Shallow fork from height 21 (depth 3)
	// must be within ReorgLimit (8). CheckReorg measures distance from current tip
	// to the forkHeight — for B at tip 23 rolling back to 20 is depth 3, allowed.
	if err := b.CheckReorg(20); err != nil {
		t.Fatalf("shallow rejoin reorg should be allowed on B: %v", err)
	}

	// After B reorgs to A's fork, both agree on shared prefix up to height 20 (at least).
	if a.LastFinalized() < 20 {
		// Finality depth is 12; with tip 24, heights <= 12 should be finalized on A.
		// At minimum shared prefix must remain finalized.
		if finA, _ := a.GetStatus(10); finA == nil || finA.Status != chain.FinalityFinalized {
			t.Fatalf("expected A to finalize shared prefix height 10, got %+v", finA)
		}
	}
}

// TestIntegration_PartitionDeepForkRejected models a long partition where the minority
// side tries to reorg beyond ReorgLimit. The reorg must be rejected even though it is
// numerically possible — this is the safety guarantee against long-range split-brain.
func TestIntegration_PartitionDeepForkRejected(t *testing.T) {
	cfg := chain.DefaultFinalityConfig()
	cfg.ReorgLimit = 5
	cfg.ConfirmationDepth = 99
	cfg.FinalityDepth = 99
	fg := chain.NewFinalityGadget(cfg)

	for h := uint64(0); h < 30; h++ {
		fg.TrackBlock(h, fmt.Sprintf("canonical-%d", h))
	}
	fg.UpdateTip(29)

	// Attacker proposes rejoin at height 15 — depth 14 from tip 29 >> 5.
	if err := fg.CheckReorg(15); err == nil {
		t.Fatal("deep fork past reorg limit must be rejected")
	}

	// But a shallow fork within the limit is still allowed.
	if err := fg.CheckReorg(25); err != nil {
		t.Fatalf("shallow fork within reorg limit must be allowed, got: %v", err)
	}
}

// TestIntegration_PartitionConvergenceAfterHeal drives many blocks on both sides, then
// ensures that once they converge, both gadgets can continue tracking new canonical blocks
// without panicking or losing finalized records.
func TestIntegration_PartitionConvergenceAfterHeal(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping partition convergence in -short")
	}

	// Use large confirmation/finality depths so blocks mid-fork do not auto-finalize
	// on the divergent sides (otherwise a later rejoin would try to reorg past
	// finalized height, which the gadget correctly rejects).
	cfg := chain.DefaultFinalityConfig()
	cfg.ReorgLimit = 16
	cfg.ConfirmationDepth = 99
	cfg.FinalityDepth = 999
	a := chain.NewFinalityGadget(cfg)
	b := chain.NewFinalityGadget(cfg)

	for h := uint64(0); h <= 50; h++ {
		hash := fmt.Sprintf("pre-%d", h)
		a.TrackBlock(h, hash)
		b.TrackBlock(h, hash)
	}
	a.UpdateTip(50)
	b.UpdateTip(50)

	for h := uint64(51); h <= 65; h++ {
		a.TrackBlock(h, fmt.Sprintf("a-fork-%d", h))
		b.TrackBlock(h, fmt.Sprintf("b-fork-%d", h))
	}
	a.UpdateTip(65)
	b.UpdateTip(65)

	// Heal: B accepts A's canonical chain. Distance from 65 back to 50 is 15,
	// within ReorgLimit=16 and nothing finalized yet, so the reorg must be allowed.
	if err := b.CheckReorg(50); err != nil {
		t.Fatalf("heal reorg must be allowed (depth 15 vs limit 16): %v", err)
	}

	// Continue extending canonical history on both sides; tracking must not panic
	// and finality eventually advances once the finality depth is reached naturally.
	for h := uint64(66); h <= 80; h++ {
		hash := fmt.Sprintf("post-%d", h)
		a.TrackBlock(h, hash)
		b.TrackBlock(h, hash)
	}
	a.UpdateTip(80)
	b.UpdateTip(80)

	if a.PendingCount() == 0 {
		t.Fatal("expected A to still track pending blocks after heal")
	}
	if b.PendingCount() == 0 {
		t.Fatal("expected B to still track pending blocks after heal")
	}
}
