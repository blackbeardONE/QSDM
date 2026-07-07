// Command qsdm-game-signer is a small operator-side signing sidecar for game
// integrations (e.g. Sky Fang) whose server runtime cannot produce ML-DSA-87
// signatures natively (a JVM, etc.).
//
// It holds ONE narrowly funded payout keystore, exposes a tiny token-gated HTTP
// API on loopback, and turns a payout request into a fully signed, submitted
// self-custody CELL transfer against a QSDM node. It can be run as a game,
// referral, or onboarding-treasury signer; all keys stay in this process.
//
// It is the robust alternative to shelling out to `qsdmcli wallet sign-tx`
// per payout: it reuses pkg/keystore + circl mldsa87 (so the canonical envelope
// bytes match the server byte-for-byte) and manages the operator wallet's nonce
// in-memory so a batch of payouts in one epoch doesn't collide on nonces before
// earlier transfers are applied.
//
// Configuration (environment):
//
//	QSDM_SIGNER_LISTEN          listen address           (default 127.0.0.1:8899)
//	QSDM_SIGNER_API_URL         QSDM node base URL        (default http://localhost:8080)
//	QSDM_SIGNER_KEYSTORE        path to the operator keystore JSON (required)
//	QSDM_SIGNER_PASSPHRASE_FILE file with the keystore passphrase   (required)
//	QSDM_SIGNER_TOKEN_FILE      file containing the bearer token (preferred)
//	QSDM_SIGNER_TOKEN           bearer token fallback for compatibility
//	QSDM_SIGNER_FEE             fee (CELL) to stamp on each transfer (default 0)
//	QSDM_SIGNER_HTTP_TIMEOUT    per-request timeout to the node       (default 10s)
//	QSDM_SIGNER_ROLE            required payout purpose, e.g. referral or faucet
//	QSDM_SIGNER_MAX_PAYOUT      maximum CELL in one payout (required for treasury roles)
//	QSDM_SIGNER_MIN_RESERVE     balance that the signer will never spend (default 0)
//
// Endpoints (all JSON):
//
//	GET  /healthz                         -> {status, address}
//	GET  /v1/balance?address=...          -> {address, balance} (proxied)
//	POST /v1/pay   {recipient, amount}    -> {transaction_id, nonce, sender}  (Bearer required)
//	POST /v1/resync                       -> {sender, nonce}                  (Bearer required)
//
// Security: bind to loopback (or a private network), keep QSDM_SIGNER_TOKEN
// secret, and protect the keystore + passphrase file like any hot wallet.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/keystore"
	"github.com/cloudflare/circl/sign/mldsa/mldsa87"
)

// txEnvelope mirrors pkg/wallet.TransactionData and cmd/qsdmcli's txEnvelope
// EXACTLY (field order is the wire/signing contract: json.Marshal emits in
// struct-declaration order and the server canonicalises by parse -> clear
// signature+public_key -> re-marshal). Do not reorder.
type txEnvelope struct {
	ID          string   `json:"id"`
	Sender      string   `json:"sender"`
	Recipient   string   `json:"recipient"`
	Amount      float64  `json:"amount"`
	Fee         float64  `json:"fee"`
	GeoTag      string   `json:"geotag"`
	ParentCells []string `json:"parent_cells"`
	Nonce       uint64   `json:"nonce,omitempty"`
	Signature   string   `json:"signature"`
	PublicKey   string   `json:"public_key,omitempty"`
	Timestamp   string   `json:"timestamp"`
}

type nonceResponse struct {
	Sender string `json:"sender"`
	Nonce  uint64 `json:"nonce"`
	Next   uint64 `json:"next"`
}

type signer struct {
	apiURL  string
	token   string
	fee     float64
	role    string
	maxPay  float64
	reserve float64
	http    *http.Client
	sender  string
	pubHex  string
	sk      *mldsa87.PrivateKey

	mu    sync.Mutex
	nonce uint64 // next nonce to use
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "qsdm-game-signer:", err)
		os.Exit(1)
	}
}

// config holds the resolved sidecar settings (from env in run()).
type config struct {
	listen   string
	apiURL   string
	ksPath   string
	passFile string
	token    string
	fee      float64
	role     string
	maxPay   float64
	reserve  float64
	timeout  time.Duration
}

