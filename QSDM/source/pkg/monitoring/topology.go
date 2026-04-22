package monitoring

import (
	"sync"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/networking"
)

// TopologyMonitor monitors network topology and connection quality
type TopologyMonitor struct {
	network      *networking.Network
	mu           sync.RWMutex
	topology     map[string]interface{}
	lastUpdate   time.Time
	updateInterval time.Duration
}

// NewTopologyMonitor creates a new topology monitor
func NewTopologyMonitor(net *networking.Network) *TopologyMonitor {
	tm := &TopologyMonitor{
		network:        net,
		topology:      make(map[string]interface{}),
		updateInterval: 5 * time.Second,
	}
	
	// Start background update goroutine
	go tm.updateLoop()
	
	return tm
}

// updateLoop periodically updates topology information
func (tm *TopologyMonitor) updateLoop() {
	ticker := time.NewTicker(tm.updateInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			tm.updateTopology()
		}
	}
}

// updateTopology updates the topology information
func (tm *TopologyMonitor) updateTopology() {
	if tm.network == nil {
		return
	}

	topology := tm.network.GetNetworkTopology()
	
	tm.mu.Lock()
	tm.topology = topology
	tm.lastUpdate = time.Now()
	tm.mu.Unlock()
}

// GetTopology returns the current network topology
func (tm *TopologyMonitor) GetTopology() map[string]interface{} {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	
	// Make a copy to avoid race conditions
	result := make(map[string]interface{})
	for k, v := range tm.topology {
		result[k] = v
	}
	result["lastUpdate"] = tm.lastUpdate
	
	return result
}

// GetPeerInfo returns information about all peers
func (tm *TopologyMonitor) GetPeerInfo() []networking.PeerInfo {
	if tm.network == nil {
		return []networking.PeerInfo{}
	}
	return tm.network.GetPeerInfo()
}

// GetConnectionQuality returns connection quality metrics
func (tm *TopologyMonitor) GetConnectionQuality() []networking.ConnectionQuality {
	if tm.network == nil {
		return []networking.ConnectionQuality{}
	}
	return tm.network.GetConnectionQuality()
}

