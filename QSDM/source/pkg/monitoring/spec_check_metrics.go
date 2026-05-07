package monitoring

// spec_check_metrics.go: Prometheus telemetry for the
// Tier-2 GPU-spec advisory checker
// (pkg/mining/telemetrycheck). Exports the data the
// operator alert dashboard needs to track:
//
//   qsdm_spec_check_catalog_entries          gauge — total entries across all signers
//   qsdm_spec_check_catalog_signers          gauge — distinct signer ids in the catalog
//   qsdm_spec_check_catalog_skus             gauge — distinct SKU names in the catalog
//   qsdm_spec_check_checked_total            counter — every Check call
//   qsdm_spec_check_match_total              counter — verdict == match
//   qsdm_spec_check_mismatch_total           counter — verdict == mismatch
//   qsdm_spec_check_unknown_sku_total        counter — verdict == unknown_sku
//   qsdm_spec_check_skipped_total            counter — verdict == skipped (catalog empty etc.)
//   qsdm_spec_check_mismatch_field_total{field} counter — per-rule firing
//
// Wiring: pkg/monitoring receives a SpecCheckProbe via
// SetSpecCheckProbe() at validator boot. When unset, the
// collector emits NOTHING (zero-cardinality fallback) so a
// pre-Tier-2 deployment's /metrics surface is bit-identical
// to before. Probe is held by atomic.Pointer so the metrics
// scrape goroutine can swap it in cheaply at boot without
// blocking on a mutex.

import (
	"sort"
	"sync/atomic"
)

// SpecCheckProbe is the read-only interface pkg/monitoring
// requires from the validator's spec-check wiring. Keeps
// this package decoupled from pkg/mining/telemetrycheck —
// the implementation in cmd/qsdm/spec_check.go satisfies
// it without us importing telemetrycheck here.
type SpecCheckProbe interface {
	// CatalogCounters returns (totalEntries, signers, skus).
	CatalogCounters() (int, int, int)

	// CheckCounters returns the cumulative verdict
	// counters in (checked, matched, mismatched,
	// unknown_sku, skipped) order. Matching the public
	// /api/v1/mining/spec-anomalies field order keeps the
	// metrics emitter and the JSON endpoint trivially
	// symmetric.
	CheckCounters() (uint64, uint64, uint64, uint64, uint64)

	// MismatchesByField returns the per-field firing
	// counters (e.g. "arch" → 7, "compute_cap" → 3).
	// Implementations SHOULD return a map sized to the
	// number of fields the rules engine knows about
	// (currently 3); the collector tolerates any size.
	MismatchesByField() map[string]uint64

	// PeerKeyCounters returns the cumulative key-pinning
	// outcomes for the peer-attester catalog source in
	// (accepted_signed, accepted_unpinned,
	// rejected_unknown_signer, rejected_unsigned,
	// rejected_bad_signature, rejected_stale) order.
	// All-zeros is the expected reading on a deployment
	// that does not pin keys AND has no peer URLs
	// configured.
	PeerKeyCounters() (uint64, uint64, uint64, uint64, uint64, uint64)

	// PeerKeyConfig returns (pins_loaded, strict_mode_int,
	// max_age_seconds) where strict_mode_int is 1 when
	// strict mode is on (0 otherwise) and max_age_seconds
	// is the freshness-window gauge value (0 when the
	// gate is disabled). Surfaced as gauges so the
	// operator dashboard can show the rollout phase at
	// a glance.
	PeerKeyConfig() (int, int, int)
}

// specCheckProbe holds the active SpecCheckProbe. nil = no
// probe wired = collector emits nothing (pre-Tier-2 posture).
var specCheckProbe atomic.Pointer[SpecCheckProbe]

// SetSpecCheckProbe installs (or, when probe == nil, removes)
// the process-wide SpecCheckProbe. Idempotent. Called once
// at boot from cmd/qsdm/main.go after buildSpecCheckWiring
// resolves successfully. Calling with nil disables the
// metrics — useful for tests but not for production.
func SetSpecCheckProbe(probe SpecCheckProbe) {
	if probe == nil {
		specCheckProbe.Store(nil)
		return
	}
	specCheckProbe.Store(&probe)
}

// currentSpecCheckProbe returns the active probe (or nil).
// Single-load atomic.Pointer indirection — cheap on the
// /metrics scrape hot path.
func currentSpecCheckProbe() SpecCheckProbe {
	p := specCheckProbe.Load()
	if p == nil {
		return nil
	}
	return *p
}

