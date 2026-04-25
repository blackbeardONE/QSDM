package enrollment

import "testing"

// These tests cover InMemoryState.RevokeIfUnderBonded — the
// post-slash auto-revoke transition that closes the
// "slash-to-zero, keep mining for free" loophole. The unit
// here is intentionally narrow: only the state mutation is
// asserted; the SlashApplier-side wiring is exercised in
// pkg/chain/slash_apply_test.go and the e2e tests under
// pkg/chain.

func TestInMemoryState_RevokeIfUnderBonded_AboveThreshold_NoOp(t *testing.T) {
	s := NewInMemoryState()
	seededRecord(t, s, "rig-1", 1000)

	revoked, remaining, err := s.RevokeIfUnderBonded("rig-1", 250, 500)
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if revoked {
		t.Error("record above threshold should not be revoked")
	}
	if remaining != 1000 {
		t.Errorf("remaining: got %d, want 1000", remaining)
	}
	rec, _ := s.Lookup("rig-1")
	if !rec.Active() {
		t.Error("record should still be Active() after no-op")
	}
	if rec.RevokedAtHeight != 0 || rec.UnbondMaturesAtHeight != 0 {
		t.Errorf("revoke fields touched on no-op: revoked=%d mature=%d",
			rec.RevokedAtHeight, rec.UnbondMaturesAtHeight)
	}
	// gpu_uuid binding still claimed.
	if owner, _ := s.GPUUUIDBound("GPU-rig-1"); owner != "rig-1" {
		t.Errorf("gpu binding lost on no-op: got %q, want %q", owner, "rig-1")
	}
}

func TestInMemoryState_RevokeIfUnderBonded_AtThreshold_NoOp(t *testing.T) {
	// Boundary: stake == minStakeDust must NOT trigger revoke.
	// The threshold is "strictly less than" by spec.
	s := NewInMemoryState()
	seededRecord(t, s, "rig-1", 500)

	revoked, remaining, err := s.RevokeIfUnderBonded("rig-1", 250, 500)
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if revoked {
		t.Error("at-threshold record should not be revoked")
	}
	if remaining != 500 {
		t.Errorf("remaining: got %d, want 500", remaining)
	}
}

func TestInMemoryState_RevokeIfUnderBonded_BelowThreshold_RevokesAndReleasesGPU(t *testing.T) {
	s := NewInMemoryState()
	seededRecord(t, s, "rig-1", 100)

	revoked, remaining, err := s.RevokeIfUnderBonded("rig-1", 250, 500)
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if !revoked {
		t.Fatal("under-threshold record should have been revoked")
	}
	if remaining != 100 {
		t.Errorf("remaining: got %d, want 100 (stake stays locked until unbond matures)",
			remaining)
	}
	rec, _ := s.Lookup("rig-1")
	if rec.Active() {
		t.Error("record should not be Active() after auto-revoke")
	}
	if rec.RevokedAtHeight != 250 {
		t.Errorf("RevokedAtHeight: got %d, want 250", rec.RevokedAtHeight)
	}
	if rec.UnbondMaturesAtHeight != 250+UnbondWindow {
		t.Errorf("UnbondMaturesAtHeight: got %d, want %d",
			rec.UnbondMaturesAtHeight, 250+UnbondWindow)
	}
	// gpu_uuid binding released so a new node can re-enroll the
	// physical card without waiting for the unbond window.
	if owner, _ := s.GPUUUIDBound("GPU-rig-1"); owner != "" {
		t.Errorf("gpu binding should be released, got owner %q", owner)
	}
}

func TestInMemoryState_RevokeIfUnderBonded_FullyDrained_StillRevokes(t *testing.T) {
	s := NewInMemoryState()
	seededRecord(t, s, "rig-1", 1000)

	if _, err := s.SlashStake("rig-1", 1000); err != nil {
		t.Fatalf("setup slash: %v", err)
	}
	revoked, remaining, err := s.RevokeIfUnderBonded("rig-1", 250, 500)
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if !revoked {
		t.Error("zero-stake record should auto-revoke (0 < threshold)")
	}
	if remaining != 0 {
		t.Errorf("remaining: got %d, want 0", remaining)
	}
}

func TestInMemoryState_RevokeIfUnderBonded_AlreadyRevoked_Idempotent(t *testing.T) {
	s := NewInMemoryState()
	seededRecord(t, s, "rig-1", 100)

	first, _, err := s.RevokeIfUnderBonded("rig-1", 250, 500)
	if err != nil || !first {
		t.Fatalf("first revoke unexpected: revoked=%v err=%v", first, err)
	}
	rec1, _ := s.Lookup("rig-1")
	matureBefore := rec1.UnbondMaturesAtHeight

	// Second call (e.g. another concurrent slash dispatch) must
	// not bump the unbond window forward — that would let an
	// attacker keep extending the lock by spamming evidence.
	second, _, err := s.RevokeIfUnderBonded("rig-1", 999, 500)
	if err != nil {
		t.Fatalf("second revoke: %v", err)
	}
	if second {
		t.Error("already-revoked record should report revoked=false on repeat")
	}
	rec2, _ := s.Lookup("rig-1")
	if rec2.UnbondMaturesAtHeight != matureBefore {
		t.Errorf("second revoke moved unbond window: got %d, want %d",
			rec2.UnbondMaturesAtHeight, matureBefore)
	}
	if rec2.RevokedAtHeight != 250 {
		t.Errorf("second revoke moved RevokedAtHeight: got %d, want 250",
			rec2.RevokedAtHeight)
	}
}

func TestInMemoryState_RevokeIfUnderBonded_ZeroThreshold_DisablesAutoRevoke(t *testing.T) {
	s := NewInMemoryState()
	seededRecord(t, s, "rig-1", 0)

	revoked, _, err := s.RevokeIfUnderBonded("rig-1", 250, 0)
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if revoked {
		t.Error("zero threshold should disable auto-revoke even on zero-stake records")
	}
}

func TestInMemoryState_RevokeIfUnderBonded_UnknownNode_Errors(t *testing.T) {
	s := NewInMemoryState()
	_, _, err := s.RevokeIfUnderBonded("nope", 1, 500)
	if err == nil {
		t.Error("unknown node_id should error")
	}
}

func TestInMemoryState_RevokeIfUnderBonded_AllowsGPURebind(t *testing.T) {
	// After auto-revoke, the released gpu_uuid binding should
	// allow a fresh node_id to enroll the same physical card.
	s := NewInMemoryState()
	seededRecord(t, s, "rig-1", 100)

	if _, _, err := s.RevokeIfUnderBonded("rig-1", 250, 500); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	// New record claiming the same gpu_uuid under a different
	// node_id should now succeed.
	rec := EnrollmentRecord{
		NodeID:           "rig-1-replacement",
		Owner:            "owner-replacement",
		GPUUUID:          "GPU-rig-1",
		HMACKey:          []byte("0123456789012345678901234567890123"),
		StakeDust:        1000,
		EnrolledAtHeight: 251,
	}
	if err := s.ApplyEnroll(rec); err != nil {
		t.Fatalf("re-enroll same gpu_uuid: %v", err)
	}
}
