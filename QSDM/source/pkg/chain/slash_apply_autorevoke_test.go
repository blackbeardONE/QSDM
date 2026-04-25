package chain

import (
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mining"
	"github.com/blackbeardONE/QSDM/pkg/mining/slashing"
)

// These tests cover the auto-revoke step in SlashApplier:
// after the stake mutation, a record whose StakeDust dips
// strictly below AutoRevokeMinStakeDust is moved into the
// unbond window. Companion to slash_apply_test.go (which
// covers the slash arithmetic and replay-defence paths).

func TestSlashApplier_AutoRevoke_FullDrain(t *testing.T) {
	// Verifier cap big enough to drain the whole bond. Default
	// AutoRevokeMinStakeDust = mining.MinEnrollStakeDust, so the
	// post-slash stake of 0 < threshold triggers revoke.
	fx := buildSlashFixture(t, 0, 50*100_000_000)
	payload := slashing.SlashPayload{
		NodeID:          fx.nodeID,
		EvidenceKind:    slashing.EvidenceKindForgedAttestation,
		EvidenceBlob:    []byte("evidence-full-drain"),
		SlashAmountDust: 50 * 100_000_000,
	}
	tx := buildSlashTx("slasher-addr", 0, 0.001, payload)
	if err := fx.slasher.ApplySlashTx(tx, 101); err != nil {
		t.Fatalf("slash apply: %v", err)
	}

	rec, _ := fx.state.Lookup(fx.nodeID)
	if rec == nil {
		t.Fatal("record vanished after slash; should remain until unbond matures")
	}
	if rec.Active() {
		t.Error("fully-drained record should not be Active() after auto-revoke")
	}
	if rec.RevokedAtHeight != 101 {
		t.Errorf("RevokedAtHeight: got %d, want 101", rec.RevokedAtHeight)
	}
	if rec.UnbondMaturesAtHeight == 0 {
		t.Error("UnbondMaturesAtHeight not set on auto-revoke")
	}
	// gpu_uuid binding should be released so a fresh node can
	// re-enroll the same physical card immediately.
	if owner, _ := fx.state.GPUUUIDBound("GPU-1234"); owner != "" {
		t.Errorf("gpu binding not released on auto-revoke: %q", owner)
	}
}

func TestSlashApplier_AutoRevoke_PartialUnderMin(t *testing.T) {
	// Verifier cap = 1 CELL leaves 9 CELL of stake, below the
	// 10 CELL minimum, so the record auto-revokes.
	fx := buildSlashFixture(t, 0, 1*100_000_000)
	payload := slashing.SlashPayload{
		NodeID:          fx.nodeID,
		EvidenceKind:    slashing.EvidenceKindForgedAttestation,
		EvidenceBlob:    []byte("evidence-partial-under"),
		SlashAmountDust: 1 * 100_000_000,
	}
	tx := buildSlashTx("slasher-addr", 0, 0.001, payload)
	if err := fx.slasher.ApplySlashTx(tx, 101); err != nil {
		t.Fatalf("slash apply: %v", err)
	}
	rec, _ := fx.state.Lookup(fx.nodeID)
	if rec.Active() {
		t.Error("under-bonded record should not be Active() after auto-revoke")
	}
	wantRemaining := uint64(9 * 100_000_000)
	if rec.StakeDust != wantRemaining {
		t.Errorf("remaining stake: got %d, want %d", rec.StakeDust, wantRemaining)
	}
}

func TestSlashApplier_AutoRevoke_PartialAtThreshold_StaysActive(t *testing.T) {
	// Drop the auto-revoke threshold so a 5 CELL post-slash
	// remainder is *at* the threshold, not below. Boundary
	// (stake == threshold) must NOT revoke — only "strictly
	// below" should.
	fx := buildSlashFixture(t, 0, 5*100_000_000)
	fx.slasher.AutoRevokeMinStakeDust = 5 * 100_000_000

	payload := slashing.SlashPayload{
		NodeID:          fx.nodeID,
		EvidenceKind:    slashing.EvidenceKindForgedAttestation,
		EvidenceBlob:    []byte("evidence-at-threshold"),
		SlashAmountDust: 5 * 100_000_000,
	}
	tx := buildSlashTx("slasher-addr", 0, 0.001, payload)
	if err := fx.slasher.ApplySlashTx(tx, 101); err != nil {
		t.Fatalf("slash apply: %v", err)
	}
	rec, _ := fx.state.Lookup(fx.nodeID)
	if !rec.Active() {
		t.Error("at-threshold record should still be Active() (no auto-revoke)")
	}
	if rec.RevokedAtHeight != 0 {
		t.Errorf("RevokedAtHeight should be untouched, got %d", rec.RevokedAtHeight)
	}
}

