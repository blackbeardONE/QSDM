package monitoring

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/branding"
)

const maxNGCProofBytes = 512 * 1024
const maxNGCProofEntries = 32

type ngcStoredProof struct {
	ReceivedAt time.Time
	Raw        json.RawMessage
}

var (
	ngcProofs []ngcStoredProof
	ngcMu     sync.RWMutex
)

func appendNGCProofRawLocked(raw json.RawMessage) {
	ngcProofs = append(ngcProofs, ngcStoredProof{ReceivedAt: time.Now().UTC(), Raw: raw})
	if len(ngcProofs) > maxNGCProofEntries {
		ngcProofs = ngcProofs[len(ngcProofs)-maxNGCProofEntries:]
	}
}

// RecordNGCProofBundle validates JSON and appends to a fixed-size ring buffer.
func RecordNGCProofBundle(data []byte) error {
	if len(data) == 0 || len(data) > maxNGCProofBytes {
		return fmt.Errorf("ngc proof body size invalid")
	}
	var head map[string]interface{}
	if err := json.Unmarshal(data, &head); err != nil {
		return fmt.Errorf("ngc proof is not valid JSON: %w", err)
	}
	if _, ok := head["cuda_proof_hash"]; !ok {
		return fmt.Errorf("ngc proof missing cuda_proof_hash")
	}

	ngcMu.Lock()
	defer ngcMu.Unlock()
	raw := json.RawMessage(make([]byte, len(data)))
	copy(raw, data)
	appendNGCProofRawLocked(raw)
	return nil
}

// RecordNGCProofBundleForIngest validates ingest nonce and HMAC before storing when requireNonce is true (strict ingest for replay resistance).
// When requireNonce is false, behavior matches RecordNGCProofBundle (HMAC is checked only at NVIDIA-lock if configured).
func RecordNGCProofBundleForIngest(data []byte, requireNonce bool, hmacSecret string) error {
	if !requireNonce {
		return RecordNGCProofBundle(data)
	}
	if len(data) == 0 || len(data) > maxNGCProofBytes {
		return fmt.Errorf("ngc proof body size invalid")
	}
	var head map[string]interface{}
	if err := json.Unmarshal(data, &head); err != nil {
		return fmt.Errorf("ngc proof is not valid JSON: %w", err)
	}
	if _, ok := head["cuda_proof_hash"]; !ok {
		return fmt.Errorf("ngc proof missing cuda_proof_hash")
	}
	n := ngcFieldString(head, branding.ProofIngestNonceFieldPreferred, branding.ProofIngestNonceFieldLegacy)
	if !ValidateAndConsumeNGCIngestNonce(strings.TrimSpace(n)) {
		return fmt.Errorf("invalid, expired, or reused ingest nonce; GET /api/v1/monitoring/ngc-challenge with ingest secret")
	}
	if strings.TrimSpace(hmacSecret) == "" || !NGCProofHMACValid(head, hmacSecret) {
		return fmt.Errorf("invalid qsdm_proof_hmac / qsdm_proof_hmac (required with ingest nonce; use v2 payload when nonce is set)")
	}

	ngcMu.Lock()
	defer ngcMu.Unlock()
	raw := json.RawMessage(make([]byte, len(data)))
	copy(raw, data)
	appendNGCProofRawLocked(raw)
	return nil
}

// NGCProofSummaries returns lightweight rows for dashboards (no full GPU fingerprint by default).
func NGCProofSummaries() []map[string]interface{} {
	ngcMu.RLock()
	defer ngcMu.RUnlock()
	out := make([]map[string]interface{}, 0, len(ngcProofs))
	for _, e := range ngcProofs {
		var m map[string]interface{}
		if err := json.Unmarshal(e.Raw, &m); err != nil {
			continue
		}
		row := map[string]interface{}{
			"received_at": e.ReceivedAt.Format(time.RFC3339Nano),
		}
		if v, ok := m["timestamp_utc"]; ok {
			row["timestamp_utc"] = v
		}
		if v, ok := m["cuda_proof_hash"]; ok {
			row["cuda_proof_hash"] = v
		}
		if v, ok := m["replay_computation_hash"]; ok {
			row["replay_computation_hash"] = v
		}
		if ai, ok := m["ai_proof"].(map[string]interface{}); ok {
			row["ai_computation_hash"] = ai["ai_computation_hash"]
			row["ai_mode"] = ai["mode"]
		}
		if tp, ok := m["tensor_proof"].(map[string]interface{}); ok {
			row["tensor_operation_proof"] = tp["tensor_operation_proof"]
			row["tensor_mode"] = tp["mode"]
		}
		if v, ok := m["execution_seconds"]; ok {
			row["execution_seconds"] = v
		}
		out = append(out, row)
	}
	return out
}

