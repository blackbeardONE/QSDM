package pulseclient

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

// DefaultPoolURL is the project's hosted PULSE pool.
const DefaultPoolURL = "https://qsdm.tech/pool/api"

// PoolAPIError is an answer from a pool or node that was not 2xx.
type PoolAPIError struct {
	Status int
	Body   string
}

func (e *PoolAPIError) Error() string {
	return fmt.Sprintf("status %d: %s", e.Status, e.Body)
}

// Unreachable reports whether err means the server could not be reached or
// answered with a server error (the miner then falls back to solo).
func Unreachable(err error) bool {
	if err == nil {
		return false
	}
	var ae *PoolAPIError
	if errors.As(err, &ae) {
		return ae.Status >= 500
	}
	return !errors.Is(err, context.Canceled)
}

func getJSON(ctx context.Context, hc *http.Client, u, token string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return do(hc, req, out)
}

func postJSON(ctx context.Context, hc *http.Client, u, token string, in, out any) error {
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return do(hc, req, out)
}

func do(hc *http.Client, req *http.Request, out any) error {
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		msg := strings.TrimSpace(string(b))
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return &PoolAPIError{Status: resp.StatusCode, Body: msg}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("decode %s: %w", req.URL.Path, err)
	}
	return nil
}

// Pool talks to one PULSE pool (miner protocol v1).
type Pool struct {
	Base  string // e.g. https://qsdm.tech/pool/api (the /v1 is added)
	HTTP  *http.Client
	Token string // the session from Register
}

func (p *Pool) url(path string) string {
	return strings.TrimRight(p.Base, "/") + "/v1" + path
}

// Info is the pool's public description (GET /info).
type Info struct {
	Name               string `json:"name"`
	PoolID             string `json:"pool_id"`
	PublicKey          string `json:"public_key"`
	Mode               string `json:"mode"` // preview | active
	Note               string `json:"note"`
	FeeBPS             uint16 `json:"fee_bps"`
	FeeTo              string `json:"fee_to"`
	NetworkFeeCapBPS   uint16 `json:"fee_network_cap_bps"`
	FeeNoticeDays      int    `json:"fee_notice_days"`
	WindowFactor       uint64 `json:"window_factor"`
	PayoutFloorDust    uint64 `json:"payout_floor_dust"`
	ShareDifficulty    uint64 `json:"share_difficulty"`
	MinShareDifficulty uint64 `json:"min_share_difficulty"`
	PreviewShares      bool   `json:"preview_accepts_shares"`
	Chain              struct {
		Tip              uint64 `json:"tip"`
		ActivationHeight uint64 `json:"activation_height"`
		Active           bool   `json:"active"`
	} `json:"chain"`
}

