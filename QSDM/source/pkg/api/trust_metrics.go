package api

import (
	"time"

	"github.com/blackbeardONE/QSDM/pkg/monitoring"
)

// TrustMetricsCollector exposes the trust-transparency surface as
// Prometheus gauges so operators can alert on the *number* of
// attestation sources behind a validator without polling the JSON
// endpoint from Alertmanager.
//
// The collector closes over the live TrustAggregator and calls its
// O(1) cached Summary() accessor on every scrape — no new locks, no
// new tickers, no extra HTTP hop. Returned gauges are:
//
//	qsdm_trust_attested              (gauge) number of distinct attestation sources currently fresh
//	qsdm_trust_total_public          (gauge) denominator (public validators + local node)
//	qsdm_trust_ratio                 (gauge) attested / total_public (0 when total_public==0)
//	qsdm_trust_ngc_service_healthy   (gauge) 1 iff ngc_service_status == "healthy", else 0
//	qsdm_trust_last_attested_seconds (gauge) unix seconds of summary.last_attested_at (0 when nil)
//	qsdm_trust_last_checked_seconds  (gauge) unix seconds of summary.last_checked_at
//	qsdm_trust_warm                  (gauge) 1 once the aggregator has completed its first full Refresh()
//
// During the warm-up window Summary() returns a zero-valued TrustSummary
// and warm=false. We surface that state as all gauges == 0 and
// qsdm_trust_warm = 0, which is Prometheus-idiomatic: alerts can gate on
// `qsdm_trust_warm == 1` before evaluating the floor checks so a
// mid-deploy restart doesn't page.
//
// Why the function returns a closure instead of being called directly
// from main.go: the monitoring.PrometheusExporter contract is
//
//	RegisterCollector(name string, func() []monitoring.Metric)
//
// i.e. *the collector runs on every /metrics scrape*. Building gauges
// eagerly would pin stale numbers; building them inside the closure
// guarantees the exposition always reflects the most recent
// Refresh() result without requiring the caller to plumb a new
// update path.
//
// Nil-safety: if agg is nil (e.g. operator has set [trust] disabled=true
// in qsdmplus.toml so the aggregator never gets constructed), the
// collector returns an empty slice rather than panicking. This keeps
// the /metrics surface clean on no-trust nodes — absent gauges are
// correct, emitting zeroes would falsely imply the floor assertions
// have a denominator to talk about.
func TrustMetricsCollector(agg *TrustAggregator) monitoring.MetricCollector {
	return func() []monitoring.Metric {
		if agg == nil {
			return nil
		}
		summary, warm := agg.Summary()

		warmVal := 0.0
		if warm {
			warmVal = 1.0
		}

		ngcHealthy := 0.0
		if summary.NGCServiceStatus == "healthy" {
			ngcHealthy = 1.0
		}

		// Parse the RFC3339 strings back to unix seconds so Grafana
		// can do age math directly (`time() - qsdm_trust_last_attested_seconds`).
		// Parse failures yield 0, which reads naturally as "no timestamp
		// ever observed" on a dashboard.
		var lastAttestedSec, lastCheckedSec float64
		if summary.LastAttestedAt != nil && *summary.LastAttestedAt != "" {
			if t, err := time.Parse(time.RFC3339, *summary.LastAttestedAt); err == nil {
				lastAttestedSec = float64(t.Unix())
			}
		}
		if summary.LastCheckedAt != "" {
			if t, err := time.Parse(time.RFC3339, summary.LastCheckedAt); err == nil {
				lastCheckedSec = float64(t.Unix())
			}
		}

		return []monitoring.Metric{
			{
				Name:  "qsdm_trust_attested",
				Help:  "Number of distinct attestation sources currently within fresh_within (Major Update §8.5.3).",
				Type:  monitoring.MetricGauge,
				Value: float64(summary.Attested),
			},
			{
				Name:  "qsdm_trust_total_public",
				Help:  "Size of the public validator set used as the attestation denominator (Major Update §8.5.3).",
				Type:  monitoring.MetricGauge,
				Value: float64(summary.TotalPublic),
			},
			{
				Name:  "qsdm_trust_ratio",
				Help:  "Ratio of attested / total_public, or 0 when total_public==0 (§8.5.2 anti-claim).",
				Type:  monitoring.MetricGauge,
				Value: summary.Ratio,
			},
			{
				Name:  "qsdm_trust_ngc_service_healthy",
				Help:  "1 iff ngc_service_status == \"healthy\"; else 0. Use with ngc_service_status enum {healthy,degraded,outage} for alerting.",
				Type:  monitoring.MetricGauge,
				Value: ngcHealthy,
			},
			{
				Name:  "qsdm_trust_last_attested_seconds",
				Help:  "Unix-seconds timestamp of the newest attestation ever seen (0 until the first attestation lands).",
				Type:  monitoring.MetricGauge,
				Value: lastAttestedSec,
			},
			{
				Name:  "qsdm_trust_last_checked_seconds",
				Help:  "Unix-seconds timestamp of the last TrustAggregator.Refresh() (0 during warm-up).",
				Type:  monitoring.MetricGauge,
				Value: lastCheckedSec,
			},
			{
				Name:  "qsdm_trust_warm",
				Help:  "1 once the TrustAggregator has completed its first full Refresh(); 0 during warm-up. Gate floor alerts on qsdm_trust_warm == 1 to avoid paging on a redeploy.",
				Type:  monitoring.MetricGauge,
				Value: warmVal,
			},
		}
	}
}
