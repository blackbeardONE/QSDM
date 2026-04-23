package quarantine

import (
	"github.com/blackbeardONE/QSDM/pkg/monitoring"
)

// MetricsCollector exposes the QuarantineManager's bookkeeping as
// Prometheus gauges so operators can alert on the *number* of
// quarantined submeshes without polling any JSON endpoint. The shape
// mirrors api.TrustMetricsCollector (same nil-safety, same O(1) scrape,
// same closure-over-live-state pattern) so the two surfaces feel
// consistent when an operator wires Alertmanager for both.
//
// Emitted gauges:
//
//	qsdm_quarantine_submeshes             (gauge) count of submeshes currently quarantined
//	qsdm_quarantine_submeshes_tracked     (gauge) count of distinct submeshes ever observed by the manager
//	qsdm_quarantine_submeshes_ratio       (gauge) submeshes / tracked, or 0 when tracked == 0
//	qsdm_quarantine_threshold             (gauge) the configured invalid-ratio quarantine threshold
//
// Why the function returns a closure instead of being called directly
// from main.go: monitoring.PrometheusExporter.RegisterCollector takes a
//
//	func() []monitoring.Metric
//
// which runs on every /metrics scrape. Closing over the manager keeps
// the exposition always reflective of the latest state without
// plumbing a new update path on every RecordTransaction call.
//
// Nil-safety: if qm is nil (e.g. operator disabled the quarantine
// subsystem in config so the manager is never constructed), the
// collector returns nil rather than panicking. Grafana / Alertmanager
// see absent gauges, which is correct — emitting zeroes would falsely
// imply the denominator has meaning.
func MetricsCollector(qm *QuarantineManager) monitoring.MetricCollector {
	return func() []monitoring.Metric {
		if qm == nil {
			return nil
		}
		s := qm.Stats()

		var ratio float64
		if s.Tracked > 0 {
			ratio = float64(s.Quarantined) / float64(s.Tracked)
		}

		return []monitoring.Metric{
			{
				Name:  "qsdm_quarantine_submeshes",
				Help:  "Number of submeshes currently quarantined by the invalid-ratio policy.",
				Type:  monitoring.MetricGauge,
				Value: float64(s.Quarantined),
			},
			{
				Name:  "qsdm_quarantine_submeshes_tracked",
				Help:  "Number of distinct submeshes the QuarantineManager has observed since process start.",
				Type:  monitoring.MetricGauge,
				Value: float64(s.Tracked),
			},
			{
				Name:  "qsdm_quarantine_submeshes_ratio",
				Help:  "Ratio of quarantined / tracked submeshes, or 0 when tracked==0 (avoid divide-by-zero alert flap).",
				Type:  monitoring.MetricGauge,
				Value: ratio,
			},
			{
				Name:  "qsdm_quarantine_threshold",
				Help:  "Configured invalid-transaction-ratio threshold above which a submesh is quarantined at the window boundary.",
				Type:  monitoring.MetricGauge,
				Value: s.Threshold,
			},
		}
	}
}
