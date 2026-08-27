package dashboard

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/api"
	"github.com/blackbeardONE/QSDM/pkg/monitoring"
)

// /ws streams metrics, health and network topology to every connected client
// (StartWSPush), and ServeWS upgraded the connection without checking any
// credential -- it was registered without requireAuth, so no authManager state
// made any difference. Topology is the peer map.
func TestWebSocketRouteRequiresAuth(t *testing.T) {
	m := monitoring.GetMetrics()
	hc := monitoring.NewHealthChecker(m)
	d := &Dashboard{
		metrics:       m,
		healthChecker: hc,
		port:          "0",
		bindAddress:   "127.0.0.1",
		authManager:   nil,
		rateLimiter:   api.NewRateLimiter(50, time.Minute),
		wsHub:         NewWSHub(),
	}

	handler, err := d.buildHandler()
	if err != nil {
		t.Fatalf("buildHandler: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	// Assert the request was refused BY THE AUTH LAYER, not merely that it
	// failed. An earlier version of this test accepted "anything but 101",
	// which passed with /ws unwrapped: httptest.NewRecorder does not
	// implement http.Hijacker, so the upgrade always fails here whether or
	// not authentication ran. The test proved the recorder's limitation,
	// not the fix.
	//
	// With requireAuth in front and a nil authManager, the response is the
	// auth-unavailable 503. Without it the request reaches handleWS and the
	// failed upgrade surfaces as 500. Distinguishing the two is what makes
	// this substantive -- "not 101" does not.
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503 from the auth layer, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "authentication is not available") {
		t.Errorf("503 did not come from the auth layer, body: %s", rr.Body.String())
	}
	if d.wsHub.ClientCount() != 0 {
		t.Errorf("an unauthenticated client was registered with the hub: %d client(s)", d.wsHub.ClientCount())
	}
}

// An empty bind address reaches net.JoinHostPort as an empty host, which yields
// ":8081" -- every interface. Neither tracked deploy script sets
// dashboard_bind_address and pkg/config applies no default, so a VPS install
// published the dashboard. Loopback is the safe default; exposing it must be
// an explicit choice.
func TestBindAddressDefaultsToLoopback(t *testing.T) {
	cases := map[string]string{
		"":            "127.0.0.1",
		"   ":         "127.0.0.1",
		"0.0.0.0":     "0.0.0.0", // explicit opt-in is honoured
		"127.0.0.1":   "127.0.0.1",
		"  10.0.0.5 ": "10.0.0.5",
		"::1":         "::1",
	}
	for in, want := range cases {
		if got := defaultBindAddress(in); got != want {
			t.Errorf("defaultBindAddress(%q) = %q, want %q", in, got, want)
		}
	}

	// The property that actually matters: the resolved address is never the
	// empty host that JoinHostPort turns into a wildcard bind.
	if strings.TrimSpace(defaultBindAddress("")) == "" {
		t.Error("an unset bind address still resolves to the wildcard")
	}
}
