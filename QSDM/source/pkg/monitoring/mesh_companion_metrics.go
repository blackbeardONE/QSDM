package monitoring

import "sync/atomic"

var meshCompanionPublishTotal atomic.Uint64

// RecordMeshCompanionPublish counts extra mesh `qsdm_mesh3d_v1` gossip publishes (API or wallet loop companion).
func RecordMeshCompanionPublish() {
	meshCompanionPublishTotal.Add(1)
}

// MeshCompanionPublishCount returns mesh companion gossip publishes since process start.
func MeshCompanionPublishCount() uint64 {
	return meshCompanionPublishTotal.Load()
}
