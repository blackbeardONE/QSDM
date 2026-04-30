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
		globalScrapeExporter.RegisterCollector("qsdm_core", corePrometheusMetrics)
		globalScrapeExporter.RegisterCollector("qsdm_process", scrapeProcessMetaMetrics)
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

	add("qsdm_nvidia_lock_http_blocks_total", "State-changing HTTP API calls blocked by NVIDIA-lock (403).", MetricCounter, float64(NvidiaLockHTTPBlockCount()), nil)
	add("qsdm_nvidia_lock_p2p_rejects_total", "P2P transactions dropped when nvidia_lock_gate_p2p is enabled and no qualifying proof.", MetricCounter, float64(NvidiaLockP2PRejectCount()), nil)
	add("qsdm_ngc_challenge_issued_total", "Successful GET /monitoring/ngc-challenge responses.", MetricCounter, float64(NGCChallengeIssuedCount()), nil)
	add("qsdm_ngc_challenge_rate_limited_total", "429 rate-limit responses on ngc-challenge.", MetricCounter, float64(NGCChallengeRateLimitedCount()), nil)
	add("qsdm_ngc_ingest_nonce_pool_size", "Tracked ingest nonces (approximate pool size).", MetricGauge, float64(NGCIngestNoncePoolSize()), nil)
	add("qsdm_ngc_proof_ingest_accepted_total", "Successful POST /monitoring/ngc-proof (bundle stored).", MetricCounter, float64(NGCIngestAcceptedTotal()), nil)
	for _, p := range NGCIngestRejectedLabeled() {
		add("qsdm_ngc_proof_ingest_rejected_total", "Rejected POST /monitoring/ngc-proof by reason.", MetricCounter, float64(p.Val), map[string]string{"reason": p.Reason})
	}
	add("qsdm_submesh_p2p_reject_route_total", "P2P txs dropped: submesh fee/geotag did not match (when submesh_config loaded).", MetricCounter, float64(SubmeshP2PRejectRouteCount()), nil)
	add("qsdm_submesh_p2p_reject_size_total", "P2P txs dropped: exceeded matched submesh max_tx_size.", MetricCounter, float64(SubmeshP2PRejectSizeCount()), nil)
	add("qsdm_submesh_api_wallet_reject_route_total", "API wallet send rejected by submesh (422): no route.", MetricCounter, float64(SubmeshAPIWalletRejectRouteCount()), nil)
	add("qsdm_submesh_api_wallet_reject_size_total", "API wallet send rejected by submesh (422): max_tx_size.", MetricCounter, float64(SubmeshAPIWalletRejectSizeCount()), nil)
	add("qsdm_submesh_api_privileged_reject_size_total", "API mint/token-create rejected by submesh (422): strictest max_tx_size.", MetricCounter, float64(SubmeshAPIPrivilegedRejectSizeCount()), nil)
	add("qsdm_mesh_companion_publish_total", "Extra mesh wire (qsdm_mesh3d_v1) gossip publishes after wallet JSON (companion path).", MetricCounter, float64(MeshCompanionPublishCount()), nil)
	add("qsdm_p2p_wallet_ingress_dedupe_skip_total", "Inbound P2P drops: same wallet tx id already ingested (mesh+JSON dedupe).", MetricCounter, float64(P2PWalletIngressDedupeSkipCount()), nil)
	add("qsdm_transactions_processed_total", "Transactions seen on the network handler.", MetricCounter, float64(tp), nil)
	add("qsdm_transactions_valid_total", "Transactions that passed validation before storage.", MetricCounter, float64(tv), nil)
	add("qsdm_transactions_invalid_total", "Transactions rejected or dropped before storage.", MetricCounter, float64(ti), nil)
	add("qsdm_transactions_stored_total", "Transactions persisted to storage.", MetricCounter, float64(ts), nil)
	add("qsdm_network_messages_sent_total", "Outbound network messages.", MetricCounter, float64(nms), nil)
	add("qsdm_network_messages_received_total", "Inbound network messages.", MetricCounter, float64(nmr), nil)
	add("qsdm_hot_reload_apply_success_total", "Successful hot-reload apply attempts.", MetricCounter, float64(hrS), nil)
	add("qsdm_hot_reload_apply_failure_total", "Failed hot-reload apply attempts.", MetricCounter, float64(hrF), nil)
	add("qsdm_hot_reload_dry_run_total", "Admin or poller hot-reload dry-run invocations.", MetricCounter, float64(hrD), nil)
	tsVal := 0.0
	if !hrAt.IsZero() {
		tsVal = float64(hrAt.Unix())
	}
	add("qsdm_hot_reload_last_dry_run_timestamp", "Unix time of last hot-reload dry-run (0 if none).", MetricGauge, tsVal, nil)
	add("qsdm_hot_reload_last_dry_run_changed", "Whether last dry-run saw file change (0/1).", MetricGauge, boolGaugeFloat(hrCh), nil)
	add("qsdm_hot_reload_last_dry_run_policy_ok", "Whether last dry-run passed policy (0/1).", MetricGauge, boolGaugeFloat(hrPOK), nil)
	add("qsdm_hot_reload_last_dry_run_load_ok", "Whether last dry-run loaded config OK (0/1).", MetricGauge, boolGaugeFloat(hrLOK), nil)

	// ---- v2 slashing pipeline ----------------------------------
	// These counters/gauges instrument pkg/chain/SlashApplier.
	// Cardinality stays bounded: kind labels come from a fixed
	// 4-element enum, reason labels from fixed enums of <=10
	// values each.
	for _, p := range SlashAppliedLabeled() {
		add("qsdm_slash_applied_total",
			"Successful slash transactions applied, by EvidenceKind.",
			MetricCounter, float64(p.Val), map[string]string{"kind": p.Kind})
	}
	for _, p := range SlashDrainedDustLabeled() {
		add("qsdm_slash_drained_dust_total",
			"Total dust forfeited by successful slashes, by EvidenceKind.",
			MetricCounter, float64(p.Val), map[string]string{"kind": p.Kind})
	}
	add("qsdm_slash_rewarded_dust_total",
		"Cumulative dust paid to slashers as RewardBPS share of forfeited stake.",
		MetricCounter, float64(SlashRewardedDustTotal()), nil)
	add("qsdm_slash_burned_dust_total",
		"Cumulative dust burned (drained but not paid to a slasher).",
		MetricCounter, float64(SlashBurnedDustTotal()), nil)
	for _, p := range SlashRejectedLabeled() {
		add("qsdm_slash_rejected_total",
			"Slash transactions rejected before any state mutation, by reason.",
			MetricCounter, float64(p.Val), map[string]string{"reason": p.Reason})
	}
	for _, p := range SlashAutoRevokedLabeled() {
		add("qsdm_slash_auto_revoked_total",
			"Records auto-revoked by SlashApplier post-slash, by reason.",
			MetricCounter, float64(p.Val), map[string]string{"reason": p.Reason})
	}

	// ---- v2 attestation arch-spoof rejection (§4.6) -------------
	// Counters for the closed-enum allowlist (unknown_arch) and
	// the arch <-> gpu_name cross-check (gpu_name_mismatch). See
	// pkg/mining/attest/archcheck and the rewritten
	// MINING_PROTOCOL_V2.md §4.6 for the rejection model.
	for _, p := range ArchSpoofRejectedLabeled() {
		add("qsdm_attest_archspoof_rejected_total",
			"v2 proofs rejected by the arch-spoof gate (§4.6), by reason.",
			MetricCounter, float64(p.Val), map[string]string{"reason": p.Reason})
	}
	for _, p := range HashrateRejectedLabeled() {
		add("qsdm_attest_hashrate_rejected_total",
			"v2 proofs rejected because Attestation.ClaimedHashrateHPS is outside the per-arch HashrateBand (§4.6).",
			MetricCounter, float64(p.Val), map[string]string{"arch": p.Arch})
	}

	// ---- v2 recent-rejection ring truncation telemetry ---------
	// Surfaces how often the in-memory ring's defensive rune
	// caps (200 for Detail, 256 for GPUName / CertSubject) fire.
	// Operators tuning the caps query
	// rate(qsdm_attest_rejection_field_truncated_total{field="detail"}[5m])
	// /
	// rate(qsdm_attest_rejection_field_runes_observed_total{field="detail"}[5m])
	// for the truncation rate, and the runes_max gauge for the
	// "how close are we?" headroom signal. See pkg/mining/attest/recentrejects.
	for _, p := range recentRejectFieldsLabeled() {
		add("qsdm_attest_rejection_field_runes_observed_total",
			"Total non-empty observations of a recent-rejection ring field (denominator for the truncation rate).",
			MetricCounter, float64(p.Observed), map[string]string{"field": p.Field})
		add("qsdm_attest_rejection_field_truncated_total",
			"Recent-rejection ring observations where the pre-truncation rune count exceeded the in-store cap (numerator for the truncation rate).",
			MetricCounter, float64(p.Truncated), map[string]string{"field": p.Field})
		add("qsdm_attest_rejection_field_runes_max",
			"Process-lifetime monotonic max of the pre-truncation rune count for the recent-rejection ring field. Resets only on process restart.",
			MetricGauge, float64(p.RunesMax), map[string]string{"field": p.Field})
	}
	// On-disk persister durability: increments on every
	// FilePersister.Append failure (disk full, permission flap,
	// compaction error). Operators alert on rate > 0; the
	// in-memory ring continues to receive records regardless.
	add("qsdm_attest_rejection_persist_errors_total",
		"On-disk persister failures observed by the recent-rejection ring (Append / compaction). The in-memory ring is unaffected; this measures forensic-record durability only.",
		MetricCounter, float64(recentRejectPersistErrorsCount()), nil)
	// Soft-cap compaction rate: increments each time the
	// FilePersister rewrites the JSONL log to enforce its
	// soft-cap. A sustained high rate (alert >5/min for 30m)
	// indicates a miner is filling the ring faster than the
	// soft-cap can absorb — independent signal from the
	// per-field truncation-rate alert.
	add("qsdm_attest_rejection_persist_compactions_total",
		"Successful soft-cap compactions performed by the recent-rejection ring's FilePersister. Sustained high rate suggests rejection-rate flooding.",
		MetricCounter, float64(recentRejectPersistCompactionsCount()), nil)
	// On-disk record gauge: best-effort current size of the
	// JSONL log in records. Updated at boot, after every
	// Append, and after every compaction. Approximate during
	// concurrent reads — operators reading this alongside
	// the compactions counter should treat ±softCap as the
	// uncertainty window.
	add("qsdm_attest_rejection_persist_records_on_disk",
		"Best-effort gauge of the recent-rejection ring's on-disk JSONL record count.",
		MetricGauge, float64(recentRejectPersistRecordsOnDisk()), nil)
	// Hard-cap drop counter: increments when the
	// FilePersister refuses an Append because admitting it
	// would breach the configured byte ceiling AND a salvage
	// in-band compaction failed to free enough headroom. ANY
	// non-zero rate is anomalous (the soft-cap loop is sized
	// to keep the file well under the hard cap on realistic
	// traffic), so operators alert rate(...) > 0 sustained
	// 10m as a flood-active signal independent of the
	// compactions-rate alert. The in-memory ring is
	// unaffected — only the on-disk forensic record is dropped.
	add("qsdm_attest_rejection_persist_hardcap_drops_total",
		"Records the recent-rejection ring's FilePersister refused to write because admitting them would exceed the configured hard byte cap. The in-memory ring is unaffected; only the on-disk forensic record is shed.",
		MetricCounter, float64(recentRejectPersistHardCapDropsCount()), nil)
	// Per-miner rate-limit drop counter. Fires BEFORE the
	// record reaches the ring or the persister: a single
	// miner's token bucket is exhausted and Store.Record
	// drops the record at entry. Distinct from the hard-cap
	// drops above which fire after admission. Operators
	// alert rate(...) > 0 sustained 10m as a "single bad
	// actor flooding" signal — the dashboard's "top
	// offenders" strip then identifies the miner_addr (the
	// metric itself stays unlabeled to keep cardinality
	// bounded against a fast-rotating attacker).
	add("qsdm_attest_rejection_per_miner_rate_limited_total",
		"Records dropped at recent-rejection ring entry by the per-miner token-bucket limiter. Indicates a single miner exhausted their per-miner rate budget.",
		MetricCounter, float64(recentRejectPerMinerRateLimitedCount()), nil)

	// ---- v2 enrollment registry --------------------------------
	add("qsdm_enrollment_applied_total",
		"Successful qsdm/enroll/v1 applications.",
		MetricCounter, float64(EnrollmentAppliedTotal()), nil)
	add("qsdm_unenrollment_applied_total",
		"Successful qsdm/unenroll/v1 applications (operator-initiated).",
		MetricCounter, float64(UnenrollmentAppliedTotal()), nil)
	add("qsdm_enrollment_unbond_swept_total",
		"Records released to owners by SweepMaturedUnbonds (counts both natural unbond and post-slash auto-revoke).",
		MetricCounter, float64(EnrollmentUnbondSweptTotal()), nil)
	for _, p := range EnrollmentRejectedLabeled() {
		add("qsdm_enrollment_rejected_total",
			"qsdm/enroll/v1 transactions rejected, by reason.",
			MetricCounter, float64(p.Val), map[string]string{"reason": p.Reason})
	}
	for _, p := range UnenrollmentRejectedLabeled() {
		add("qsdm_unenrollment_rejected_total",
			"qsdm/unenroll/v1 transactions rejected, by reason.",
			MetricCounter, float64(p.Val), map[string]string{"reason": p.Reason})
	}
	add("qsdm_enrollment_active_count",
		"Currently enrolled (Active) miners, point-in-time.",
		MetricGauge, float64(EnrollmentStateActiveCount()), nil)
	add("qsdm_enrollment_bonded_dust",
		"Total stake dust currently bonded across Active records, point-in-time.",
		MetricGauge, float64(EnrollmentStateBondedDust()), nil)
	add("qsdm_enrollment_pending_unbond_count",
		"Records in the unbond window (revoked, awaiting sweep), point-in-time.",
		MetricGauge, float64(EnrollmentStatePendingUnbondCount()), nil)
	add("qsdm_enrollment_pending_unbond_dust",
		"Stake dust locked in pending-unbond records, point-in-time.",
		MetricGauge, float64(EnrollmentStatePendingUnbondDust()), nil)

	// ---- v2 governance parameter pipeline ----------------------
	// Counters keyed by param name; param-set is a tightly
	// bounded enum (currently {reward_bps, auto_revoke_min_stake_dust})
	// so cardinality is fine.
	for _, p := range GovStagedLabeled() {
		add("qsdm_gov_param_staged_total",
			"qsdm/gov/v1 param-set transactions accepted (staged for activation), by param.",
			MetricCounter, float64(p.Val), map[string]string{"param": p.Param})
	}
	for _, p := range GovActivatedLabeled() {
		add("qsdm_gov_param_activated_total",
			"qsdm/gov/v1 staged changes promoted to active by Promote(), by param.",
			MetricCounter, float64(p.Val), map[string]string{"param": p.Param})
	}
	for _, p := range GovParamValueLabeled() {
		add("qsdm_gov_param_value",
			"Currently-active value for each governance-tunable parameter.",
			MetricGauge, float64(p.Val), map[string]string{"param": p.Param})
	}
	for _, p := range GovRejectedLabeled() {
		add("qsdm_gov_param_rejected_total",
			"qsdm/gov/v1 param-set transactions rejected before staging, by reason.",
			MetricCounter, float64(p.Val), map[string]string{"reason": p.Reason})
	}

	// ---- v2 governance authority-rotation pipeline -------------
	// op label is bounded to {add, remove, other}; the gauge is
	// a single time series. Total cardinality across this block:
	// 3 ops × 3 counters + 1 gauge = 10 series, well-bounded.
	for _, p := range GovAuthorityVotedLabeled() {
		add("qsdm_gov_authority_voted_total",
			"qsdm/gov/v1 authority-rotation votes recorded, by op (add|remove|other).",
			MetricCounter, float64(p.Val), map[string]string{"op": p.Op})
	}
	for _, p := range GovAuthorityCrossedLabeled() {
		add("qsdm_gov_authority_crossed_total",
			"qsdm/gov/v1 authority-rotation proposals that crossed the M-of-N threshold, by op.",
			MetricCounter, float64(p.Val), map[string]string{"op": p.Op})
	}
	for _, p := range GovAuthorityActivatedLabeled() {
		add("qsdm_gov_authority_activated_total",
			"qsdm/gov/v1 authority-rotation proposals activated by Promote(), by op.",
			MetricCounter, float64(p.Val), map[string]string{"op": p.Op})
	}
	add("qsdm_gov_authority_count",
		"Current size of the active AuthorityList (number of multisig members).",
		MetricGauge, float64(AuthorityCountGauge()), nil)

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
		{Name: "qsdm_process_uptime_seconds", Help: "Node process uptime in seconds.", Type: MetricGauge, Value: time.Since(scrapeProcessStart).Seconds(), Labels: nil},
	}
	if nid != "" {
		out = append(out, Metric{
			Name:   "qsdm_build_info",
			Help:   "Labeled node identity for scrape grouping (value is always 1).",
			Type:   MetricGauge,
			Value:  1,
			Labels: map[string]string{"node_id": nid},
		})
	}
	return out
}
