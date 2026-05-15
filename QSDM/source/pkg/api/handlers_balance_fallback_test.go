package api

// Tests for the mining-ledger fallback path in GetBalance
// (pkg/api/handlers.go::GetBalance, the /api/v1/wallet/balance
// handler).
//
// Why this fallback exists, in one paragraph: the BLR1 solo
// validator runs the FileStorage backend, which intentionally
// returns balance=0, nonce=0 for every address (see
// pkg/storage/file_storage.go and the audit Notes on crypto-04 /
// v0.4.1 — "FileStorage.GetNonce returns (0, nil) symmetric with
// GetBalance's silent-zero so the new public read endpoint
// works"). The authoritative CELL ledger lives in the validator's
// in-memory AccountStore, surfaced via the MiningAccountProbe
// interface that handlers_mining.go installs at boot. Without
// this fallback, qsdm.tech/wallet.html and every other consumer
// of /api/v1/wallet/balance shows zero for accounts that hold
// real CELL — the user-visible symptom that prompted this fix.
//
// The contract pinned by these tests:
//
//   (1) storage non-zero -> response is storage value, source=storage
//   (2) storage zero, no probe wired -> response is 0, source=storage
//   (3) storage zero, probe wired but no record for address ->
//       response is 0, source=storage
//   (4) storage zero, probe wired with non-zero record for
//       address -> response is the probe value, source=mining-ledger
//   (5) storage zero, probe wired with zero record (present=true,
//       balance=0 — "received and spent everything") ->
//       response is 0, source=storage (we only lift when the
//       probe value is strictly > 0; equal-zero gives no new
//       information so we don't add a misleading source label)

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func decodeBalanceResp(t *testing.T, body []byte) (addr string, bal float64, source string) {
	t.Helper()
	var resp map[string]interface{}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode balance body: %v (raw=%s)", err, string(body))
	}
	addr, _ = resp["address"].(string)
	bal, _ = resp["balance"].(float64)
	source, _ = resp["source"].(string)
	return
}

func TestGetBalance_StorageNonZero_NoFallback(t *testing.T) {
	SetMiningAccountProbe(nil)
	t.Cleanup(func() { SetMiningAccountProbe(nil) })

	h := setupTestHandlers()
	h.storage.(*mockStorage).SetBalance("test_address", 123.456)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/wallet/balance?address=test_address", nil)
	rec := httptest.NewRecorder()
	h.GetBalance(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	addr, bal, source := decodeBalanceResp(t, rec.Body.Bytes())
	if addr != "test_address" {
		t.Errorf("address: want test_address, got %q", addr)
	}
	if bal != 123.456 {
		t.Errorf("balance: want 123.456, got %v", bal)
	}
	if source != "storage" {
		t.Errorf("source: want storage, got %q", source)
	}
}

func TestGetBalance_StorageZero_NoProbe_StaysZero(t *testing.T) {
	SetMiningAccountProbe(nil)
	t.Cleanup(func() { SetMiningAccountProbe(nil) })

	h := setupTestHandlers()
	// Storage is at zero by default (mockStorage gives 0 for unknown).

	req := httptest.NewRequest(http.MethodGet, "/api/v1/wallet/balance?address=unknown_addr", nil)
	rec := httptest.NewRecorder()
	h.GetBalance(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	_, bal, source := decodeBalanceResp(t, rec.Body.Bytes())
	if bal != 0 {
		t.Errorf("balance: want 0, got %v", bal)
	}
	if source != "storage" {
		t.Errorf("source: want storage when no probe is wired, got %q", source)
	}
}

func TestGetBalance_StorageZero_ProbeMissesAddress_StaysZero(t *testing.T) {
	SetMiningAccountProbe(&fakeAccountProbe{
		addrs: map[string]struct {
			bal   float64
			nonce uint64
		}{
			"other_addr": {bal: 999, nonce: 3},
		},
	})
	t.Cleanup(func() { SetMiningAccountProbe(nil) })

	h := setupTestHandlers()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/wallet/balance?address=unknown_addr", nil)
	rec := httptest.NewRecorder()
	h.GetBalance(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	_, bal, source := decodeBalanceResp(t, rec.Body.Bytes())
	if bal != 0 {
		t.Errorf("balance: want 0 (probe has no record for this address), got %v", bal)
	}
	if source != "storage" {
		t.Errorf("source: want storage when probe misses, got %q", source)
	}
}

func TestGetBalance_StorageZero_ProbeHasBalance_LiftsToMiningLedger(t *testing.T) {
	// This is the prod symptom the fallback exists to fix:
	// BLR1 FileStorage returns 0, the on-chain AccountStore has
	// 45,316 CELL at qsdm1miner-rtx3050, and the user sees the
	// wrong number in the browser wallet UI. After this fallback
	// landed, /wallet/balance lifts to the mining-ledger value.
	SetMiningAccountProbe(&fakeAccountProbe{
		addrs: map[string]struct {
			bal   float64
			nonce uint64
		}{
			"qsdm1miner-rtx3050": {bal: 45315.83207005032, nonce: 4},
		},
	})
	t.Cleanup(func() { SetMiningAccountProbe(nil) })

	h := setupTestHandlers()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/wallet/balance?address=qsdm1miner-rtx3050", nil)
	rec := httptest.NewRecorder()
	h.GetBalance(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	addr, bal, source := decodeBalanceResp(t, rec.Body.Bytes())
	if addr != "qsdm1miner-rtx3050" {
		t.Errorf("address: want qsdm1miner-rtx3050, got %q", addr)
	}
	if bal != 45315.83207005032 {
		t.Errorf("balance: want 45315.83207005032 lifted from mining ledger, got %v", bal)
	}
	if source != "mining-ledger" {
		t.Errorf("source: want mining-ledger when probe lifted the value, got %q", source)
	}
}

func TestGetBalance_StorageZero_ProbeAlsoZero_NoSpuriousLift(t *testing.T) {
	// Negative case for the lift: when the probe agrees with
	// storage that the balance is zero (present=true,
	// balance=0 — "received and spent everything"), we do NOT
	// label the response source=mining-ledger because the
	// probe value doesn't add information beyond what storage
	// already said. Stops the wallet UI from showing a
	// confusing "ledger says 0" banner where it should just
	// say "you have 0 CELL".
	SetMiningAccountProbe(&fakeAccountProbe{
		addrs: map[string]struct {
			bal   float64
			nonce uint64
		}{
			"spent_addr": {bal: 0, nonce: 12},
		},
	})
	t.Cleanup(func() { SetMiningAccountProbe(nil) })

	h := setupTestHandlers()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/wallet/balance?address=spent_addr", nil)
	rec := httptest.NewRecorder()
	h.GetBalance(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	_, bal, source := decodeBalanceResp(t, rec.Body.Bytes())
	if bal != 0 {
		t.Errorf("balance: want 0, got %v", bal)
	}
	if source != "storage" {
		t.Errorf("source: want storage when probe also reports 0 (no lift), got %q", source)
	}
}
