package governance

import (
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func expireProposal(t *testing.T, sv *SnapshotVoting, proposalID string) {
	t.Helper()
	sv.Mu.Lock()
	defer sv.Mu.Unlock()
	proposal, ok := sv.Proposals[proposalID]
	if !ok {
		t.Fatalf("proposal %q is missing", proposalID)
	}
	proposal.ExpiresAt = time.Now().Add(-time.Millisecond)
}

func TestProposalExecutor_PassedProposal(t *testing.T) {
	dir := t.TempDir()
	sv := NewSnapshotVoting(filepath.Join(dir, "proposals.json"))

	if err := sv.AddProposal("prop1", "Increase block size", time.Hour, 2); err != nil {
		t.Fatalf("AddProposal: %v", err)
	}
	if err := sv.Vote("prop1", "voter1", 3, true); err != nil {
		t.Fatalf("Vote voter1: %v", err)
	}
	if err := sv.Vote("prop1", "voter2", 2, false); err != nil {
		t.Fatalf("Vote voter2: %v", err)
	}

	// Expire the fixture explicitly instead of relying on scheduler timing.
	expireProposal(t, sv, "prop1")
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

	sv.AddProposal("prop_fail", "Bad idea", time.Hour, 2)
	sv.Vote("prop_fail", "voter1", 1, false)
	sv.Vote("prop_fail", "voter2", 3, false)
	expireProposal(t, sv, "prop_fail")
	passed, err := sv.FinalizeProposal("prop_fail")
	if err != nil {
		t.Fatalf("FinalizeProposal: %v", err)
	}
	if passed {
		t.Fatal("expected failed proposal")
	}

	var executed atomic.Int32
	exec := NewProposalExecutor(sv, time.Hour)
	exec.AttachAction("prop_fail", &ProposalAction{Type: ActionConfigChange})
	exec.RegisterHandler(ActionConfigChange, func(_ string, _ map[string]interface{}) error {
		executed.Add(1)
		return nil
	})

	exec.pollAndExecute()

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

	sv.AddProposal("prop_once", "Once only", time.Hour, 1)
	sv.Vote("prop_once", "v1", 2, true)
	expireProposal(t, sv, "prop_once")
	passed, err := sv.FinalizeProposal("prop_once")
	if err != nil {
		t.Fatalf("FinalizeProposal: %v", err)
	}
	if !passed {
		t.Fatal("expected passed proposal")
	}

	var count atomic.Int32
	exec := NewProposalExecutor(sv, time.Hour)
	exec.AttachAction("prop_once", &ProposalAction{Type: ActionCustom})
	exec.RegisterHandler(ActionCustom, func(_ string, _ map[string]interface{}) error {
		count.Add(1)
		return nil
	})

	// Drive the executor synchronously twice. The second pass must observe the
	// first execution record and skip the proposal.
	exec.pollAndExecute()
	exec.pollAndExecute()

	if count.Load() != 1 {
		t.Fatalf("expected exactly 1 execution, got %d", count.Load())
	}
	if history := exec.ExecutionHistory(); len(history) != 1 || !history[0].Success {
		t.Fatalf("expected one successful execution record, got %+v", history)
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
	sv.AddProposal("low_quorum", "Need more votes", time.Hour, 100)
	sv.Vote("low_quorum", "v1", 1, true)
	expireProposal(t, sv, "low_quorum")
	if _, err := sv.FinalizeProposal("low_quorum"); err == nil {
		t.Fatal("expected finalization to reject insufficient quorum")
	}

	var executed atomic.Int32
	exec := NewProposalExecutor(sv, time.Hour)
	exec.AttachAction("low_quorum", &ProposalAction{Type: ActionCustom})
	exec.RegisterHandler(ActionCustom, func(_ string, _ map[string]interface{}) error {
		executed.Add(1)
		return nil
	})

	exec.pollAndExecute()

	if executed.Load() != 0 {
		t.Fatal("should not execute when quorum not reached")
	}
}
