package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

const testFaucetTreasuryAddress = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"

func configureFaucetTest(t *testing.T, activity *fakeLocalWalletLedger, payout TreasuryPayoutService) {
	t.Helper()
	t.Setenv("QSDM_LOCAL_CELL_FAUCET", "1")
	t.Setenv("QSDM_LOCAL_CELL_FAUCET_TOKEN", "test-faucet-token")
	t.Setenv("QSDM_LOCAL_CELL_FAUCET_TARGET_BALANCE", "1")
	t.Setenv("QSDM_LOCAL_CELL_FAUCET_MAX_GRANT", "1")
	t.Setenv("QSDM_FAUCET_TREASURY_ADDRESS", testFaucetTreasuryAddress)
	SetReferralRewardPoolLedger(activity)
	SetFaucetTreasuryPayoutService(payout)
	t.Cleanup(func() {
		SetReferralRewardPoolLedger(nil)
		SetFaucetTreasuryPayoutService(nil)
	})
}

func postFaucetClaim(t *testing.T, h *Handlers, address string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(localCellFaucetClaimRequest{Address: address, Amount: 999})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/faucet/claim", bytes.NewReader(body))
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set(localCellFaucetTokenHeader, "test-faucet-token")
	w := httptest.NewRecorder()
	h.LocalCellFaucetClaim(w, req)
	return w
}

func TestFaucetClaimRequiresFundedTreasurySigner(t *testing.T) {
	configureFaucetTest(t, &fakeLocalWalletLedger{}, nil)
	w := postFaucetClaim(t, setupTestHandlers(), "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestFaucetClaimPaysOnceThroughTreasury(t *testing.T) {
	address := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	activity := &fakeLocalWalletLedger{
		balances: map[string]float64{address: 0},
		present:  map[string]bool{address: true},
	}
	payout := &fakeTreasuryPayoutService{address: testFaucetTreasuryAddress, balance: 10, role: "faucet"}
	configureFaucetTest(t, activity, payout)
	h := setupTestHandlers()

	first := postFaucetClaim(t, h, address)
	if first.Code != http.StatusOK {
		t.Fatalf("first status=%d body=%s", first.Code, first.Body.String())
	}
	var firstResponse LocalCellFaucetClaimResponse
	if err := json.Unmarshal(first.Body.Bytes(), &firstResponse); err != nil {
		t.Fatal(err)
	}
	if firstResponse.Status != "funded" || firstResponse.AmountGranted != 1 || payout.balance != 9 {
		t.Fatalf("unexpected first response=%+v treasury_balance=%v", firstResponse, payout.balance)
	}

	second := postFaucetClaim(t, h, address)
	if second.Code != http.StatusOK {
		t.Fatalf("second status=%d body=%s", second.Code, second.Body.String())
	}
	var secondResponse LocalCellFaucetClaimResponse
	if err := json.Unmarshal(second.Body.Bytes(), &secondResponse); err != nil {
		t.Fatal(err)
	}
	if secondResponse.Status != "already_claimed" || secondResponse.AmountGranted != 0 || payout.balance != 9 {
		t.Fatalf("unexpected duplicate response=%+v treasury_balance=%v", secondResponse, payout.balance)
	}
}

func TestFaucetClaimRejectsInvalidAddress(t *testing.T) {
	payout := &fakeTreasuryPayoutService{address: testFaucetTreasuryAddress, balance: 10, role: "faucet"}
	configureFaucetTest(t, &fakeLocalWalletLedger{}, payout)
	w := postFaucetClaim(t, setupTestHandlers(), "not-a-wallet")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if payout.balance != 10 {
		t.Fatalf("invalid address changed treasury balance: %v", payout.balance)
	}
}
