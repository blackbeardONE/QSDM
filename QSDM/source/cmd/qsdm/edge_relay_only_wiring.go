package main

// edge_relay_only_wiring.go: the node-process surface of the HL2 edge
// relay-only rule (the rule itself lives in pkg/chain/edge_relay_only.go):
// qsdm_edge_relay_only_* metrics.

import (
	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mining/enrollment"
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
			{Name: "qsdm_relay_settlement_scope_activation_height", Type: monitoring.MetricGauge,
				Help:  "First height at which a Relay settlement is valid only on the built-in resource tasks and consumed proofs and receipts stay consumed on every task",
				Value: float64(chain.RelaySettlementScopeHeight())},
			{Name: "qsdm_relay_settlement_scope_admission_rejected_total", Type: monitoring.MetricCounter,
				Help:  "Relay settlement envelopes for other tasks refused at mempool admission",
				Value: float64(chain.RelaySettlementScopeAdmissionRejected())},
			{Name: "qsdm_edge_relay_only_admission_rejected_total", Type: monitoring.MetricCounter,
				Help:  "Resource-task submits refused at mempool admission because they are not pooled Relay settlements",
				Value: float64(chain.EdgeRelayOnlyAdmissionRejected())},
			{Name: "qsdm_task_catalog_reserved_admission_rejected_total", Type: monitoring.MetricCounter,
				Help:  "Catalog actions for a reserved built-in task ID refused at mempool admission (sender is not the QSDM task manager)",
				Value: float64(chain.ReservedTaskCatalogAdmissionRejected())},
			{Name: "qsdm_custom_task_pay_activation_height", Type: monitoring.MetricGauge,
				Help:  "First height at which a submit to a task other than the built-in resource tasks may not ask for a reward (rc1112)",
				Value: float64(chain.CustomTaskPayHeight())},
			{Name: "qsdm_custom_task_pay_admission_rejected_total", Type: monitoring.MetricCounter,
				Help:  "Submits asking a task other than the built-in resource tasks for a reward, refused at mempool admission",
				Value: float64(chain.CustomTaskPayAdmissionRejected())},
			{Name: "qsdm_enrollment_deferred_prune_activation_height", Type: monitoring.MetricGauge,
				Help:  "First height at which deferred-bond enrollments that never earned anything are removed after a week (rc1112)",
				Value: float64(enrollment.DeferredPruneHeight())},
			{Name: "qsdm_enrollment_deferred_pruned_total", Type: monitoring.MetricCounter,
				Help:  "Deferred-bond enrollments removed by this process because they earned nothing in a week",
				Value: float64(chain.DeferredEnrollmentsPruned())},
		}
	}
}
