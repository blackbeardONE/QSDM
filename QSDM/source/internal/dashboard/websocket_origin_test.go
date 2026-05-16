package dashboard

// Audit row net-04: WebSocket origin validation in production.
// Pins the contract that the dashboard's WebSocket upgrader rejects
// browser-initiated upgrades from off-allowlist origins, while
// preserving the dev path (no allowlist installed ⇒ permissive).

import (
	"net/http"
	"sync"
	"testing"
)

// resetWSAllowedOriginsForTest clears the package-level allowlist so
// a test starts from a known state. Resets the once-guard too so the
// init-from-env path can be re-exercised.
func resetWSAllowedOriginsForTest() {
	wsAllowedOriginsValue.Store(nil)
	// Replace the once-guard so a subsequent test that wants the
	// env-init path can fire it again. sync.Once has no public
	// "Reset" so we re-bind the variable in-place.
	wsAllowedOriginsInitOnce = sync.Once{}
}

func TestWSCheckOrigin_NoAllowlistInstalled_IsPermissive(t *testing.T) {
	resetWSAllowedOriginsForTest()
	t.Setenv("QSDM_WS_ALLOWED_ORIGINS", "")
	t.Setenv("QSDM_CORS_ALLOWED_ORIGINS", "")

	r := &http.Request{Header: http.Header{}}
	r.Header.Set("Origin", "https://anyone-anywhere.example.com")
	if !wsCheckOrigin(r) {
		t.Fatal("dev mode (no allowlist + no env): CheckOrigin must accept all origins")
	}
}

func TestWSCheckOrigin_AllowlistInstalled_AcceptsAllowedOrigin(t *testing.T) {
	resetWSAllowedOriginsForTest()
	SetWebSocketAllowedOrigins([]string{
		"https://dashboard.qsdm.tech",
		"https://qsdm.tech",
	})
	defer resetWSAllowedOriginsForTest()

	r := &http.Request{Header: http.Header{}}
	r.Header.Set("Origin", "https://dashboard.qsdm.tech")
	if !wsCheckOrigin(r) {
		t.Fatal("origin on the allowlist must be accepted")
	}
}

func TestWSCheckOrigin_AllowlistInstalled_RejectsUnknownOrigin(t *testing.T) {
	resetWSAllowedOriginsForTest()
	SetWebSocketAllowedOrigins([]string{"https://dashboard.qsdm.tech"})
	defer resetWSAllowedOriginsForTest()

	r := &http.Request{Header: http.Header{}}
	r.Header.Set("Origin", "https://attacker.example.com")
	if wsCheckOrigin(r) {
		t.Fatal("origin not on the allowlist MUST be rejected")
	}
}

func TestWSCheckOrigin_AllowlistInstalled_RejectsMissingOriginHeader(t *testing.T) {
	resetWSAllowedOriginsForTest()
	SetWebSocketAllowedOrigins([]string{"https://dashboard.qsdm.tech"})
	defer resetWSAllowedOriginsForTest()

	// No Origin header at all (typical wscat / k6 / curl) — in
	// production this MUST be rejected so the gate is CSRF-shaped.
	r := &http.Request{Header: http.Header{}}
	if wsCheckOrigin(r) {
		t.Fatal("missing Origin header with an installed allowlist MUST be rejected")
	}
}

func TestWSCheckOrigin_CaseInsensitiveHostMatch(t *testing.T) {
	resetWSAllowedOriginsForTest()
	SetWebSocketAllowedOrigins([]string{"https://dashboard.qsdm.tech"})
	defer resetWSAllowedOriginsForTest()

	r := &http.Request{Header: http.Header{}}
	r.Header.Set("Origin", "https://Dashboard.Qsdm.Tech")
	if !wsCheckOrigin(r) {
		t.Fatal("origin with mixed-case host MUST match a lower-case allowlist entry")
	}
}

func TestWSCheckOrigin_SetEmptyClearsAllowlist(t *testing.T) {
	resetWSAllowedOriginsForTest()
	SetWebSocketAllowedOrigins([]string{"https://dashboard.qsdm.tech"})
	SetWebSocketAllowedOrigins(nil)
	defer resetWSAllowedOriginsForTest()

	r := &http.Request{Header: http.Header{}}
	r.Header.Set("Origin", "https://anyone-anywhere.example.com")
	if !wsCheckOrigin(r) {
		t.Fatal("after SetWebSocketAllowedOrigins(nil): allowlist is cleared, dev-mode permissive behaviour restored")
	}
}

func TestWSCheckOrigin_EnvFallback_CORSAllowedOrigins(t *testing.T) {
	resetWSAllowedOriginsForTest()
	t.Setenv("QSDM_WS_ALLOWED_ORIGINS", "")
	t.Setenv("QSDM_CORS_ALLOWED_ORIGINS", "https://prod.qsdm.tech,https://api.qsdm.tech")
	defer resetWSAllowedOriginsForTest()

	r := &http.Request{Header: http.Header{}}
	r.Header.Set("Origin", "https://prod.qsdm.tech")
	if !wsCheckOrigin(r) {
		t.Fatal("env fallback via QSDM_CORS_ALLOWED_ORIGINS must populate allowlist on first use")
	}

	r2 := &http.Request{Header: http.Header{}}
	r2.Header.Set("Origin", "https://attacker.example.com")
	if wsCheckOrigin(r2) {
		t.Fatal("env-populated allowlist must reject off-list origin")
	}
}

func TestSnapshotOriginsAPI_ReturnsCopy(t *testing.T) {
	resetWSAllowedOriginsForTest()
	defer resetWSAllowedOriginsForTest()
	SetWebSocketAllowedOrigins([]string{"https://dashboard.qsdm.tech"})

	snap := WebSocketAllowedOriginsSnapshot()
	if len(snap) != 1 || snap[0] != "https://dashboard.qsdm.tech" {
		t.Fatalf("unexpected snapshot: %v", snap)
	}

	// Mutating the returned slice MUST NOT affect the package state —
	// otherwise an /api/v1/status caller could overwrite the allowlist
	// by passing the snapshot through a json encoder that mutates the
	// backing array.
	snap[0] = "https://evil.example.com"
	snap2 := WebSocketAllowedOriginsSnapshot()
	if snap2[0] != "https://dashboard.qsdm.tech" {
		t.Fatal("WebSocketAllowedOriginsSnapshot must return a copy, not the backing slice")
	}
}