func configFromEnv() (config, error) {
	c := config{
		listen:   env("QSDM_SIGNER_LISTEN", "127.0.0.1:8899"),
		apiURL:   strings.TrimRight(env("QSDM_SIGNER_API_URL", "http://localhost:8080"), "/"),
		ksPath:   os.Getenv("QSDM_SIGNER_KEYSTORE"),
		passFile: os.Getenv("QSDM_SIGNER_PASSPHRASE_FILE"),
		token:    os.Getenv("QSDM_SIGNER_TOKEN"),
		fee:      0.0,
		role:     strings.ToLower(strings.TrimSpace(os.Getenv("QSDM_SIGNER_ROLE"))),
		maxPay:   0.0,
		reserve:  0.0,
		timeout:  10 * time.Second,
	}
	if tokenFile := strings.TrimSpace(os.Getenv("QSDM_SIGNER_TOKEN_FILE")); tokenFile != "" {
		token, err := os.ReadFile(tokenFile)
		if err != nil {
			return c, fmt.Errorf("QSDM_SIGNER_TOKEN_FILE: %w", err)
		}
		c.token = string(trimTrailingNewline(token))
		zero(token)
	}
	if c.ksPath == "" || c.passFile == "" || strings.TrimSpace(c.token) == "" {
		return c, errors.New("QSDM_SIGNER_KEYSTORE, QSDM_SIGNER_PASSPHRASE_FILE and QSDM_SIGNER_TOKEN_FILE (or QSDM_SIGNER_TOKEN) are required")
	}
	if v := strings.TrimSpace(os.Getenv("QSDM_SIGNER_FEE")); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return c, fmt.Errorf("QSDM_SIGNER_FEE: %w", err)
		}
		c.fee = f
	}
	if v := strings.TrimSpace(os.Getenv("QSDM_SIGNER_HTTP_TIMEOUT")); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return c, fmt.Errorf("QSDM_SIGNER_HTTP_TIMEOUT: %w", err)
		}
		c.timeout = d
	}
	if v := strings.TrimSpace(os.Getenv("QSDM_SIGNER_MAX_PAYOUT")); v != "" {
		amount, err := strconv.ParseFloat(v, 64)
		if err != nil || amount <= 0 {
			return c, fmt.Errorf("QSDM_SIGNER_MAX_PAYOUT must be positive")
		}
		c.maxPay = amount
	}
	if v := strings.TrimSpace(os.Getenv("QSDM_SIGNER_MIN_RESERVE")); v != "" {
		amount, err := strconv.ParseFloat(v, 64)
		if err != nil || amount < 0 {
			return c, fmt.Errorf("QSDM_SIGNER_MIN_RESERVE cannot be negative")
		}
		c.reserve = amount
	}
	if (c.role == "referral" || c.role == "faucet") && c.maxPay <= 0 {
		return c, errors.New("QSDM_SIGNER_MAX_PAYOUT is required for referral and faucet treasury roles")
	}
	if c.role == "referral" || c.role == "faucet" {
		if err := validateLoopbackListen(c.listen); err != nil {
			return c, err
		}
		if err := validateSecureNodeURL(c.apiURL); err != nil {
			return c, err
		}
	}
	return c, nil
}

// loadSigner loads + decrypts the operator keystore and builds a *signer ready
// to serve (callers should resyncNonce before first use). Factored out of run()
// so tests can construct a signer against a fake node without binding a socket.
func loadSigner(c config) (*signer, error) {
	ksData, err := os.ReadFile(c.ksPath)
	if err != nil {
		return nil, fmt.Errorf("read keystore: %w", err)
	}
	ks, err := keystore.Unmarshal(ksData)
	if err != nil {
		return nil, fmt.Errorf("parse keystore: %w", err)
	}
	if err := keystore.Validate(ks); err != nil {
		return nil, fmt.Errorf("validate keystore: %w", err)
	}
	passphrase, err := os.ReadFile(c.passFile)
	if err != nil {
		return nil, fmt.Errorf("read passphrase file: %w", err)
	}
	passphrase = trimTrailingNewline(passphrase)
	priv, err := keystore.Decrypt(ks, passphrase)
	if err != nil {
		return nil, fmt.Errorf("decrypt keystore: %w", err)
	}
	zero(passphrase)

	var sk mldsa87.PrivateKey
	if err := sk.UnmarshalBinary(priv); err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}
	zero(priv)

	pubBytes, err := hex.DecodeString(ks.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("keystore public_key not hex: %w", err)
	}
	sum := sha256.Sum256(pubBytes)

	return &signer{
		apiURL:  c.apiURL,
		token:   c.token,
		fee:     c.fee,
		role:    c.role,
		maxPay:  c.maxPay,
		reserve: c.reserve,
		http:    &http.Client{Timeout: c.timeout},
		sender:  hex.EncodeToString(sum[:]),
		pubHex:  ks.PublicKey,
		sk:      &sk,
	}, nil
}

