package enrollment

import (
	"path/filepath"
	"testing"
)

func mkRecord(node, owner, gpu string, stake, h uint64) EnrollmentRecord {
	return EnrollmentRecord{
		NodeID:           node,
		Owner:            owner,
		GPUUUID:          gpu,
		HMACKey:          []byte("0123456789abcdef0123456789abcdef"),
		StakeDust:        stake,
		EnrolledAtHeight: h,
	}
}

func TestInMemoryState_SaveLoad_RoundTripsRecords(t *testing.T) {
	s := NewInMemoryState()
	if err := s.ApplyEnroll(mkRecord("rig-a", "qsdm1alice", "GPU-A", 100, 5)); err != nil {
		t.Fatalf("ApplyEnroll a: %v", err)
	}
	if err := s.ApplyEnroll(mkRecord("rig-b", "qsdm1bob", "GPU-B", 200, 7)); err != nil {
		t.Fatalf("ApplyEnroll b: %v", err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "enrollment.json")
	if err := s.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	restored := NewInMemoryState()
	loaded, err := restored.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded != 2 {
		t.Fatalf("loaded count: got %d want 2", loaded)
	}

	for _, nodeID := range []string{"rig-a", "rig-b"} {
		rec, err := restored.Lookup(nodeID)
		if err != nil {
			t.Fatalf("Lookup %s: %v", nodeID, err)
		}
		if rec == nil {
			t.Fatalf("Lookup %s: nil record", nodeID)
		}
		if !rec.Active() {
			t.Fatalf("Lookup %s: expected Active() after restore", nodeID)
		}
	}

	// byGPUActive is rebuilt: GPU-A → rig-a, GPU-B → rig-b.
	if got, err := restored.GPUUUIDBound("GPU-A"); err != nil || got != "rig-a" {
		t.Fatalf("GPUUUIDBound(GPU-A): got %q err %v", got, err)
	}
	if got, err := restored.GPUUUIDBound("GPU-B"); err != nil || got != "rig-b" {
		t.Fatalf("GPUUUIDBound(GPU-B): got %q err %v", got, err)
	}
}

func TestInMemoryState_Load_RejectsNonEmptyState(t *testing.T) {
	s := NewInMemoryState()
	if err := s.ApplyEnroll(mkRecord("rig-a", "qsdm1alice", "GPU-A", 100, 5)); err != nil {
		t.Fatalf("ApplyEnroll: %v", err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "x.json")
	if err := s.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// Loading into the same (now non-empty) state must fail.
	if _, err := s.Load(path); err == nil {
		t.Fatal("Load on non-empty state should fail")
	}
}

func TestInMemoryState_Load_MissingFileIsZero(t *testing.T) {
	s := NewInMemoryState()
	loaded, err := s.Load(filepath.Join(t.TempDir(), "no-such.json"))
	if err != nil {
		t.Fatalf("missing file should be no-error: %v", err)
	}
	if loaded != 0 {
		t.Fatalf("missing file should yield 0 records, got %d", loaded)
	}
}

func TestInMemoryState_Save_EmptyPathIsNoop(t *testing.T) {
	s := NewInMemoryState()
	if err := s.Save(""); err != nil {
		t.Fatalf("empty path should be no-op, got %v", err)
	}
}

func TestInMemoryState_RoundTrip_PreservesRevokedRecord(t *testing.T) {
	s := NewInMemoryState()
	if err := s.ApplyEnroll(mkRecord("rig-z", "qsdm1z", "GPU-Z", 100, 5)); err != nil {
		t.Fatalf("ApplyEnroll: %v", err)
	}
	if err := s.ApplyUnenroll("rig-z", 12); err != nil {
		t.Fatalf("ApplyUnenroll: %v", err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "ru.json")
	if err := s.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	restored := NewInMemoryState()
	if _, err := restored.Load(path); err != nil {
		t.Fatalf("Load: %v", err)
	}
	rec, err := restored.Lookup("rig-z")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if rec.Active() {
		t.Fatal("revoked record should NOT be Active() after restore")
	}
	if rec.RevokedAtHeight != 12 {
		t.Errorf("RevokedAtHeight: got %d want 12", rec.RevokedAtHeight)
	}
	// Revoked records do NOT contribute to the active-GPU index.
	// GPUUUIDBound returns ("", nil) for missing GPUs, so we
	// assert on the bound NodeID being empty rather than on an
	// error (which is reserved for I/O / lock failures).
	if got, err := restored.GPUUUIDBound("GPU-Z"); err != nil || got != "" {
		t.Fatalf("revoked GPU bound after restore: got %q err %v", got, err)
	}
}
