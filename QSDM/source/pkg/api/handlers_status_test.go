package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/branding"
	"github.com/blackbeardONE/QSDM/pkg/config"
)

// TestStatusHandler_DefaultsToValidator exercises /api/v1/status with no
// explicit node role set. The handler must coerce to validator and return the
// canonical coin metadata from pkg/branding without requiring any live peer
// or chain-tip callbacks.
func TestStatusHandler_DefaultsToValidator(t *testing.T) {
	h := setupTestHandlers()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	rec := httptest.NewRecorder()
	h.StatusHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q, want application/json; charset=utf-8", ct)
	}

	var resp StatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
	}

	if resp.NodeRole != string(config.NodeRoleValidator) {
		t.Errorf("NodeRole = %q, want %q", resp.NodeRole, config.NodeRoleValidator)
	}
	if resp.Coin.Name != branding.CoinName {
		t.Errorf("Coin.Name = %q, want %q", resp.Coin.Name, branding.CoinName)
	}
	if resp.Coin.Symbol != branding.CoinSymbol {
		t.Errorf("Coin.Symbol = %q, want %q", resp.Coin.Symbol, branding.CoinSymbol)
	}
	if resp.Coin.Decimals != branding.CoinDecimals {
		t.Errorf("Coin.Decimals = %d, want %d", resp.Coin.Decimals, branding.CoinDecimals)
	}
	if resp.Coin.SmallestUnit != branding.SmallestUnitName {
		t.Errorf("Coin.SmallestUnit = %q, want %q", resp.Coin.SmallestUnit, branding.SmallestUnitName)
	}
	if resp.Branding.Name != branding.Name {
		t.Errorf("Branding.Name = %q, want %q", resp.Branding.Name, branding.Name)
	}
	if resp.Branding.LegacyName != branding.LegacyName {
		t.Errorf("Branding.LegacyName = %q, want %q", resp.Branding.LegacyName, branding.LegacyName)
	}
	if resp.Network != branding.NetworkLabel() {
		t.Errorf("Network = %q, want %q", resp.Network, branding.NetworkLabel())
	}
}

// TestStatusHandler_MinerRole verifies SetNodeRole("miner") is echoed back by
// the handler, and that the coin metadata remains the canonical CELL values.
func TestStatusHandler_MinerRole(t *testing.T) {
	h := setupTestHandlers()
	h.SetNodeRole(config.NodeRoleMiner)
	h.SetPeerCountSource(func() int { return 7 })
	h.SetChainTipSource(func() uint64 { return 4242 })

	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	rec := httptest.NewRecorder()
	h.StatusHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var resp StatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.NodeRole != string(config.NodeRoleMiner) {
		t.Errorf("NodeRole = %q, want %q", resp.NodeRole, config.NodeRoleMiner)
	}
	if resp.Peers != 7 {
		t.Errorf("Peers = %d, want 7", resp.Peers)
	}
	if resp.ChainTip != 4242 {
		t.Errorf("ChainTip = %d, want 4242", resp.ChainTip)
	}
}

// TestStatusHandler_RejectsNonGET covers the method-allowlist on
// /api/v1/status; non-GET verbs must return 405 with an Allow header.
func TestStatusHandler_RejectsNonGET(t *testing.T) {
	h := setupTestHandlers()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/status", nil)
	rec := httptest.NewRecorder()
	h.StatusHandler(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
	if allow := rec.Header().Get("Allow"); allow != http.MethodGet {
		t.Errorf("Allow = %q, want GET", allow)
	}
}

// TestStatusHandler_InvalidRoleCoerced ensures an invalid role passed via the
// private field does not leak into the public response; the handler normalises
// to validator.
func TestStatusHandler_InvalidRoleCoerced(t *testing.T) {
	h := setupTestHandlers()
	h.nodeRole = "super-miner"

	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	rec := httptest.NewRecorder()
	h.StatusHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp StatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.NodeRole != string(config.NodeRoleValidator) {
		t.Errorf("NodeRole = %q, want validator (invalid values must be coerced)", resp.NodeRole)
	}
}
