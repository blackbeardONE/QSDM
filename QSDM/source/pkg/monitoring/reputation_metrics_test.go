package monitoring

import (
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/monitoring/repmetrics"
)

type fakeRepProvider struct{ snap repmetrics.ReputationSnapshot }

func (f *fakeRepProvider) Snapshot() repmetrics.ReputationSnapshot { return f.snap }

func TestReputationPrometheusMetrics_NoProviders(t *testing.T) {
	defer repmetrics.RegisterReputationProvider("tx", nil)
	defer repmetrics.RegisterReputationProvider("evidence", nil)

	if got := reputationPrometheusMetrics(); got != nil {
		t.Errorf("with no providers registered, got %v; want nil", got)
	}
}

func TestReputationPrometheusMetrics_TwoProviders(t *testing.T) {
	defer repmetrics.RegisterReputationProvider("tx", nil)
	defer repmetrics.RegisterReputationProvider("evidence", nil)

	repmetrics.RegisterReputationProvider("tx", &fakeRepProvider{snap: repmetrics.ReputationSnapshot{
		TotalPeers: 10, BannedPeers: 2, MinScore: -150, MaxScore: 800, AvgScore: 250,
	}})
	repmetrics.RegisterReputationProvider("evidence", &fakeRepProvider{snap: repmetrics.ReputationSnapshot{
		TotalPeers: 4, BannedPeers: 0, MinScore: 50, MaxScore: 200, AvgScore: 120,
	}})

	rows := reputationPrometheusMetrics()
	if got, want := len(rows), 2*5; got != want {
		t.Fatalf("len(rows) = %d; want %d (2 trackers × 5 gauges)", got, want)
	}

	type key struct{ name, tracker string }
	seen := map[key]float64{}
	for _, m := range rows {
		seen[key{m.Name, m.Labels["tracker"]}] = m.Value
	}

	checks := []struct {
		name, tracker string
		want          float64
	}{
		{"qsdm_reputation_peers_total", "tx", 10},
		{"qsdm_reputation_peers_banned", "tx", 2},
		{"qsdm_reputation_score_min", "tx", -150},
		{"qsdm_reputation_score_max", "tx", 800},
		{"qsdm_reputation_score_avg", "tx", 250},
		{"qsdm_reputation_peers_total", "evidence", 4},
		{"qsdm_reputation_peers_banned", "evidence", 0},
		{"qsdm_reputation_score_avg", "evidence", 120},
	}
	for _, c := range checks {
		got, ok := seen[key{c.name, c.tracker}]
		if !ok {
			t.Errorf("missing %s{tracker=%q}", c.name, c.tracker)
			continue
		}
		if got != c.want {
			t.Errorf("%s{tracker=%q} = %v; want %v", c.name, c.tracker, got, c.want)
		}
	}
}

func TestReputationPrometheusMetrics_ReflectsInExposition(t *testing.T) {
	defer repmetrics.RegisterReputationProvider("tx", nil)
	repmetrics.RegisterReputationProvider("tx", &fakeRepProvider{snap: repmetrics.ReputationSnapshot{
		TotalPeers: 7, BannedPeers: 1,
	}})

	exposition := PrometheusExposition()
	if !strings.Contains(exposition, `qsdm_reputation_peers_total{tracker="tx"}`) {
		t.Error("exposition missing qsdm_reputation_peers_total{tracker=tx}")
	}
	if !strings.Contains(exposition, `qsdm_reputation_peers_banned{tracker="tx"}`) {
		t.Error("exposition missing qsdm_reputation_peers_banned{tracker=tx}")
	}
}
