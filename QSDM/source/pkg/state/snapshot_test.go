package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func tmpDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(os.TempDir(), "qsdm_snap_test_"+time.Now().Format("150405.000"))
	os.MkdirAll(dir, 0755)
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func dummyState() map[string]interface{} {
	return map[string]interface{}{
		"balance:alice": 100.0,
		"balance:bob":   50.0,
		"block_height":  42,
	}
}

func TestSnapshotManager_TakeAndLoad(t *testing.T) {
	dir := tmpDir(t)
	sm := NewSnapshotManager(ManagerConfig{Dir: dir, MaxSnapshots: 10}, dummyState)

	meta, err := sm.TakeSnapshot()
	if err != nil {
		t.Fatalf("TakeSnapshot: %v", err)
	}
	if meta.Height != 1 {
		t.Fatalf("expected height 1, got %d", meta.Height)
	}
	if meta.Hash == "" {
		t.Fatal("expected non-empty hash")
	}

	snap, err := sm.LoadSnapshot(1)
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	if snap.Height != 1 {
		t.Fatalf("loaded snap height: expected 1, got %d", snap.Height)
	}
	if snap.Data["balance:alice"] != 100.0 {
		t.Fatalf("unexpected balance: %v", snap.Data["balance:alice"])
	}
}

func TestSnapshotManager_Pruning(t *testing.T) {
	dir := tmpDir(t)
	sm := NewSnapshotManager(ManagerConfig{Dir: dir, MaxSnapshots: 3}, dummyState)

	for i := 0; i < 5; i++ {
		sm.TakeSnapshot()
	}

	list := sm.ListSnapshots()
	if len(list) != 3 {
		t.Fatalf("expected 3 retained, got %d", len(list))
	}
	// Oldest retained should be height 3
	if list[0].Height != 3 {
		t.Fatalf("expected oldest height 3, got %d", list[0].Height)
	}

	// Pruned files should not exist on disk
	_, err := os.Stat(filepath.Join(dir, "snap_000001_*.json"))
	if err == nil {
		t.Fatal("snap 1 file should be pruned from disk")
	}
}

func TestSnapshotManager_LatestSnapshot(t *testing.T) {
	dir := tmpDir(t)
	sm := NewSnapshotManager(ManagerConfig{Dir: dir, MaxSnapshots: 5}, dummyState)

	sm.TakeSnapshot()
	sm.TakeSnapshot()
	sm.TakeSnapshot()

	snap, err := sm.LatestSnapshot()
	if err != nil {
		t.Fatalf("LatestSnapshot: %v", err)
	}
	if snap.Height != 3 {
		t.Fatalf("expected height 3, got %d", snap.Height)
	}
}

func TestSnapshotManager_NoSnapshots(t *testing.T) {
	dir := tmpDir(t)
	sm := NewSnapshotManager(ManagerConfig{Dir: dir, MaxSnapshots: 5}, dummyState)

	_, err := sm.LatestSnapshot()
	if err == nil {
		t.Fatal("expected error when no snapshots")
	}
	_, err = sm.LoadSnapshot(999)
	if err == nil {
		t.Fatal("expected error for nonexistent height")
	}
}

func TestSnapshotManager_PersistIndex(t *testing.T) {
	dir := tmpDir(t)
	sm1 := NewSnapshotManager(ManagerConfig{Dir: dir, MaxSnapshots: 5}, dummyState)
	sm1.TakeSnapshot()
	sm1.TakeSnapshot()

	// Create new manager pointing at same dir — should restore index
	sm2 := NewSnapshotManager(ManagerConfig{Dir: dir, MaxSnapshots: 5}, dummyState)
	list := sm2.ListSnapshots()
	if len(list) != 2 {
		t.Fatalf("expected 2 snapshots from index, got %d", len(list))
	}

	// New snapshot should continue from height 3
	meta, _ := sm2.TakeSnapshot()
	if meta.Height != 3 {
		t.Fatalf("expected height 3, got %d", meta.Height)
	}
}

func TestSnapshotManager_StartStop(t *testing.T) {
	dir := tmpDir(t)
	sm := NewSnapshotManager(ManagerConfig{Dir: dir, MaxSnapshots: 10}, dummyState)

	sm.Start(15 * time.Millisecond)
	time.Sleep(50 * time.Millisecond)
	sm.Stop()

	list := sm.ListSnapshots()
	if len(list) < 2 {
		t.Fatalf("expected at least 2 auto-snapshots, got %d", len(list))
	}
}

func TestSnapshotManager_HashDeterminism(t *testing.T) {
	dir := tmpDir(t)
	fixed := map[string]interface{}{"key": "value"}
	sm := NewSnapshotManager(ManagerConfig{Dir: dir, MaxSnapshots: 5}, func() map[string]interface{} { return fixed })

	m1, _ := sm.TakeSnapshot()
	m2, _ := sm.TakeSnapshot()

	if m1.Hash != m2.Hash {
		t.Fatalf("same state should produce same hash: %s != %s", m1.Hash, m2.Hash)
	}
}
