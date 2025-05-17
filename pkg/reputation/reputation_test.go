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

    // Additional tests

    // Test multiple nodes
    node2 := "node2"
    rm.SetStake(node2, 50)
    rep2 := rm.GetReputation(node2)
    if rep2 != 1.0 {
        t.Errorf("Expected initial reputation 1.0 for node2, got %f", rep2)
    }

    // Test penalize with zero penalty
    rm.Penalize(node2, 0.0)
    rep2 = rm.GetReputation(node2)
    if rep2 != 1.0 {
        t.Errorf("Expected reputation 1.0 after zero penalty, got %f", rep2)
    }

    // Test penalize with negative penalty (should not change reputation)
    rm.Penalize(node2, -0.1)
    rep2 = rm.GetReputation(node2)
    if rep2 != 1.0 {
        t.Errorf("Expected reputation 1.0 after negative penalty, got %f", rep2)
    }
}
