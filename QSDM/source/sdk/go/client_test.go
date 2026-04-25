package qsdm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestServer(handler http.HandlerFunc) (*httptest.Server, *Client) {
	srv := httptest.NewServer(handler)
	c := NewClient(srv.URL)
	return srv, c
}

func TestClient_GetBalance(t *testing.T) {
	srv, c := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("address"); got != "addr-1" {
			t.Errorf("unexpected address query: %s", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]float64{"balance": 42.5})
	})
	defer srv.Close()

	v, err := c.GetBalance("addr-1")
	if err != nil {
		t.Fatalf("GetBalance: %v", err)
	}
	if v != 42.5 {
		t.Fatalf("expected 42.5, got %v", v)
	}
}

func TestClient_SendTransaction_PostsBody(t *testing.T) {
	srv, c := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("want POST, got %s", r.Method)
		}
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body["from"] != "a" || body["to"] != "b" || body["amount"].(float64) != 1.5 {
			t.Fatalf("unexpected body: %+v", body)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"transaction_id": "tx-123"})
	})
	defer srv.Close()

	id, err := c.SendTransaction("a", "b", 1.5)
	if err != nil {
		t.Fatalf("SendTransaction: %v", err)
	}
	if id != "tx-123" {
		t.Fatalf("expected tx-123, got %s", id)
	}
}

func TestClient_ErrAPI_NotFound(t *testing.T) {
	srv, c := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not here", http.StatusNotFound)
	})
	defer srv.Close()

	_, err := c.GetTransaction("missing")
	if err == nil {
		t.Fatal("expected error")
	}
	if !IsNotFound(err) {
		t.Fatalf("expected IsNotFound, got %v", err)
	}
}

func TestClient_Unauthorized(t *testing.T) {
	srv, c := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusUnauthorized)
	})
	defer srv.Close()

	_, err := c.GetNodeStatus(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !IsUnauthorized(err) {
		t.Fatalf("expected IsUnauthorized, got %v", err)
	}
}

func TestClient_AuthHeaders(t *testing.T) {
	cases := []struct {
		name      string
		configure func(c *Client)
		wantHdr   string
		wantValue string
	}{
		{
			name:      "bearer",
			configure: func(c *Client) { c.SetToken("jwt-xyz") },
			wantHdr:   "Authorization",
			wantValue: "Bearer jwt-xyz",
		},
		{
			name:      "apikey",
			configure: func(c *Client) { c.SetAPIKey("k-123") },
			wantHdr:   "X-API-Key",
			wantValue: "k-123",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, c := newTestServer(func(w http.ResponseWriter, r *http.Request) {
				if got := r.Header.Get(tc.wantHdr); got != tc.wantValue {
					t.Errorf("%s: want %q, got %q", tc.wantHdr, tc.wantValue, got)
				}
				_, _ = w.Write([]byte(`{}`))
			})
			defer srv.Close()
			tc.configure(c)
			_, _ = c.GetNodeStatus(context.Background())
		})
	}
}

func TestClient_GetNodeStatus_MapsKnownFields(t *testing.T) {
	srv, c := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"node_id":   "node-a",
			"version":   "1.0.0",
			"uptime":    "1h",
			"chain_tip": 42,
			"peers":     5,
			"extra":     "kept-in-map",
		})
	})
	defer srv.Close()

	ns, err := c.GetNodeStatus(context.Background())
	if err != nil {
		t.Fatalf("GetNodeStatus: %v", err)
	}
	if ns.NodeID != "node-a" || ns.Version != "1.0.0" || ns.ChainTip != 42 || ns.Peers != 5 {
		t.Fatalf("unexpected status: %+v", ns)
	}
	if ns.Extra["extra"] != "kept-in-map" {
		t.Fatalf("extra not preserved: %+v", ns.Extra)
	}
}

func TestClient_GetMetricsPrometheus(t *testing.T) {
	srv, c := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("qsdm_tx_total 42\n"))
	})
	defer srv.Close()

	body, err := c.GetMetricsPrometheus(context.Background())
	if err != nil {
		t.Fatalf("GetMetricsPrometheus: %v", err)
	}
	if !strings.Contains(body, "qsdm_tx_total") {
		t.Fatalf("expected metric line, got %q", body)
	}
}

func TestClient_BaseURLTrimsTrailingSlash(t *testing.T) {
	c := NewClient("http://example.com/")
	if c.BaseURL != "http://example.com" {
		t.Fatalf("expected trailing slash stripped, got %q", c.BaseURL)
	}
}
