// Package qsdm provides an official Go client for the QSDM HTTP API.
//
// It wraps the `pkg/api` REST surface exposed by a running qsdm node:
// wallet balance, transaction send/query, health probes, node metadata, peer listing,
// and Prometheus/JSON metrics snapshots.
//
// The client is safe for concurrent use and does not embed any node-local state;
// authentication is supplied through SetAPIKey or SetToken and forwarded as headers.
package qsdm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client is the QSDM HTTP API client.
type Client struct {
	BaseURL    string
	HTTPClient *http.Client
	APIKey     string
	Token      string
}

// NewClient creates a new QSDM API client with a 30s default timeout.
func NewClient(baseURL string) *Client {
	return &Client{
		BaseURL:    strings.TrimRight(baseURL, "/"),
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// SetAPIKey sets the API key for authentication (sent as X-API-Key header).
func (c *Client) SetAPIKey(apiKey string) { c.APIKey = apiKey }

// SetToken sets the JWT token for authentication (sent as Authorization: Bearer).
func (c *Client) SetToken(token string) { c.Token = token }

// ErrAPI indicates the server returned a non-2xx response. Callers can use errors.As to
// extract the status code and response body for diagnostics.
type ErrAPI struct {
	StatusCode int
	Body       string
	URL        string
}

func (e *ErrAPI) Error() string {
	return fmt.Sprintf("qsdm: %s returned %d: %s", e.URL, e.StatusCode, truncate(e.Body, 256))
}

// IsNotFound reports whether err is a 404 API error.
func IsNotFound(err error) bool {
	var ae *ErrAPI
	return errors.As(err, &ae) && ae.StatusCode == http.StatusNotFound
}

// IsUnauthorized reports whether err is a 401/403 API error.
func IsUnauthorized(err error) bool {
	var ae *ErrAPI
	return errors.As(err, &ae) && (ae.StatusCode == http.StatusUnauthorized || ae.StatusCode == http.StatusForbidden)
}

// GetBalance retrieves the balance for an address.
func (c *Client) GetBalance(address string) (float64, error) {
	return c.GetBalanceContext(context.Background(), address)
}

// GetBalanceContext is GetBalance with an explicit context.
func (c *Client) GetBalanceContext(ctx context.Context, address string) (float64, error) {
	q := url.Values{}
	q.Set("address", address)
	var resp struct {
		Balance float64 `json:"balance"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/v1/wallet/balance?"+q.Encode(), nil, &resp); err != nil {
		return 0, err
	}
	return resp.Balance, nil
}

type WalletNonceResponse struct {
	Sender string `json:"sender"`
	Nonce  uint64 `json:"nonce"`
	Next   uint64 `json:"next"`
}

// GetWalletNonce returns the last applied and next required nonce for a wallet.
func (c *Client) GetWalletNonce(address string) (*WalletNonceResponse, error) {
	return c.GetWalletNonceContext(context.Background(), address)
}

// GetWalletNonceContext is GetWalletNonce with an explicit context.
func (c *Client) GetWalletNonceContext(ctx context.Context, address string) (*WalletNonceResponse, error) {
	q := url.Values{}
	q.Set("sender", address)
	var response WalletNonceResponse
	if err := c.do(ctx, http.MethodGet, "/api/v1/wallet/nonce?"+q.Encode(), nil, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

// SendTransaction sends a transaction and returns its ID.
func (c *Client) SendTransaction(from, to string, amount float64) (string, error) {
	return c.SendTransactionContext(context.Background(), from, to, amount)
}

// SendTransactionContext is SendTransaction with an explicit context.
func (c *Client) SendTransactionContext(ctx context.Context, from, to string, amount float64) (string, error) {
	body := map[string]interface{}{"from": from, "to": to, "amount": amount}
	var resp struct {
		TransactionID string `json:"transaction_id"`
	}
	if err := c.do(ctx, http.MethodPost, "/api/v1/wallet/send", body, &resp); err != nil {
		return "", err
	}
	return resp.TransactionID, nil
}

// StreamUsageReceipt is a payer-session-signed cumulative active-use receipt.
type StreamUsageReceipt struct {
	StreamID                string `json:"stream_id"`
	Sequence                uint64 `json:"sequence"`
	CumulativeActiveSeconds uint64 `json:"cumulative_active_seconds"`
	ObservedAt              string `json:"observed_at"`
	Signature               string `json:"signature,omitempty"`
}

// StreamAction is the canonical action signed by a QSDM wallet.
type StreamAction struct {
	ID                 string              `json:"id"`
	Sender             string              `json:"sender"`
	StreamID           string              `json:"stream_id"`
	Action             string              `json:"action"`
	Provider           string              `json:"provider,omitempty"`
	ServiceID          string              `json:"service_id,omitempty"`
	DeviceIDHash       string              `json:"device_id_hash,omitempty"`
	SessionPublicKey   string              `json:"session_public_key,omitempty"`
	PriceDust          uint64              `json:"price_dust,omitempty"`
	PricePeriodSeconds uint64              `json:"price_period_seconds,omitempty"`
	BudgetDust         uint64              `json:"budget_dust,omitempty"`
	MaxActiveSeconds   uint64              `json:"max_active_seconds,omitempty"`
	ExpiresAt          string              `json:"expires_at,omitempty"`
	Receipt            *StreamUsageReceipt `json:"receipt,omitempty"`
	Nonce              uint64              `json:"nonce,omitempty"`
	Timestamp          string              `json:"timestamp"`
}

type StreamActionEnvelope struct {
	Action    StreamAction `json:"action"`
	Signature string       `json:"signature"`
	PublicKey string       `json:"public_key"`
}

type StreamState struct {
	StreamID                string `json:"stream_id"`
	Payer                   string `json:"payer"`
	Provider                string `json:"provider"`
	ServiceID               string `json:"service_id"`
	DeviceIDHash            string `json:"device_id_hash"`
	SessionPublicKey        string `json:"session_public_key"`
	PriceDust               uint64 `json:"price_dust"`
	PricePeriodSeconds      uint64 `json:"price_period_seconds"`
	BudgetDust              uint64 `json:"budget_dust"`
	MaxActiveSeconds        uint64 `json:"max_active_seconds"`
	ExpiresAt               string `json:"expires_at"`
	Status                  string `json:"status"`
	CumulativeActiveSeconds uint64 `json:"cumulative_active_seconds"`
	PausedDurationSeconds   uint64 `json:"paused_duration_seconds"`
	LastReceiptSequence     uint64 `json:"last_receipt_sequence"`
	LastReceiptObservedAt   string `json:"last_receipt_observed_at,omitempty"`
	AccruedDust             uint64 `json:"accrued_dust"`
	SettledDust             uint64 `json:"settled_dust"`
	RefundedDust            uint64 `json:"refunded_dust"`
	RemainingBudgetDust     uint64 `json:"remaining_budget_dust"`
	UnsettledDust           uint64 `json:"unsettled_dust"`
	OpenedAt                string `json:"opened_at"`
	LastPausedAt            string `json:"last_paused_at,omitempty"`
	LastResumedAt           string `json:"last_resumed_at,omitempty"`
	ClosedAt                string `json:"closed_at,omitempty"`
	LastAction              string `json:"last_action"`
	LastActionID            string `json:"last_action_id"`
	LastActionAt            string `json:"last_action_at"`
	ActionCount             uint64 `json:"action_count"`
}

type StreamsResponse struct {
	Runtime   string        `json:"runtime"`
	Source    string        `json:"source"`
	StateRoot string        `json:"state_root"`
	Streams   []StreamState `json:"streams"`
}

type StreamResponse struct {
	Runtime   string      `json:"runtime"`
	Source    string      `json:"source"`
	StateRoot string      `json:"state_root"`
	Stream    StreamState `json:"stream"`
}

type StreamActionSubmitResponse struct {
	ActionID      string `json:"action_id"`
	StreamID      string `json:"stream_id"`
	Action        string `json:"action"`
	Sender        string `json:"sender"`
	Status        string `json:"status"`
	MempoolStatus string `json:"mempool_status"`
}

// StreamActionNonceResponse contains the exact current nonce required by the
// sender's next qsdm/streams/v1 action.
type StreamActionNonceResponse struct {
	Runtime     string `json:"runtime"`
	Source      string `json:"source"`
	Sender      string `json:"sender"`
	ActionNonce uint64 `json:"action_nonce"`
	Present     bool   `json:"present"`
}

type StreamFilters struct {
	Payer     string
	Provider  string
	Status    string
	ServiceID string
}

func (c *Client) GetStreamActionNonce(address string) (*StreamActionNonceResponse, error) {
	return c.GetStreamActionNonceContext(context.Background(), address)
}

func (c *Client) GetStreamActionNonceContext(ctx context.Context, address string) (*StreamActionNonceResponse, error) {
	query := url.Values{}
	query.Set("sender", address)
	var response StreamActionNonceResponse
	if err := c.do(ctx, http.MethodGet, "/api/v1/streams/nonce?"+query.Encode(), nil, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (c *Client) GetStreams(filters StreamFilters) (*StreamsResponse, error) {
	return c.GetStreamsContext(context.Background(), filters)
}

func (c *Client) GetStreamsContext(ctx context.Context, filters StreamFilters) (*StreamsResponse, error) {
	query := url.Values{}
	if filters.Payer != "" {
		query.Set("payer", filters.Payer)
	}
	if filters.Provider != "" {
		query.Set("provider", filters.Provider)
	}
	if filters.Status != "" {
		query.Set("status", filters.Status)
	}
	if filters.ServiceID != "" {
		query.Set("service_id", filters.ServiceID)
	}
	path := "/api/v1/streams"
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var response StreamsResponse
	if err := c.do(ctx, http.MethodGet, path, nil, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (c *Client) GetStream(streamID string) (*StreamResponse, error) {
	return c.GetStreamContext(context.Background(), streamID)
}

func (c *Client) GetStreamContext(ctx context.Context, streamID string) (*StreamResponse, error) {
	var response StreamResponse
	if err := c.do(ctx, http.MethodGet, "/api/v1/streams/"+url.PathEscape(streamID), nil, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (c *Client) SubmitStreamAction(envelope StreamActionEnvelope) (*StreamActionSubmitResponse, error) {
	return c.SubmitStreamActionContext(context.Background(), envelope)
}

func (c *Client) SubmitStreamActionContext(ctx context.Context, envelope StreamActionEnvelope) (*StreamActionSubmitResponse, error) {
	var response StreamActionSubmitResponse
	if err := c.do(ctx, http.MethodPost, "/api/v1/streams/actions/submit-signed", envelope, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

// GetTransaction retrieves a transaction by ID.
func (c *Client) GetTransaction(txID string) (map[string]interface{}, error) {
	return c.GetTransactionContext(context.Background(), txID)
}

// GetTransactionContext is GetTransaction with an explicit context.
//
// The endpoint is GET /api/v1/transactions/{tx_id} (note the plural
// "transactions"; the path uses the brace-syntax form in openapi.yaml
// and the actual mux registration at pkg/api/handlers.go:269-270).
// Earlier SDK builds (≤0.3.0) hit /api/v1/transaction/{id} (singular)
// which returns 404 in production — the typo dated back to the
// pre-rebrand scaffolding window and was not caught because the SDK
// tests start a fake httptest server that accepts any URL. Fixed in
// 0.3.1.
func (c *Client) GetTransactionContext(ctx context.Context, txID string) (map[string]interface{}, error) {
	var resp map[string]interface{}
	if err := c.do(ctx, http.MethodGet, "/api/v1/transactions/"+url.PathEscape(txID), nil, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// HealthStatus is the minimal health payload returned by /api/v1/health/*.
type HealthStatus struct {
	Status    string `json:"status"`
	Timestamp string `json:"timestamp,omitempty"`
	Version   string `json:"version,omitempty"`
}

// GetLiveness fetches the node liveness probe result.
func (c *Client) GetLiveness(ctx context.Context) (*HealthStatus, error) {
	var h HealthStatus
	if err := c.do(ctx, http.MethodGet, "/api/v1/health/live", nil, &h); err != nil {
		return nil, err
	}
	return &h, nil
}

// GetReadiness fetches the node readiness probe result.
func (c *Client) GetReadiness(ctx context.Context) (*HealthStatus, error) {
	var h HealthStatus
	if err := c.do(ctx, http.MethodGet, "/api/v1/health/ready", nil, &h); err != nil {
		return nil, err
	}
	return &h, nil
}

// NodeStatus is the subset of node metadata that SDK users typically care about.
// Additional fields are available under Extra.
//
// As of the Major Update, the endpoint also exposes NodeRole (validator or
// miner), Network pill text, Coin metadata (name/symbol/decimals) and the
// live Tokenomics block-emission snapshot. These fields are populated when
// present but the older minimal fields remain backwards-compatible.
type NodeStatus struct {
	NodeID  string `json:"node_id,omitempty"`
	Version string `json:"version,omitempty"`
	// GitSHA is the short git commit SHA the running binary was
	// built from. Populated when the validator binary was built
	// with `-ldflags -X pkg/buildinfo.GitSHA=<short-sha>` (the
	// canonical release pipeline pattern; see
	// release_evidence.{sh,ps1} and build_release.ps1). Empty
	// string for dev builds where the SHA was not injected.
	// Added in v0.4.4: pairs with the matching field on
	// /api/v1/health and lets a consumer map a running endpoint
	// to a specific commit without scraping log timestamps.
	GitSHA string `json:"git_sha,omitempty"`
	// BuildDate is the UTC RFC 3339 timestamp at which the running
	// binary was built. Same -ldflags-injection mechanism as
	// GitSHA; empty for dev builds. Added in v0.4.4.
	BuildDate  string                 `json:"build_date,omitempty"`
	Uptime     string                 `json:"uptime,omitempty"`
	ChainTip   uint64                 `json:"chain_tip,omitempty"`
	Peers      int                    `json:"peers,omitempty"`
	NodeRole   string                 `json:"node_role,omitempty"`
	Network    string                 `json:"network,omitempty"`
	Coin       *CoinInfo              `json:"coin,omitempty"`
	Branding   *BrandInfo             `json:"branding,omitempty"`
	Tokenomics *TokenomicsInfo        `json:"tokenomics,omitempty"`
	Extra      map[string]interface{} `json:"-"`
}

// CoinInfo mirrors the coin block on /api/v1/status.
type CoinInfo struct {
	Name         string `json:"name"`
	Symbol       string `json:"symbol"`
	Decimals     int    `json:"decimals"`
	SmallestUnit string `json:"smallest_unit"`
}

// BrandInfo mirrors the branding block on /api/v1/status.
type BrandInfo struct {
	Name       string `json:"name"`
	LegacyName string `json:"legacy_name,omitempty"`
	FullTitle  string `json:"full_title,omitempty"`
}

// TokenomicsInfo mirrors the tokenomics block on /api/v1/status. All
// dust-denominated fields are exact integers; CELL-denominated fields are
// display strings and MUST NOT be used for arithmetic.
type TokenomicsInfo struct {
	CapDust                uint64 `json:"cap_dust"`
	CapCell                string `json:"cap_cell"`
	EmittedDust            uint64 `json:"emitted_dust"`
	EmittedCell            string `json:"emitted_cell"`
	RemainingDust          uint64 `json:"remaining_dust"`
	BlockRewardDust        uint64 `json:"block_reward_dust"`
	BlockRewardCell        string `json:"block_reward_cell"`
	CurrentEpoch           uint32 `json:"current_epoch"`
	NextHalvingHeight      uint64 `json:"next_halving_height"`
	NextHalvingETASeconds  uint64 `json:"next_halving_eta_seconds"`
	TargetBlockTimeSeconds uint64 `json:"target_block_time_seconds"`
	BlocksPerEpoch         uint64 `json:"blocks_per_epoch"`
}

// GetNodeStatus fetches node metadata. It uses a two-pass decode: the full
// response is captured into Extra for forward-compatibility while the
// typed fields above are populated when present.
func (c *Client) GetNodeStatus(ctx context.Context) (*NodeStatus, error) {
	var raw map[string]interface{}
	if err := c.do(ctx, http.MethodGet, "/api/v1/status", nil, &raw); err != nil {
		return nil, err
	}
	ns := &NodeStatus{Extra: raw}
	if v, ok := raw["node_id"].(string); ok {
		ns.NodeID = v
	}
	if v, ok := raw["version"].(string); ok {
		ns.Version = v
	}
	if v, ok := raw["uptime"].(string); ok {
		ns.Uptime = v
	}
	if v, ok := raw["chain_tip"].(float64); ok {
		ns.ChainTip = uint64(v)
	}
	if v, ok := raw["peers"].(float64); ok {
		ns.Peers = int(v)
	}
	if v, ok := raw["node_role"].(string); ok {
		ns.NodeRole = v
	}
	if v, ok := raw["network"].(string); ok {
		ns.Network = v
	}

	// Remarshal+unmarshal the nested blocks through the typed structs so
	// callers get ergonomic fields without a second HTTP round-trip.
	if coin, ok := raw["coin"].(map[string]interface{}); ok {
		if b, err := json.Marshal(coin); err == nil {
			var c CoinInfo
			if err := json.Unmarshal(b, &c); err == nil {
				ns.Coin = &c
			}
		}
	}
	if brand, ok := raw["branding"].(map[string]interface{}); ok {
		if b, err := json.Marshal(brand); err == nil {
			var bi BrandInfo
			if err := json.Unmarshal(b, &bi); err == nil {
				ns.Branding = &bi
			}
		}
	}
	if tok, ok := raw["tokenomics"].(map[string]interface{}); ok {
		if b, err := json.Marshal(tok); err == nil {
			var t TokenomicsInfo
			if err := json.Unmarshal(b, &t); err == nil {
				ns.Tokenomics = &t
			}
		}
	}
	return ns, nil
}

// GetPeers returns the current peer list from the node.
//
// DEPRECATED in 0.3.1: this method targets /api/v1/network/peers,
// which is not registered on the public pkg/api server (verified
// against pkg/api/handlers.go's mux). The closest analogues are
// /api/admin/peers (admin-only, mTLS-required, see
// pkg/api/handlers_admin.go:54) and /api/topology on the operator
// dashboard (cookie-or-bearer auth, internal/dashboard/dashboard.go:261).
// Neither is reachable from a JWT-bearer SDK client. Calling this
// method against a production node returns ApiError 404. Pending
// removal in 0.4.0 once a public peer-summary endpoint exists or
// callers migrate to GetNetworkTopology.
func (c *Client) GetPeers(ctx context.Context) ([]map[string]interface{}, error) {
	var resp struct {
		Peers []map[string]interface{} `json:"peers"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/v1/network/peers", nil, &resp); err != nil {
		return nil, err
	}
	return resp.Peers, nil
}

// GetMetricsJSON returns the node's JSON metrics snapshot.
//
// DEPRECATED in 0.3.1: /api/metrics is registered only on the
// operator dashboard server (internal/dashboard/dashboard.go:258,
// requireAuth-gated), not on the public pkg/api server the SDK
// targets. Production calls against a pkg/api node return
// ApiError 404. For Prometheus scrape access from the dashboard,
// callers should hit the dashboard's /api/metrics/prometheus
// endpoint directly with the appropriate dashboard credentials.
// Pending removal in 0.4.0.
func (c *Client) GetMetricsJSON(ctx context.Context) (map[string]interface{}, error) {
	var resp map[string]interface{}
	if err := c.do(ctx, http.MethodGet, "/api/metrics", nil, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// GetMetricsPrometheus returns the raw Prometheus text exposition.
//
// DEPRECATED in 0.3.1: see GetMetricsJSON. Same dashboard-vs-
// public-API mismatch; production calls against a pkg/api node
// return ApiError 404. Pending removal in 0.4.0.
func (c *Client) GetMetricsPrometheus(ctx context.Context) (string, error) {
	req, err := c.newRequest(ctx, http.MethodGet, "/api/metrics/prometheus", nil)
	if err != nil {
		return "", err
	}
	resp, body, err := c.sendRaw(req)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", &ErrAPI{StatusCode: resp.StatusCode, Body: string(body), URL: req.URL.String()}
	}
	return string(body), nil
}

// --- internals ---

func (c *Client) do(ctx context.Context, method, path string, reqBody, out interface{}) error {
	req, err := c.newRequest(ctx, method, path, reqBody)
	if err != nil {
		return err
	}
	resp, body, err := c.sendRaw(req)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &ErrAPI{StatusCode: resp.StatusCode, Body: string(body), URL: req.URL.String()}
	}
	if out == nil || len(body) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("qsdm: decode %s: %w", req.URL.String(), err)
	}
	return nil
}

func (c *Client) newRequest(ctx context.Context, method, path string, reqBody interface{}) (*http.Request, error) {
	var body io.Reader
	if reqBody != nil {
		buf, err := json.Marshal(reqBody)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(buf)
	}
	full := c.BaseURL + path
	req, err := http.NewRequestWithContext(ctx, method, full, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	c.addAuthHeaders(req)
	return req, nil
}

func (c *Client) sendRaw(req *http.Request) (*http.Response, []byte, error) {
	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp, nil, err
	}
	return resp, body, nil
}

func (c *Client) addAuthHeaders(req *http.Request) {
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	} else if c.APIKey != "" {
		req.Header.Set("X-API-Key", c.APIKey)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
