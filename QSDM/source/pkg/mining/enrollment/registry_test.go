package enrollment

import (
	"bytes"
	"errors"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mining/attest/hmac"
)

// ----- StateBackedRegistry --------------------------------------

func TestStateBackedRegistry_Lookup_Active(t *testing.T) {
	s := NewInMemoryState()
	key := bytes.Repeat([]byte{0xAA}, 32)
	if err := s.ApplyEnroll(EnrollmentRecord{
		NodeID:           "alice",
		Owner:            "q1",
		GPUUUID:          "GPU-1",
		HMACKey:          key,
		StakeDust:        1_000_000_000,
		EnrolledAtHeight: 10,
	}); err != nil {
		t.Fatalf("ApplyEnroll: %v", err)
	}
	r := NewStateBackedRegistry(s)
	entry, err := r.Lookup("alice")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if entry.NodeID != "alice" || entry.GPUUUID != "GPU-1" {
		t.Fatalf("bad entry: %+v", entry)
	}
	if !bytes.Equal(entry.HMACKey, key) {
		t.Fatalf("HMACKey mismatch: got %x want %x", entry.HMACKey, key)
	}
	// Mutating the returned key must not corrupt state.
	entry.HMACKey[0] ^= 0xFF
	entry2, err := r.Lookup("alice")
	if err != nil {
		t.Fatalf("second Lookup: %v", err)
	}
	if !bytes.Equal(entry2.HMACKey, key) {
		t.Fatal("StateBackedRegistry did not defensively copy HMACKey")
	}
}

func TestStateBackedRegistry_Lookup_NotRegistered(t *testing.T) {
	s := NewInMemoryState()
	r := NewStateBackedRegistry(s)
	_, err := r.Lookup("unknown")
	if err == nil {
		t.Fatal("expected error on unknown node_id")
	}
	if !errors.Is(err, hmac.ErrNodeNotRegistered) {
		t.Fatalf("want ErrNodeNotRegistered, got %v", err)
	}
}

func TestStateBackedRegistry_Lookup_Revoked(t *testing.T) {
	s := NewInMemoryState()
	if err := s.ApplyEnroll(EnrollmentRecord{
		NodeID:           "alice",
		Owner:            "q1",
		GPUUUID:          "GPU-1",
		HMACKey:          bytes.Repeat([]byte{1}, 32),
		StakeDust:        1_000_000_000,
		EnrolledAtHeight: 10,
	}); err != nil {
		t.Fatalf("ApplyEnroll: %v", err)
	}
	if err := s.ApplyUnenroll("alice", 100); err != nil {
		t.Fatalf("ApplyUnenroll: %v", err)
	}
	r := NewStateBackedRegistry(s)
	_, err := r.Lookup("alice")
	if err == nil {
		t.Fatal("expected error on revoked node_id")
	}
	if !errors.Is(err, hmac.ErrNodeRevoked) {
		t.Fatalf("want ErrNodeRevoked, got %v", err)
	}
}

func TestStateBackedRegistry_NilStatePanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on nil state")
		}
	}()
	_ = NewStateBackedRegistry(nil)
}

// ----- InMemoryState mechanics ----------------------------------

func TestInMemoryState_ApplyEnroll_Duplicate(t *testing.T) {
	s := NewInMemoryState()
	rec := EnrollmentRecord{NodeID: "a", Owner: "q1", GPUUUID: "GPU-1",
		HMACKey: bytes.Repeat([]byte{1}, 32), StakeDust: 1_000_000_000}
	if err := s.ApplyEnroll(rec); err != nil {
		t.Fatalf("first ApplyEnroll: %v", err)
	}
	if err := s.ApplyEnroll(rec); err == nil {
		t.Fatal("duplicate ApplyEnroll should fail")
	}
}

func TestInMemoryState_ApplyUnenroll_Twice(t *testing.T) {
	s := NewInMemoryState()
	rec := EnrollmentRecord{NodeID: "a", Owner: "q1", GPUUUID: "GPU-1",
		HMACKey: bytes.Repeat([]byte{1}, 32), StakeDust: 1_000_000_000}
	_ = s.ApplyEnroll(rec)
	if err := s.ApplyUnenroll("a", 10); err != nil {
		t.Fatalf("first ApplyUnenroll: %v", err)
	}
	if err := s.ApplyUnenroll("a", 20); err == nil {
		t.Fatal("second ApplyUnenroll should fail")
	}
}

