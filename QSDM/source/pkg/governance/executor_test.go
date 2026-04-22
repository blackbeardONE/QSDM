package governance

import (
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestProposalExecutor_PassedProposal(t *testing.T) {
	dir := t.TempDir()
	sv := NewSnapshotVoting(filepath.Join(dir, "proposals.json"))

	sv.AddProposal("prop1", "Increase block size", 800*time.Millisecond, 2)
	sv.Vote("prop1", "voter1", 3, true)
	sv.Vote("prop1", "voter2", 2, false)

	time.Sleep(900 * time.Millisecond) // ensure proposal is past ExpiresAt under scheduler load
	passed, err := sv.FinalizeProposal("prop1")
	if err != nil {
		t.Fatalf("FinalizeProposal: %v", err)
	}
	if !passed {
		t.Fatal("expected proposal to pass")
	}

	var executed atomic.Int32
	exec := NewProposalExecutor(sv, 1*time.Hour)
	exec.AttachAction("prop1", &ProposalAction{
		Type:       ActionParameterSet,
		Parameters: map[string]interface{}{"block_size": 2048},
	})
	exec.RegisterHandler(ActionParameterSet, func(pid string, params map[string]interface{}) error {
		executed.Add(1)
		return nil
	})

	// Use ExecuteNow for deterministic testing (no timing dependency)
	if err := exec.ExecuteNow("prop1"); err != nil {
		t.Fatalf("ExecuteNow: %v", err)
	}

	if executed.Load() != 1 {
		t.Fatalf("expected handler called once, got %d", executed.Load())
	}

	history := exec.ExecutionHistory()
	if len(history) != 1 {
		t.Fatalf("expected 1 execution record, got %d", len(history))
	}
	if !history[0].Success {
		t.Fatalf("expected success, got error: %s", history[0].Error)
	}
}

func TestProposalExecutor_FailedProposal(t *testing.T) {
	dir := t.TempDir()
	sv := NewSnapshotVoting(filepath.Join(dir, "proposals.json"))

	sv.AddProposal("prop_fail", "Bad idea", 200*time.Millisecond, 2)
	sv.Vote("prop_fail", "voter1", 1, false)
	sv.Vote("prop_fail", "voter2", 3, false)

	time.Sleep(300 * time.Millisecond)
	sv.FinalizeProposal("prop_fail")

	var executed atomic.Int32
	exec := NewProposalExecutor(sv, 50*time.Millisecond)
	exec.AttachAction("prop_fail", &ProposalAction{Type: ActionConfigChange})
	exec.RegisterHandler(ActionConfigChange, func(_ string, _ map[string]interface{}) error {
		executed.Add(1)
		return nil
	})

	exec.Start()
	time.Sleep(300 * time.Millisecond)
	exec.Stop()

	if executed.Load() != 0 {
		t.Fatal("handler should not be called for failed proposals")
	}

	history := exec.ExecutionHistory()
	if len(history) != 1 {
		t.Fatalf("expected 1 record (failure), got %d", len(history))
	}
	if history[0].Success {
		t.Fatal("expected failure record")
	}
}

func TestProposalExecutor_NoDoubleExecution(t *testing.T) {
	dir := t.TempDir()
	sv := NewSnapshotVoting(filepath.Join(dir, "proposals.json"))

	sv.AddProposal("prop_once", "Once only", 200*time.Millisecond, 1)
	sv.Vote("prop_once", "v1", 2, true)

	time.Sleep(300 * time.Millisecond)
	sv.FinalizeProposal("prop_once")

	var count atomic.Int32
	exec := NewProposalExecutor(sv, 50*time.Millisecond)
	exec.AttachAction("prop_once", &ProposalAction{Type: ActionCustom})
	exec.RegisterHandler(ActionCustom, func(_ string, _ map[string]interface{}) error {
		count.Add(1)
		return nil
	})

	exec.Start()
	time.Sleep(300 * time.Millisecond)
	exec.Stop()

	if count.Load() != 1 {
		t.Fatalf("expected exactly 1 execution, got %d", count.Load())
	}
}

func TestProposalExecutor_ExecuteNow(t *testing.T) {
	dir := t.TempDir()
	sv := NewSnapshotVoting(filepath.Join(dir, "proposals.json"))
	sv.AddProposal("immediate", "Do it now", 1*time.Hour, 0)

	var executed bool
	exec := NewProposalExecutor(sv, 1*time.Hour)
	exec.AttachAction("immediate", &ProposalAction{
		Type:       ActionContractUpgrade,
		Parameters: map[string]interface{}{"contract": "token_v3"},
	})
	exec.RegisterHandler(ActionContractUpgrade, func(_ string, params map[string]interface{}) error {
		executed = true
		return nil
	})

	err := exec.ExecuteNow("immediate")
	if err != nil {
		t.Fatalf("ExecuteNow: %v", err)
	}
	if !executed {
		t.Fatal("handler not called")
	}

	err = exec.ExecuteNow("immediate")
	if err == nil {
		t.Fatal("expected error for double execution")
	}
}

func TestProposalExecutor_NoAction(t *testing.T) {
	dir := t.TempDir()
	sv := NewSnapshotVoting(filepath.Join(dir, "proposals.json"))

	exec := NewProposalExecutor(sv, 1*time.Hour)
	err := exec.ExecuteNow("no_such_proposal")
	if err == nil {
		t.Fatal("expected error for missing action")
	}
}

func TestProposalExecutor_QuorumNotReached(t *testing.T) {
	dir := t.TempDir()
	sv := NewSnapshotVoting(filepath.Join(dir, "proposals.json"))
	sv.AddProposal("low_quorum", "Need more votes", 200*time.Millisecond, 100)
	sv.Vote("low_quorum", "v1", 1, true)

	time.Sleep(300 * time.Millisecond)
	sv.FinalizeProposal("low_quorum") // will fail because quorum not met

	var executed atomic.Int32
	exec := NewProposalExecutor(sv, 50*time.Millisecond)
	exec.AttachAction("low_quorum", &ProposalAction{Type: ActionCustom})
	exec.RegisterHandler(ActionCustom, func(_ string, _ map[string]interface{}) error {
		executed.Add(1)
		return nil
	})

	exec.Start()
	time.Sleep(300 * time.Millisecond)
	exec.Stop()

	if executed.Load() != 0 {
		t.Fatal("should not execute when quorum not reached")
	}
}