// specCheckPrometheusMetrics is the collector function the
// global scrape exporter registers under the
// "qsdm_spec_check" name. Returns nil when no probe is
// wired so the scrape body is empty (no help/type lines for
// metrics that cannot be valued — keeps a pre-Tier-2
// deployment's /metrics output bit-identical).
func specCheckPrometheusMetrics() []Metric {
	probe := currentSpecCheckProbe()
	if probe == nil {
		return nil
	}
	totalEntries, signers, skus := probe.CatalogCounters()
	checked, matched, mismatched, unknown, skipped := probe.CheckCounters()
	byField := probe.MismatchesByField()

	out := make([]Metric, 0, 8+len(byField))
	out = append(out,
		Metric{
			Name:  "qsdm_spec_check_catalog_entries",
			Help:  "Total GPU observations in the spec-check catalog (across baseline + peer attesters).",
			Type:  MetricGauge,
			Value: float64(totalEntries),
		},
		Metric{
			Name:  "qsdm_spec_check_catalog_signers",
			Help:  "Distinct catalog signer IDs (one per peer attester plus 'baseline').",
			Type:  MetricGauge,
			Value: float64(signers),
		},
		Metric{
			Name:  "qsdm_spec_check_catalog_skus",
			Help:  "Distinct GPU SKU names known to the spec-check catalog.",
			Type:  MetricGauge,
			Value: float64(skus),
		},
		Metric{
			Name:  "qsdm_spec_check_checked_total",
			Help:  "Cumulative number of accepted v2 proofs that ran through the Tier-2 advisory checker.",
			Type:  MetricCounter,
			Value: float64(checked),
		},
		Metric{
			Name:  "qsdm_spec_check_match_total",
			Help:  "Cumulative spec-check verdicts of kind 'match'.",
			Type:  MetricCounter,
			Value: float64(matched),
		},
		Metric{
			Name:  "qsdm_spec_check_mismatch_total",
			Help:  "Cumulative spec-check verdicts of kind 'mismatch' (advisory; proof was still accepted).",
			Type:  MetricCounter,
			Value: float64(mismatched),
		},
		Metric{
			Name:  "qsdm_spec_check_unknown_sku_total",
			Help:  "Cumulative spec-check verdicts of kind 'unknown_sku' (catalog has no entry for the claimed SKU).",
			Type:  MetricCounter,
			Value: float64(unknown),
		},
		Metric{
			Name:  "qsdm_spec_check_skipped_total",
			Help:  "Cumulative spec-check verdicts of kind 'skipped' (catalog empty or claim degenerate).",
			Type:  MetricCounter,
			Value: float64(skipped),
		},
	)

	// Per-field counters. Sort for deterministic
	// /metrics output — Prometheus parsers don't care, but
	// human eyeballs and OpenMetrics line-diff CI checks do.
	fields := make([]string, 0, len(byField))
	for f := range byField {
		fields = append(fields, f)
	}
	sort.Strings(fields)
	for _, f := range fields {
		out = append(out, Metric{
			Name:   "qsdm_spec_check_mismatch_field_total",
			Help:   "Cumulative spec-check rule firings per field (advisory). Labels: field.",
			Type:   MetricCounter,
			Labels: map[string]string{"field": f},
			Value:  float64(byField[f]),
		})
	}

	// Peer-attester key-pinning counters + config gauges.
	// Probe contract: returns five labelled counters and
	// two config gauges. We split the labelled counter
	// into separate Metric entries (one per result label)
	// because the monitoring.Metric struct accepts a
	// label map but emits each metric line independently.
	signedAccepted, unpinnedAccepted, rejUnknown, rejUnsigned, rejBadSig, rejStale := probe.PeerKeyCounters()
	pins, strictInt, maxAgeSec := probe.PeerKeyConfig()
	out = append(out,
		Metric{
			Name:   "qsdm_spec_check_peer_profile_signature_total",
			Help:   "Cumulative outcomes of the per-attester key-pinning + freshness gate on peer telemetry profiles.",
			Type:   MetricCounter,
			Labels: map[string]string{"result": "accepted_signed"},
			Value:  float64(signedAccepted),
		},
		Metric{
			Name:   "qsdm_spec_check_peer_profile_signature_total",
			Help:   "Cumulative outcomes of the per-attester key-pinning + freshness gate on peer telemetry profiles.",
			Type:   MetricCounter,
			Labels: map[string]string{"result": "accepted_unpinned"},
			Value:  float64(unpinnedAccepted),
		},
		Metric{
			Name:   "qsdm_spec_check_peer_profile_signature_total",
			Help:   "Cumulative outcomes of the per-attester key-pinning + freshness gate on peer telemetry profiles.",
			Type:   MetricCounter,
			Labels: map[string]string{"result": "rejected_unknown_signer"},
			Value:  float64(rejUnknown),
		},
		Metric{
			Name:   "qsdm_spec_check_peer_profile_signature_total",
			Help:   "Cumulative outcomes of the per-attester key-pinning + freshness gate on peer telemetry profiles.",
			Type:   MetricCounter,
			Labels: map[string]string{"result": "rejected_unsigned"},
			Value:  float64(rejUnsigned),
		},
		Metric{
			Name:   "qsdm_spec_check_peer_profile_signature_total",
			Help:   "Cumulative outcomes of the per-attester key-pinning + freshness gate on peer telemetry profiles.",
			Type:   MetricCounter,
			Labels: map[string]string{"result": "rejected_bad_signature"},
			Value:  float64(rejBadSig),
		},
		Metric{
			Name:   "qsdm_spec_check_peer_profile_signature_total",
			Help:   "Cumulative outcomes of the per-attester key-pinning + freshness gate on peer telemetry profiles.",
			Type:   MetricCounter,
			Labels: map[string]string{"result": "rejected_stale"},
			Value:  float64(rejStale),
		},
		Metric{
			Name:  "qsdm_spec_check_peer_keys_pinned",
			Help:  "Number of attester signer_ids the validator has a pinned key for. 0 = pinning disabled.",
			Type:  MetricGauge,
			Value: float64(pins),
		},
		Metric{
			Name:  "qsdm_spec_check_peer_keys_strict",
			Help:  "1 = unknown signer_ids rejected; 0 = unknown signer_ids accepted with a warning. Only meaningful when peer_keys_pinned > 0.",
			Type:  MetricGauge,
			Value: float64(strictInt),
		},
		Metric{
			Name:  "qsdm_spec_check_peer_profile_max_age_seconds",
			Help:  "Maximum acceptable age of a peer telemetry profile (seconds). 0 = freshness gate disabled.",
			Type:  MetricGauge,
			Value: float64(maxAgeSec),
		},
	)
	return out
}
