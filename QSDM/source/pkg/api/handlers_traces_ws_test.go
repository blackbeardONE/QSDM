package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/blackbeardONE/QSDM/internal/logging"
	"github.com/blackbeardONE/QSDM/pkg/config"
)

// The contract-trace WebSocket must upgrade through the middleware chain the
// node really serves: Server.Start wraps the routes in setupMiddleware and then
// in the request-size cap. AuditLogMiddleware wraps the ResponseWriter to
// record the status, and that wrapper did not pass http.Hijacker through, so
// every upgrade answered 500 "websocket: response does not implement
// http.Hijacker".
func TestContractTraceWebSocketUpgradesThroughServerMiddleware(t *testing.T) {
	srv, err := NewServer(&config.Config{JWTHMACSecret: strings.Repeat("s", 32)}, logging.NewSilentLogger(), nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	t.Cleanup(func() { _ = srv.Stop() })
	srv.SetContractEngine(makeTraceHandlers(t).contractEngine)

	// The same handler Server.Start serves, minus the listener.
	ts := httptest.NewServer(RequestSizeLimitMiddleware(1 << 20)(srv.SetupTestHandler()))
	defer ts.Close()
	url := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/v1/contracts/traces/ws?n=5"

	// The route is not public, so the handshake needs an access token.
	if conn, resp, err := websocket.DefaultDialer.Dial(url, nil); err == nil {
		conn.Close()
		t.Fatal("the handshake succeeded without a token; the route requires a JWT")
	} else if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("handshake without a token: %v (response %+v), want 401", err, resp)
	}

	token, err := srv.authManager.CreateToken("trace-viewer", "trace-viewer", "user", TokenTypeAccess, 15*time.Minute)
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	conn, resp, err := websocket.DefaultDialer.Dial(url, http.Header{"Authorization": {"Bearer " + token}})
	if err != nil {
		status, body := 0, ""
		if resp != nil {
			status = resp.StatusCode
			raw, _ := io.ReadAll(resp.Body)
			body = string(raw)
		}
		t.Fatalf("WebSocket handshake through the server middleware failed: %v (status %d, body %q)", err, status, body)
	}
	defer conn.Close()

	if err := conn.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	var msg struct {
		Type   string            `json:"type"`
		Traces []json.RawMessage `json:"traces"`
	}
	if err := conn.ReadJSON(&msg); err != nil {
		t.Fatalf("read the first trace message: %v", err)
	}
	if msg.Type != "contract_traces" || len(msg.Traces) == 0 {
		t.Fatalf("first message: type %q with %d traces, want contract_traces with at least one", msg.Type, len(msg.Traces))
	}
}
