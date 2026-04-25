package monitoring

import (
	"strings"
	"testing"
)

func TestPrometheusExposition_containsNvidiaSeries(t *testing.T) {
	s := PrometheusExposition()
	for _, sub := range []string{
		"qsdm_nvidia_lock_http_blocks_total",
		"qsdm_nvidia_lock_p2p_rejects_total",
		"qsdm_ngc_challenge_issued_total",
		"qsdm_ngc_proof_ingest_accepted_total",
		"qsdm_ngc_proof_ingest_rejected_total",
		"# TYPE qsdm_ngc_ingest_nonce_pool_size gauge",
		"qsdm_submesh_p2p_reject_route_total",
		"qsdm_submesh_api_wallet_reject_route_total",
		"qsdm_mesh_companion_publish_total",
		"qsdm_p2p_wallet_ingress_dedupe_skip_total",
		"qsdm_hot_reload_apply_success_total",
		"qsdm_hot_reload_dry_run_total",
		"qsdm_hot_reload_last_dry_run_changed",
	} {
		if !strings.Contains(s, sub) {
			t.Fatalf("exposition missing %q", sub)
		}
	}
}
