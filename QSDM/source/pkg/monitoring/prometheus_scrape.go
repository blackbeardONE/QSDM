package monitoring

import (
	"sync"
	"time"
)

var (
	globalScrapeExporter       *PrometheusExporter
	globalScrapeExporterOnce   sync.Once
	scrapeProcessStart         = time.Now()
	scrapeNodeID               string
	scrapeNodeIdentityMu       sync.RWMutex
)

// SetScrapeProcessIdentity sets the node_id label on build_info metrics (call with libp2p host id).
func SetScrapeProcessIdentity(nodeID string) {
	scrapeNodeIdentityMu.Lock()
	scrapeNodeID = nodeID
	scrapeNodeIdentityMu.Unlock()
}

// GlobalScrapePrometheusExporter returns the process-wide exporter used for
// /api/metrics/prometheus exposition and optional extra RegisterCollector calls
// (e.g. from the node or dashboard MetricsSource).
func GlobalScrapePrometheusExporter() *PrometheusExporter {
	globalScrapeExporterOnce.Do(func() {
		globalScrapeExporter = NewPrometheusExporter()
		globalScrapeExporter.RegisterCollector("qsdmplus_core", corePrometheusMetrics)
		globalScrapeExporter.RegisterCollector("qsdmplus_process", scrapeProcessMetaMetrics)
	})
	return globalScrapeExporter
}

// PrometheusExposition returns OpenMetrics text using the global scrape exporter
// so scrape output and per-collector extensions share one registry.
func PrometheusExposition() string {
	return GlobalScrapePrometheusExporter().Render()
}

