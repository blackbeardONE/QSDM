package api

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const localCellFaucetTokenHeader = "X-QSDM-Local-Faucet-Token"

type localCellFaucetClaimRequest struct {
	Address string  `json:"address"`
	Amount  float64 `json:"amount,omitempty"` // compatibility only; operator policy decides the grant
}

type LocalCellFaucetClaimResponse struct {
	Address         string  `json:"address"`
	Status          string  `json:"status"`
	AmountGranted   float64 `json:"amount_granted"`
	BalanceBefore   float64 `json:"balance_before"`
	BalanceAfter    float64 `json:"balance_after"`
	TargetBalance   float64 `json:"target_balance"`
	Source          string  `json:"source"`
	TreasuryAddress string  `json:"treasury_address,omitempty"`
	TransactionID   string  `json:"transaction_id,omitempty"`
	CheckedAt       string  `json:"checked_at"`
}

func (h *Handlers) LocalCellFaucetClaim(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !envTruthy("QSDM_LOCAL_CELL_FAUCET") {
		writeErrorResponse(w, http.StatusServiceUnavailable, "operator-funded starter grants are disabled")
		return
	}
	if !isLoopbackRemote(r) {
		writeErrorResponse(w, http.StatusForbidden, "starter grants only accept loopback clients")
		return
	}

	token := strings.TrimSpace(os.Getenv("QSDM_LOCAL_CELL_FAUCET_TOKEN"))
	if token == "" {
		writeErrorResponse(w, http.StatusServiceUnavailable, "starter grant access token is not configured")
		return
	}
	providedToken := strings.TrimSpace(r.Header.Get(localCellFaucetTokenHeader))
	if subtle.ConstantTimeCompare([]byte(providedToken), []byte(token)) != 1 {
		writeErrorResponse(w, http.StatusForbidden, "invalid starter grant access token")
		return
	}

	payoutService := currentFaucetTreasuryPayoutService()
	if payoutService == nil {
		writeErrorResponse(w, http.StatusServiceUnavailable, "onboarding treasury signer is not configured")
		return
	}
	treasury, err := payoutService.Status(r.Context())
	if err != nil {
		writeErrorResponse(w, http.StatusBadGateway, "onboarding treasury signer is unavailable")
		return
	}
	if err := validateExpectedTreasuryAddress("QSDM_FAUCET_TREASURY_ADDRESS", treasury.Address); err != nil {
		writeErrorResponse(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	if err := validateTreasuryRole("faucet", treasury.Role); err != nil {
		writeErrorResponse(w, http.StatusServiceUnavailable, err.Error())
		return
	}

	var req localCellFaucetClaimRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
		writeErrorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}
	address := strings.ToLower(strings.TrimSpace(req.Address))
	if err := ValidateAddress(address); err != nil {
		writeErrorResponse(w, http.StatusBadRequest, "invalid wallet address: "+err.Error())
		return
	}

	target := localCellFaucetFloatEnv("QSDM_LOCAL_CELL_FAUCET_TARGET_BALANCE", 1)
	maxGrant := localCellFaucetFloatEnv("QSDM_LOCAL_CELL_FAUCET_MAX_GRANT", 1)
	if target <= 0 || maxGrant <= 0 {
		writeErrorResponse(w, http.StatusServiceUnavailable, "starter grant limits are invalid")
		return
	}

	activityLedger := currentReferralRewardPoolLedger()
	if activityLedger == nil {
		writeErrorResponse(w, http.StatusServiceUnavailable, "account activity ledger is not configured")
		return
	}
	balanceBefore, _, _ := activityLedger.BalanceOf(address)
	if balanceBefore >= target {
		writeJSONResponse(w, http.StatusOK, LocalCellFaucetClaimResponse{
			Address:         address,
			Status:          "already_funded",
			BalanceBefore:   balanceBefore,
			BalanceAfter:    balanceBefore,
			TargetBalance:   target,
			Source:          "qsdm-onboarding-treasury",
			TreasuryAddress: treasury.Address,
			CheckedAt:       time.Now().UTC().Format(time.RFC3339),
		})
		return
	}

	grant := target - balanceBefore
	if grant > maxGrant {
		grant = maxGrant
	}
	if grant <= 0 || math.IsNaN(grant) || math.IsInf(grant, 0) {
		writeErrorResponse(w, http.StatusBadRequest, "grant amount must be positive")
		return
	}
	if treasury.Balance < grant {
		writeErrorResponse(w, http.StatusPaymentRequired, "onboarding treasury has insufficient CELL")
		return
	}

	payout, err := payoutService.Pay(r.Context(), TreasuryPayoutRequest{
		RequestID: localCellFaucetClaimID(address),
		Purpose:   "faucet",
		Recipient: address,
		Amount:    grant,
	})
	if err != nil {
		writeErrorResponse(w, http.StatusBadGateway, "onboarding treasury payout failed")
		return
	}

	balanceAfter := balanceBefore + grant
	if current, _, present := activityLedger.BalanceOf(address); present {
		balanceAfter = current
	}
	status := "funded"
	amountGranted := grant
	if payout.Duplicate {
		status = "already_claimed"
		amountGranted = 0
	}

	writeJSONResponse(w, http.StatusOK, LocalCellFaucetClaimResponse{
		Address:         address,
		Status:          status,
		AmountGranted:   amountGranted,
		BalanceBefore:   balanceBefore,
		BalanceAfter:    balanceAfter,
		TargetBalance:   target,
		Source:          "qsdm-onboarding-treasury",
		TreasuryAddress: payout.Sender,
		TransactionID:   payout.TransactionID,
		CheckedAt:       time.Now().UTC().Format(time.RFC3339),
	})
}

func localCellFaucetClaimID(address string) string {
	sum := sha256.Sum256([]byte("qsdm-onboarding-grant:v1:" + address))
	return "faucet_" + hex.EncodeToString(sum[:])
}

func localCellFaucetFloatEnv(key string, fallback float64) float64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return fallback
	}
	return value
}

func isLoopbackRemote(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
