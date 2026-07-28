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

func TestClient_GetWalletNonce(t *testing.T) {
	const address = "1111111111111111111111111111111111111111111111111111111111111111"
	srv, client := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/wallet/nonce" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("sender"); got != address {
			t.Fatalf("unexpected sender query: %s", got)
		}
		_ = json.NewEncoder(w).Encode(WalletNonceResponse{
			Sender: address,
			Nonce:  8,
			Next:   9,
		})
	})
	defer srv.Close()

	response, err := client.GetWalletNonce(address)
	if err != nil {
		t.Fatalf("GetWalletNonce: %v", err)
	}
	if response.Sender != address || response.Nonce != 8 || response.Next != 9 {
		t.Fatalf("unexpected nonce response: %+v", response)
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

func TestClient_CELLStreamEndpoints(t *testing.T) {
	var requests []string
	srv, client := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.RequestURI())
		switch {
		case r.Method == http.MethodPost:
			var envelope StreamActionEnvelope
			if err := json.NewDecoder(r.Body).Decode(&envelope); err != nil {
				t.Fatal(err)
			}
			if envelope.Action.ID != "action-1" || envelope.Signature != "aa" {
				t.Fatalf("unexpected stream envelope: %+v", envelope)
			}
			_ = json.NewEncoder(w).Encode(StreamActionSubmitResponse{
				ActionID: "action-1", Status: "accepted",
			})
		case strings.HasSuffix(r.URL.Path, "/stream-1"):
			_ = json.NewEncoder(w).Encode(StreamResponse{
				Stream: StreamState{StreamID: "stream-1"},
			})
		default:
			_ = json.NewEncoder(w).Encode(StreamsResponse{Streams: []StreamState{}})
		}
	})
	defer srv.Close()

	if _, err := client.GetStreams(StreamFilters{
		Payer: "payer 1", Status: "active", ServiceID: "qsdm-vpn",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetStream("stream-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.SubmitStreamAction(StreamActionEnvelope{
		Action:    StreamAction{ID: "action-1", StreamID: "stream-1", Action: "pause"},
		Signature: "aa", PublicKey: "bb",
	}); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 3 {
		t.Fatalf("requests = %v", requests)
	}
	if !strings.Contains(requests[0], "payer=payer+1") ||
		!strings.Contains(requests[0], "service_id=qsdm-vpn") {
		t.Fatalf("stream filters missing: %s", requests[0])
	}
	if requests[1] != "GET /api/v1/streams/stream-1" {
		t.Fatalf("get stream request = %s", requests[1])
	}
	if requests[2] != "POST /api/v1/streams/actions/submit-signed" {
		t.Fatalf("submit stream request = %s", requests[2])
	}
}

func TestClient_GetStreamActionNonce(t *testing.T) {
	const address = "2222222222222222222222222222222222222222222222222222222222222222"
	srv, client := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/streams/nonce" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("sender"); got != address {
			t.Fatalf("unexpected sender query: %s", got)
		}
		_ = json.NewEncoder(w).Encode(StreamActionNonceResponse{
			Runtime:     "qsdm-native",
			Source:      "chain",
			Sender:      address,
			ActionNonce: 0,
			Present:     false,
		})
	})
	defer srv.Close()

	response, err := client.GetStreamActionNonce(address)
	if err != nil {
		t.Fatalf("GetStreamActionNonce: %v", err)
	}
	if response.ActionNonce != 0 || response.Present {
		t.Fatalf("unexpected stream nonce response: %+v", response)
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

// TestClient_GetTransaction_PinsPluralPath is the regression guard
// for commit b7e3a38: GetTransaction must hit /api/v1/transactions/{id}
// (plural) and not the pre-rebrand /api/v1/transaction/{id} (singular,
// 404 in production). Symmetric to the JS-side guard added in the
// same commit at sdk/javascript/qsdm.test.js. The httptest server
// would have happily responded to either URL — a positive path-pin
// is required for the test to actually catch a typo revival.
func TestClient_GetTransaction_PinsPluralPath(t *testing.T) {
	const wantURL = "/api/v1/transactions/tx-77"
	srv, c := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != wantURL {
			t.Errorf("GetTransaction URL path: want %q, got %q "+
				"(regression of the pre-0.3.1 singular typo? see "+
				"pkg/api/handlers.go:269-270 for the canonical mux registration)",
				wantURL, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"id":     "tx-77",
			"amount": 12.5,
			"status": "confirmed",
		})
	})
	defer srv.Close()

	out, err := c.GetTransaction("tx-77")
	if err != nil {
		t.Fatalf("GetTransaction: %v", err)
	}
	if out["id"] != "tx-77" {
		t.Fatalf("expected id=tx-77, got %v (full=%+v)", out["id"], out)
	}
	if out["status"] != "confirmed" {
		t.Fatalf("expected status=confirmed, got %v (full=%+v)", out["status"], out)
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
