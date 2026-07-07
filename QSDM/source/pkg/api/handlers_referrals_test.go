package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/wallet"
)

const testReferralTreasuryAddress = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"

type fakeTreasuryPayoutService struct {
	address  string
	balance  float64
	role     string
	payments map[string]TreasuryPayoutReceipt
	err      error
}

func (f *fakeTreasuryPayoutService) Status(context.Context) (TreasuryPayoutStatus, error) {
	if f.err != nil {
		return TreasuryPayoutStatus{}, f.err
	}
	role := f.role
	if role == "" {
		role = "referral"
	}
	return TreasuryPayoutStatus{Address: f.address, Balance: f.balance, Role: role}, nil
}

func TestReferralLedgerSaveReplacesExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "referrals.json")
	first := referralLedgerFile{
		Version:       1,
		Registrations: map[string]ReferralRegistrationRecord{},
		Claims:        map[string]ReferralClaimReceipt{},
	}
	if err := saveReferralLedgerFile(path, first); err != nil {
		t.Fatalf("first save: %v", err)
	}
	first.Registrations["wallet"] = ReferralRegistrationRecord{ID: "registration"}
	if err := saveReferralLedgerFile(path, first); err != nil {
		t.Fatalf("replacement save: %v", err)
	}
	loaded, err := loadReferralLedgerFile(path)
	if err != nil {
		t.Fatalf("load replacement: %v", err)
	}
	if loaded.Registrations["wallet"].ID != "registration" {
		t.Fatalf("replacement was not persisted: %#v", loaded.Registrations)
	}
}

func (f *fakeTreasuryPayoutService) Pay(_ context.Context, req TreasuryPayoutRequest) (TreasuryPayoutReceipt, error) {
	if f.err != nil {
		return TreasuryPayoutReceipt{}, f.err
	}
	if existing, ok := f.payments[req.RequestID]; ok {
		existing.Duplicate = true
		return existing, nil
	}
	if f.balance < req.Amount {
		return TreasuryPayoutReceipt{}, fmt.Errorf("insufficient treasury balance")
	}
	f.balance -= req.Amount
	receipt := TreasuryPayoutReceipt{
		TransactionID: "treasury_" + req.RequestID,
		Sender:        f.address, Recipient: req.Recipient, Amount: req.Amount,
	}
	if f.payments == nil {
		f.payments = map[string]TreasuryPayoutReceipt{}
	}
	f.payments[req.RequestID] = receipt
	return receipt, nil
}

