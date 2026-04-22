// Package qsdmplus provides an official Go client for the QSDM+ HTTP API.
//
// It wraps the `pkg/api` REST surface exposed by a running qsdmplus node:
// wallet balance, transaction send/query, health probes, node metadata, peer listing,
// and Prometheus/JSON metrics snapshots.
//
// The client is safe for concurrent use and does not embed any node-local state;
// authentication is supplied through SetAPIKey or SetToken and forwarded as headers.
package qsdmplus

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

// Client is the QSDM+ HTTP API client.
type Client struct {
	BaseURL    string
	HTTPClient *http.Client
	APIKey     string
	Token      string
}

// NewClient creates a new QSDM+ API client with a 30s default timeout.
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
	return fmt.Sprintf("qsdmplus: %s returned %d: %s", e.URL, e.StatusCode, truncate(e.Body, 256))
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

// GetTransaction retrieves a transaction by ID.
func (c *Client) GetTransaction(txID string) (map[string]interface{}, error) {
	return c.GetTransactionContext(context.Background(), txID)
}

// GetTransactionContext is GetTransaction with an explicit context.
func (c *Client) GetTransactionContext(ctx context.Context, txID string) (map[string]interface{}, error) {
	var resp map[string]interface{}
	if err := c.do(ctx, http.MethodGet, "/api/v1/transaction/"+url.PathEscape(txID), nil, &resp); err != nil {
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
	NodeID     string                 `json:"node_id,omitempty"`
	Version    string                 `json:"version,omitempty"`
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

// GetPeers returns the current peer list from the node. The shape is opaque to the SDK
// (it mirrors whatever the server returns).
func (c *Client) GetPeers(ctx context.Context) ([]map[string]interface{}, error) {
	var resp struct {
		Peers []map[string]interface{} `json:"peers"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/v1/network/peers", nil, &resp); err != nil {
		return nil, err
	}
	return resp.Peers, nil
}

// GetMetricsJSON returns the node's JSON metrics snapshot (from /api/metrics).
func (c *Client) GetMetricsJSON(ctx context.Context) (map[string]interface{}, error) {
	var resp map[string]interface{}
	if err := c.do(ctx, http.MethodGet, "/api/metrics", nil, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// GetMetricsPrometheus returns the raw Prometheus text exposition.
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
		return fmt.Errorf("qsdmplus: decode %s: %w", req.URL.String(), err)
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
