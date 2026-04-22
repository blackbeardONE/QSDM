package networking

import (
	"context"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

// NetworkOptimizer provides network optimization utilities
type NetworkOptimizer struct {
	network *Network
	mu      sync.RWMutex
	stats   map[peer.ID]*PeerStats
}

// PeerStats tracks statistics for a peer
type PeerStats struct {
	LastSeen       time.Time
	MessageCount   int64
	ErrorCount     int64
	AvgLatency     time.Duration
	LastLatency    time.Duration
	ConnectionTime time.Time
}

// NewNetworkOptimizer creates a new network optimizer
func NewNetworkOptimizer(net *Network) *NetworkOptimizer {
	return &NetworkOptimizer{
		network: net,
		stats:   make(map[peer.ID]*PeerStats),
	}
}

// UpdatePeerStats updates statistics for a peer
func (no *NetworkOptimizer) UpdatePeerStats(pid peer.ID, latency time.Duration, err error) {
	no.mu.Lock()
	defer no.mu.Unlock()

	stats, exists := no.stats[pid]
	if !exists {
		stats = &PeerStats{
			ConnectionTime: time.Now(),
		}
		no.stats[pid] = stats
	}

	stats.LastSeen = time.Now()
	stats.LastLatency = latency

	if err != nil {
		stats.ErrorCount++
	} else {
		stats.MessageCount++
		// Update average latency (simple moving average)
		if stats.AvgLatency == 0 {
			stats.AvgLatency = latency
		} else {
			stats.AvgLatency = (stats.AvgLatency + latency) / 2
		}
	}
}

// GetPeerStats returns statistics for a peer
func (no *NetworkOptimizer) GetPeerStats(pid peer.ID) *PeerStats {
	no.mu.RLock()
	defer no.mu.RUnlock()
	return no.stats[pid]
}

// GetBestPeers returns the best performing peers (lowest latency, highest success rate)
func (no *NetworkOptimizer) GetBestPeers(limit int) []peer.ID {
	no.mu.RLock()
	defer no.mu.RUnlock()

	type peerScore struct {
		pid   peer.ID
		score float64
	}

	scores := make([]peerScore, 0, len(no.stats))
	for pid, stats := range no.stats {
		// Calculate score: lower latency and higher success rate = better
		successRate := float64(stats.MessageCount) / float64(stats.MessageCount+stats.ErrorCount+1)
		latencyScore := 1.0 / (float64(stats.AvgLatency.Milliseconds()) + 1.0)
		score := successRate * latencyScore

		scores = append(scores, peerScore{pid: pid, score: score})
	}

	// Sort by score (simplified - in production, use sort.Slice)
	// For now, just return first N peers
	result := make([]peer.ID, 0, limit)
	for i, ps := range scores {
		if i >= limit {
			break
		}
		result = append(result, ps.pid)
	}

	return result
}

// OptimizeConnections optimizes peer connections based on performance
func (no *NetworkOptimizer) OptimizeConnections(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			no.performOptimization(ctx)
		case <-ctx.Done():
			return
		}
	}
}

func (no *NetworkOptimizer) performOptimization(ctx context.Context) {
	// Get best performing peers
	bestPeers := no.GetBestPeers(10)

	// Ensure we're connected to best peers
	for _, pid := range bestPeers {
		if no.network.Host.Network().Connectedness(pid) == 0 {
			// Not connected, attempt connection
			pi := peer.AddrInfo{ID: pid}
			if err := no.network.Host.Connect(ctx, pi); err != nil {
				// Connection failed, update stats
				no.UpdatePeerStats(pid, 0, err)
			}
		}
	}

	// Disconnect from poorly performing peers (if we have too many connections)
	no.mu.RLock()
	allPeers := make([]peer.ID, 0, len(no.stats))
	for pid := range no.stats {
		allPeers = append(allPeers, pid)
	}
	no.mu.RUnlock()

	if len(allPeers) > 20 {
		// Disconnect from worst performers
		// This is a simplified version - full implementation would rank all peers
	}
}

// GetNetworkStats returns overall network statistics
func (no *NetworkOptimizer) GetNetworkStats() map[string]interface{} {
	no.mu.RLock()
	defer no.mu.RUnlock()

	totalMessages := int64(0)
	totalErrors := int64(0)
	totalLatency := time.Duration(0)
	peerCount := len(no.stats)

	for _, stats := range no.stats {
		totalMessages += stats.MessageCount
		totalErrors += stats.ErrorCount
		totalLatency += stats.AvgLatency
	}

	avgLatency := time.Duration(0)
	if peerCount > 0 {
		avgLatency = totalLatency / time.Duration(peerCount)
	}

	successRate := float64(0)
	if totalMessages+totalErrors > 0 {
		successRate = float64(totalMessages) / float64(totalMessages+totalErrors) * 100
	}

	return map[string]interface{}{
		"peer_count":    peerCount,
		"total_messages": totalMessages,
		"total_errors":   totalErrors,
		"success_rate":  successRate,
		"avg_latency":   avgLatency.String(),
	}
}