func corePrometheusMetrics() []Metric {
	m := GetMetrics()
	m.mu.RLock()
	tp := m.TransactionsProcessed
	tv := m.TransactionsValid
	ti := m.TransactionsInvalid
	ts := m.TransactionsStored
	nms := m.NetworkMessagesSent
	nmr := m.NetworkMessagesRecv
	hrS := m.HotReloadApplySuccess
	hrF := m.HotReloadApplyFailure
	hrD := m.HotReloadDryRunTotal
	hrAt := m.LastHotReloadDryRunAt
	hrCh := m.LastHotReloadDryRunChanged
	hrPOK := m.LastHotReloadDryRunPolicyOK
	hrLOK := m.LastHotReloadDryRunLoadOK
	m.mu.RUnlock()

	var out []Metric
	add := func(name, help string, typ MetricType, v float64, labels map[string]string) {
		out = append(out, Metric{Name: name, Help: help, Type: typ, Value: v, Labels: labels})
	}

	add("qsdmplus_nvidia_lock_http_blocks_total", "State-changing HTTP API calls blocked by NVIDIA-lock (403).", MetricCounter, float64(NvidiaLockHTTPBlockCount()), nil)
	add("qsdmplus_nvidia_lock_p2p_rejects_total", "P2P transactions dropped when nvidia_lock_gate_p2p is enabled and no qualifying proof.", MetricCounter, float64(NvidiaLockP2PRejectCount()), nil)
	add("qsdmplus_ngc_challenge_issued_total", "Successful GET /monitoring/ngc-challenge responses.", MetricCounter, float64(NGCChallengeIssuedCount()), nil)
	add("qsdmplus_ngc_challenge_rate_limited_total", "429 rate-limit responses on ngc-challenge.", MetricCounter, float64(NGCChallengeRateLimitedCount()), nil)
	add("qsdmplus_ngc_ingest_nonce_pool_size", "Tracked ingest nonces (approximate pool size).", MetricGauge, float64(NGCIngestNoncePoolSize()), nil)
	add("qsdmplus_ngc_proof_ingest_accepted_total", "Successful POST /monitoring/ngc-proof (bundle stored).", MetricCounter, float64(NGCIngestAcceptedTotal()), nil)
	for _, p := range NGCIngestRejectedLabeled() {
		add("qsdmplus_ngc_proof_ingest_rejected_total", "Rejected POST /monitoring/ngc-proof by reason.", MetricCounter, float64(p.Val), map[string]string{"reason": p.Reason})
	}
	add("qsdmplus_submesh_p2p_reject_route_total", "P2P txs dropped: submesh fee/geotag did not match (when submesh_config loaded).", MetricCounter, float64(SubmeshP2PRejectRouteCount()), nil)
	add("qsdmplus_submesh_p2p_reject_size_total", "P2P txs dropped: exceeded matched submesh max_tx_size.", MetricCounter, float64(SubmeshP2PRejectSizeCount()), nil)
	add("qsdmplus_submesh_api_wallet_reject_route_total", "API wallet send rejected by submesh (422): no route.", MetricCounter, float64(SubmeshAPIWalletRejectRouteCount()), nil)
	add("qsdmplus_submesh_api_wallet_reject_size_total", "API wallet send rejected by submesh (422): max_tx_size.", MetricCounter, float64(SubmeshAPIWalletRejectSizeCount()), nil)
	add("qsdmplus_submesh_api_privileged_reject_size_total", "API mint/token-create rejected by submesh (422): strictest max_tx_size.", MetricCounter, float64(SubmeshAPIPrivilegedRejectSizeCount()), nil)
	add("qsdmplus_mesh_companion_publish_total", "Extra mesh wire (qsdm_mesh3d_v1) gossip publishes after wallet JSON (companion path).", MetricCounter, float64(MeshCompanionPublishCount()), nil)
	add("qsdmplus_p2p_wallet_ingress_dedupe_skip_total", "Inbound P2P drops: same wallet tx id already ingested (mesh+JSON dedupe).", MetricCounter, float64(P2PWalletIngressDedupeSkipCount()), nil)
	add("qsdmplus_transactions_processed_total", "Transactions seen on the network handler.", MetricCounter, float64(tp), nil)
	add("qsdmplus_transactions_valid_total", "Transactions that passed validation before storage.", MetricCounter, float64(tv), nil)
	add("qsdmplus_transactions_invalid_total", "Transactions rejected or dropped before storage.", MetricCounter, float64(ti), nil)
	add("qsdmplus_transactions_stored_total", "Transactions persisted to storage.", MetricCounter, float64(ts), nil)
	add("qsdmplus_network_messages_sent_total", "Outbound network messages.", MetricCounter, float64(nms), nil)
	add("qsdmplus_network_messages_received_total", "Inbound network messages.", MetricCounter, float64(nmr), nil)
	add("qsdmplus_hot_reload_apply_success_total", "Successful hot-reload apply attempts.", MetricCounter, float64(hrS), nil)
	add("qsdmplus_hot_reload_apply_failure_total", "Failed hot-reload apply attempts.", MetricCounter, float64(hrF), nil)
	add("qsdmplus_hot_reload_dry_run_total", "Admin or poller hot-reload dry-run invocations.", MetricCounter, float64(hrD), nil)
	tsVal := 0.0
	if !hrAt.IsZero() {
		tsVal = float64(hrAt.Unix())
	}
	add("qsdmplus_hot_reload_last_dry_run_timestamp", "Unix time of last hot-reload dry-run (0 if none).", MetricGauge, tsVal, nil)
	add("qsdmplus_hot_reload_last_dry_run_changed", "Whether last dry-run saw file change (0/1).", MetricGauge, boolGaugeFloat(hrCh), nil)
	add("qsdmplus_hot_reload_last_dry_run_policy_ok", "Whether last dry-run passed policy (0/1).", MetricGauge, boolGaugeFloat(hrPOK), nil)
	add("qsdmplus_hot_reload_last_dry_run_load_ok", "Whether last dry-run loaded config OK (0/1).", MetricGauge, boolGaugeFloat(hrLOK), nil)
	return out
}

func boolGaugeFloat(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

func scrapeProcessMetaMetrics() []Metric {
	scrapeNodeIdentityMu.RLock()
	nid := scrapeNodeID
	scrapeNodeIdentityMu.RUnlock()
	out := []Metric{
		{Name: "qsdmplus_process_uptime_seconds", Help: "Node process uptime in seconds.", Type: MetricGauge, Value: time.Since(scrapeProcessStart).Seconds(), Labels: nil},
	}
	if nid != "" {
		out = append(out, Metric{
			Name:   "qsdmplus_build_info",
			Help:   "Labeled node identity for scrape grouping (value is always 1).",
			Type:   MetricGauge,
			Value:  1,
			Labels: map[string]string{"node_id": nid},
		})
	}
	return out
}
