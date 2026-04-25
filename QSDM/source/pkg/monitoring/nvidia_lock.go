package monitoring

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/branding"
)

// NvidiaLockProofOK reports whether a recently ingested NGC proof satisfies NVIDIA-lock policy.
// Policy: proof received within maxAge, JSON field architecture (case-insensitive) contains "nvidia",
// and gpu_fingerprint.available is true (GPU reported by sidecar, typically via nvidia-smi in container).
// If expectedNodeID is non-empty, the proof JSON must carry string field qsdm_node_id (or the legacy
// alias qsdm_node_id) with value (after trim) equal to expectedNodeID (after trim), so bundles
// can be bound to a specific node.
// If proofHMACSecret is non-empty, qsdm_proof_hmac (or legacy qsdm_proof_hmac) must be a valid
// HMAC-SHA256 over NGCProofHMACPayload.
// If consumeMatching is true, the first qualifying proof is removed from the ring (one state-changing
// API use per proof when ingest nonces are required).
func NvidiaLockProofOK(maxAge time.Duration, expectedNodeID, proofHMACSecret string, consumeMatching bool) (ok bool, detail string) {
	if maxAge <= 0 {
		maxAge = 15 * time.Minute
	}
	wantID := strings.TrimSpace(expectedNodeID)
	now := time.Now().UTC()

	ngcMu.Lock()
	defer ngcMu.Unlock()

	if len(ngcProofs) == 0 {
		return false, "NVIDIA lock: no NGC proof bundles ingested; run the NGC sidecar with QSDM_NGC_REPORT_URL (legacy QSDM_NGC_REPORT_URL still accepted) and matching ingest secret"
	}

	for i := len(ngcProofs) - 1; i >= 0; i-- {
		e := ngcProofs[i]
		if now.Sub(e.ReceivedAt) > maxAge {
			continue
		}
		var m map[string]interface{}
		if err := json.Unmarshal(e.Raw, &m); err != nil {
			continue
		}
		arch, _ := m["architecture"].(string)
		if !strings.Contains(strings.ToLower(arch), "nvidia") {
			continue
		}
		gf, _ := m["gpu_fingerprint"].(map[string]interface{})
		if gf == nil {
			continue
		}
		avail, _ := gf["available"].(bool)
		if !avail {
			continue
		}
		if wantID != "" {
			got := ngcFieldString(m, branding.ProofNodeIDFieldPreferred, branding.ProofNodeIDFieldLegacy)
			if strings.TrimSpace(got) != wantID {
				continue
			}
		}
		if !NGCProofHMACValid(m, proofHMACSecret) {
			continue
		}
		if consumeMatching {
			ngcProofs = append(ngcProofs[:i], ngcProofs[i+1:]...)
		}
		return true, ""
	}

	if wantID != "" {
		return false, "NVIDIA lock: no qualifying proof within window with matching qsdm_node_id / qsdm_node_id (set QSDM_NGC_PROOF_NODE_ID on the sidecar to match QSDM_NVIDIA_LOCK_EXPECTED_NODE_ID on the node; QSDM_* names remain accepted, plus GPU attestation as usual)"
	}
	if strings.TrimSpace(proofHMACSecret) != "" {
		return false, "NVIDIA lock: no qualifying proof with valid qsdm_proof_hmac / qsdm_proof_hmac (set QSDM_NGC_PROOF_HMAC_SECRET on the sidecar to match QSDM_NVIDIA_LOCK_PROOF_HMAC_SECRET on the node; QSDM_* names remain accepted)"
	}
	return false, "NVIDIA lock: no qualifying proof within window (need GPU-attested bundle: architecture mentions NVIDIA and gpu_fingerprint.available=true); use GPU profile sidecar or widen QSDM_NVIDIA_LOCK_MAX_PROOF_AGE (legacy QSDM_NVIDIA_LOCK_MAX_PROOF_AGE still accepted)"
}
