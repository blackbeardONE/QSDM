package monitoring

import (
	"strings"
	"sync/atomic"
)

// Counters for POST /api/v1/monitoring/ngc-proof (operator SLOs; does not count GET challenge/list).
var (
	ngcIngestAccepted           atomic.Uint64
	ngcIngestRejectDisabled     atomic.Uint64 // ingest secret unset (404)
	ngcIngestRejectUnauthorized atomic.Uint64
	ngcIngestRejectBodyRead     atomic.Uint64
	ngcIngestRejectBodyTooLarge atomic.Uint64
	ngcIngestRejectInvalidJSON  atomic.Uint64
	ngcIngestRejectMissingCUDA  atomic.Uint64
	ngcIngestRejectNonce        atomic.Uint64
	ngcIngestRejectHMAC         atomic.Uint64
	ngcIngestRejectOther        atomic.Uint64
)

// RecordNGCProofIngestAccepted increments successful proof stores.
func RecordNGCProofIngestAccepted() {
	ngcIngestAccepted.Add(1)
}

// RecordNGCProofIngestRejected increments rejects with a bounded reason key (used for Prometheus labels).
func RecordNGCProofIngestRejected(reason string) {
	switch reason {
	case "ingest_disabled":
		ngcIngestRejectDisabled.Add(1)
	case "unauthorized":
		ngcIngestRejectUnauthorized.Add(1)
	case "body_read":
		ngcIngestRejectBodyRead.Add(1)
	case "body_too_large":
		ngcIngestRejectBodyTooLarge.Add(1)
	case "invalid_json":
		ngcIngestRejectInvalidJSON.Add(1)
	case "missing_cuda_hash":
		ngcIngestRejectMissingCUDA.Add(1)
	case "nonce":
		ngcIngestRejectNonce.Add(1)
	case "hmac":
		ngcIngestRejectHMAC.Add(1)
	default:
		ngcIngestRejectOther.Add(1)
	}
}

// NGCProofIngestRejectReason maps RecordNGCProofBundle* errors to metric reason keys.
func NGCProofIngestRejectReason(err error) string {
	if err == nil {
		return "other"
	}
	s := err.Error()
	switch {
	case strings.Contains(s, "not valid JSON"):
		return "invalid_json"
	case strings.Contains(s, "body size invalid"):
		return "body_too_large"
	case strings.Contains(s, "missing cuda_proof_hash"):
		return "missing_cuda_hash"
	case strings.Contains(s, "ingest nonce"):
		return "nonce"
	case strings.Contains(s, "qsdm_proof_hmac"), strings.Contains(s, "proof_hmac"):
		return "hmac"
	default:
		return "other"
	}
}

// NGCIngestAcceptedTotal returns successful ingests since process start.
func NGCIngestAcceptedTotal() uint64 {
	return ngcIngestAccepted.Load()
}

// NGCIngestRejectedTotal returns all rejected POST ingests since process start.
func NGCIngestRejectedTotal() uint64 {
	return ngcIngestRejectDisabled.Load() +
		ngcIngestRejectUnauthorized.Load() +
		ngcIngestRejectBodyRead.Load() +
		ngcIngestRejectBodyTooLarge.Load() +
		ngcIngestRejectInvalidJSON.Load() +
		ngcIngestRejectMissingCUDA.Load() +
		ngcIngestRejectNonce.Load() +
		ngcIngestRejectHMAC.Load() +
		ngcIngestRejectOther.Load()
}

// NGCIngestStatsMap returns JSON-friendly counters for dashboard and health.
func NGCIngestStatsMap() map[string]interface{} {
	return map[string]interface{}{
		"accepted_total": NGCIngestAcceptedTotal(),
		"rejected_total": NGCIngestRejectedTotal(),
		"rejected_by_reason": map[string]interface{}{
			"ingest_disabled":   ngcIngestRejectDisabled.Load(),
			"unauthorized":      ngcIngestRejectUnauthorized.Load(),
			"body_read":         ngcIngestRejectBodyRead.Load(),
			"body_too_large":    ngcIngestRejectBodyTooLarge.Load(),
			"invalid_json":      ngcIngestRejectInvalidJSON.Load(),
			"missing_cuda_hash": ngcIngestRejectMissingCUDA.Load(),
			"nonce":             ngcIngestRejectNonce.Load(),
			"hmac":              ngcIngestRejectHMAC.Load(),
			"other":             ngcIngestRejectOther.Load(),
		},
	}
}

// ResetNGCIngestMetricsForTest clears ingest counters (tests only).
func ResetNGCIngestMetricsForTest() {
	ngcIngestAccepted.Store(0)
	ngcIngestRejectDisabled.Store(0)
	ngcIngestRejectUnauthorized.Store(0)
	ngcIngestRejectBodyRead.Store(0)
	ngcIngestRejectBodyTooLarge.Store(0)
	ngcIngestRejectInvalidJSON.Store(0)
	ngcIngestRejectMissingCUDA.Store(0)
	ngcIngestRejectNonce.Store(0)
	ngcIngestRejectHMAC.Store(0)
	ngcIngestRejectOther.Store(0)
}

// NGCIngestRejectedLabeled returns (reason, value) pairs for Prometheus exposition (stable order).
func NGCIngestRejectedLabeled() []struct {
	Reason string
	Val    uint64
} {
	return []struct {
		Reason string
		Val    uint64
	}{
		{"ingest_disabled", ngcIngestRejectDisabled.Load()},
		{"unauthorized", ngcIngestRejectUnauthorized.Load()},
		{"body_read", ngcIngestRejectBodyRead.Load()},
		{"body_too_large", ngcIngestRejectBodyTooLarge.Load()},
		{"invalid_json", ngcIngestRejectInvalidJSON.Load()},
		{"missing_cuda_hash", ngcIngestRejectMissingCUDA.Load()},
		{"nonce", ngcIngestRejectNonce.Load()},
		{"hmac", ngcIngestRejectHMAC.Load()},
		{"other", ngcIngestRejectOther.Load()},
	}
}