func run() error {
	c, err := configFromEnv()
	if err != nil {
		return err
	}
	s, err := loadSigner(c)
	if err != nil {
		return err
	}
	if err := s.resyncNonce(); err != nil {
		return fmt.Errorf("initial nonce sync (is the node at %s on v0.4.1+?): %w", c.apiURL, err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/v1/balance", s.handleBalance)
	mux.HandleFunc("/v1/pay", s.requireToken(s.handlePay))
	mux.HandleFunc("/v1/resync", s.requireToken(s.handleResync))
	mux.HandleFunc("/v1/verify", s.requireToken(s.handleVerify))

	fmt.Fprintf(os.Stderr, "qsdm-game-signer: sender=%s role=%s node=%s listen=%s nonce=%d max_payout=%.8f reserve=%.8f\n",
		s.sender, c.role, c.apiURL, c.listen, s.nonce, c.maxPay, c.reserve)
	srv := &http.Server{Addr: c.listen, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	return srv.ListenAndServe()
}

// ---- handlers ---------------------------------------------------------------

func (s *signer) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok", "address": s.sender, "role": s.role,
		"max_payout": s.maxPay, "min_reserve": s.reserve,
	})
}

func (s *signer) handleBalance(w http.ResponseWriter, r *http.Request) {
	addr := r.URL.Query().Get("address")
	if addr == "" {
		addr = s.sender
	}
	body, code, err := s.nodeGET("/api/v1/wallet/balance?address=" + addr)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = w.Write(body)
}

type payRequest struct {
	RequestID string  `json:"request_id,omitempty"`
	Purpose   string  `json:"purpose,omitempty"`
	Recipient string  `json:"recipient"`
	Amount    float64 `json:"amount"`
}

type payResponse struct {
	TransactionID string  `json:"transaction_id"`
	Nonce         uint64  `json:"nonce"`
	Sender        string  `json:"sender"`
	Recipient     string  `json:"recipient"`
	Amount        float64 `json:"amount"`
	Duplicate     bool    `json:"duplicate,omitempty"`
}

func (s *signer) handlePay(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "POST only"})
		return
	}
	var req payRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid body: " + err.Error()})
		return
	}
	req.Recipient = strings.TrimSpace(strings.ToLower(req.Recipient))
	req.RequestID = strings.TrimSpace(req.RequestID)
	req.Purpose = strings.ToLower(strings.TrimSpace(req.Purpose))
	if err := validateWalletAddress(req.Recipient); err != nil || req.Amount <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "recipient and positive amount required"})
		return
	}
	if s.role != "" && req.Purpose != s.role {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "payout purpose is not allowed by this signer"})
		return
	}
	if (s.role == "referral" || s.role == "faucet") && req.RequestID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "request_id is required for treasury payouts"})
		return
	}
	if len(req.RequestID) > 128 || len(req.Purpose) > 32 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "request_id or purpose is too long"})
		return
	}
	if s.maxPay > 0 && req.Amount > s.maxPay {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "payout exceeds signer maximum"})
		return
	}

	// One payout at a time so the in-memory nonce stays consistent across a batch.
	s.mu.Lock()
	defer s.mu.Unlock()

	balance, err := s.balance()
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	if balance-req.Amount-s.fee < s.reserve {
		writeJSON(w, http.StatusPaymentRequired, map[string]any{"error": "treasury reserve policy blocks this payout"})
		return
	}

	txID := deriveID(s.sender, req.Recipient, req.Amount, s.nonce)
	if req.RequestID != "" {
		txID = deriveRequestID(s.sender, req.Purpose, req.RequestID)
	}
	txID, used, duplicate, err := s.signAndSubmit(txID, req.Purpose, req.Recipient, req.Amount, s.nonce)
	if err != nil {
		// If the node rejected on a nonce problem, refetch and retry once.
		if isNonceError(err) {
			if rerr := s.resyncNonceLocked(); rerr == nil {
				txID, used, duplicate, err = s.signAndSubmit(txID, req.Purpose, req.Recipient, req.Amount, s.nonce)
			}
		}
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
			return
		}
	}
	if duplicate {
		_ = s.resyncNonceLocked()
	} else {
		s.nonce = used + 1 // advance only on a newly accepted transfer
	}

	writeJSON(w, http.StatusOK, payResponse{
		TransactionID: txID,
		Nonce:         used,
		Sender:        s.sender,
		Recipient:     req.Recipient,
		Amount:        req.Amount,
		Duplicate:     duplicate,
	})
}

