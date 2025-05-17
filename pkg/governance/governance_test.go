package governance

import (
    "testing"
)

func TestSnapshotVoting(t *testing.T) {
    sv := NewSnapshotVoting()

    // Add voters with token weights
    sv.Voters["voter1"] = 10
    sv.Voters["voter2"] = 5

    // Add proposal
    err := sv.AddProposal("prop1", "Increase block size")
    if err != nil {
        t.Fatalf("AddProposal failed: %v", err)
    }

    // Duplicate proposal should fail
    err = sv.AddProposal("prop1", "Duplicate proposal")
    if err == nil {
        t.Fatalf("Expected error for duplicate proposal")
    }

    // Vote for proposal
    err = sv.Vote("prop1", "voter1", 5, true)
    if err != nil {
        t.Fatalf("Vote failed: %v", err)
    }

    // Vote with insufficient weight
    err = sv.Vote("prop1", "voter2", 10, true)
    if err == nil {
        t.Fatalf("Expected error for insufficient voting weight")
    }

    // Vote against proposal
    err = sv.Vote("prop1", "voter2", 5, false)
    if err != nil {
        t.Fatalf("Vote failed: %v", err)
    }

    // Finalize proposal
    passed, err := sv.FinalizeProposal("prop1")
    if err != nil {
        t.Fatalf("FinalizeProposal failed: %v", err)
    }
    if !passed {
        t.Errorf("Expected proposal to pass")
    }

    // Finalize again should fail
    _, err = sv.FinalizeProposal("prop1")
    if err == nil {
        t.Fatalf("Expected error for already finalized proposal")
    }
}
