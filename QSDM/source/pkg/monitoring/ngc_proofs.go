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
		return fmt.Errorf("invalid qsdm_proof_hmac / qsdmplus_proof_hmac (required with ingest nonce; use v2 payload when nonce is set)")
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
