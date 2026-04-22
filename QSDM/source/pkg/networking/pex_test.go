package networking

import (
	"testing"
	"time"
)

func TestPEX_AddPeer(t *testing.T) {
	pm := NewPEXManager("self", DefaultPEXConfig())
	added := pm.AddPeer(PEXPeer{ID: "peer-1", Addresses: []string{"/ip4/1.2.3.4/tcp/9000"}, Source: "manual"})
	if !added {
		t.Fatal("expected new peer to be added")
	}
	if pm.PeerCount() != 1 {
		t.Fatal("expected 1 peer")
	}
}

func TestPEX_IgnoreSelf(t *testing.T) {
	pm := NewPEXManager("self", DefaultPEXConfig())
	added := pm.AddPeer(PEXPeer{ID: "self"})
	if added {
		t.Fatal("should not add self")
	}
}

func TestPEX_IgnoreEmpty(t *testing.T) {
	pm := NewPEXManager("self", DefaultPEXConfig())
	added := pm.AddPeer(PEXPeer{ID: ""})
	if added {
		t.Fatal("should not add empty ID")
	}
}

func TestPEX_UpdateExisting(t *testing.T) {
	pm := NewPEXManager("self", DefaultPEXConfig())
	pm.AddPeer(PEXPeer{ID: "peer-1", Addresses: []string{"addr1"}})
	added := pm.AddPeer(PEXPeer{ID: "peer-1", Addresses: []string{"addr2"}})
	if added {
		t.Fatal("updating should return false")
	}

	p, _ := pm.GetPeer("peer-1")
	if len(p.Addresses) != 2 {
		t.Fatalf("expected merged addresses, got %d", len(p.Addresses))
	}
}

func TestPEX_EvictCapacity(t *testing.T) {
	cfg := DefaultPEXConfig()
	cfg.MaxKnownPeers = 3
	pm := NewPEXManager("self", cfg)

	pm.AddPeer(PEXPeer{ID: "a", LastSeen: time.Now().Add(-3 * time.Hour)})
	pm.AddPeer(PEXPeer{ID: "b", LastSeen: time.Now().Add(-2 * time.Hour)})
	pm.AddPeer(PEXPeer{ID: "c", LastSeen: time.Now().Add(-1 * time.Hour)})
	pm.AddPeer(PEXPeer{ID: "d", LastSeen: time.Now()}) // should evict 'a' (oldest)

	if pm.PeerCount() != 3 {
		t.Fatalf("expected 3, got %d", pm.PeerCount())
	}
	if _, ok := pm.GetPeer("a"); ok {
		t.Fatal("oldest peer 'a' should have been evicted")
	}
}

func TestPEX_MarkFailed(t *testing.T) {
	cfg := DefaultPEXConfig()
	cfg.MaxFailCount = 3
	pm := NewPEXManager("self", cfg)
	pm.AddPeer(PEXPeer{ID: "p1"})

	for i := 0; i < 3; i++ {
		pm.MarkFailed("p1")
	}

	p, _ := pm.GetPeer("p1")
	if p.Reachable {
		t.Fatal("peer should be unreachable after max failures")
	}
}

func TestPEX_MarkReachable(t *testing.T) {
	cfg := DefaultPEXConfig()
	cfg.MaxFailCount = 1
	pm := NewPEXManager("self", cfg)
	pm.AddPeer(PEXPeer{ID: "p1"})
	pm.MarkFailed("p1")

	pm.MarkReachable("p1")
	p, _ := pm.GetPeer("p1")
	if !p.Reachable {
		t.Fatal("peer should be reachable after mark")
	}
	if p.FailCount != 0 {
		t.Fatal("fail count should be reset")
	}
}

func TestPEX_BuildRequest(t *testing.T) {
	pm := NewPEXManager("node-1", DefaultPEXConfig())
	req := pm.BuildRequest()
	if req.Type != PEXRequest {
		t.Fatal("expected request type")
	}
	if req.Sender != "node-1" {
		t.Fatal("sender should be self")
	}
}

func TestPEX_BuildResponse(t *testing.T) {
	pm := NewPEXManager("self", DefaultPEXConfig())
	pm.AddPeer(PEXPeer{ID: "a", Addresses: []string{"addr-a"}})
	pm.AddPeer(PEXPeer{ID: "b", Addresses: []string{"addr-b"}})

	resp := pm.BuildResponse("requester")
	if resp.Type != PEXResponse {
		t.Fatal("expected response type")
	}
	if len(resp.Peers) != 2 {
		t.Fatalf("expected 2 peers in response, got %d", len(resp.Peers))
	}
}

func TestPEX_ResponseExcludesRequester(t *testing.T) {
	pm := NewPEXManager("self", DefaultPEXConfig())
	pm.AddPeer(PEXPeer{ID: "requester"})
	pm.AddPeer(PEXPeer{ID: "other"})

	resp := pm.BuildResponse("requester")
	for _, p := range resp.Peers {
		if p.ID == "requester" {
			t.Fatal("response should not include requester")
		}
	}
}

