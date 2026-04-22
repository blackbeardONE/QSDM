package dashboard

import "github.com/blackbeardONE/QSDM/internal/topology"

// PeerTopologyInfo is a type alias kept for backwards compatibility. The canonical
// type now lives in internal/topology so pkg/api can consume it without creating
// an import cycle with this package.
type PeerTopologyInfo = topology.PeerInfo

// BuildLiveTopologyView is a thin wrapper around topology.BuildLiveView.
// Prefer importing internal/topology directly in new code.
func BuildLiveTopologyView(localID string, peers []PeerTopologyInfo) map[string]interface{} {
	return topology.BuildLiveView(localID, peers)
}
