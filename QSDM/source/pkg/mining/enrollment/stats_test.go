package enrollment

import "testing"

// stats_test.go: covers InMemoryState.Stats(). The metric
// gauges in pkg/monitoring/enrollment_metrics.go read from
// this snapshot every scrape, so the four counts must stay
// faithful to the actual record set across enroll, unenroll,
// sweep, and slash transitions.

func TestInMemoryState_Stats_Empty(t *testing.T) {
	s := NewInMemoryState()
	got := s.Stats()
	if got != (Stats{}) {
		t.Fatalf("empty state: want zero Stats, got %+v", got)
	}
}

func TestInMemoryState_Stats_OnlyActiveRecords(t *testing.T) {
	s := NewInMemoryState()
	seededRecord(t, s, "rig-1", 100)
	seededRecord(t, s, "rig-2", 250)

	got := s.Stats()
	if got.ActiveCount != 2 {
		t.Errorf("ActiveCount: got %d, want 2", got.ActiveCount)
	}
	if got.BondedDust != 350 {
		t.Errorf("BondedDust: got %d, want 350", got.BondedDust)
	}
	if got.PendingUnbondCount != 0 || got.PendingUnbondDust != 0 {
		t.Errorf("pending non-zero: %+v", got)
	}
}

func TestInMemoryState_Stats_PartitionsActiveAndPending(t *testing.T) {
	s := NewInMemoryState()
	seededRecord(t, s, "rig-active", 100)
	seededRecord(t, s, "rig-unbonding", 200)

	if err := s.ApplyUnenroll("rig-unbonding", 10); err != nil {
		t.Fatalf("unenroll: %v", err)
	}

	got := s.Stats()
	if got.ActiveCount != 1 {
		t.Errorf("ActiveCount: got %d, want 1", got.ActiveCount)
	}
	if got.BondedDust != 100 {
		t.Errorf("BondedDust: got %d, want 100", got.BondedDust)
	}
	if got.PendingUnbondCount != 1 {
		t.Errorf("PendingUnbondCount: got %d, want 1", got.PendingUnbondCount)
	}
	if got.PendingUnbondDust != 200 {
		t.Errorf("PendingUnbondDust: got %d, want 200", got.PendingUnbondDust)
	}
}

func TestInMemoryState_Stats_DrainedRevokedRecordCountedZeroDust(t *testing.T) {
	// A fully-slashed record stays in the unbond window but
	// has zero pending dust. Stats must still count it under
	// PendingUnbondCount so operators see the backlog of
	// records waiting on a sweep.
	s := NewInMemoryState()
	seededRecord(t, s, "rig-drained", 500)

	revoked, remaining, err := s.RevokeIfUnderBonded("rig-drained", 10, 1000)
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if !revoked {
		t.Fatalf("expected RevokeIfUnderBonded to revoke under-bonded record")
	}
	// Drain the record's dust to simulate post-slash state.
	if _, err := s.SlashStake("rig-drained", 500); err != nil {
		t.Fatalf("slash: %v", err)
	}

	got := s.Stats()
	if got.ActiveCount != 0 {
		t.Errorf("ActiveCount: got %d, want 0", got.ActiveCount)
	}
	if got.BondedDust != 0 {
		t.Errorf("BondedDust: got %d, want 0", got.BondedDust)
	}
	if got.PendingUnbondCount != 1 {
		t.Errorf("PendingUnbondCount: got %d, want 1 (drained but still un-swept)", got.PendingUnbondCount)
	}
	if got.PendingUnbondDust != 0 {
		t.Errorf("PendingUnbondDust: got %d, want 0", got.PendingUnbondDust)
	}
	_ = remaining
}

func TestInMemoryState_Stats_AfterSweep_RecordRemoved(t *testing.T) {
	s := NewInMemoryState()
	seededRecord(t, s, "rig-1", 300)
	if err := s.ApplyUnenroll("rig-1", 5); err != nil {
		t.Fatalf("unenroll: %v", err)
	}
	// Sweep at height past UnbondWindowBlocks. Walk the record
	// out of pending state.
	rec, _ := s.Lookup("rig-1")
	releases := s.SweepMaturedUnbonds(rec.UnbondMaturesAtHeight)
	if len(releases) != 1 {
		t.Fatalf("sweep: got %d releases, want 1", len(releases))
	}

	got := s.Stats()
	if got != (Stats{}) {
		t.Fatalf("post-sweep should be zero Stats, got %+v", got)
	}
}
