package api

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/monitoring"
)

func recordTrustGPUProof(t *testing.T, nodeID string, available bool) {
	t.Helper()
	bundle, err := json.Marshal(map[string]interface{}{
		"qsdm_node_id":    nodeID,
		"timestamp_utc":   time.Now().UTC().Format(time.RFC3339),
		"cuda_proof_hash": "test-proof",
		"gpu_fingerprint": map[string]interface{}{
			"available": available,
		},
	})
	if err != nil {
		t.Fatalf("marshal NGC proof: %v", err)
	}
	if err := monitoring.RecordNGCProofBundle(bundle); err != nil {
		t.Fatalf("record NGC proof: %v", err)
	}
}

func TestMonitoringLocalSourcePreservesCPUOnlyProof(t *testing.T) {
	monitoring.ResetNGCProofsForTest()
	t.Cleanup(monitoring.ResetNGCProofsForTest)
	recordTrustGPUProof(t, "cpu-only", false)

	source := &MonitoringLocalSource{NodeID: "local"}
	latest, ok := source.LocalLatest()
	if !ok {
		t.Fatal("expected latest attestation")
	}
	if latest.GPUAvailable {
		t.Fatal("CPU-only proof was falsely reported as GPU available")
	}

	distinct := source.LocalDistinctAttestations()
	if len(distinct) != 1 {
		t.Fatalf("distinct attestations = %d, want 1", len(distinct))
	}
	if distinct[0].GPUAvailable {
		t.Fatal("distinct CPU-only proof was falsely reported as GPU available")
	}
}

func TestMonitoringLocalSourcePreservesGPUProof(t *testing.T) {
	monitoring.ResetNGCProofsForTest()
	t.Cleanup(monitoring.ResetNGCProofsForTest)
	recordTrustGPUProof(t, "gpu-node", true)

	source := &MonitoringLocalSource{NodeID: "local"}
	latest, ok := source.LocalLatest()
	if !ok || !latest.GPUAvailable {
		t.Fatalf("latest GPU availability = %v, ok = %v; want true, true", latest.GPUAvailable, ok)
	}

	distinct := source.LocalDistinctAttestations()
	if len(distinct) != 1 || !distinct[0].GPUAvailable {
		t.Fatalf("distinct attestations = %+v; want one GPU-enabled row", distinct)
	}
}
