package chain

import (
	"errors"
	"testing"
)

func stakeOn(t *testing.T, s *TaskStateStore, id, taskID, sender string, h uint64) {
	t.Helper()
	a := TaskAction{ID: id, Action: "stake", TaskID: taskID, Sender: sender, Amount: 5, Nonce: 1, Timestamp: "2026-11-16T00:00:00Z"}
	if _, err := s.applyAction(a, false, true, h); err != nil {
		t.Fatalf("stake: %v", err)
	}
}

// rc1113 rule 1: from the rc1113 height, catalog-register needs a task ID with
// no stake, pool or participants; below it (or with no height) nothing changes.
func TestRC1113_RegisterNeedsFreshTaskID(t *testing.T) {
	t.Cleanup(func() { SetRC1113RulesHeight(0) })

	// No height configured: the old behaviour, a later register takes over.
	SetRC1113RulesHeight(0)
	s := NewTaskStateStore()
	stakeOn(t, s, "s1", "edge-x", "bob", 500)
	if _, err := s.applyAction(testCatalogAction(t, "r1", TaskActionCatalogRegister, "edge-x", "mallory", 1, 1), false, true, 500); err != nil {
		t.Fatalf("unset height must keep the old rule: %v", err)
	}

	SetRC1113RulesHeight(100)
	// Below the height: unchanged.
	s = NewTaskStateStore()
	stakeOn(t, s, "s1", "edge-x", "bob", 99)
	if _, err := s.applyAction(testCatalogAction(t, "r1", TaskActionCatalogRegister, "edge-x", "mallory", 1, 1), false, true, 99); err != nil {
		t.Fatalf("below the height must keep the old rule: %v", err)
	}

	// At the height: a staked ID cannot be taken over.
	s = NewTaskStateStore()
	stakeOn(t, s, "s1", "edge-x", "bob", 100)
	_, err := s.applyAction(testCatalogAction(t, "r1", TaskActionCatalogRegister, "edge-x", "mallory", 1, 1), false, true, 100)
	if !errors.Is(err, ErrTaskRegisterNotFresh) {
		t.Fatalf("got %v, want ErrTaskRegisterNotFresh", err)
	}
	if task, _ := s.GetTask("edge-x"); task.Manifest != nil {
		t.Fatal("a refused register left a manifest")
	}
	// A fresh ID still registers.
	if _, err := s.applyAction(testCatalogAction(t, "r2", TaskActionCatalogRegister, "edge-y", "mallory", 1, 1), false, true, 100); err != nil {
		t.Fatalf("a fresh ID must register: %v", err)
	}
	// A funded (pool only) ID is not fresh either.
	f := TaskAction{ID: "f1", Action: "fund", TaskID: "edge-z", Sender: "carol", Amount: 3, Nonce: 1, Timestamp: "2026-11-16T00:00:00Z"}
	if _, err := s.applyAction(f, false, true, 101); err != nil {
		t.Fatalf("fund: %v", err)
	}
	if _, err := s.applyAction(testCatalogAction(t, "r3", TaskActionCatalogRegister, "edge-z", "mallory", 2, 1), false, true, 101); !errors.Is(err, ErrTaskRegisterNotFresh) {
		t.Fatalf("funded ID: got %v, want ErrTaskRegisterNotFresh", err)
	}
}
