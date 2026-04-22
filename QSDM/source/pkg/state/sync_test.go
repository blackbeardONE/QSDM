package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func syncTmpDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(os.TempDir(), "qsdm_sync_test_"+time.Now().Format("150405.000"))
	os.MkdirAll(dir, 0755)
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func TestSyncManager_FullSyncCycle(t *testing.T) {
	// Serving node: has a snapshot
	serveDir := syncTmpDir(t)
	serveSnap := NewSnapshotManager(ManagerConfig{Dir: serveDir, MaxSnapshots: 5}, func() map[string]interface{} {
		return map[string]interface{}{"balance:alice": 500.0, "height": 10}
	})
	serveSnap.TakeSnapshot()

	serveSM := NewSyncManager(serveSnap, "node-serve", nil)

	// Joining node: empty, wants to sync
	joinDir := syncTmpDir(t)
	var appliedState map[string]interface{}
	joinSnap := NewSnapshotManager(ManagerConfig{Dir: joinDir, MaxSnapshots: 5}, func() map[string]interface{} {
		return nil
	})
	joinSM := NewSyncManager(joinSnap, "node-join", func(data map[string]interface{}) error {
		appliedState = data
		return nil
	})

	// Step 1: joining node creates request
	req := joinSM.CreateSyncRequest(0)
	if joinSM.Status() != SyncRequesting {
		t.Fatalf("expected requesting, got %s", joinSM.Status())
	}

	// Step 2: serving node handles request
	resp, err := serveSM.HandleSyncRequest(req)
	if err != nil {
		t.Fatalf("HandleSyncRequest: %v", err)
	}
	if resp.Height != 1 {
		t.Fatalf("expected height 1, got %d", resp.Height)
	}

	// Step 3: joining node applies response
	if err := joinSM.ApplySync(*resp); err != nil {
		t.Fatalf("ApplySync: %v", err)
	}
	if joinSM.Status() != SyncComplete {
		t.Fatalf("expected complete, got %s", joinSM.Status())
	}
	if joinSM.Progress() != 1.0 {
		t.Fatalf("expected progress 1.0, got %f", joinSM.Progress())
	}
	if appliedState["balance:alice"] != 500.0 {
		t.Fatalf("expected balance 500, got %v", appliedState["balance:alice"])
	}
}

func TestSyncManager_NoSnapshot(t *testing.T) {
	dir := syncTmpDir(t)
	sm := NewSnapshotManager(ManagerConfig{Dir: dir, MaxSnapshots: 5}, func() map[string]interface{} {
		return nil
	})
	syncMgr := NewSyncManager(sm, "node-1", nil)

	req := SyncRequest{RequestID: "r1", FromHeight: 0, RequesterID: "node-2"}
	_, err := syncMgr.HandleSyncRequest(req)
	if err == nil {
		t.Fatal("expected error when no snapshot available")
	}
}

func TestSyncManager_AlreadySynced(t *testing.T) {
	dir := syncTmpDir(t)
	sm := NewSnapshotManager(ManagerConfig{Dir: dir, MaxSnapshots: 5}, func() map[string]interface{} {
		return map[string]interface{}{"x": 1}
	})
	sm.TakeSnapshot()
	syncMgr := NewSyncManager(sm, "node-1", nil)

	req := SyncRequest{RequestID: "r1", FromHeight: 999, RequesterID: "node-2"}
	_, err := syncMgr.HandleSyncRequest(req)
	if err == nil {
		t.Fatal("expected error when requester is already ahead")
	}
}

func TestSyncManager_HashMismatch(t *testing.T) {
	dir := syncTmpDir(t)
	sm := NewSnapshotManager(ManagerConfig{Dir: dir, MaxSnapshots: 5}, func() map[string]interface{} {
		return nil
	})
	syncMgr := NewSyncManager(sm, "node-1", nil)

	resp := SyncResponse{
		RequestID: "r1",
		Height:    1,
		Hash:      "badhash",
		Data:      map[string]interface{}{"x": 1},
	}
	err := syncMgr.ApplySync(resp)
	if err == nil {
		t.Fatal("expected hash mismatch error")
	}
	if syncMgr.Status() != SyncFailed {
		t.Fatalf("expected failed status, got %s", syncMgr.Status())
	}
}

func TestSyncManager_Reset(t *testing.T) {
	dir := syncTmpDir(t)
	sm := NewSnapshotManager(ManagerConfig{Dir: dir, MaxSnapshots: 5}, func() map[string]interface{} {
		return nil
	})
	syncMgr := NewSyncManager(sm, "node-1", nil)

	syncMgr.CreateSyncRequest(0)
	if syncMgr.Status() != SyncRequesting {
		t.Fatal("expected requesting")
	}

	syncMgr.Reset()
	if syncMgr.Status() != SyncIdle {
		t.Fatal("expected idle after reset")
	}
}

func TestSyncManager_Info(t *testing.T) {
	dir := syncTmpDir(t)
	sm := NewSnapshotManager(ManagerConfig{Dir: dir, MaxSnapshots: 5}, func() map[string]interface{} {
		return nil
	})
	syncMgr := NewSyncManager(sm, "node-42", nil)

	info := syncMgr.Info()
	if info["peer_id"] != "node-42" {
		t.Fatalf("expected peer_id node-42, got %v", info["peer_id"])
	}
	if info["status"] != "idle" {
		t.Fatalf("expected idle, got %v", info["status"])
	}
}
