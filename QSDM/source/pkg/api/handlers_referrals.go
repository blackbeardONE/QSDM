package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/fileutil"
)

const (
	defaultReferralRewardCell      = 5
	defaultReferralMinAccountNonce = 1
	referralLedgerPathEnv          = "QSDM_REFERRAL_LEDGER_PATH"
)

// ReferralRewardPoolLedger is the read-only balance view used by the
// referral transparency endpoint. The live validator wires this to the
// AccountStore in solo/local mode.
type ReferralRewardPoolLedger interface {
	BalanceOf(address string) (balance float64, nonce uint64, present bool)
}

type referralRewardPoolLedgerHolder struct {
	mu     sync.RWMutex
	ledger ReferralRewardPoolLedger
}

var referralRewardPoolHolder = &referralRewardPoolLedgerHolder{}

var referralLedgerMu sync.Mutex

func SetReferralRewardPoolLedger(ledger ReferralRewardPoolLedger) {
	referralRewardPoolHolder.mu.Lock()
	defer referralRewardPoolHolder.mu.Unlock()
	referralRewardPoolHolder.ledger = ledger
}

func currentReferralRewardPoolLedger() ReferralRewardPoolLedger {
	referralRewardPoolHolder.mu.RLock()
	defer referralRewardPoolHolder.mu.RUnlock()
	return referralRewardPoolHolder.ledger
}

type ReferralRewardPoolStatusResponse struct {
	Enabled                    bool    `json:"enabled"`
	Funded                     bool    `json:"funded"`
	ClaimsEnabled              bool    `json:"claims_enabled"`
	Claimable                  bool    `json:"claimable"`
	PoolAddress                string  `json:"pool_address"`
	Balance                    float64 `json:"balance"`
	RewardPerQualifiedReferral float64 `json:"reward_per_qualified_referral"`
	MinReferredAccountNonce    uint64  `json:"min_referred_account_nonce"`
	Registrations              int     `json:"registrations"`
	Qualified                  int     `json:"qualified"`
	Claimed                    int     `json:"claimed"`
	PendingClaims              int     `json:"pending_claims"`
	Present                    bool    `json:"present"`
	Source                     string  `json:"source"`
	FundingMethod              string  `json:"funding_method"`
	LedgerConfigured           bool    `json:"ledger_configured"`
	Message                    string  `json:"message"`
	CheckedAt                  string  `json:"checked_at"`
}

type ReferralRegistrationEnvelope struct {
	ID           string `json:"id"`
	Referrer     string `json:"referrer"`
	Referred     string `json:"referred"`
	ReferralCode string `json:"referral_code"`
	InstallID    string `json:"install_id,omitempty"`
	Timestamp    string `json:"timestamp"`
	Signature    string `json:"signature"`
	PublicKey    string `json:"public_key,omitempty"`
}

type ReferralRegistrationRecord struct {
	ID             string `json:"id"`
	Referrer       string `json:"referrer"`
	Referred       string `json:"referred"`
	ReferralCode   string `json:"referral_code"`
	InstallID      string `json:"install_id,omitempty"`
	Signature      string `json:"signature"`
	PublicKey      string `json:"public_key"`
	RegisteredAt   string `json:"registered_at"`
	LastActivityAt string `json:"last_activity_at,omitempty"`
}

type ReferralClaimReceipt struct {
	TxID          string  `json:"tx_id"`
	Referrer      string  `json:"referrer"`
	Referred      string  `json:"referred"`
	Amount        float64 `json:"amount"`
	SourceAddress string  `json:"source_address,omitempty"`
	Status        string  `json:"status"`
	Reason        string  `json:"reason,omitempty"`
	CreatedAt     string  `json:"created_at"`
	CompletedAt   string  `json:"completed_at,omitempty"`
}

type ReferralRegisterResponse struct {
	Status       string                     `json:"status"`
	Registered   bool                       `json:"registered"`
	Registration ReferralRegistrationRecord `json:"registration"`
	Message      string                     `json:"message"`
}

type ReferralStatusResponse struct {
	Registered              bool                        `json:"registered"`
	Qualified               bool                        `json:"qualified"`
	Claimable               bool                        `json:"claimable"`
	Claimed                 bool                        `json:"claimed"`
	Registration            *ReferralRegistrationRecord `json:"registration,omitempty"`
	Claim                   *ReferralClaimReceipt       `json:"claim,omitempty"`
	ActivityNonce           uint64                      `json:"activity_nonce"`
	MinReferredAccountNonce uint64                      `json:"min_referred_account_nonce"`
	Message                 string                      `json:"message"`
}