func (s *signer) handleResync(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.resyncNonceLocked(); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sender": s.sender, "nonce": s.nonce})
}

type verifyRequest struct {
	Message   string `json:"message"`    // the exact message bytes the wallet signed (UTF-8)
	Signature string `json:"signature"`  // hex ML-DSA-87 signature
	PublicKey string `json:"public_key"` // hex ML-DSA-87 public key
}

type verifyResponse struct {
	Valid   bool   `json:"valid"`
	Address string `json:"address"` // hex(sha256(public_key)) — the QSDM address that signed
}

// handleVerify checks an ML-DSA-87 signature over an arbitrary message and
// returns whether it is valid plus the address derived from the public key.
// This is the primitive a game server uses to confirm wallet ownership during
// "link wallet" (challenge nonce signed in the player's QSDM wallet) WITHOUT
// implementing ML-DSA-87 itself. No keystore is touched — pure verification.
func (s *signer) handleVerify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "POST only"})
		return
	}
	var req verifyRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid body: " + err.Error()})
		return
	}
	pubBytes, err := hex.DecodeString(strings.TrimSpace(req.PublicKey))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "public_key not hex"})
		return
	}
	sigBytes, err := hex.DecodeString(strings.TrimSpace(req.Signature))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "signature not hex"})
		return
	}
	var pk mldsa87.PublicKey
	if err := pk.UnmarshalBinary(pubBytes); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "public_key parse: " + err.Error()})
		return
	}
	valid := mldsa87.Verify(&pk, []byte(req.Message), nil, sigBytes)
	sum := sha256.Sum256(pubBytes)
	writeJSON(w, http.StatusOK, verifyResponse{Valid: valid, Address: hex.EncodeToString(sum[:])})
}

// ---- core signing -----------------------------------------------------------

// signAndSubmit builds, signs, and submits a transfer at the given nonce, and
// returns the transaction id. The envelope id is derived from the signed inputs
// so a retry at the same nonce produces the same id (idempotent on the node).
func (s *signer) signAndSubmit(txID, purpose, recipient string, amount float64, nonce uint64) (string, uint64, bool, error) {
	env := txEnvelope{
		ID:          txID,
		Sender:      s.sender,
		Recipient:   recipient,
		Amount:      amount,
		Fee:         s.fee,
		GeoTag:      purpose,
		ParentCells: []string{},
		Nonce:       nonce,
		Timestamp:   time.Now().UTC().Format(time.RFC3339),
	}

	// Canonical bytes: clear signature + public_key, marshal in field order.
	env.Signature = ""
	env.PublicKey = ""
	canonical, err := json.Marshal(env)
	if err != nil {
		return "", nonce, false, fmt.Errorf("marshal canonical: %w", err)
	}
	sig := make([]byte, mldsa87.SignatureSize)
	if err := mldsa87.SignTo(s.sk, canonical, nil, true /*randomized*/, sig); err != nil {
		return "", nonce, false, fmt.Errorf("sign: %w", err)
	}
	env.Signature = hex.EncodeToString(sig)
	env.PublicKey = s.pubHex

	final, err := json.Marshal(env)
	if err != nil {
		return "", nonce, false, fmt.Errorf("marshal final: %w", err)
	}

	body, code, err := s.nodePOST("/api/v1/wallet/submit-signed", final)
	if err != nil {
		return "", nonce, false, err
	}
	if code == http.StatusConflict && strings.Contains(strings.ToLower(string(body)), "duplicate") {
		return env.ID, nonce, true, nil
	}
	if code < 200 || code >= 300 {
		return "", nonce, false, fmt.Errorf("submit-signed HTTP %d: %s", code, string(body))
	}
	txID = jsonString(body, "transaction_id")
	if txID == "" {
		txID = env.ID
	}
	return txID, nonce, false, nil
}