func TestPEX_ResponseExcludesUnreachable(t *testing.T) {
	cfg := DefaultPEXConfig()
	cfg.MaxFailCount = 1
	pm := NewPEXManager("self", cfg)
	pm.AddPeer(PEXPeer{ID: "good"})
	pm.AddPeer(PEXPeer{ID: "bad"})
	pm.MarkFailed("bad")

	resp := pm.BuildResponse("x")
	for _, p := range resp.Peers {
		if p.ID == "bad" {
			t.Fatal("response should not include unreachable peers")
		}
	}
}

func TestPEX_HandleRequestResponse(t *testing.T) {
	pm1 := NewPEXManager("node-1", DefaultPEXConfig())
	pm2 := NewPEXManager("node-2", DefaultPEXConfig())

	pm1.AddPeer(PEXPeer{ID: "peer-a", Addresses: []string{"/ip4/1.1.1.1/tcp/9000"}})
	pm1.AddPeer(PEXPeer{ID: "peer-b", Addresses: []string{"/ip4/2.2.2.2/tcp/9000"}})

	// node-2 sends request
	req := pm2.BuildRequest()
	resp, _ := pm1.HandleMessage(req)
	if resp == nil {
		t.Fatal("expected response")
	}

	// node-2 processes response
	_, newCount := pm2.HandleMessage(*resp)
	if newCount != 2 {
		t.Fatalf("expected 2 new peers, got %d", newCount)
	}
	if pm2.PeerCount() != 2 {
		t.Fatalf("expected 2 known peers, got %d", pm2.PeerCount())
	}
}

func TestPEX_HandleResponse_SetsSourcePEX(t *testing.T) {
	pm := NewPEXManager("self", DefaultPEXConfig())
	msg := PEXMessage{
		Type:   PEXResponse,
		Sender: "other",
		Peers:  []PEXPeer{{ID: "learned", Addresses: []string{"addr"}}},
	}
	pm.HandleMessage(msg)

	p, ok := pm.GetPeer("learned")
	if !ok {
		t.Fatal("should have learned peer")
	}
	if p.Source != "pex" {
		t.Fatalf("expected source 'pex', got '%s'", p.Source)
	}
}

func TestPEX_EvictStale(t *testing.T) {
	cfg := DefaultPEXConfig()
	cfg.StaleTimeout = 100 * time.Millisecond
	pm := NewPEXManager("self", cfg)

	pm.AddPeer(PEXPeer{ID: "old", LastSeen: time.Now().Add(-1 * time.Hour)})
	pm.AddPeer(PEXPeer{ID: "new", LastSeen: time.Now()})

	evicted := pm.EvictStale()
	if evicted != 1 {
		t.Fatalf("expected 1 evicted, got %d", evicted)
	}
	if pm.PeerCount() != 1 {
		t.Fatal("should have 1 peer left")
	}
}

func TestPEX_ReachablePeers(t *testing.T) {
	cfg := DefaultPEXConfig()
	cfg.MaxFailCount = 1
	pm := NewPEXManager("self", cfg)
	pm.AddPeer(PEXPeer{ID: "good"})
	pm.AddPeer(PEXPeer{ID: "bad"})
	pm.MarkFailed("bad")

	reachable := pm.ReachablePeers()
	if len(reachable) != 1 || reachable[0].ID != "good" {
		t.Fatal("should only return reachable peers")
	}
	if pm.ReachableCount() != 1 {
		t.Fatal("expected 1 reachable")
	}
}

func TestPEX_RemovePeer(t *testing.T) {
	pm := NewPEXManager("self", DefaultPEXConfig())
	pm.AddPeer(PEXPeer{ID: "p1"})

	if !pm.RemovePeer("p1") {
		t.Fatal("should return true for existing")
	}
	if pm.RemovePeer("p1") {
		t.Fatal("should return false for non-existing")
	}
}

func TestPEX_EncodeDecodePEXMessage(t *testing.T) {
	msg := PEXMessage{
		Type:   PEXResponse,
		Sender: "node-1",
		Peers:  []PEXPeer{{ID: "peer-1", Addresses: []string{"addr1"}}},
	}

	data, err := msg.Encode()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	decoded, err := DecodePEXMessage(data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded.Sender != "node-1" || len(decoded.Peers) != 1 {
		t.Fatal("roundtrip failed")
	}
}

func TestPEX_OnNewPeerCallback(t *testing.T) {
	pm := NewPEXManager("self", DefaultPEXConfig())

	var called bool
	pm.OnNewPeer(func(p PEXPeer) { called = true })

	pm.AddPeer(PEXPeer{ID: "new-peer"})
	time.Sleep(50 * time.Millisecond) // callback is async
	if !called {
		t.Fatal("OnNewPeer callback should have been called")
	}
}