func TestReferralRewardPoolStatusDisabled(t *testing.T) {
	SetReferralRewardPoolLedger(nil)
	SetReferralTreasuryPayoutService(nil)
	t.Cleanup(func() {
		SetReferralRewardPoolLedger(nil)
		SetReferralTreasuryPayoutService(nil)
	})
	t.Setenv("QSDM_REFERRAL_REWARD_POOL_ENABLED", "")

	handlers := setupTestHandlers()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/referrals/reward-pool", nil)
	w := httptest.NewRecorder()

	handlers.ReferralRewardPoolStatus(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp ReferralRewardPoolStatusResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Enabled || resp.Funded {
		t.Fatalf("expected disabled/unfunded, got %+v", resp)
	}
}

func TestReferralRewardPoolStatusFunded(t *testing.T) {
	t.Cleanup(func() {
		SetReferralRewardPoolLedger(nil)
		SetReferralTreasuryPayoutService(nil)
	})
	t.Setenv("QSDM_REFERRAL_REWARD_POOL_ENABLED", "1")
	t.Setenv("QSDM_REFERRAL_REWARD_POOL_ADDRESS", testReferralTreasuryAddress)
	t.Setenv("QSDM_REFERRAL_REWARD_CELL", "5")
	SetReferralTreasuryPayoutService(&fakeTreasuryPayoutService{address: testReferralTreasuryAddress, balance: 500})

	handlers := setupTestHandlers()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/referrals/reward-pool", nil)
	w := httptest.NewRecorder()

	handlers.ReferralRewardPoolStatus(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp ReferralRewardPoolStatusResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Enabled || !resp.Funded || resp.Balance != 500 || resp.RewardPerQualifiedReferral != 5 {
		t.Fatalf("unexpected funded response: %+v", resp)
	}
	if resp.Claimable {
		t.Fatalf("claims should be disabled by default until eligibility ledger is enabled: %+v", resp)
	}
	if resp.FundingMethod != "isolated-signer-signed-transfer" {
		t.Fatalf("funding method=%q, want isolated-signer-signed-transfer", resp.FundingMethod)
	}
}

func TestReferralRewardPoolStatusClaimableRequiresClaimsEnabled(t *testing.T) {
	t.Cleanup(func() {
		SetReferralRewardPoolLedger(nil)
		SetReferralTreasuryPayoutService(nil)
	})
	t.Setenv("QSDM_REFERRAL_REWARD_POOL_ENABLED", "1")
	t.Setenv("QSDM_REFERRAL_CLAIMS_ENABLED", "1")
	t.Setenv("QSDM_REFERRAL_LEDGER_PATH", filepath.Join(t.TempDir(), "referrals.json"))
	t.Setenv("QSDM_REFERRAL_REWARD_POOL_ADDRESS", testReferralTreasuryAddress)
	t.Setenv("QSDM_REFERRAL_REWARD_CELL", "5")
	SetReferralTreasuryPayoutService(&fakeTreasuryPayoutService{address: testReferralTreasuryAddress, balance: 500})

	handlers := setupTestHandlers()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/referrals/reward-pool", nil)
	w := httptest.NewRecorder()

	handlers.ReferralRewardPoolStatus(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp ReferralRewardPoolStatusResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.ClaimsEnabled {
		t.Fatalf("expected claims-enabled response, got %+v", resp)
	}
	if resp.Claimable {
		t.Fatalf("pool should not be claimable until at least one referral is qualified: %+v", resp)
	}
}

func TestReferralRewardPoolStatusMethod(t *testing.T) {
	handlers := setupTestHandlers()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/referrals/reward-pool", nil)
	w := httptest.NewRecorder()

	handlers.ReferralRewardPoolStatus(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func signedReferralEnvelope(t *testing.T, ws *wallet.WalletService, referrer string) ReferralRegistrationEnvelope {
	t.Helper()
	pubKey := ws.GetPublicKey()
	if pubKey == nil {
		t.Fatal("ws.GetPublicKey returned nil")
	}
	referredHash := sha256.Sum256(pubKey)
	referred := hex.EncodeToString(referredHash[:])
	idHash := sha256.Sum256([]byte(referrer + referred + time.Now().UTC().Format(time.RFC3339Nano)))
	env := ReferralRegistrationEnvelope{
		ID:           hex.EncodeToString(idHash[:16]),
		Referrer:     referrer,
		Referred:     referred,
		ReferralCode: referralCodeForAddress(referrer),
		InstallID:    "test-install",
		Timestamp:    time.Now().UTC().Format(time.RFC3339),
	}
	env.Signature = ""
	env.PublicKey = ""
	canonical, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal referral envelope: %v", err)
	}
	sig, err := ws.SignData(canonical)
	if err != nil {
		t.Fatalf("sign referral envelope: %v", err)
	}
	env.Signature = hex.EncodeToString(sig)
	env.PublicKey = hex.EncodeToString(pubKey)
	return env
}

func postReferralRegistration(t *testing.T, h *Handlers, env ReferralRegistrationEnvelope) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal registration: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/referrals/register-signed", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ReferralRegisterSigned(w, req)
	return w
}

func registerReferralForTest(t *testing.T, h *Handlers, env ReferralRegistrationEnvelope) {
	t.Helper()
	w := postReferralRegistration(t, h, env)
	if w.Code != http.StatusOK {
		t.Fatalf("register status=%d body=%s", w.Code, w.Body.String())
	}
}

func configureReferralLedgerTest(t *testing.T, ledger *fakeLocalWalletLedger) *fakeTreasuryPayoutService {
	t.Helper()
	path := filepath.Join(t.TempDir(), "referrals.json")
	t.Setenv("QSDM_REFERRAL_LEDGER_PATH", path)
	t.Setenv("QSDM_REFERRAL_REWARD_POOL_ENABLED", "1")
	t.Setenv("QSDM_REFERRAL_REWARD_POOL_ADDRESS", testReferralTreasuryAddress)
	t.Setenv("QSDM_REFERRAL_REWARD_CELL", "5")
	t.Setenv("QSDM_REFERRAL_MIN_ACCOUNT_NONCE", "1")
	SetReferralRewardPoolLedger(ledger)
	payout := &fakeTreasuryPayoutService{address: testReferralTreasuryAddress, balance: 10}
	SetReferralTreasuryPayoutService(payout)
	t.Cleanup(func() {
		SetReferralRewardPoolLedger(nil)
		SetReferralTreasuryPayoutService(nil)
	})
	return payout
}

func TestReferralRegisterSignedAcceptsSignedReferredWallet(t *testing.T) {
	ws, err := wallet.NewWalletService()
	if err != nil {
		t.Skipf("wallet requires Dilithium: %v", err)
	}
	referrer := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	ledger := &fakeLocalWalletLedger{}
	configureReferralLedgerTest(t, ledger)
	h := setupTestHandlersWithSubmesh(nil, ws)

	env := signedReferralEnvelope(t, ws, referrer)
	w := postReferralRegistration(t, h, env)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp ReferralRegisterResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Registered || resp.Registration.Referrer != referrer || resp.Registration.Referred != env.Referred {
		t.Fatalf("unexpected registration response: %+v", resp)
	}
}

func TestReferralRegisterRejectsSelfReferral(t *testing.T) {
	ws, err := wallet.NewWalletService()
	if err != nil {
		t.Skipf("wallet requires Dilithium: %v", err)
	}
	ledger := &fakeLocalWalletLedger{}
	configureReferralLedgerTest(t, ledger)
	h := setupTestHandlersWithSubmesh(nil, ws)

	env := signedReferralEnvelope(t, ws, ws.GetAddress())
	env.ReferralCode = referralCodeForAddress(env.Referrer)
	w := postReferralRegistration(t, h, env)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestReferralClaimRequiresClaimsEnabled(t *testing.T) {
	ws, err := wallet.NewWalletService()
	if err != nil {
		t.Skipf("wallet requires Dilithium: %v", err)
	}
	referrer := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	ledger := &fakeLocalWalletLedger{
		balances: map[string]float64{"ref-pool-test": 10},
		nonces:   map[string]uint64{},
		present:  map[string]bool{"ref-pool-test": true},
	}
	configureReferralLedgerTest(t, ledger)
	h := setupTestHandlersWithSubmesh(nil, ws)
	env := signedReferralEnvelope(t, ws, referrer)
	registerReferralForTest(t, h, env)

	body := []byte(`{"referrer":"` + referrer + `","referred":"` + env.Referred + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/referrals/claim", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.ReferralClaim(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestReferralClaimRequiresReferredActivity(t *testing.T) {
	ws, err := wallet.NewWalletService()
	if err != nil {
		t.Skipf("wallet requires Dilithium: %v", err)
	}
	referrer := "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	ledger := &fakeLocalWalletLedger{
		balances: map[string]float64{"ref-pool-test": 10},
		nonces:   map[string]uint64{},
		present:  map[string]bool{"ref-pool-test": true},
	}
	payout := configureReferralLedgerTest(t, ledger)
	t.Setenv("QSDM_REFERRAL_CLAIMS_ENABLED", "1")
	h := setupTestHandlersWithSubmesh(nil, ws)
	env := signedReferralEnvelope(t, ws, referrer)
	registerReferralForTest(t, h, env)

	body := []byte(`{"referrer":"` + referrer + `","referred":"` + env.Referred + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/referrals/claim", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.ReferralClaim(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if payout.balance != 10 || len(payout.payments) != 0 {
		t.Fatalf("claim changed treasury despite no activity: balance=%v payments=%+v", payout.balance, payout.payments)
	}
}

func TestReferralClaimPaysOnceFromPool(t *testing.T) {
	ws, err := wallet.NewWalletService()
	if err != nil {
		t.Skipf("wallet requires Dilithium: %v", err)
	}
	referrer := "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	ledger := &fakeLocalWalletLedger{
		balances: map[string]float64{"ref-pool-test": 10},
		nonces:   map[string]uint64{},
		present:  map[string]bool{"ref-pool-test": true},
	}
	payout := configureReferralLedgerTest(t, ledger)
	t.Setenv("QSDM_REFERRAL_CLAIMS_ENABLED", "1")
	h := setupTestHandlersWithSubmesh(nil, ws)
	env := signedReferralEnvelope(t, ws, referrer)
	ledger.present[env.Referred] = true
	ledger.nonces[env.Referred] = 1
	registerReferralForTest(t, h, env)

	body := []byte(`{"referrer":"` + referrer + `","referred":"` + env.Referred + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/referrals/claim", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.ReferralClaim(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if payout.balance != 5 || len(payout.payments) != 1 {
		t.Fatalf("unexpected treasury state after claim: balance=%v payments=%+v", payout.balance, payout.payments)
	}

	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/referrals/claim", bytes.NewReader(body))
	w2 := httptest.NewRecorder()
	h.ReferralClaim(w2, req2)
	if w2.Code != http.StatusConflict {
		t.Fatalf("second status=%d body=%s", w2.Code, w2.Body.String())
	}
	if payout.balance != 5 || len(payout.payments) != 1 {
		t.Fatalf("duplicate claim changed treasury state: balance=%v payments=%+v", payout.balance, payout.payments)
	}
}
