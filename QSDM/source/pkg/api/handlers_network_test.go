package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/blackbeardONE/QSDM/internal/topology"
)

func TestGetNetworkTopology_EmptyProviderReturnsLocalOnly(t *testing.T) {
	// Clear any provider left over from another test.
	topoState.mu.Lock()
	topoState.provider = nil
	topoState.mu.Unlock()

	h := setupTestHandlers()
	h.nodeID = "node-xyz"

	req := httptest.NewRequest(http.MethodGet, "/api/v1/network/topology", nil)
	w := httptest.NewRecorder()

	h.GetNetworkTopology(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v — %s", err, w.Body.String())
	}
	if body["live_peer_count"].(float64) != 0 {
		t.Fatalf("expected 0 peers, got %v", body["live_peer_count"])
	}
	cells := body["cells"].([]interface{})
	if len(cells) != 1 {
		t.Fatalf("expected 1 cell (local), got %d", len(cells))
	}
	if cells[0].(map[string]interface{})["id"] != "node-xyz" {
		t.Fatalf("expected local id node-xyz, got %v", cells[0].(map[string]interface{})["id"])
	}
}

func TestGetNetworkTopology_WithProviderProjectsPeers(t *testing.T) {
	prov := TopologyProviderFunc(func() (string, []topology.PeerInfo) {
		return "local-A", []topology.PeerInfo{
			{ID: "peer-1", Region: "eu", Reputation: 0.8, Connected: true, MessagesInLast: 10, LatencyMs: 50},
			{ID: "peer-2", Region: "us", Reputation: -0.5, Connected: true, MessagesInLast: 1, LatencyMs: 400},
		}
	})

	topoState.mu.Lock()
	topoState.provider = prov
	topoState.mu.Unlock()
	t.Cleanup(func() {
		topoState.mu.Lock()
		topoState.provider = nil
		topoState.mu.Unlock()
	})

	h := setupTestHandlers()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/network/topology", nil)
	w := httptest.NewRecorder()
	h.GetNetworkTopology(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if int(body["live_peer_count"].(float64)) != 2 {
		t.Fatalf("expected 2 peers, got %v", body["live_peer_count"])
	}
	if int(body["healthy_peers"].(float64)) != 1 {
		t.Fatalf("expected 1 healthy peer, got %v", body["healthy_peers"])
	}
	cells := body["cells"].([]interface{})
	if len(cells) != 3 {
		t.Fatalf("expected 3 cells (local + 2 peers), got %d", len(cells))
	}
	// First cell must be local with provider-supplied ID.
	if cells[0].(map[string]interface{})["id"] != "local-A" {
		t.Fatalf("expected local-A, got %v", cells[0].(map[string]interface{})["id"])
	}
}

func TestGetNetworkTopology_MethodNotAllowed(t *testing.T) {
	h := setupTestHandlers()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/network/topology", nil)
	w := httptest.NewRecorder()
	h.GetNetworkTopology(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}
