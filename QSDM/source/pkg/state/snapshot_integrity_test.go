package state

// Audit row store-02: snapshot hash verification on load. Pins the
// contract that SnapshotManager.LoadSnapshot rejects a snapshot
// whose embedded Hash field does not match the SHA-256 of its Data
// field (tampered file, bit-rot, partial write, etc.).

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSnapshotIntegrity_HappyPath confirms that an unmodified
// snapshot still loads cleanly. Guards against an over-strict hash
// check that would break the legitimate path.
func TestSnapshotIntegrity_HappyPath(t *testing.T) {
	dir := tmpDir(t)
	sm := NewSnapshotManager(ManagerConfig{Dir: dir, MaxSnapshots: 10}, dummyState)
	meta, err := sm.TakeSnapshot()
	if err != nil {
		t.Fatalf("TakeSnapshot: %v", err)
	}
	snap, err := sm.LoadSnapshot(meta.Height)
	if err != nil {
		t.Fatalf("LoadSnapshot on untampered snapshot must succeed: %v", err)
	}
	if snap.Data["balance:alice"] != 100.0 {
		t.Fatalf("data round-trip failed: %v", snap.Data)
	}
}

// TestSnapshotIntegrity_TamperedDataRejected confirms that flipping
// a byte in the Data field is detected (the recomputed hash diverges
// from the stored Hash).
func TestSnapshotIntegrity_TamperedDataRejected(t *testing.T) {
	dir := tmpDir(t)
	sm := NewSnapshotManager(ManagerConfig{Dir: dir, MaxSnapshots: 10}, dummyState)
	meta, err := sm.TakeSnapshot()
	if err != nil {
		t.Fatalf("TakeSnapshot: %v", err)
	}

	// Tamper: read the snapshot, mutate one value in Data, write back
	// (leaving the Hash field intact — the attacker doesn't recompute
	// the hash because they don't WANT us to detect the change).
	path := filepath.Join(dir, meta.File)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	var snap Snapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	snap.Data["balance:alice"] = 999999.0 // <- attacker bumps alice's balance

	tampered, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		t.Fatalf("re-marshal: %v", err)
	}
	if err := os.WriteFile(path, tampered, 0644); err != nil {
		t.Fatalf("write tampered: %v", err)
	}

	// LoadSnapshot must REJECT and return ErrSnapshotIntegrity.
	_, err = sm.LoadSnapshot(meta.Height)
	if err == nil {
		t.Fatal("LoadSnapshot on tampered snapshot must FAIL, got nil error")
	}
	if !errors.Is(err, ErrSnapshotIntegrity) {
		t.Fatalf("LoadSnapshot on tampered snapshot must return ErrSnapshotIntegrity, got: %v", err)
	}
	if !strings.Contains(err.Error(), "hash mismatch") {
		t.Fatalf("error message should mention hash mismatch; got: %v", err)
	}
}

// TestSnapshotIntegrity_TamperedHashRejected confirms that flipping
// the Hash field (and leaving Data alone) is also detected.
// Catches an attacker who knows they only need to lie about the hash
// to bypass a naive "do the hashes match" check that uses the stored
// hash on both sides.
func TestSnapshotIntegrity_TamperedHashRejected(t *testing.T) {
	dir := tmpDir(t)
	sm := NewSnapshotManager(ManagerConfig{Dir: dir, MaxSnapshots: 10}, dummyState)
	meta, err := sm.TakeSnapshot()
	if err != nil {
		t.Fatalf("TakeSnapshot: %v", err)
	}

	path := filepath.Join(dir, meta.File)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	var snap Snapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// Replace the Hash with a different valid-format SHA-256 hex
	// string. The Data field is untouched.
	snap.Hash = "0000000000000000000000000000000000000000000000000000000000000000"

	tampered, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		t.Fatalf("re-marshal: %v", err)
	}
	if err := os.WriteFile(path, tampered, 0644); err != nil {
		t.Fatalf("write tampered: %v", err)
	}

	_, err = sm.LoadSnapshot(meta.Height)
	if err == nil {
		t.Fatal("LoadSnapshot on hash-tampered snapshot must FAIL, got nil error")
	}
	if !errors.Is(err, ErrSnapshotIntegrity) {
		t.Fatalf("LoadSnapshot on hash-tampered snapshot must return ErrSnapshotIntegrity, got: %v", err)
	}
}

// TestSnapshotIntegrity_EmptyHashRejected confirms that a snapshot
// file produced WITHOUT going through TakeSnapshot (i.e. one whose
// Hash field is empty) is rejected. Closes the gap where an attacker
// could strip the Hash field instead of forging it.
func TestSnapshotIntegrity_EmptyHashRejected(t *testing.T) {
	dir := tmpDir(t)
	sm := NewSnapshotManager(ManagerConfig{Dir: dir, MaxSnapshots: 10}, dummyState)
	meta, err := sm.TakeSnapshot()
	if err != nil {
		t.Fatalf("TakeSnapshot: %v", err)
	}

	path := filepath.Join(dir, meta.File)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	var snap Snapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	snap.Hash = ""

	stripped, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		t.Fatalf("re-marshal: %v", err)
	}
	if err := os.WriteFile(path, stripped, 0644); err != nil {
		t.Fatalf("write stripped: %v", err)
	}

	_, err = sm.LoadSnapshot(meta.Height)
	if err == nil {
		t.Fatal("LoadSnapshot on hash-stripped snapshot must FAIL, got nil error")
	}
	if !errors.Is(err, ErrSnapshotIntegrity) {
		t.Fatalf("LoadSnapshot on hash-stripped snapshot must return ErrSnapshotIntegrity, got: %v", err)
	}
}
