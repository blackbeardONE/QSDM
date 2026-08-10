package monitoring

import (
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/chain"
)

func metricByName(t *testing.T, metrics []Metric, name string) Metric {
	t.Helper()
	for _, metric := range metrics {
		if metric.Name == name {
			return metric
		}
	}
	t.Fatalf("metric %q not found", name)
	return Metric{}
}

func TestConsensusDiagnosticsCollectorRequiresPeerReactorForMultiValidator(t *testing.T) {
	metrics := ConsensusDiagnosticsCollector(
		"network-producer",
		func() int { return 2 },
		func() chain.SyntheticPresealStats {
			return chain.SyntheticPresealStats{
				Attempts:               3,
				Commits:                1,
				RejectedMultivalidator: 2,
			}
		},
		func() uint64 { return 4 },
		false,
	)()

	mode := metricByName(t, metrics, "qsdm_consensus_preseal_mode_info")
	if mode.Labels["mode"] != "peer_vote_required" {
		t.Fatalf("mode = %q, want peer_vote_required", mode.Labels["mode"])
	}
	if got := metricByName(t, metrics, "qsdm_consensus_peer_vote_reactor_ready").Value; got != 0 {
		t.Fatalf("reactor_ready = %v, want 0", got)
	}
	if got := metricByName(t, metrics, "qsdm_consensus_peer_vote_commits_total").Value; got != 4 {
		t.Fatalf("peer commits = %v, want 4", got)
	}
	if got := metricByName(t, metrics, "qsdm_consensus_synthetic_preseal_rejected_multivalidator_total").Value; got != 2 {
		t.Fatalf("synthetic rejections = %v, want 2", got)
	}
}

func TestConsensusDiagnosticsCollectorLabelsSingletonSyntheticMode(t *testing.T) {
	metrics := ConsensusDiagnosticsCollector(
		"network-producer",
		func() int { return 1 },
		nil,
		nil,
		false,
	)()
	mode := metricByName(t, metrics, "qsdm_consensus_preseal_mode_info")
	if mode.Labels["mode"] != "synthetic_singleton" {
		t.Fatalf("mode = %q, want synthetic_singleton", mode.Labels["mode"])
	}
}
