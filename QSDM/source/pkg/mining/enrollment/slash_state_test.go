package enrollment

import "testing"

func seededRecord(t *testing.T, s *InMemoryState, nodeID string, stake uint64) {
	t.Helper()
	rec := EnrollmentRecord{
		NodeID:           nodeID,
		Owner:            "owner-" + nodeID,
		GPUUUID:          "GPU-" + nodeID,
		HMACKey:          []byte("0123456789012345678901234567890123"),
		StakeDust:        stake,
		EnrolledAtHeight: 1,
	}
	if err := s.ApplyEnroll(rec); err != nil {
		t.Fatalf("seed %q: %v", nodeID, err)
	}
}

func TestInMemoryState_SlashStake_PartialForfeit(t *testing.T) {
	s := NewInMemoryState()
	seededRecord(t, s, "rig-1", 1000)

	got, err := s.SlashStake("rig-1", 400)
	if err != nil {
		t.Fatalf("slash: %v", err)
	}
	if got != 400 {
		t.Errorf("slashed: got %d, want 400", got)
	}
	rec, _ := s.Lookup("rig-1")
	if rec.StakeDust != 600 {
		t.Errorf("remaining stake: got %d, want 600", rec.StakeDust)
	}
}

func TestInMemoryState_SlashStake_ClampsAtRemaining(t *testing.T) {
	s := NewInMemoryState()
	seededRecord(t, s, "rig-1", 100)

	got, err := s.SlashStake("rig-1", 500)
	if err != nil {
		t.Fatalf("slash: %v", err)
	}
	if got != 100 {
		t.Errorf("clamped slash: got %d, want 100", got)
	}
	rec, _ := s.Lookup("rig-1")
	if rec.StakeDust != 0 {
		t.Errorf("remaining stake: got %d, want 0", rec.StakeDust)
	}

	// Second slash on a drained record returns 0 with no error.
	got, err = s.SlashStake("rig-1", 50)
	if err != nil || got != 0 {
		t.Errorf("double-drain: got %d err=%v, want 0 nil", got, err)
	}
}

func TestInMemoryState_SlashStake_UnknownNodeErrors(t *testing.T) {
	s := NewInMemoryState()
	_, err := s.SlashStake("nope", 1)
	if err == nil {
		t.Error("unknown node_id should error")
	}
}

func TestInMemoryState_MarkEvidenceSeen_DedupSemantics(t *testing.T) {
	s := NewInMemoryState()
	var h [32]byte
	for i := range h {
		h[i] = byte(i)
	}
	if !s.MarkEvidenceSeen(h) {
		t.Error("first mark should return true")
	}
	if s.MarkEvidenceSeen(h) {
		t.Error("repeat mark should return false")
	}
	if !s.EvidenceSeen(h) {
		t.Error("seen check: got false, want true")
	}

	// A different hash must still be marked fresh.
	var h2 [32]byte
	h2[0] = 0xFF
	if !s.MarkEvidenceSeen(h2) {
		t.Error("distinct hash should mark fresh")
	}
}

func TestInMemoryState_Clone_PreservesSlashFields(t *testing.T) {
	s := NewInMemoryState()
	seededRecord(t, s, "rig-1", 500)
	var h [32]byte
	h[7] = 0xAA
	s.MarkEvidenceSeen(h)

	cloneCS := s.Clone()
	clone, ok := cloneCS.(*InMemoryState)
	if !ok {
		t.Fatalf("clone wrong type: %T", cloneCS)
	}
	if !clone.EvidenceSeen(h) {
		t.Error("clone did not preserve seenEvidence")
	}

	// Mutate the clone; live state must not shift.
	if _, err := clone.SlashStake("rig-1", 200); err != nil {
		t.Fatalf("clone slash: %v", err)
	}
	liveRec, _ := s.Lookup("rig-1")
	if liveRec.StakeDust != 500 {
		t.Errorf("live state drifted after clone slash: got %d, want 500",
			liveRec.StakeDust)
	}
	cloneRec, _ := clone.Lookup("rig-1")
	if cloneRec.StakeDust != 300 {
		t.Errorf("clone state not mutated: got %d, want 300", cloneRec.StakeDust)
	}

	// Mark a new hash on the clone; live must NOT see it.
	var h2 [32]byte
	h2[0] = 0xBB
	clone.MarkEvidenceSeen(h2)
	if s.EvidenceSeen(h2) {
		t.Error("live state saw clone-only evidence hash")
	}
}

func TestInMemoryState_Restore_ReplacesSlashFields(t *testing.T) {
	live := NewInMemoryState()
	seededRecord(t, live, "rig-1", 500)

	snap := live.Clone()
	// Drift the live state.
	live.SlashStake("rig-1", 300)
	var hLive [32]byte
	hLive[0] = 0xCC
	live.MarkEvidenceSeen(hLive)

	if err := live.Restore(snap); err != nil {
		t.Fatalf("restore: %v", err)
	}

	rec, _ := live.Lookup("rig-1")
	if rec.StakeDust != 500 {
		t.Errorf("restored stake: got %d, want 500", rec.StakeDust)
	}
	if live.EvidenceSeen(hLive) {
		t.Error("restore did not clear post-snapshot seenEvidence entry")
	}
}
