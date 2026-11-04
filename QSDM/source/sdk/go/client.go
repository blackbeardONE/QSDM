// Package qsdm provides an official Go client for the QSDM HTTP API.
//
// It wraps the public `pkg/api` REST surface of a qsdm node (for example
// https://api.qsdm.tech): wallet balance and nonce, signed CELL transfers
// with Proof-of-Entanglement parents, receipts, CELL Streams, health probes
// and node status. Public routes need no credentials.
//
// # Sending CELL
//
// Nodes accept a CELL transfer from a client only as an envelope signed with
// ML-DSA-87 (POST /api/v1/wallet/submit-signed), and since block 816,600 the
// envelope must name Proof-of-Entanglement parents (HTTP 422 otherwise). The
// SDK never holds a private key: SendTransfer prepares the envelope (nonce
// and parents from the node), has a caller-supplied TransferSigner sign
// TransferEnvelope.SigningBytes, and submits it. PrepareTransfer,
// SignTransfer and SubmitSignedTransfer are the same steps one by one, so an
// envelope signed elsewhere (for example by `qsdmcli wallet sign-tx`) can be
// submitted too. The unsigned SendTransaction is deprecated and fails
// without contacting the node.
//
// A few deprecated methods call routes that public nodes do not serve
// (operator-only or not registered); their doc comments say which.
//
// The client is safe for concurrent use and does not embed any node-local state.
// Operator credentials, where a route needs them, are set with SetToken and
// sent as an Authorization header.
//
// # Install
//
//	go get github.com/blackbeardONE/QSDM/QSDM/source/sdk/go
//
// The doubled path segment is not a typo: the repository is
// github.com/blackbeardONE/QSDM and this package lives at QSDM/source/sdk/go
// within it. This package is its own Go module (see go.mod) precisely so
// that path resolves — the parent module declares a path that does not match
// its own location in the repo, so it is not itself go-gettable.
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

// SetAPIKey sets an API key, sent as the X-API-Key header when no token is
// set. QSDM nodes do not read this header (pkg/api authenticates operators
// only with Authorization: Bearer); it is kept for gateways that do.
func (c *Client) SetAPIKey(apiKey string) { c.APIKey = apiKey }

// SetToken sets an operator JWT, sent as Authorization: Bearer. Public
// routes do not need it.
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

// GetBalance returns the CELL balance of an address
// (GET /api/v1/wallet/balance, public).
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

// WalletNonceResponse is the body of GET /api/v1/wallet/nonce.
type WalletNonceResponse struct {
	Sender string `json:"sender"`
	// Nonce is the last nonce already used by the wallet.
	Nonce uint64 `json:"nonce"`
	// Next is the nonce to sign the next transfer with (Nonce + 1).
	Next uint64 `json:"next"`
}

// GetWalletNonce returns the last applied and next required nonce for a
// wallet (GET /api/v1/wallet/nonce?sender=, public). The node counts only
// committed transfers, so a transfer still waiting for its block holds
// Next until that block is sealed.
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

// SendTransaction always returns ErrUnsignedSendRemoved, without contacting
// the node.
//
// Deprecated: it posted an unsigned {from, to, amount} body to
// POST /api/v1/wallet/send, which no public client can use. That route is
// not on the node's public allowlist: it needs an operator JWT plus
// X-Timestamp/X-Nonce/X-Signature headers signed with the node's own
// request-signing key (pkg/api RequestSigningMiddleware), read-only
// followers refuse it with 503, it reads {recipient, amount, fee, geotag,
// parent_cells} (so the SDK's {from, to} body never matched it), and it
// spends from the node's own wallet, never from `from`. Since 2026-10-16 the
// chain accepts only ML-DSA-87 signed transfers, and since block 816,600
// they must carry Proof-of-Entanglement parents. Use SendTransfer with a
// TransferSigner, or PrepareTransfer, SignTransfer and SubmitSignedTransfer.
func (c *Client) SendTransaction(from, to string, amount float64) (string, error) {
	return c.SendTransactionContext(context.Background(), from, to, amount)
}

// SendTransactionContext always returns ErrUnsignedSendRemoved.
//
// Deprecated: see SendTransaction. Use SendTransfer.
func (c *Client) SendTransactionContext(ctx context.Context, from, to string, amount float64) (string, error) {
	return "", ErrUnsignedSendRemoved
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

// SubmitStreamAction posts a wallet-signed CELL Stream action to
// POST /api/v1/streams/actions/submit-signed. The route is public (the
// signature is the authorization), but a node that admits only signed
// wallet transfers answers 503; the public pilot pauses stream actions
// during its recovery.
func (c *Client) SubmitStreamAction(envelope StreamActionEnvelope) (*StreamActionSubmitResponse, error) {
	return c.SubmitStreamActionContext(context.Background(), envelope)
}

// SubmitStreamActionContext is SubmitStreamAction with an explicit context.
func (c *Client) SubmitStreamActionContext(ctx context.Context, envelope StreamActionEnvelope) (*StreamActionSubmitResponse, error) {
	var response StreamActionSubmitResponse
	if err := c.do(ctx, http.MethodPost, "/api/v1/streams/actions/submit-signed", envelope, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

// GetTransaction retrieves a transaction by ID from
// GET /api/v1/transactions/{tx_id}.
//
// Deprecated: the route is not public. It is registered in pkg/api but not
// on the public allowlist, so a public node answers 401 without an operator
// JWT. Use GetReceipt (GET /api/v1/receipts/{tx_id}, public) to follow a
// transaction.
func (c *Client) GetTransaction(txID string) (map[string]interface{}, error) {
	return c.GetTransactionContext(context.Background(), txID)
}

// GetTransactionContext is GetTransaction with an explicit context.
//
// The endpoint is GET /api/v1/transactions/{tx_id} (plural, as registered
// in pkg/api/handlers.go). SDK builds up to 0.3.0 called the singular
// /api/v1/transaction/{id}, which no node serves; fixed in 0.3.1.
//
// Deprecated: operator-only route; use GetReceipt.
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

// GetPeers requests a peer list from /api/v1/network/peers.
//
// Deprecated: no QSDM node registers /api/v1/network/peers, so the call
// always fails: 401 without credentials (the path is not on the public
// allowlist), 404 with an operator JWT. Peer lists exist only on
// administrative routes (/api/admin/peers, mTLS) and the operator
// dashboard. For the number of connected peers use GetNodeStatus (Peers).
func (c *Client) GetPeers(ctx context.Context) ([]map[string]interface{}, error) {
	var resp struct {
		Peers []map[string]interface{} `json:"peers"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/v1/network/peers", nil, &resp); err != nil {
		return nil, err
	}
	return resp.Peers, nil
}

// GetMetricsJSON requests the JSON metrics snapshot at /api/metrics.
//
// Deprecated: /api/metrics exists only on the operator dashboard server
// (internal/dashboard, login required), not on the node API, so against a
// node it always fails (401 without credentials, 404 with an operator JWT).
// There is no public metrics endpoint.
func (c *Client) GetMetricsJSON(ctx context.Context) (map[string]interface{}, error) {
	var resp map[string]interface{}
	if err := c.do(ctx, http.MethodGet, "/api/metrics", nil, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// GetMetricsPrometheus requests the Prometheus text exposition at
// /api/metrics/prometheus.
//
// Deprecated: like GetMetricsJSON, the route exists only on the operator
// dashboard server (a scrape token or dashboard login is required), not on
// the node API. There is no public metrics endpoint.
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
