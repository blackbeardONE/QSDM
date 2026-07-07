package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// TreasuryPayoutService is the production boundary between QSDM Core and a
// narrowly funded hot payout wallet. Core never receives the wallet private
// key: the signer sidecar creates and submits a normal signed CELL transfer.
type TreasuryPayoutService interface {
	Status(ctx context.Context) (TreasuryPayoutStatus, error)
	Pay(ctx context.Context, req TreasuryPayoutRequest) (TreasuryPayoutReceipt, error)
}

type TreasuryPayoutStatus struct {
	Address string  `json:"address"`
	Balance float64 `json:"balance"`
	Role    string  `json:"role,omitempty"`
}

type TreasuryPayoutRequest struct {
	RequestID string  `json:"request_id"`
	Purpose   string  `json:"purpose"`
	Recipient string  `json:"recipient"`
	Amount    float64 `json:"amount"`
}

type TreasuryPayoutReceipt struct {
	TransactionID string  `json:"transaction_id"`
	Nonce         uint64  `json:"nonce"`
	Sender        string  `json:"sender"`
	Recipient     string  `json:"recipient"`
	Amount        float64 `json:"amount"`
	Duplicate     bool    `json:"duplicate,omitempty"`
}

type treasuryPayoutHolder struct {
	mu       sync.RWMutex
	referral TreasuryPayoutService
	faucet   TreasuryPayoutService
}

var treasuryPayouts = &treasuryPayoutHolder{}

func SetReferralTreasuryPayoutService(service TreasuryPayoutService) {
	treasuryPayouts.mu.Lock()
	defer treasuryPayouts.mu.Unlock()
	treasuryPayouts.referral = service
}

func currentReferralTreasuryPayoutService() TreasuryPayoutService {
	treasuryPayouts.mu.RLock()
	defer treasuryPayouts.mu.RUnlock()
	return treasuryPayouts.referral
}

func SetFaucetTreasuryPayoutService(service TreasuryPayoutService) {
	treasuryPayouts.mu.Lock()
	defer treasuryPayouts.mu.Unlock()
	treasuryPayouts.faucet = service
}

func currentFaucetTreasuryPayoutService() TreasuryPayoutService {
	treasuryPayouts.mu.RLock()
	defer treasuryPayouts.mu.RUnlock()
	return treasuryPayouts.faucet
}

func validateExpectedTreasuryAddress(envKey, actual string) error {
	expected := strings.ToLower(strings.TrimSpace(os.Getenv(envKey)))
	actual = strings.ToLower(strings.TrimSpace(actual))
	if expected == "" {
		return nil
	}
	if err := ValidateAddress(expected); err != nil {
		return fmt.Errorf("%s is not a valid QSDM wallet address", envKey)
	}
	if expected != actual {
		return fmt.Errorf("treasury signer address %s does not match %s", actual, envKey)
	}
	return nil
}

func validateTreasuryRole(expected, actual string) error {
	actual = strings.ToLower(strings.TrimSpace(actual))
	if actual == "" {
		return fmt.Errorf("treasury signer did not report a role")
	}
	if actual != expected {
		return fmt.Errorf("treasury signer role %q does not match required role %q", actual, expected)
	}
	return nil
}

// HTTPTreasuryPayoutService talks to qsdm-game-signer (also packaged as the
// QSDM treasury signer). The bearer token authorizes only this Core process;
// spending limits and the wallet key remain in the isolated signer process.
type HTTPTreasuryPayoutService struct {
	baseURL string
	token   string
	client  *http.Client
}

func NewHTTPTreasuryPayoutService(baseURL, token string, timeout time.Duration) (*HTTPTreasuryPayoutService, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	token = strings.TrimSpace(token)
	if baseURL == "" || token == "" {
		return nil, fmt.Errorf("treasury signer URL and token are required")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("invalid treasury signer URL %q", baseURL)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("treasury signer URL must use http or https")
	}
	if parsed.Scheme == "http" {
		host := strings.ToLower(parsed.Hostname())
		ip := net.ParseIP(host)
		if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return nil, fmt.Errorf("plain HTTP treasury signer URL must use a loopback host")
		}
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &HTTPTreasuryPayoutService{
		baseURL: baseURL,
		token:   token,
		client:  &http.Client{Timeout: timeout},
	}, nil
}

func (s *HTTPTreasuryPayoutService) Status(ctx context.Context) (TreasuryPayoutStatus, error) {
	var health struct {
		Address string `json:"address"`
		Role    string `json:"role"`
	}
	if err := s.doJSON(ctx, http.MethodGet, "/healthz", nil, false, &health); err != nil {
		return TreasuryPayoutStatus{}, err
	}
	if err := ValidateAddress(strings.TrimSpace(health.Address)); err != nil {
		return TreasuryPayoutStatus{}, fmt.Errorf("treasury signer returned invalid address: %w", err)
	}

	var balance struct {
		Address string  `json:"address"`
		Balance float64 `json:"balance"`
	}
	path := "/v1/balance?address=" + url.QueryEscape(health.Address)
	if err := s.doJSON(ctx, http.MethodGet, path, nil, false, &balance); err != nil {
		return TreasuryPayoutStatus{}, err
	}
	if balance.Address != "" && !strings.EqualFold(balance.Address, health.Address) {
		return TreasuryPayoutStatus{}, fmt.Errorf("treasury signer balance response address mismatch")
	}
	return TreasuryPayoutStatus{Address: health.Address, Balance: balance.Balance, Role: health.Role}, nil
}

func (s *HTTPTreasuryPayoutService) Pay(ctx context.Context, payout TreasuryPayoutRequest) (TreasuryPayoutReceipt, error) {
	if strings.TrimSpace(payout.RequestID) == "" {
		return TreasuryPayoutReceipt{}, fmt.Errorf("treasury payout request_id is required")
	}
	if strings.TrimSpace(payout.Purpose) == "" {
		return TreasuryPayoutReceipt{}, fmt.Errorf("treasury payout purpose is required")
	}
	if err := ValidateAddress(strings.TrimSpace(payout.Recipient)); err != nil {
		return TreasuryPayoutReceipt{}, fmt.Errorf("invalid treasury payout recipient: %w", err)
	}
	if payout.Amount <= 0 {
		return TreasuryPayoutReceipt{}, fmt.Errorf("treasury payout amount must be positive")
	}

	var receipt TreasuryPayoutReceipt
	if err := s.doJSON(ctx, http.MethodPost, "/v1/pay", payout, true, &receipt); err != nil {
		return TreasuryPayoutReceipt{}, err
	}
	if receipt.TransactionID == "" || receipt.Sender == "" {
		return TreasuryPayoutReceipt{}, fmt.Errorf("treasury signer returned an incomplete receipt")
	}
	if receipt.Recipient != payout.Recipient || receipt.Amount != payout.Amount {
		return TreasuryPayoutReceipt{}, fmt.Errorf("treasury signer receipt does not match payout request")
	}
	return receipt, nil
}

func (s *HTTPTreasuryPayoutService) doJSON(ctx context.Context, method, path string, payload any, authenticated bool, out any) error {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, s.baseURL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if authenticated {
		req.Header.Set("Authorization", "Bearer "+s.token)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("treasury signer request failed: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("treasury signer HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode treasury signer response: %w", err)
	}
	return nil
}