func (s *signer) balance() (float64, error) {
	body, code, err := s.nodeGET("/api/v1/wallet/balance?address=" + s.sender)
	if err != nil {
		return 0, err
	}
	if code != http.StatusOK {
		return 0, fmt.Errorf("balance HTTP %d: %s", code, string(body))
	}
	var response struct {
		Balance float64 `json:"balance"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return 0, fmt.Errorf("decode balance: %w", err)
	}
	return response.Balance, nil
}

func (s *signer) resyncNonce() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.resyncNonceLocked()
}

func (s *signer) resyncNonceLocked() error {
	body, code, err := s.nodeGET("/api/v1/wallet/nonce?sender=" + s.sender)
	if err != nil {
		return err
	}
	if code != http.StatusOK {
		return fmt.Errorf("nonce HTTP %d: %s", code, string(body))
	}
	var nr nonceResponse
	if err := json.Unmarshal(body, &nr); err != nil {
		return fmt.Errorf("decode nonce: %w", err)
	}
	if nr.Sender != s.sender {
		return fmt.Errorf("node echoed wrong sender: want %q got %q", s.sender, nr.Sender)
	}
	s.nonce = nr.Next
	return nil
}

// ---- node HTTP --------------------------------------------------------------

func (s *signer) nodeGET(path string) ([]byte, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), s.http.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.apiURL+path, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Accept", "application/json")
	return s.do(req)
}

func (s *signer) nodePOST(path string, payload []byte) ([]byte, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), s.http.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.apiURL+path, bytes.NewReader(payload))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	return s.do(req)
}

func (s *signer) do(req *http.Request) ([]byte, int, error) {
	resp, err := s.http.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return body, resp.StatusCode, nil
}

// ---- middleware + helpers ---------------------------------------------------

func (s *signer) requireToken(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		auth := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(auth)), []byte(s.token)) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		next(w, r)
	}
}

func deriveID(sender, recipient string, amount float64, nonce uint64) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%.8f|%d", sender, recipient, amount, nonce)))
	return hex.EncodeToString(h[:8]) // 16 hex chars
}

func deriveRequestID(sender, purpose, requestID string) string {
	h := sha256.Sum256([]byte("qsdm-treasury-payout:v1|" + sender + "|" + purpose + "|" + requestID))
	return hex.EncodeToString(h[:])
}

func validateWalletAddress(address string) error {
	if len(address) != sha256.Size*2 {
		return fmt.Errorf("address must be 64 hexadecimal characters")
	}
	_, err := hex.DecodeString(address)
	return err
}

func validateLoopbackListen(listen string) error {
	host, _, err := net.SplitHostPort(strings.TrimSpace(listen))
	if err != nil {
		return fmt.Errorf("QSDM_SIGNER_LISTEN must be a loopback host:port for treasury roles: %w", err)
	}
	host = strings.ToLower(strings.TrimSpace(host))
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return errors.New("QSDM_SIGNER_LISTEN must use localhost or a loopback IP for treasury roles")
	}
	return nil
}

func validateSecureNodeURL(raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return errors.New("QSDM_SIGNER_API_URL must be an absolute HTTP(S) URL")
	}
	if parsed.Scheme == "https" {
		return nil
	}
	if parsed.Scheme != "http" {
		return errors.New("QSDM_SIGNER_API_URL must use HTTP or HTTPS")
	}
	host := strings.ToLower(parsed.Hostname())
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return errors.New("plain HTTP QSDM_SIGNER_API_URL must use a loopback host for treasury roles")
	}
	return nil
}

func isNonceError(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "nonce")
}

func jsonString(body []byte, key string) string {
	needle := "\"" + key + "\""
	i := bytes.Index(body, []byte(needle))
	if i < 0 {
		return ""
	}
	c := bytes.IndexByte(body[i+len(needle):], ':')
	if c < 0 {
		return ""
	}
	rest := body[i+len(needle)+c+1:]
	q1 := bytes.IndexByte(rest, '"')
	if q1 < 0 {
		return ""
	}
	rest = rest[q1+1:]
	q2 := bytes.IndexByte(rest, '"')
	if q2 < 0 {
		return ""
	}
	return string(rest[:q2])
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func trimTrailingNewline(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}

func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