type ReferralClaimRequest struct {
	Referrer string `json:"referrer"`
	Referred string `json:"referred"`
}

type ReferralClaimResponse struct {
	Status      string               `json:"status"`
	TxID        string               `json:"tx_id,omitempty"`
	Referrer    string               `json:"referrer"`
	Referred    string               `json:"referred"`
	Amount      float64              `json:"amount"`
	PoolAddress string               `json:"pool_address"`
	Receipt     ReferralClaimReceipt `json:"receipt"`
	Message     string               `json:"message"`
}

type referralLedgerFile struct {
	Version       int                                   `json:"version"`
	Registrations map[string]ReferralRegistrationRecord `json:"registrations_by_referred"`
	Claims        map[string]ReferralClaimReceipt       `json:"claims_by_referred"`
}

func (h *Handlers) ReferralRewardPoolStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	reward := referralRewardCell()
	enabled := envTruthy("QSDM_REFERRAL_REWARD_POOL_ENABLED")
	ledgerPath := referralLedgerPath()
	ledgerConfigured := ledgerPath != ""
	payoutService := currentReferralTreasuryPayoutService()
	claimsEnabled := envTruthy("QSDM_REFERRAL_CLAIMS_ENABLED") && ledgerConfigured && payoutService != nil
	minNonce := referralMinReferredAccountNonce()

	var address string
	var balance float64
	var present bool
	var payoutErr error
	if enabled && payoutService != nil {
		var status TreasuryPayoutStatus
		status, payoutErr = payoutService.Status(r.Context())
		if payoutErr == nil {
			address = status.Address
			balance = status.Balance
			present = true
			payoutErr = validateTreasuryRole("referral", status.Role)
			if payoutErr == nil {
				payoutErr = validateExpectedTreasuryAddress("QSDM_REFERRAL_REWARD_POOL_ADDRESS", address)
			}
		}
	}

	activityLedger := currentReferralRewardPoolLedger()
	registrations, qualified, claimed, pending := referralLedgerCounts(ledgerPath, activityLedger, minNonce)
	funded := enabled && payoutErr == nil && present && balance >= reward
	claimable := funded && claimsEnabled && qualified > claimed
	message := "QSDM Referral Reward Pool is not enabled on this validator."
	switch {
	case envTruthy("QSDM_REFERRAL_CLAIMS_ENABLED") && !ledgerConfigured:
		message = "Referral claims were requested, but QSDM_REFERRAL_LEDGER_PATH is not configured."
	case enabled && payoutService == nil:
		message = "QSDM Referral Reward Pool is enabled, but no isolated treasury signer is configured."
	case enabled && payoutErr != nil:
		message = "QSDM Referral Reward Pool signer is unavailable or misconfigured: " + payoutErr.Error()
	case claimable:
		message = "QSDM Referral Reward Pool is funded for qualified referral payouts."
	case enabled && funded:
		message = "QSDM Referral Reward Pool is funded, but referral claims are disabled until the signed eligibility ledger is enabled."
	case enabled:
		message = "QSDM Referral Reward Pool is enabled but needs more CELL before payouts can be claimed."
	}

	writeJSONResponse(w, http.StatusOK, ReferralRewardPoolStatusResponse{
		Enabled:                    enabled,
		Funded:                     funded,
		ClaimsEnabled:              claimsEnabled,
		Claimable:                  claimable,
		PoolAddress:                address,
		Balance:                    balance,
		RewardPerQualifiedReferral: reward,
		MinReferredAccountNonce:    minNonce,
		Registrations:              registrations,
		Qualified:                  qualified,
		Claimed:                    claimed,
		PendingClaims:              pending,
		Present:                    present,
		Source:                     "qsdm-referral-treasury-wallet",
		FundingMethod:              "isolated-signer-signed-transfer",
		LedgerConfigured:           ledgerConfigured,
		Message:                    message,
		CheckedAt:                  time.Now().UTC().Format(time.RFC3339),
	})
}

