package monitoring

// spec_penalty_metrics.go: Prometheus telemetry for the
// Tier-3 reward-downgrade engine
// (pkg/mining/telemetrycheck.PerMinerStats). Exports the
// data the operator dashboard needs to track:
//
//   qsdm_spec_penalty_active                  gauge — 1 when wired, 0 otherwise
//   qsdm_spec_penalty_window_size             gauge — resolved sliding-window length
//   qsdm_spec_penalty_threshold_pct           gauge — resolved mismatch threshold
//   qsdm_spec_penalty_multiplier              gauge — resolved penalty multiplier
//   qsdm_spec_penalty_min_observations        gauge — resolved warmup floor
//   qsdm_spec_penalty_tracked_miners          gauge — distinct miner_addrs observed
//   qsdm_spec_penalty_penalised_miners        gauge — miners currently below 1.0
//   qsdm_spec_penalty_blockdriver_payouts_total counter — payouts that were multiplied < 1.0
//   qsdm_spec_penalty_blockdriver_withheld_dust counter — cumulative dust unminted
//
// Per-miner cardinality is intentionally NOT exported as
// a Prometheus label — that would let an attacker spam
// proofs from synthetic addresses to inflate the metrics
// surface. The /api/v1/mining/penalty endpoint is the
// per-miner-detail surface; Prometheus only sees the
// aggregates.
//
// Wiring: pkg/monitoring receives a SpecPenaltyProbe via
// SetSpecPenaltyProbe() at validator boot. When unset, the
// collector emits NOTHING so a pre-Tier-3 deployment's
// /metrics surface is bit-identical to before.

import (
	"sync/atomic"
)

// SpecPenaltyProbe is the read-only interface
// pkg/monitoring requires from the validator's spec-
// penalty wiring. Mirrors the SpecCheckProbe pattern —
// concrete implementation lives in cmd/qsdm/spec_check.go
// so this package stays decoupled from the
// telemetrycheck struct shape.
type SpecPenaltyProbe interface {
	// PenaltyConfig returns the resolved governance
	// constants in (window_size, threshold_pct,
	// multiplier, min_observations) order.
	PenaltyConfig() (int, float64, float64, int)

	// PenaltyAggregate returns (tracked_miners,
	// penalised_miners). tracked counts every miner ever
	// observed; penalised is the subset currently with
	// multiplier < 1.0.
	PenaltyAggregate() (int, int)

	// BlockdriverPenaltyCounters returns the cumulative
	// blockdriver-side counters: (payouts_penalised,
	// withheld_dust). Pulled from the Driver's atomic
	// counters via cmd/qsdm.
	BlockdriverPenaltyCounters() (uint64, uint64)
}

// specPenaltyProbe holds the active probe. nil = no probe
// wired = collector emits nothing.
var specPenaltyProbe atomic.Pointer[SpecPenaltyProbe]

// SetSpecPenaltyProbe installs (or, when probe == nil,
// removes) the process-wide Tier-3 probe. Idempotent.
// Called once at boot from cmd/qsdm/main.go AFTER the
// blockdriver and telemetrycheck wiring resolve. nil
// disables the collector — useful for tests but not
// for production.
func SetSpecPenaltyProbe(probe SpecPenaltyProbe) {
	if probe == nil {
		specPenaltyProbe.Store(nil)
		return
	}
	specPenaltyProbe.Store(&probe)
}

// currentSpecPenaltyProbe returns the active probe (or
// nil). Single-load atomic.Pointer indirection — cheap
// on the /metrics scrape hot path.
func currentSpecPenaltyProbe() SpecPenaltyProbe {
	p := specPenaltyProbe.Load()
	if p == nil {
		return nil
	}
	return *p
}

// specPenaltyPrometheusMetrics is the collector function
// the global scrape exporter registers under the
// "qsdm_spec_penalty" name. Returns nil when no probe is
// wired so the scrape body is empty (no help/type lines
// for metrics that cannot be valued — keeps a pre-Tier-3
// deployment's /metrics output bit-identical).
func specPenaltyPrometheusMetrics() []Metric {
	probe := currentSpecPenaltyProbe()
	if probe == nil {
		return nil
	}
	winSize, threshold, multiplier, minObs := probe.PenaltyConfig()
	tracked, penalised := probe.PenaltyAggregate()
	payouts, withheld := probe.BlockdriverPenaltyCounters()

	return []Metric{
		{
			Name:  "qsdm_spec_penalty_active",
			Help:  "1 when the Tier-3 reward-downgrade engine is wired (QSDM_SPEC_PENALTY_ENABLED), else 0.",
			Type:  MetricGauge,
			Value: 1,
		},
		{
			Name:  "qsdm_spec_penalty_window_size",
			Help:  "Resolved Tier-3 sliding-window length (proofs per miner) used to compute mismatch ratio.",
			Type:  MetricGauge,
			Value: float64(winSize),
		},
		{
			Name:  "qsdm_spec_penalty_threshold_pct",
			Help:  "Mismatch percentage at or above which a miner's reward multiplier drops.",
			Type:  MetricGauge,
			Value: threshold,
		},
		{
			Name:  "qsdm_spec_penalty_multiplier",
			Help:  "Reward multiplier applied to over-threshold miners (1.0 = full reward, 0.0 = full forfeit).",
			Type:  MetricGauge,
			Value: multiplier,
		},
		{
			Name:  "qsdm_spec_penalty_min_observations",
			Help:  "Minimum proofs in window before a Tier-3 penalty can fire (warmup floor).",
			Type:  MetricGauge,
			Value: float64(minObs),
		},
		{
			Name:  "qsdm_spec_penalty_tracked_miners",
			Help:  "Distinct miner addresses for which the Tier-3 engine has observed at least one verdict.",
			Type:  MetricGauge,
			Value: float64(tracked),
		},
		{
			Name:  "qsdm_spec_penalty_penalised_miners",
			Help:  "Miners currently with a Tier-3 reward multiplier strictly less than 1.0.",
			Type:  MetricGauge,
			Value: float64(penalised),
		},
		{
			Name:  "qsdm_spec_penalty_blockdriver_payouts_total",
			Help:  "Cumulative number of per-block miner shares the blockdriver has multiplied by < 1.0.",
			Type:  MetricCounter,
			Value: float64(payouts),
		},
		{
			Name:  "qsdm_spec_penalty_blockdriver_withheld_dust",
			Help:  "Cumulative dust NOT minted across the validator's lifetime due to Tier-3 reward downgrade.",
			Type:  MetricCounter,
			Value: float64(withheld),
		},
	}
}
