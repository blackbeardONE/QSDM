package reputation

import (
	"fmt"
	"github.com/blackbeardONE/QSDM/internal/alerting"
	"sync"
)

// ReputationManager manages node reputations based on stakes and penalties.
type ReputationManager struct {
	mu          sync.Mutex
	reputations map[string]float64 // nodeID -> reputation score
	stakes      map[string]float64 // nodeID -> stake amount
}

// NewReputationManager creates a new ReputationManager.
func NewReputationManager() *ReputationManager {
	return &ReputationManager{
		reputations: make(map[string]float64),
		stakes:      make(map[string]float64),
	}
}

// SetStake sets the stake amount for a node.
func (rm *ReputationManager) SetStake(nodeID string, stake float64) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	rm.stakes[nodeID] = stake
	if _, exists := rm.reputations[nodeID]; !exists {
		rm.reputations[nodeID] = 1.0 // default reputation
	}
}

// Penalize penalizes a node by reducing its reputation.
func (rm *ReputationManager) Penalize(nodeID string, penalty float64) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	if _, exists := rm.reputations[nodeID]; exists {
		// Only apply positive penalties (negative values are ignored)
		if penalty > 0 {
			rm.reputations[nodeID] -= penalty
			if rm.reputations[nodeID] < 0 {
				rm.reputations[nodeID] = 0
			}
			fmt.Printf("Node %s penalized by %.2f, new reputation: %.2f\n", nodeID, penalty, rm.reputations[nodeID])
			// Send alert for reputation penalty
			alerting.Alert(fmt.Sprintf("Node %s penalized by %.2f, new reputation: %.2f", nodeID, penalty, rm.reputations[nodeID]))
		}
	}
}

// GetReputation returns the reputation score of a node.
func (rm *ReputationManager) GetReputation(nodeID string) float64 {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	rep := rm.reputations[nodeID]
	// Cap reputation at 1.0 maximum
	if rep > 1.0 {
		return 1.0
	}
	return rep
}
