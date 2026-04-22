package topology

import "testing"

func TestBuildLiveView_EmptyPeers(t *testing.T) {
	view := BuildLiveView("local", nil)
	if view["live_peer_count"].(int) != 0 {
		t.Fatalf("expected 0 peers, got %v", view["live_peer_count"])
	}
	cells := view["cells"].([]map[string]interface{})
	if len(cells) != 1 || cells[0]["id"] != "local" {
		t.Fatalf("expected only local cell, got %+v", cells)
	}
}

func TestBuildLiveView_IncludesConnectedAndDegraded(t *testing.T) {
	peers := []PeerInfo{
		{ID: "peer-a", Region: "eu", Reputation: 0.8, Connected: true, MessagesInLast: 120, LatencyMs: 40},
		{ID: "peer-b", Region: "us", Reputation: -0.6, Connected: true, MessagesInLast: 2, LatencyMs: 900},
		{ID: "peer-c", Region: "us", Reputation: 0.1, Connected: false, LatencyMs: 0},
	}
	view := BuildLiveView("local", peers)
	if view["live_peer_count"].(int) != 3 {
		t.Fatalf("expected 3 peers, got %v", view["live_peer_count"])
	}
	if view["healthy_peers"].(int) != 1 {
		t.Fatalf("expected 1 healthy peer, got %v", view["healthy_peers"])
	}
	cells := view["cells"].([]map[string]interface{})
	if len(cells) != 4 {
		t.Fatalf("want 4 cells, got %d", len(cells))
	}
	var gotDegraded, gotStale bool
	for _, c := range cells {
		switch c["role"] {
		case "degraded":
			gotDegraded = true
		case "stale":
			gotStale = true
		}
	}
	if !gotDegraded {
		t.Error("expected at least one degraded peer")
	}
	if !gotStale {
		t.Error("expected at least one stale peer")
	}
	links := view["links"].([]map[string]interface{})
	if len(links) != 6 {
		t.Fatalf("want 6 links, got %d", len(links))
	}
}

func TestBuildLiveView_StableOrdering(t *testing.T) {
	peers := []PeerInfo{
		{ID: "z", Region: "us", Connected: true, Reputation: 0.2},
		{ID: "a", Region: "eu", Connected: true, Reputation: 0.3},
		{ID: "m", Region: "eu", Connected: true, Reputation: 0.1},
	}
	view1 := BuildLiveView("local", peers)
	view2 := BuildLiveView("local", peers)
	c1 := view1["cells"].([]map[string]interface{})
	c2 := view2["cells"].([]map[string]interface{})
	for i := range c1 {
		if c1[i]["id"] != c2[i]["id"] {
			t.Fatalf("ordering not stable at %d: %v vs %v", i, c1[i]["id"], c2[i]["id"])
		}
	}
	if c1[1]["id"] != "a" || c1[2]["id"] != "m" || c1[3]["id"] != "z" {
		t.Fatalf("unexpected ordering: %v, %v, %v", c1[1]["id"], c1[2]["id"], c1[3]["id"])
	}
}
