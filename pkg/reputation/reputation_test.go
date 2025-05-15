package reputation

import (
    "testing"
)

func TestReputationManager(t *testing.T) {
    rm := NewReputationManager()

    nodeID := "node1"
    rm.SetStake(nodeID, 100)

    rep := rm.GetReputation(nodeID)
    if rep != 1.0 {
        t.Errorf("Expected initial reputation 1.0, got %f", rep)
    }

    rm.Penalize(nodeID, 0.3)
    rep = rm.GetReputation(nodeID)
    if rep != 0.7 {
        t.Errorf("Expected reputation 0.7 after penalty, got %f", rep)
    }

    rm.Penalize(nodeID, 1.0)
    rep = rm.GetReputation(nodeID)
    if rep != 0.0 {
        t.Errorf("Expected reputation 0.0 after penalty floor, got %f", rep)
    }
}
