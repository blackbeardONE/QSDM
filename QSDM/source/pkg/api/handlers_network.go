package api

import (
	"encoding/json"
	"net/http"
	"sync"

	"github.com/blackbeardONE/QSDM/internal/topology"
)

// TopologyProvider supplies peer information for the live topology endpoint.
// Implementations typically wrap the libp2p host to return connected peers with
// reputation, region, and latency metadata. When unset, GET /api/v1/network/topology
// returns an empty topology (200 OK) so the dashboard UI stays functional.
type TopologyProvider interface {
	// LocalID returns the label used for the central node in the topology view.
	LocalID() string
	// Peers returns a snapshot of peer info for the topology projection.
	Peers() []topology.PeerInfo
}

// TopologyProviderFunc adapts a plain function into a TopologyProvider.
type TopologyProviderFunc func() (string, []topology.PeerInfo)

// LocalID satisfies TopologyProvider.
func (f TopologyProviderFunc) LocalID() string {
	id, _ := f()
	return id
}

// Peers satisfies TopologyProvider.
func (f TopologyProviderFunc) Peers() []topology.PeerInfo {
	_, p := f()
	return p
}

type topologyState struct {
	mu       sync.RWMutex
	provider TopologyProvider
}

var topoState topologyState

// SetTopologyProvider wires a TopologyProvider used by the live topology handler.
// Passing nil clears the provider.
func (s *Server) SetTopologyProvider(p TopologyProvider) {
	topoState.mu.Lock()
	defer topoState.mu.Unlock()
	topoState.provider = p
}

// GetNetworkTopology serves GET /api/v1/network/topology with a live JSON projection
// of the current peer set. The shape matches internal/topology.BuildLiveView so the
// existing dashboard WebGL renderer can consume it directly.
func (h *Handlers) GetNetworkTopology(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	topoState.mu.RLock()
	provider := topoState.provider
	topoState.mu.RUnlock()

	var (
		localID string
		peers   []topology.PeerInfo
	)
	if provider != nil {
		localID = provider.LocalID()
		peers = provider.Peers()
	}
	if localID == "" {
		localID = h.nodeID
		if localID == "" {
			localID = "local"
		}
	}

	view := topology.BuildLiveView(localID, peers)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(view)
}
