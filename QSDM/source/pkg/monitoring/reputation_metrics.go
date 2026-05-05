package monitoring

// Peer-reputation Prometheus exposition.
//
// The actual registration primitives live in the
// pkg/monitoring/repmetrics leaf so pkg/networking can
// import them without an import cycle (this root package
// imports pkg/networking via topology.go). This file is
// the exposition surface only.
//
// Re-exported for backwards-compat with callers wired to
// pkg/monitoring directly:
//
//   monitoring.ReputationProvider           — interface
//   monitoring.ReputationSnapshot           — value type
//   monitoring.RegisterReputationProvider   — wire a tracker

import "github.com/blackbeardONE/QSDM/pkg/monitoring/repmetrics"

// ReputationSnapshot is the per-tracker scrape-time view.
type ReputationSnapshot = repmetrics.ReputationSnapshot

// ReputationProvider is the interface
// pkg/networking.ReputationTracker satisfies.
type ReputationProvider = repmetrics.ReputationProvider

// RegisterReputationProvider wires a tracker into the
// monitoring layer under the given label-stable name.
func RegisterReputationProvider(tracker string, p ReputationProvider) {
	repmetrics.RegisterReputationProvider(tracker, p)
}

// reputationPrometheusMetrics is the collector hook
// registered with the global scrape exporter. Emits five
// gauges per registered tracker:
//
//   qsdm_reputation_peers_total{tracker}
//   qsdm_reputation_peers_banned{tracker}
//   qsdm_reputation_score_min{tracker}
//   qsdm_reputation_score_max{tracker}
//   qsdm_reputation_score_avg{tracker}
//
// If no providers are registered (test/dev scrape), the
// collector returns an empty slice — the alerts are scoped
// by `tracker=` label, so absence simply means no alert
// evaluation. This is unlike the netmetrics provider
// which always emits a row with a `provider="none"` label,
// because here the absence-of-tracker case is much more
// common (most subagent unit tests don't construct a
// reputation tracker at all).
func reputationPrometheusMetrics() []Metric {
	providers := repmetrics.Providers()
	if len(providers) == 0 {
		return nil
	}

	out := make([]Metric, 0, len(providers)*5)
	for tracker, p := range providers {
		snap := p.Snapshot()
		labels := map[string]string{"tracker": tracker}

		out = append(out,
			Metric{
				Name:   "qsdm_reputation_peers_total",
				Help:   "Total tracked peer records in the named ReputationTracker. tracker=\"tx\" is the transaction-gossip tracker; tracker=\"evidence\" is the consensus-evidence tracker (stricter penalty config). Pulled at scrape time.",
				Type:   MetricGauge,
				Value:  float64(snap.TotalPeers),
				Labels: labels,
			},
			Metric{
				Name:   "qsdm_reputation_peers_banned",
				Help:   "Tracked peers currently in Banned=true state in the named ReputationTracker. A high banned/total ratio indicates either widespread peer misbehaviour or an over-aggressive penalty config; see REPUTATION_INCIDENT.md.",
				Type:   MetricGauge,
				Value:  float64(snap.BannedPeers),
				Labels: labels,
			},
			Metric{
				Name:   "qsdm_reputation_score_min",
				Help:   "Lowest reputation score across all tracked peers in the named tracker. Approaches the BanThreshold (-200 default) when many peers are accumulating penalties. 0 when the tracker has no peers.",
				Type:   MetricGauge,
				Value:  snap.MinScore,
				Labels: labels,
			},
			Metric{
				Name:   "qsdm_reputation_score_max",
				Help:   "Highest reputation score across all tracked peers in the named tracker. Capped at MaxScore (1000 default).",
				Type:   MetricGauge,
				Value:  snap.MaxScore,
				Labels: labels,
			},
			Metric{
				Name:   "qsdm_reputation_score_avg",
				Help:   "Arithmetic mean reputation score across all tracked peers. A drift toward 0 (or below) over time indicates either a high penalty regime or a population of underperforming peers.",
				Type:   MetricGauge,
				Value:  snap.AvgScore,
				Labels: labels,
			},
		)
	}

	return out
}