// Info reads GET /info.
func (p *Pool) Info(ctx context.Context) (*Info, error) {
	var out Info
	if err := getJSON(ctx, p.HTTP, p.url("/info"), "", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Register opens a session for login with the wallet's public key and the
// signature from SignRegister.
func (p *Pool) Register(ctx context.Context, login, password, publicKeyHex, signatureHex string) error {
	var out struct {
		Session string `json:"session"`
	}
	in := map[string]string{"login": login, "password": password, "public_key": publicKeyHex, "signature": signatureHex}
	if err := postJSON(ctx, p.HTTP, p.url("/register"), "", in, &out); err != nil {
		return err
	}
	if out.Session == "" {
		return errors.New("pulse: the pool returned no session")
	}
	p.Token = out.Session
	return nil
}

// Job is the pool's work: the producer's work, unchanged, plus the share
// difficulty and the job ID.
type Job struct {
	JobID             string          `json:"job_id"`
	ShareDifficulty   string          `json:"share_difficulty"`   // decimal
	NetworkDifficulty string          `json:"network_difficulty"` // decimal
	WindowTarget      uint64          `json:"window_target"`
	Work              json.RawMessage `json:"work"`
}

// Work reads GET /work.
func (p *Pool) Work(ctx context.Context) (*Job, error) {
	var out Job
	if err := getJSON(ctx, p.HTTP, p.url("/work"), p.Token, &out); err != nil {
		return nil, err
	}
	if out.JobID == "" || len(out.Work) == 0 {
		return nil, errors.New("pulse: the pool sent no job")
	}
	return &out, nil
}

// ShareResult is the pool's answer to a share.
type ShareResult struct {
	Accepted        bool     `json:"accepted"`
	Reason          string   `json:"reason"`
	Difficulty      uint64   `json:"difficulty"`
	NetworkSolution bool     `json:"network_solution"`
	Preview         bool     `json:"preview"`
	Receipt         *Receipt `json:"receipt,omitempty"`
	ReceiptSig      string   `json:"receipt_sig,omitempty"`
}

// Share sends one share (POST /shares). nonceHex is the 32-byte nonce in hex.
func (p *Pool) Share(ctx context.Context, jobID, nonceHex string, batchCount uint32) (*ShareResult, error) {
	var out ShareResult
	in := map[string]any{"job_id": jobID, "nonce": nonceHex, "batch_count": batchCount}
	if err := postJSON(ctx, p.HTTP, p.url("/shares"), p.Token, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ReportProof tells the pool about a network proof the producer accepted.
func (p *Pool) ReportProof(ctx context.Context, proofID string) error {
	return postJSON(ctx, p.HTTP, p.url("/proofs"), p.Token, map[string]string{"proof_id": proofID}, nil)
}

// AddressStats is the pool's no-account view of one payout address.
type AddressStats map[string]any

// Address reads GET /address/{addr}.
func (p *Pool) Address(ctx context.Context, addr string) (AddressStats, error) {
	if !IsAddress(addr) {
		return nil, errors.New("pulse: not an address")
	}
	var out AddressStats
	if err := getJSON(ctx, p.HTTP, p.url("/address/"+addr), "", &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Node talks to a QSDM node's PULSE routes (base e.g. https://api.qsdm.tech/api/v1).
type Node struct {
	Base string
	HTTP *http.Client
}

func (n *Node) url(path string) string { return strings.TrimRight(n.Base, "/") + path }

// Submit sends a signed member transaction (POST /pulse/submit-signed).
func (n *Node) Submit(ctx context.Context, env *Envelope) (string, error) {
	var out struct {
		TxID string `json:"tx_id"`
	}
	if err := postJSON(ctx, n.HTTP, n.url("/pulse/submit-signed"), "", env, &out); err != nil {
		return "", err
	}
	return out.TxID, nil
}

// Member is GET /pulse/member: whether owner is in a pool, and which.
type Member struct {
	Owner  string `json:"owner"`
	Member bool   `json:"member"`
	Pool   string `json:"pool"`
}

// Member reads an owner's pool membership.
func (n *Node) Member(ctx context.Context, owner string) (*Member, error) {
	var out Member
	if err := getJSON(ctx, n.HTTP, n.url("/pulse/member?owner="+url.QueryEscape(owner)), "", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Escrowed is one unsettled escrowed reward.
type Escrowed struct {
	ProofID string `json:"proof_id"`
	Owner   string `json:"owner"`
	Dust    uint64 `json:"dust"`
	Height  uint64 `json:"height"`
}

// State is GET /pulse/state?pool=<id>.
type State struct {
	ActivationHeight   uint64 `json:"activation_height"`
	MaxFeeBPS          uint16 `json:"max_fee_bps"`
	PayeeEpochCapDust  uint64 `json:"payee_epoch_cap_dust"`
	ReclaimAfterBlocks uint64 `json:"reclaim_after_blocks"`
	Active             bool   `json:"active"`
	Tip                uint64 `json:"tip"`
	Pool               *struct {
		ID           string            `json:"id"`
		Escrowed     []Escrowed        `json:"escrowed"`
		EscrowedDust uint64            `json:"escrowed_dust"`
		Owed         map[string]uint64 `json:"owed"`
	} `json:"pool"`
}

// State reads the rule's parameters and one pool's chain state.
func (n *Node) State(ctx context.Context, pool string) (*State, error) {
	u := n.url("/pulse/state")
	if pool != "" {
		u += "?pool=" + url.QueryEscape(pool)
	}
	var out State
	if err := getJSON(ctx, n.HTTP, u, "", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Reclaimable lists owner's escrowed proofs that a transaction in the next
// block may reclaim (the chain refuses one before reclaim_after_blocks have
// passed since it was escrowed).
func (s *State) Reclaimable(owner string) []Escrowed {
	if s == nil || s.Pool == nil || s.ReclaimAfterBlocks == 0 {
		return nil
	}
	next := s.Tip + 1
	var out []Escrowed
	for _, e := range s.Pool.Escrowed {
		if e.Owner == owner && next >= e.Height && next-e.Height >= s.ReclaimAfterBlocks {
			out = append(out, e)
		}
	}
	return out
}
