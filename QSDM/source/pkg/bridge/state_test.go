package bridge

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSaveAndLoadState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bridge_state.json")

	bp := &BridgeProtocol{locks: make(map[string]*Lock)}
	asp := &AtomicSwapProtocol{swaps: make(map[string]*Swap)}

	bp.locks["lock1"] = &Lock{
		ID: "lock1", SourceChain: "chain-a", TargetChain: "chain-b",
		Asset: "TOKEN", Amount: 42.5, Recipient: "addr1",
		LockedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
		SecretHash: "hash1", Secret: "secret1", Status: LockStatusLocked,
	}
	asp.swaps["swap1"] = &Swap{
		ID: "swap1", InitiatorChain: "chain-x", ParticipantChain: "chain-y",
		InitiatorAmount: 10, ParticipantAmount: 20, Status: SwapStatusInitiated,
		CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
	}

	if err := SaveState(path, bp, asp); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("state file not created: %v", err)
	}

	bp2 := &BridgeProtocol{locks: make(map[string]*Lock)}
	asp2 := &AtomicSwapProtocol{swaps: make(map[string]*Swap)}

	lc, sc, err := LoadState(path, bp2, asp2)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if lc != 1 {
		t.Errorf("expected 1 lock, got %d", lc)
	}
	if sc != 1 {
		t.Errorf("expected 1 swap, got %d", sc)
	}

	lock := bp2.locks["lock1"]
	if lock == nil {
		t.Fatal("lock1 not found after load")
	}
	if lock.Amount != 42.5 {
		t.Errorf("lock amount = %f, want 42.5", lock.Amount)
	}
	if lock.Status != LockStatusLocked {
		t.Errorf("lock status = %s, want locked", lock.Status)
	}
	if lock.Secret != "secret1" {
		t.Errorf("lock secret not preserved")
	}

	swap := asp2.swaps["swap1"]
	if swap == nil {
		t.Fatal("swap1 not found after load")
	}
	if swap.InitiatorAmount != 10 {
		t.Errorf("swap initiator amount = %f, want 10", swap.InitiatorAmount)
	}
}

func TestLoadState_MissingFile(t *testing.T) {
	bp := &BridgeProtocol{locks: make(map[string]*Lock)}
	lc, sc, err := LoadState("/nonexistent/path/state.json", bp, nil)
	if err != nil {
		t.Errorf("expected nil error for missing file, got %v", err)
	}
	if lc != 0 || sc != 0 {
		t.Errorf("expected 0,0 counts for missing file, got %d,%d", lc, sc)
	}
}

func TestAutoSaver(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auto_state.json")

	bp := &BridgeProtocol{locks: make(map[string]*Lock)}
	bp.locks["l1"] = &Lock{ID: "l1", Status: LockStatusLocked, Amount: 1}

	saver := NewAutoSaver(path, bp, nil, 50*time.Millisecond)
	time.Sleep(150 * time.Millisecond)
	saver.Stop()

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("auto-saver did not create state file: %v", err)
	}

	bp2 := &BridgeProtocol{locks: make(map[string]*Lock)}
	lc, _, err := LoadState(path, bp2, nil)
	if err != nil {
		t.Fatalf("LoadState after auto-save: %v", err)
	}
	if lc != 1 {
		t.Errorf("expected 1 lock after auto-save reload, got %d", lc)
	}
}
