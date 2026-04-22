package tests

import (
	"os"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/governance"
)

func TestGovernanceCLIProposeList(t *testing.T) {
	// Use a temporary file to avoid file locking issues
	tmpFile, err := os.CreateTemp("", "test_snapshot_*.json")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	tmpFile.Close()
	defer os.Remove(tmpFile.Name())

	sv := governance.NewSnapshotVoting(tmpFile.Name())

	// Test that the CLI functions work correctly by calling them directly
	// The CLI is a function that takes a SnapshotVoting instance, not a standalone program
	
	// Test propose functionality
	err = sv.AddProposal("test1", "Test proposal", 60*1e9, 1)
	if err != nil {
		t.Fatalf("Failed to add proposal: %v", err)
	}

	// Verify proposal was added
	sv.Mu.RLock()
	proposal, exists := sv.Proposals["test1"]
	sv.Mu.RUnlock()
	
	if !exists {
		t.Error("Expected proposal to be added")
	}
	if proposal == nil {
		t.Error("Expected proposal to exist")
	}
	if proposal.Description != "Test proposal" {
		t.Errorf("Expected description 'Test proposal', got '%s'", proposal.Description)
	}

	// Test list functionality (checking that proposals exist)
	sv.Mu.RLock()
	proposalCount := len(sv.Proposals)
	sv.Mu.RUnlock()
	
	if proposalCount == 0 {
		t.Error("Expected at least one proposal in the list")
	}

	// Note: The actual CLI interactive function (governancecli.GovernanceCLI) requires
	// stdin/stdout interaction which is difficult to test directly. This test verifies
	// the underlying functionality that the CLI uses.
}
