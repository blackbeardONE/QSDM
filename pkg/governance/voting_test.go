package governance

import (
    "testing"
    "time"
)

func TestSnapshotVoting(t *testing.T) {
    snapshot := NewSnapshot("test1", 1*time.Minute)

    snapshot.SetTokenWeight("voter1", 100)
    snapshot.SetTokenWeight("voter2", 50)
    snapshot.SetTokenWeight("voter3", 25)

    err := snapshot.CastVote("voter1", VoteYes)
    if err != nil {
        t.Fatalf("CastVote failed: %v", err)
    }
    err = snapshot.CastVote("voter2", VoteNo)
    if err != nil {
        t.Fatalf("CastVote failed: %v", err)
    }
    err = snapshot.CastVote("voter3", VoteAbstain)
    if err != nil {
        t.Fatalf("CastVote failed: %v", err)
    }

    yes, no, abstain := snapshot.TallyVotes()
    if yes != 100 {
        t.Errorf("Expected yes votes 100, got %f", yes)
    }
    if no != 50 {
        t.Errorf("Expected no votes 50, got %f", no)
    }
    if abstain != 25 {
        t.Errorf("Expected abstain votes 25, got %f", abstain)
    }

    result := snapshot.Result()
    expected := "Yes: 57.14%, No: 28.57%, Abstain: 14.29%"
    if result != expected {
        t.Errorf("Expected result %q, got %q", expected, result)
    }

    // Test expired voting
    snapshot.Expiry = time.Now().Add(-1 * time.Minute)
    err = snapshot.CastVote("voter1", VoteNo)
    if err == nil {
        t.Errorf("Expected error on expired voting, got nil")
    }
}
