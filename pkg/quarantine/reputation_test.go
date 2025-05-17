package quarantine

import (
    "testing"
)

func TestReputationManager(t *testing.T) {
    rm := NewReputationManager(10, 5)

    nodeID := "node1"

    // Initial reputation should be 0
    if rep := rm.GetReputation(nodeID); rep != 0 {
        t.Errorf("Expected initial reputation 0, got %d", rep)
    }

    // Penalize node
    rm.Penalize(nodeID)
    if rep := rm.GetReputation(nodeID); rep != -10 {
        t.Errorf("Expected reputation -10 after penalty, got %d", rep)
    }

    // Reward node
    rm.Reward(nodeID)
    if rep := rm.GetReputation(nodeID); rep != -5 {
        t.Errorf("Expected reputation -5 after reward, got %d", rep)
    }
}