func (h *Handlers) ReferralRegisterSigned(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if referralLedgerPath() == "" {
		writeErrorResponse(w, http.StatusServiceUnavailable, "referral eligibility ledger is not configured")
		return
	}

	var env ReferralRegistrationEnvelope
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&env); err != nil {
		writeErrorResponse(w, http.StatusBadRequest, "invalid referral envelope: "+err.Error())
		return
	}
	if err := h.validateReferralRegistrationEnvelope(env); err != nil {
		writeErrorResponse(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.verifyReferralRegistrationEnvelope(env); err != nil {
		writeErrorResponse(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	referralLedgerMu.Lock()
	defer referralLedgerMu.Unlock()

	store, err := loadReferralLedgerFile(referralLedgerPath())
	if err != nil {
		writeErrorResponse(w, http.StatusInternalServerError, "failed to read referral ledger")
		return
	}
	if existing, ok := store.Registrations[env.Referred]; ok {
		if existing.Referrer == env.Referrer && strings.EqualFold(existing.ReferralCode, env.ReferralCode) {
			writeJSONResponse(w, http.StatusOK, ReferralRegisterResponse{
				Status:       "already_registered",
				Registered:   true,
				Registration: existing,
				Message:      "referral was already registered for this referred wallet",
			})
			return
		}
		writeErrorResponse(w, http.StatusConflict, "referred wallet is already bound to a different referral")
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	record := ReferralRegistrationRecord{
		ID:           env.ID,
		Referrer:     env.Referrer,
		Referred:     env.Referred,
		ReferralCode: env.ReferralCode,
		InstallID:    env.InstallID,
		Signature:    env.Signature,
		PublicKey:    env.PublicKey,
		RegisteredAt: now,
	}
	store.Registrations[env.Referred] = record
	if err := saveReferralLedgerFile(referralLedgerPath(), store); err != nil {
		writeErrorResponse(w, http.StatusInternalServerError, "failed to save referral ledger")
		return
	}

	writeJSONResponse(w, http.StatusOK, ReferralRegisterResponse{
		Status:       "registered",
		Registered:   true,
		Registration: record,
		Message:      "referral registration accepted",
	})
}

func (h *Handlers) ReferralStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	referred := strings.TrimSpace(r.URL.Query().Get("referred"))
	if referred == "" {
		writeErrorResponse(w, http.StatusBadRequest, "referred query parameter is required")
		return
	}
	if err := ValidateAddress(referred); err != nil {
		writeErrorResponse(w, http.StatusBadRequest, "invalid referred address: "+err.Error())
		return
	}
	resp, statusCode := h.referralStatusForReferred(r.Context(), referred)
	writeJSONResponse(w, statusCode, resp)
}

func (h *Handlers) ReferralClaim(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !envTruthy("QSDM_REFERRAL_CLAIMS_ENABLED") {
		writeErrorResponse(w, http.StatusServiceUnavailable, "referral claims are disabled")
		return
	}
	if referralLedgerPath() == "" {
		writeErrorResponse(w, http.StatusServiceUnavailable, "referral eligibility ledger is not configured")
		return
	}

	var req ReferralClaimRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
		writeErrorResponse(w, http.StatusBadRequest, "invalid claim request: "+err.Error())
		return
	}
	req.Referrer = strings.TrimSpace(req.Referrer)
	req.Referred = strings.TrimSpace(req.Referred)
	if err := ValidateAddress(req.Referrer); err != nil {
		writeErrorResponse(w, http.StatusBadRequest, "invalid referrer address: "+err.Error())
		return
	}
	if err := ValidateAddress(req.Referred); err != nil {
		writeErrorResponse(w, http.StatusBadRequest, "invalid referred address: "+err.Error())
		return
	}
	if req.Referrer == req.Referred {
		writeErrorResponse(w, http.StatusBadRequest, "self-referrals cannot be claimed")
		return
	}

	payoutService := currentReferralTreasuryPayoutService()
	if payoutService == nil {
		writeErrorResponse(w, http.StatusServiceUnavailable, "referral treasury signer is not configured")
		return
	}
	treasuryStatus, err := payoutService.Status(r.Context())
	if err != nil {
		writeErrorResponse(w, http.StatusBadGateway, "referral treasury signer is unavailable")
		return
	}
	if err := validateExpectedTreasuryAddress("QSDM_REFERRAL_REWARD_POOL_ADDRESS", treasuryStatus.Address); err != nil {
		writeErrorResponse(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	if err := validateTreasuryRole("referral", treasuryStatus.Role); err != nil {
		writeErrorResponse(w, http.StatusServiceUnavailable, err.Error())
		return
	}

	referralLedgerMu.Lock()
	defer referralLedgerMu.Unlock()

	store, err := loadReferralLedgerFile(referralLedgerPath())
	if err != nil {
		writeErrorResponse(w, http.StatusInternalServerError, "failed to read referral ledger")
		return
	}
	reg, ok := store.Registrations[req.Referred]
	if !ok {
		writeErrorResponse(w, http.StatusNotFound, "referred wallet has no signed referral registration")
		return
	}
	if reg.Referrer != req.Referrer {
		writeErrorResponse(w, http.StatusConflict, "claim referrer does not match referral registration")
		return
	}
	if existing, ok := store.Claims[req.Referred]; ok {
		switch existing.Status {
		case "claimed":
			writeJSONResponse(w, http.StatusConflict, ReferralClaimResponse{
				Status:      "already_claimed",
				TxID:        existing.TxID,
				Referrer:    req.Referrer,
				Referred:    req.Referred,
				Amount:      existing.Amount,
				PoolAddress: existing.SourceAddress,
				Receipt:     existing,
				Message:     "this referred wallet already produced a referral reward",
			})
			return
		}
	}

	qualified, activityNonce, reason := referralRegistrationQualified(reg, currentReferralRewardPoolLedger())
	if !qualified {
		writeJSONResponse(w, http.StatusConflict, ReferralStatusResponse{
			Registered:              true,
			Qualified:               false,
			Claimable:               false,
			Registration:            &reg,
			ActivityNonce:           activityNonce,
			MinReferredAccountNonce: referralMinReferredAccountNonce(),
			Message:                 reason,
		})
		return
	}

	reward := referralRewardCell()
	if treasuryStatus.Balance < reward {
		writeErrorResponse(w, http.StatusPaymentRequired, "referral reward pool is not funded enough for this claim")
		return
	}

	now := time.Now().UTC()
	txID := referralClaimTxID(req.Referrer, req.Referred, reg.ID)
	receipt := ReferralClaimReceipt{
		TxID:          txID,
		Referrer:      req.Referrer,
		Referred:      req.Referred,
		Amount:        reward,
		SourceAddress: treasuryStatus.Address,
		Status:        "pending",
		CreatedAt:     now.Format(time.RFC3339),
	}
	store.Claims[req.Referred] = receipt
	if err := saveReferralLedgerFile(referralLedgerPath(), store); err != nil {
		writeErrorResponse(w, http.StatusInternalServerError, "failed to reserve referral claim")
		return
	}

	payoutReceipt, err := payoutService.Pay(r.Context(), TreasuryPayoutRequest{
		RequestID: txID,
		Purpose:   "referral",
		Recipient: req.Referrer,
		Amount:    reward,
	})
	if err != nil {
		receipt.Status = "failed"
		receipt.Reason = err.Error()
		store.Claims[req.Referred] = receipt
		_ = saveReferralLedgerFile(referralLedgerPath(), store)
		writeErrorResponse(w, http.StatusBadGateway, "referral treasury payout failed")
		return
	}

	receipt.TxID = payoutReceipt.TransactionID
	receipt.Status = "claimed"
	receipt.CompletedAt = time.Now().UTC().Format(time.RFC3339)
	store.Claims[req.Referred] = receipt
	if err := saveReferralLedgerFile(referralLedgerPath(), store); err != nil {
		writeErrorResponse(w, http.StatusInternalServerError, "referral payout transferred but receipt finalization failed; claim is left pending for reconciliation")
		return
	}

	writeJSONResponse(w, http.StatusOK, ReferralClaimResponse{
		Status:      "claimed",
		TxID:        receipt.TxID,
		Referrer:    req.Referrer,
		Referred:    req.Referred,
		Amount:      reward,
		PoolAddress: treasuryStatus.Address,
		Receipt:     receipt,
		Message:     "referral reward paid from QSDM Referral Reward Pool",
	})
}

func referralLedgerPath() string {
	return strings.TrimSpace(os.Getenv(referralLedgerPathEnv))
}

func referralRewardCell() float64 {
	reward := localCellFaucetFloatEnv("QSDM_REFERRAL_REWARD_CELL", defaultReferralRewardCell)
	if reward <= 0 || math.IsNaN(reward) || math.IsInf(reward, 0) {
		return defaultReferralRewardCell
	}
	return reward
}

func referralMinReferredAccountNonce() uint64 {
	raw := strings.TrimSpace(os.Getenv("QSDM_REFERRAL_MIN_ACCOUNT_NONCE"))
	if raw == "" {
		return defaultReferralMinAccountNonce
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return defaultReferralMinAccountNonce
	}
	return value
}

func (h *Handlers) validateReferralRegistrationEnvelope(env ReferralRegistrationEnvelope) error {
	if err := ValidateTransactionID(env.ID); err != nil {
		return fmt.Errorf("invalid registration id: %w", err)
	}
	if err := ValidateAddress(env.Referrer); err != nil {
		return fmt.Errorf("invalid referrer address: %w", err)
	}
	if err := ValidateAddress(env.Referred); err != nil {
		return fmt.Errorf("invalid referred address: %w", err)
	}
	if env.Referrer == env.Referred {
		return fmt.Errorf("self-referrals are not allowed")
	}
	if err := ValidateString(env.ReferralCode, "referral_code", 6, 64); err != nil {
		return err
	}
	if strings.ToUpper(env.ReferralCode) != referralCodeForAddress(env.Referrer) {
		return fmt.Errorf("referral_code does not match referrer wallet")
	}
	if err := ValidateString(env.InstallID, "install_id", 0, 128); err != nil {
		return err
	}
	if err := ValidateTimestamp(env.Timestamp); err != nil {
		return err
	}
	if env.PublicKey == "" {
		return fmt.Errorf("envelope.public_key is required")
	}
	if env.Signature == "" {
		return fmt.Errorf("envelope.signature is required")
	}
	return nil
}

func (h *Handlers) verifyReferralRegistrationEnvelope(env ReferralRegistrationEnvelope) error {
	if h.walletService == nil {
		return errors.New(msgWalletServiceUnavailable)
	}
	pubBytes, err := hex.DecodeString(env.PublicKey)
	if err != nil {
		return fmt.Errorf("envelope.public_key is not valid hex")
	}
	derivedAddr := hex.EncodeToString(sha256Sum(pubBytes))
	if derivedAddr != env.Referred {
		return fmt.Errorf("envelope.referred does not match hex(sha256(public_key))")
	}
	sigBytes, err := hex.DecodeString(env.Signature)
	if err != nil {
		return fmt.Errorf("envelope.signature is not valid hex")
	}
	unsigned := env
	unsigned.Signature = ""
	unsigned.PublicKey = ""
	canonical, err := json.Marshal(unsigned)
	if err != nil {
		return fmt.Errorf("failed to canonicalise referral envelope")
	}
	ok, verr := h.walletService.VerifySignature(canonical, sigBytes, pubBytes)
	if verr != nil || !ok {
		return fmt.Errorf("signature does not verify under envelope.public_key")
	}
	return nil
}

func referralCodeForAddress(address string) string {
	sum := sha256.Sum256([]byte(address))
	return strings.ToUpper(hex.EncodeToString(sum[:])[:12])
}

func loadReferralLedgerFile(path string) (referralLedgerFile, error) {
	store := referralLedgerFile{
		Version:       1,
		Registrations: map[string]ReferralRegistrationRecord{},
		Claims:        map[string]ReferralClaimReceipt{},
	}
	if strings.TrimSpace(path) == "" {
		return store, fmt.Errorf("referral ledger path is empty")
	}
	data, err := os.ReadFile(path) // #nosec G304 -- path is the operator-configured referral ledger, not request data.
	if os.IsNotExist(err) {
		return store, nil
	}
	if err != nil {
		return store, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return store, nil
	}
	if err := json.Unmarshal(data, &store); err != nil {
		return store, err
	}
	if store.Version == 0 {
		store.Version = 1
	}
	if store.Registrations == nil {
		store.Registrations = map[string]ReferralRegistrationRecord{}
	}
	if store.Claims == nil {
		store.Claims = map[string]ReferralClaimReceipt{}
	}
	return store, nil
}

func saveReferralLedgerFile(path string, store referralLedgerFile) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("referral ledger path is empty")
	}
	if store.Version == 0 {
		store.Version = 1
	}
	if store.Registrations == nil {
		store.Registrations = map[string]ReferralRegistrationRecord{}
	}
	if store.Claims == nil {
		store.Claims = map[string]ReferralClaimReceipt{}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	return fileutil.WriteFileAtomic(path, data, 0o600)
}

func referralLedgerCounts(path string, ledger ReferralRewardPoolLedger, minNonce uint64) (registrations, qualified, claimed, pending int) {
	if path == "" {
		return 0, 0, 0, 0
	}
	referralLedgerMu.Lock()
	defer referralLedgerMu.Unlock()
	store, err := loadReferralLedgerFile(path)
	if err != nil {
		return 0, 0, 0, 0
	}
	for _, reg := range store.Registrations {
		registrations++
		if ok, _, _ := referralRegistrationQualifiedWithMin(reg, ledger, minNonce); ok {
			qualified++
		}
	}
	for _, claim := range store.Claims {
		switch claim.Status {
		case "claimed":
			claimed++
		case "pending":
			pending++
		}
	}
	return registrations, qualified, claimed, pending
}

func (h *Handlers) referralStatusForReferred(ctx context.Context, referred string) (ReferralStatusResponse, int) {
	if referralLedgerPath() == "" {
		return ReferralStatusResponse{Message: "referral eligibility ledger is not configured"}, http.StatusServiceUnavailable
	}
	referralLedgerMu.Lock()
	defer referralLedgerMu.Unlock()
	store, err := loadReferralLedgerFile(referralLedgerPath())
	if err != nil {
		return ReferralStatusResponse{Message: "failed to read referral ledger"}, http.StatusInternalServerError
	}
	reg, ok := store.Registrations[referred]
	if !ok {
		return ReferralStatusResponse{
			Registered:              false,
			MinReferredAccountNonce: referralMinReferredAccountNonce(),
			Message:                 "referred wallet has no signed referral registration",
		}, http.StatusOK
	}
	qualified, activityNonce, reason := referralRegistrationQualified(reg, currentReferralRewardPoolLedger())
	claim, claimed, pending := referralClaimForReferred(store, referred)
	treasuryReady := false
	if service := currentReferralTreasuryPayoutService(); service != nil {
		if status, err := service.Status(ctx); err == nil &&
			validateTreasuryRole("referral", status.Role) == nil &&
			validateExpectedTreasuryAddress("QSDM_REFERRAL_REWARD_POOL_ADDRESS", status.Address) == nil &&
			status.Balance >= referralRewardCell() {
			treasuryReady = true
		}
	}
	if qualified && !treasuryReady {
		reason = "referred wallet is qualified, but the referral treasury is unavailable or unfunded"
	}
	return ReferralStatusResponse{
		Registered:              true,
		Qualified:               qualified,
		Claimable:               qualified && treasuryReady && envTruthy("QSDM_REFERRAL_CLAIMS_ENABLED") && !claimed && !pending,
		Claimed:                 claimed,
		Registration:            &reg,
		Claim:                   claim,
		ActivityNonce:           activityNonce,
		MinReferredAccountNonce: referralMinReferredAccountNonce(),
		Message:                 reason,
	}, http.StatusOK
}

func referralClaimForReferred(store referralLedgerFile, referred string) (*ReferralClaimReceipt, bool, bool) {
	claim, ok := store.Claims[referred]
	if !ok {
		return nil, false, false
	}
	return &claim, claim.Status == "claimed", claim.Status == "pending"
}

func referralRegistrationQualified(reg ReferralRegistrationRecord, ledger ReferralRewardPoolLedger) (bool, uint64, string) {
	return referralRegistrationQualifiedWithMin(reg, ledger, referralMinReferredAccountNonce())
}

func referralRegistrationQualifiedWithMin(reg ReferralRegistrationRecord, ledger ReferralRewardPoolLedger, minNonce uint64) (bool, uint64, string) {
	if reg.Referrer == "" || reg.Referred == "" {
		return false, 0, "referral registration is incomplete"
	}
	if reg.Referrer == reg.Referred {
		return false, 0, "self-referrals are not eligible"
	}
	if ledger == nil {
		return false, 0, "account activity ledger is not configured"
	}
	_, nonce, present := ledger.BalanceOf(reg.Referred)
	if !present {
		return false, 0, "referred wallet has no account activity yet"
	}
	if nonce < minNonce {
		return false, nonce, fmt.Sprintf("referred wallet needs account nonce >= %d before referral reward can be claimed", minNonce)
	}
	return true, nonce, "referred wallet is qualified for referral reward"
}

func referralClaimTxID(referrer, referred, registrationID string) string {
	sum := sha256.Sum256([]byte("qsdm-referral-claim:" + referrer + ":" + referred + ":" + registrationID))
	return "refclaim_" + hex.EncodeToString(sum[:])[:24]
}
