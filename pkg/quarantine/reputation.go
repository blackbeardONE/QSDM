package quarantine

import (
    "sync"
    "fmt"
)

// ReputationManager manages node reputations based on transaction validity.
type ReputationManager struct {
    mu          sync.Mutex
    reputations map[string]int // node ID to reputation score
    penalty     int
    reward      int
}

// NewReputationManager creates a new ReputationManager.
func NewReputationManager(penalty, reward int) *ReputationManager {
    return &ReputationManager{
        reputations: make(map[string]int),
        penalty:     penalty,
        reward:      reward,
    }
}

// Penalize penalizes a node for invalid transactions.
func (rm *ReputationManager) Penalize(nodeID string) {
    rm.mu.Lock()
    defer rm.mu.Unlock()
    rm.reputations[nodeID] -= rm.penalty
    fmt.Printf("Node %s penalized. New reputation: %d\n", nodeID, rm.reputations[nodeID])
}

// Reward rewards a node for valid transactions.
func (rm *ReputationManager) Reward(nodeID string) {
    rm.mu.Lock()
    defer rm.mu.Unlock()
    rm.reputations[nodeID] += rm.reward
    fmt.Printf("Node %s rewarded. New reputation: %d\n", nodeID, rm.reputations[nodeID])
}

// GetReputation returns the reputation score of a node.
func (rm *ReputationManager) GetReputation(nodeID string) int {
    rm.mu.Lock()
    defer rm.mu.Unlock()
    return rm.reputations[nodeID]
}
