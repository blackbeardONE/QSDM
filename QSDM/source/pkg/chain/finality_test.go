package chain

import (
	"testing"
)

func TestFinalityGadget_TrackAndConfirm(t *testing.T) {
	cfg := DefaultFinalityConfig()
	cfg.ConfirmationDepth = 3
	cfg.FinalityDepth = 6
	fg := NewFinalityGadget(cfg)

	for i := uint64(0); i < 10; i++ {
		fg.TrackBlock(i, "hash_"+string(rune('0'+i)))
	}

	// At tip=5, blocks 0-2 should be confirmed (depth >= 3)
	confirmed, finalized := fg.UpdateTip(5)
	if confirmed != 3 {
		t.Fatalf("expected 3 confirmed, got %d", confirmed)
	}
	if finalized != 0 {
		t.Fatalf("expected 0 finalized, got %d", finalized)
	}

	rec, ok := fg.GetStatus(0)
	if !ok {
		t.Fatal("expected record for height 0")
	}
	if rec.Status != FinalityConfirmed {
		t.Fatalf("expected confirmed, got %s", rec.Status)
	}
	if rec.Confirmations != 5 {
		t.Fatalf("expected 5 confirmations, got %d", rec.Confirmations)
	}
}

func TestFinalityGadget_Finalize(t *testing.T) {
	cfg := DefaultFinalityConfig()
	cfg.ConfirmationDepth = 2
	cfg.FinalityDepth = 5
	fg := NewFinalityGadget(cfg)

	for i := uint64(0); i < 3; i++ {
		fg.TrackBlock(i, "hash")
	}

	// First: confirm at depth 2
	fg.UpdateTip(4)
	// Then: finalize at depth 5
	_, finalized := fg.UpdateTip(7)
	if finalized < 1 {
		t.Fatalf("expected at least 1 finalized, got %d", finalized)
	}

	if !fg.IsFinalized(0) {
		t.Fatal("block 0 should be finalized at tip 7 with depth 5")
	}
	if fg.LastFinalized() < 1 {
		t.Fatal("last finalized should be at least 1")
	}
}

func TestFinalityGadget_ReorgLimit(t *testing.T) {
	cfg := DefaultFinalityConfig()
	cfg.ReorgLimit = 10
	fg := NewFinalityGadget(cfg)

	fg.TrackBlock(0, "h0")
	fg.UpdateTip(100)

	// Within limit
	if err := fg.CheckReorg(95); err != nil {
		t.Fatalf("expected no error for shallow reorg: %v", err)
	}

	// Exceeds limit
	if err := fg.CheckReorg(80); err == nil {
		t.Fatal("expected error for deep reorg beyond limit")
	}
}

func TestFinalityGadget_CannotReorgPastFinalized(t *testing.T) {
	cfg := DefaultFinalityConfig()
	cfg.ConfirmationDepth = 1
	cfg.FinalityDepth = 2
	cfg.ReorgLimit = 100
	fg := NewFinalityGadget(cfg)

	fg.TrackBlock(5, "h5")
	fg.UpdateTip(7) // confirms
	fg.UpdateTip(8) // finalizes

	if !fg.IsFinalized(5) {
		t.Fatal("block 5 should be finalized")
	}

	err := fg.CheckReorg(4) // trying to fork before finalized
	if err == nil {
		t.Fatal("expected error for reorg past finalized block")
	}
}

func TestFinalityGadget_PendingAndFinalizedCounts(t *testing.T) {
	cfg := DefaultFinalityConfig()
	cfg.ConfirmationDepth = 2
	cfg.FinalityDepth = 4
	fg := NewFinalityGadget(cfg)

	for i := uint64(0); i < 5; i++ {
		fg.TrackBlock(i, "h")
	}

	if fg.PendingCount() != 5 {
		t.Fatalf("expected 5 pending, got %d", fg.PendingCount())
	}

	fg.UpdateTip(10)

	if fg.PendingCount() != 0 {
		t.Fatalf("expected 0 pending after update, got %d", fg.PendingCount())
	}
	if fg.FinalizedCount() != 5 {
		t.Fatalf("expected 5 finalized, got %d", fg.FinalizedCount())
	}
}

func TestFinalityGadget_GetStatusMissing(t *testing.T) {
	fg := NewFinalityGadget(DefaultFinalityConfig())
	_, ok := fg.GetStatus(999)
	if ok {
		t.Fatal("expected false for non-tracked height")
	}
}

func TestFinalityGadget_ReorgAtTipIsOk(t *testing.T) {
	fg := NewFinalityGadget(DefaultFinalityConfig())
	fg.UpdateTip(10)

	// Fork at current tip is fine
	if err := fg.CheckReorg(10); err != nil {
		t.Fatalf("reorg at tip should be ok: %v", err)
	}
	// Fork above tip is fine
	if err := fg.CheckReorg(15); err != nil {
		t.Fatalf("reorg above tip should be ok: %v", err)
	}
}

func TestFinalityGadget_PolAnchorDelaysFinalize(t *testing.T) {
	cfg := DefaultFinalityConfig()
	// NewFinalityGadget clamps ConfirmationDepth 0 to 6; use 1 for shallow confirmation in this test.
	cfg.ConfirmationDepth = 1
	cfg.FinalityDepth = 2
	vs := NewValidatorSet(DefaultValidatorSetConfig())
	_ = vs.Register("v1", 100)
	pf := NewPolFollower(vs, 2.0/3.0)
	pf.SetAnchorFinality(true)
	pf.RecordLocalSealedBlock(1, "sr1")

	fg := NewFinalityGadget(cfg)
	fg.SetPolFollower(pf)
	fg.TrackBlockWithMeta(1, "h1", "sr1")

	fg.UpdateTip(3)
	rec, _ := fg.GetStatus(1)
	if rec.Status != FinalityConfirmed {
		t.Fatalf("expected confirmed, got %s", rec.Status)
	}

	pf.MarkLocalRoundCertificatePublished(1)
	fg.UpdateTip(3)
	rec2, _ := fg.GetStatus(1)
	if rec2.Status != FinalityFinalized {
		t.Fatalf("expected finalized after POL mark, got %s", rec2.Status)
	}
}
