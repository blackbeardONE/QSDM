package main

// edge_relay_only_wiring.go: the node-process surface of the HL2 edge
// relay-only rule (the rule itself lives in pkg/chain/edge_relay_only.go):
// qsdm_edge_relay_only_* metrics.

import (
	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/monitoring"
)

// edgeRelayOnlyMetricsCollector exports the configured activation height and
// the admission refusals.
func edgeRelayOnlyMetricsCollector() monitoring.MetricCollector {
	return func() []monitoring.Metric {
		return []monitoring.Metric{
			{Name: "qsdm_edge_relay_only_activation_height", Type: monitoring.MetricGauge,
				Help:  "Configured edge resource-task relay-only activation height (0 = not scheduled)",
				Value: float64(chain.EdgeRelayOnlyActivationHeight())},
			{Name: "qsdm_edge_relay_only_admission_rejected_total", Type: monitoring.MetricCounter,
				Help:  "Resource-task submits refused at mempool admission because they are not pooled Relay settlements",
				Value: float64(chain.EdgeRelayOnlyAdmissionRejected())},
			{Name: "qsdm_task_catalog_reserved_admission_rejected_total", Type: monitoring.MetricCounter,
				Help:  "Catalog actions for a reserved built-in task ID refused at mempool admission (sender is not the QSDM task manager)",
				Value: float64(chain.ReservedTaskCatalogAdmissionRejected())},
		}
	}
}
