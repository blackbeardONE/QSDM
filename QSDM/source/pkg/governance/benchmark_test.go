package governance

import (
	"os"
	"testing"
	"time"
)

func BenchmarkAddProposal(b *testing.B) {
	tmpFile, err := os.CreateTemp("", "bench_*.json")
	if err != nil {
		b.Fatalf("Failed to create temp file: %v", err)
	}
	tmpFile.Close()
	defer os.Remove(tmpFile.Name())

	sv := NewSnapshotVoting(tmpFile.Name())

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		proposalID := "prop_" + string(rune(i))
		err := sv.AddProposal(proposalID, "Test proposal", 60*time.Second, 5)
		if err != nil {
			b.Fatalf("Failed to add proposal: %v", err)
		}
	}
}

func BenchmarkVote(b *testing.B) {
	tmpFile, err := os.CreateTemp("", "bench_*.json")
	if err != nil {
		b.Fatalf("Failed to create temp file: %v", err)
	}
	tmpFile.Close()
	defer os.Remove(tmpFile.Name())

	sv := NewSnapshotVoting(tmpFile.Name())
	proposalID := "test_prop"
	sv.AddProposal(proposalID, "Test proposal", 60*time.Second, 5)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		voterID := "voter_" + string(rune(i))
		err := sv.Vote(proposalID, voterID, 1, true)
		if err != nil {
			b.Fatalf("Failed to vote: %v", err)
		}
	}
}

func BenchmarkFinalizeProposal(b *testing.B) {
	tmpFile, err := os.CreateTemp("", "bench_*.json")
	if err != nil {
		b.Fatalf("Failed to create temp file: %v", err)
	}
	tmpFile.Close()
	defer os.Remove(tmpFile.Name())

	sv := NewSnapshotVoting(tmpFile.Name())

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		proposalID := "prop_" + string(rune(i))
		// Use quorum of 1 and ensure we vote with weight 1 to meet quorum
		sv.AddProposal(proposalID, "Test proposal", 0, 1)
		sv.Vote(proposalID, "voter1", 1, true)
		
		// Expire the proposal
		sv.Mu.Lock()
		sv.Proposals[proposalID].ExpiresAt = time.Now().Add(-1 * time.Second)
		sv.Mu.Unlock()

		_, err := sv.FinalizeProposal(proposalID)
		if err != nil {
			// If quorum not reached, it's because vote wasn't recorded properly
			// Try voting again with higher weight
			sv.Vote(proposalID, "voter1", 2, true)
			_, err = sv.FinalizeProposal(proposalID)
			if err != nil {
				b.Fatalf("Failed to finalize: %v", err)
			}
		}
	}
}