// TestInMemoryState_GPURebind_AfterUnenroll: after unenroll
// (but before sweep), the gpu_uuid should be free for a NEW
// node_id to bind to. This is the operator-friendly behaviour:
// you can unenroll rig #1 and immediately enroll the replacement
// rig #2 using the same physical GPU. Only the NAME is reserved
// during the unbond window; the PHYSICAL binding is released.
func TestInMemoryState_GPURebind_AfterUnenroll(t *testing.T) {
	s := NewInMemoryState()
	rec1 := EnrollmentRecord{NodeID: "a", Owner: "q1", GPUUUID: "GPU-1",
		HMACKey: bytes.Repeat([]byte{1}, 32), StakeDust: 1_000_000_000}
	_ = s.ApplyEnroll(rec1)
	_ = s.ApplyUnenroll("a", 10)

	bound, _ := s.GPUUUIDBound("GPU-1")
	if bound != "" {
		t.Fatalf("expected GPU-1 released, got bound to %q", bound)
	}
	rec2 := EnrollmentRecord{NodeID: "b", Owner: "q1", GPUUUID: "GPU-1",
		HMACKey: bytes.Repeat([]byte{2}, 32), StakeDust: 1_000_000_000}
	if err := s.ApplyEnroll(rec2); err != nil {
		t.Fatalf("rebind after unenroll should succeed, got %v", err)
	}
}

// TestInMemoryState_NodeID_StillReserved_DuringUnbond:
// conversely, the NAME is reserved until sweep. Re-using
// "alice" within the unbond window is rejected.
func TestInMemoryState_NodeID_StillReserved_DuringUnbond(t *testing.T) {
	s := NewInMemoryState()
	rec := EnrollmentRecord{NodeID: "alice", Owner: "q1", GPUUUID: "GPU-1",
		HMACKey: bytes.Repeat([]byte{1}, 32), StakeDust: 1_000_000_000}
	_ = s.ApplyEnroll(rec)
	_ = s.ApplyUnenroll("alice", 10)

	got, _ := s.Lookup("alice")
	if got == nil {
		t.Fatal("revoked record should still be looked up pre-sweep")
	}
	if got.Active() {
		t.Fatal("revoked record should not be Active()")
	}
}

// TestInMemoryState_SweepMaturedUnbonds.
func TestInMemoryState_SweepMaturedUnbonds(t *testing.T) {
	s := NewInMemoryState()
	rec := EnrollmentRecord{NodeID: "a", Owner: "q1", GPUUUID: "GPU-1",
		HMACKey: bytes.Repeat([]byte{1}, 32), StakeDust: 9_999}
	_ = s.ApplyEnroll(rec)
	_ = s.ApplyUnenroll("a", 10)

	// Too early — nothing released.
	if got := s.SweepMaturedUnbonds(10 + UnbondWindow - 1); len(got) != 0 {
		t.Fatalf("premature sweep released %d records", len(got))
	}
	// At maturity — released.
	got := s.SweepMaturedUnbonds(10 + UnbondWindow)
	if len(got) != 1 {
		t.Fatalf("sweep released %d, want 1", len(got))
	}
	if got[0].Owner != "q1" || got[0].StakeDust != 9_999 {
		t.Fatalf("bad release: %+v", got[0])
	}
	// Record gone from state.
	rec2, _ := s.Lookup("a")
	if rec2 != nil {
		t.Fatal("swept record still in state")
	}
}

func TestEnrollmentRecord_Active_And_Mature(t *testing.T) {
	r := EnrollmentRecord{EnrolledAtHeight: 1}
	if !r.Active() {
		t.Fatal("fresh record should be Active")
	}
	r.RevokedAtHeight = 10
	r.UnbondMaturesAtHeight = 20
	if r.Active() {
		t.Fatal("revoked record should not be Active")
	}
	if r.MatureForUnbond(15) {
		t.Fatal("should not be mature yet")
	}
	if !r.MatureForUnbond(20) {
		t.Fatal("should be mature at maturity height")
	}
	if !r.MatureForUnbond(25) {
		t.Fatal("should be mature after maturity height")
	}
}