func TestSlashApplier_AutoRevoke_Disabled(t *testing.T) {
	// AutoRevokeMinStakeDust = 0 disables auto-revoke entirely:
	// even a fully-drained record stays Active().
	fx := buildSlashFixture(t, 0, 50*100_000_000)
	fx.slasher.AutoRevokeMinStakeDust = 0

	payload := slashing.SlashPayload{
		NodeID:          fx.nodeID,
		EvidenceKind:    slashing.EvidenceKindForgedAttestation,
		EvidenceBlob:    []byte("evidence-disabled-revoke"),
		SlashAmountDust: 50 * 100_000_000,
	}
	tx := buildSlashTx("slasher-addr", 0, 0.001, payload)
	if err := fx.slasher.ApplySlashTx(tx, 101); err != nil {
		t.Fatalf("slash apply: %v", err)
	}
	rec, _ := fx.state.Lookup(fx.nodeID)
	if !rec.Active() {
		t.Error("auto-revoke disabled but record was revoked")
	}
	if rec.StakeDust != 0 {
		t.Errorf("stake should still be drained, got %d", rec.StakeDust)
	}
}

func TestSlashApplier_AutoRevoke_DefaultsToMinEnrollStakeDust(t *testing.T) {
	// Wires the default applier and asserts the field equals
	// the protocol constant — guards against accidental
	// drift if a future change reorders construction.
	fx := buildSlashFixture(t, 0, 1)
	if fx.slasher.AutoRevokeMinStakeDust != mining.MinEnrollStakeDust {
		t.Errorf("default AutoRevokeMinStakeDust: got %d, want %d",
			fx.slasher.AutoRevokeMinStakeDust, mining.MinEnrollStakeDust)
	}
}

func TestSlashApplier_AutoRevoke_OnAlreadyRevokedRecord_NoDoubleWindow(t *testing.T) {
	// Manually revoke first, then apply a slash with fresh
	// evidence. The slash should still drain stake, but the
	// unbond window must not be pushed forward by the
	// auto-revoke step (idempotency check).
	fx := buildSlashFixture(t, 0, 100_000_000)

	// First slash drains 1 CELL → leaves 9 CELL → triggers
	// auto-revoke at height 101.
	p1 := slashing.SlashPayload{
		NodeID:          fx.nodeID,
		EvidenceKind:    slashing.EvidenceKindForgedAttestation,
		EvidenceBlob:    []byte("evidence-first"),
		SlashAmountDust: 100_000_000,
	}
	if err := fx.slasher.ApplySlashTx(buildSlashTx("slasher-addr", 0, 0.001, p1), 101); err != nil {
		t.Fatalf("first slash: %v", err)
	}
	r1, _ := fx.state.Lookup(fx.nodeID)
	matureBefore := r1.UnbondMaturesAtHeight

	// Second slash with different evidence at a later height.
	p2 := slashing.SlashPayload{
		NodeID:          fx.nodeID,
		EvidenceKind:    slashing.EvidenceKindForgedAttestation,
		EvidenceBlob:    []byte("evidence-second"),
		SlashAmountDust: 100_000_000,
	}
	if err := fx.slasher.ApplySlashTx(buildSlashTx("slasher-addr", 1, 0.001, p2), 250); err != nil {
		t.Fatalf("second slash: %v", err)
	}
	r2, _ := fx.state.Lookup(fx.nodeID)
	if r2.UnbondMaturesAtHeight != matureBefore {
		t.Errorf("second slash extended unbond window: got %d, want %d",
			r2.UnbondMaturesAtHeight, matureBefore)
	}
	if r2.RevokedAtHeight != r1.RevokedAtHeight {
		t.Errorf("second slash moved RevokedAtHeight: got %d, want %d",
			r2.RevokedAtHeight, r1.RevokedAtHeight)
	}
	// But stake should keep draining on the second slash.
	if r2.StakeDust != r1.StakeDust-100_000_000 {
		t.Errorf("second slash stake delta wrong: got %d, want %d",
			r2.StakeDust, r1.StakeDust-100_000_000)
	}
}