// ResetNGCProofsForTest clears the in-memory NGC proof ring buffer (test isolation).
func ResetNGCProofsForTest() {
	ngcMu.Lock()
	defer ngcMu.Unlock()
	ngcProofs = nil
}

// NGCProofNodeAttestation is one row of the "distinct by node id" view
// over the NGC proof ring buffer. Each entry represents the newest
// proof bundle observed for a given `qsdm_node_id` (or
// `qsdm_node_id` legacy alias). Rows whose bundle did not carry a
// node id are grouped under NodeID == "" so the caller can fold them
// into the local node's identity.
//
// This exists so api.TrustAggregator can surface multiple
// CPU-fallback sidecars (each running on a different operator host
// and stamping a different QSDM_NGC_PROOF_NODE_ID) as distinct
// attestation sources instead of collapsing them into a single
// "local" peer row. The previous NGCProofSummaries() path only
// returned the ring buffer in insertion order and did not attempt
// any deduplication.
type NGCProofNodeAttestation struct {
	NodeID          string
	TimestampUTC    time.Time // parsed from bundle["timestamp_utc"] (RFC3339)
	ReceivedAt      time.Time // wall clock at POST time (always set)
	CUDAProofHash   string
	GPUAvailable    bool
	GPUArchitecture string // best-effort parse of bundle["gpu_fingerprint"]
}

// NGCProofDistinctByNodeID walks the ring buffer and returns one
// attestation per distinct qsdm_node_id using the newest row
// seen for each id (newest-wins by TimestampUTC, falling back to
// ReceivedAt when the bundle does not expose a canonical timestamp).
//
// The ordering of the returned slice is arbitrary — callers that
// need deterministic ordering should sort by AttestedAt or NodeID
// themselves. Empty-id rows are preserved as NodeID == "" so the
// caller can map them onto the local node's own identity.
func NGCProofDistinctByNodeID() []NGCProofNodeAttestation {
	ngcMu.RLock()
	defer ngcMu.RUnlock()
	if len(ngcProofs) == 0 {
		return nil
	}
	byID := make(map[string]NGCProofNodeAttestation, len(ngcProofs))
	for _, e := range ngcProofs {
		var m map[string]interface{}
		if err := json.Unmarshal(e.Raw, &m); err != nil {
			continue
		}
		row := NGCProofNodeAttestation{ReceivedAt: e.ReceivedAt}
		if s, ok := m["qsdm_node_id"].(string); ok {
			row.NodeID = strings.TrimSpace(s)
		}
		if row.NodeID == "" {
			if s, ok := m["qsdm_node_id"].(string); ok {
				row.NodeID = strings.TrimSpace(s)
			}
		}
		if s, ok := m["timestamp_utc"].(string); ok {
			if t, err := time.Parse(time.RFC3339, s); err == nil {
				row.TimestampUTC = t.UTC()
			}
		}
		if s, ok := m["cuda_proof_hash"].(string); ok {
			row.CUDAProofHash = s
		}
		if gp, ok := m["gpu_fingerprint"].(map[string]interface{}); ok {
			if av, ok := gp["available"].(bool); ok {
				row.GPUAvailable = av
			}
			if devs, ok := gp["devices"].([]interface{}); ok && len(devs) > 0 {
				if dev, ok := devs[0].(map[string]interface{}); ok {
					if s, ok := dev["name"].(string); ok {
						row.GPUArchitecture = s
					}
				}
			}
		}
		// Newest-wins per node id. Prefer TimestampUTC; if the incoming
		// row has no parseable timestamp, ReceivedAt is authoritative.
		cur, existed := byID[row.NodeID]
		if !existed || rowNewerThan(row, cur) {
			byID[row.NodeID] = row
		}
	}
	out := make([]NGCProofNodeAttestation, 0, len(byID))
	for _, v := range byID {
		out = append(out, v)
	}
	return out
}

// rowNewerThan compares two NGCProofNodeAttestations by best-available
// timestamp: TimestampUTC first, ReceivedAt as tiebreaker / fallback.
func rowNewerThan(a, b NGCProofNodeAttestation) bool {
	at := a.TimestampUTC
	if at.IsZero() {
		at = a.ReceivedAt
	}
	bt := b.TimestampUTC
	if bt.IsZero() {
		bt = b.ReceivedAt
	}
	return at.After(bt)
}
