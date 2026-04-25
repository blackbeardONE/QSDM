package monitoring

import (
	"encoding/json"
	"testing"
	"time"
)

// helper: build a minimal valid NGC proof bundle as JSON bytes.
func makeBundle(nodeID string, ts time.Time, extra map[string]interface{}) []byte {
	m := map[string]interface{}{
		"cuda_proof_hash": "deadbeef",
	}
	if nodeID != "" {
		m["qsdm_node_id"] = nodeID
	}
	if !ts.IsZero() {
		m["timestamp_utc"] = ts.UTC().Format(time.RFC3339)
	}
	for k, v := range extra {
		m[k] = v
	}
	b, _ := json.Marshal(m)
	return b
}

func TestNGCProofDistinctByNodeID_NewestWins(t *testing.T) {
	t.Cleanup(ResetNGCProofsForTest)
	ResetNGCProofsForTest()

	// Truncate to second precision: RFC3339 (no fractional seconds) is
	// the format NGC bundles store on the wire, so parse-back loses
	// any sub-second residual on the local clock.
	now := time.Now().UTC().Truncate(time.Second)
	older := now.Add(-10 * time.Minute)
	newer := now.Add(-1 * time.Minute)

	// Node "alpha" has two bundles — newer should win.
	if err := RecordNGCProofBundle(makeBundle("alpha", older, nil)); err != nil {
		t.Fatalf("record alpha older: %v", err)
	}
	if err := RecordNGCProofBundle(makeBundle("alpha", newer, nil)); err != nil {
		t.Fatalf("record alpha newer: %v", err)
	}
	// Node "beta" has one bundle — should appear as its own row.
	betaTs := now.Add(-3 * time.Minute) // already second-truncated via `now`
	if err := RecordNGCProofBundle(makeBundle("beta", betaTs, nil)); err != nil {
		t.Fatalf("record beta: %v", err)
	}

	rows := NGCProofDistinctByNodeID()
	if len(rows) != 2 {
		t.Fatalf("len(rows)=%d, want 2; rows=%+v", len(rows), rows)
	}
	seen := map[string]NGCProofNodeAttestation{}
	for _, r := range rows {
		seen[r.NodeID] = r
	}
	a, ok := seen["alpha"]
	if !ok {
		t.Fatalf("alpha row missing; got %+v", seen)
	}
	if !a.TimestampUTC.Equal(newer) {
		t.Errorf("alpha TimestampUTC=%s, want %s (newest-wins)", a.TimestampUTC, newer)
	}
	b, ok := seen["beta"]
	if !ok {
		t.Fatalf("beta row missing; got %+v", seen)
	}
	if !b.TimestampUTC.Equal(betaTs) {
		t.Errorf("beta TimestampUTC=%s, want %s", b.TimestampUTC, betaTs)
	}
}

func TestNGCProofDistinctByNodeID_EmptyNodeIDGroup(t *testing.T) {
	t.Cleanup(ResetNGCProofsForTest)
	ResetNGCProofsForTest()

	now := time.Now().UTC()
	// Two bundles, neither carrying a node id. Both should collapse
	// into a single empty-id row (caller folds into local identity).
	if err := RecordNGCProofBundle(makeBundle("", now.Add(-5*time.Minute), nil)); err != nil {
		t.Fatalf("record anon 1: %v", err)
	}
	if err := RecordNGCProofBundle(makeBundle("", now.Add(-1*time.Minute), nil)); err != nil {
		t.Fatalf("record anon 2: %v", err)
	}

	rows := NGCProofDistinctByNodeID()
	if len(rows) != 1 {
		t.Fatalf("len(rows)=%d, want 1 (both collapse on empty id); rows=%+v", len(rows), rows)
	}
	if rows[0].NodeID != "" {
		t.Errorf("NodeID=%q, want empty", rows[0].NodeID)
	}
}

func TestNGCProofDistinctByNodeID_LegacyQSDMAlias(t *testing.T) {
	t.Cleanup(ResetNGCProofsForTest)
	ResetNGCProofsForTest()

	// Bundle uses the legacy qsdm_node_id field (no qsdm_ prefix).
	// The distinct view should still pick it up.
	b := map[string]interface{}{
		"cuda_proof_hash": "cafebabe",
		"qsdm_node_id":    "legacy-node-42",
		"timestamp_utc":   time.Now().UTC().Format(time.RFC3339),
	}
	raw, _ := json.Marshal(b)
	if err := RecordNGCProofBundle(raw); err != nil {
		t.Fatalf("record legacy bundle: %v", err)
	}
	rows := NGCProofDistinctByNodeID()
	if len(rows) != 1 || rows[0].NodeID != "legacy-node-42" {
		t.Fatalf("legacy alias not honoured; rows=%+v", rows)
	}
}

func TestNGCProofDistinctByNodeID_EmptyBuffer(t *testing.T) {
	t.Cleanup(ResetNGCProofsForTest)
	ResetNGCProofsForTest()

	if rows := NGCProofDistinctByNodeID(); rows != nil {
		t.Errorf("empty ring buffer should return nil; got %+v", rows)
	}
}

func TestNGCProofDistinctByNodeID_GPUFingerprintExtracted(t *testing.T) {
	t.Cleanup(ResetNGCProofsForTest)
	ResetNGCProofsForTest()

	b := makeBundle("alpha", time.Now().UTC(), map[string]interface{}{
		"gpu_fingerprint": map[string]interface{}{
			"available": true,
			"devices": []interface{}{
				map[string]interface{}{
					"name":               "NVIDIA GeForce RTX 3050",
					"driver_version":     "575.00",
					"compute_capability": "8.6",
				},
			},
		},
	})
	if err := RecordNGCProofBundle(b); err != nil {
		t.Fatalf("record: %v", err)
	}
	rows := NGCProofDistinctByNodeID()
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}
	r := rows[0]
	if !r.GPUAvailable {
		t.Error("GPUAvailable should be true")
	}
	if r.GPUArchitecture != "NVIDIA GeForce RTX 3050" {
		t.Errorf("GPUArchitecture=%q, want 'NVIDIA GeForce RTX 3050'", r.GPUArchitecture)
	}
}
