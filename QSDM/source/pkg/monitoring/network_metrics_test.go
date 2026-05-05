package monitoring

import (
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/monitoring/netmetrics"
)

type fakeNetProvider struct{ peers int }

func (f *fakeNetProvider) PeerCount() int { return f.peers }

func TestNetworkPrometheusMetrics_NoProvider(t *testing.T) {
	defer netmetrics.RegisterNetworkProvider(nil)
	netmetrics.RegisterNetworkProvider(nil)

	rows := networkPrometheusMetrics()
	var foundPeers, foundIn, foundOut bool
	for _, m := range rows {
		switch m.Name {
		case "qsdm_p2p_peers_connected":
			foundPeers = true
			if m.Labels["provider"] != "none" {
				t.Errorf("provider label = %q; want \"none\"", m.Labels["provider"])
			}
			if m.Value != 0 {
				t.Errorf("peer count = %v; want 0 with no provider", m.Value)
			}
		case "qsdm_p2p_messages_total":
			switch m.Labels["direction"] {
			case "in":
				foundIn = true
			case "out":
				foundOut = true
			}
		}
	}
	if !foundPeers {
		t.Error("missing qsdm_p2p_peers_connected row")
	}
	if !foundIn {
		t.Error("missing qsdm_p2p_messages_total{direction=\"in\"} row")
	}
	if !foundOut {
		t.Error("missing qsdm_p2p_messages_total{direction=\"out\"} row")
	}
}

func TestNetworkPrometheusMetrics_LiveProvider(t *testing.T) {
	defer netmetrics.RegisterNetworkProvider(nil)

	netmetrics.RegisterNetworkProvider(&fakeNetProvider{peers: 5})
	rows := networkPrometheusMetrics()
	var found bool
	for _, m := range rows {
		if m.Name != "qsdm_p2p_peers_connected" {
			continue
		}
		found = true
		if m.Labels["provider"] != "live" {
			t.Errorf("provider label = %q; want \"live\"", m.Labels["provider"])
		}
		if m.Value != 5 {
			t.Errorf("peer count = %v; want 5", m.Value)
		}
	}
	if !found {
		t.Error("missing qsdm_p2p_peers_connected row with live provider")
	}
}

func TestRecordGossipMessage_ReflectsInExposition(t *testing.T) {
	defer netmetrics.RegisterNetworkProvider(nil)

	startIn, startOut := GossipMessageCounts()
	RecordGossipMessage(GossipDirectionIn)
	RecordGossipMessage(GossipDirectionOut)
	RecordGossipMessage("bogus")

	gotIn, gotOut := GossipMessageCounts()
	if gotIn != startIn+1 || gotOut != startOut+1 {
		t.Fatalf("counters = (%d,%d); want (%d,%d)", gotIn, gotOut, startIn+1, startOut+1)
	}

	exposition := PrometheusExposition()
	if !strings.Contains(exposition, `qsdm_p2p_messages_total{direction="in"}`) {
		t.Error("exposition missing direction=in row")
	}
	if !strings.Contains(exposition, `qsdm_p2p_messages_total{direction="out"}`) {
		t.Error("exposition missing direction=out row")
	}
}
