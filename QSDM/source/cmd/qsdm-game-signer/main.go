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
// bytes match the server byte-for-byte).
//
// Every role is hardened the same way (there is no unguarded role):
//
//   - a per-payout maximum, a rolling 24h total cap and a rolling 24h
//     per-recipient cap are mandatory, and the caps survive restarts;
//   - the listener must be loopback, and the node URL must be HTTPS or
//     loopback HTTP;
//   - every payout needs a request_id and a purpose equal to the role;
//   - a durable journal (see journal.go) makes request_id idempotent across
//     block inclusion and restarts, and records every decision for audit;
//   - one transfer is in flight at a time: a validator only admits envelope
//     nonce last_applied+1, so the next payout waits for the next block
//     (HTTP 503 payout_in_flight with Retry-After).
//
// The reward wallet must be spent by this signer only. The signer infers that
// an envelope was applied when the wallet's nonce moves past it.
//
// Configuration (environment):
//
//	QSDM_SIGNER_LISTEN               loopback listen address    (default 127.0.0.1:8899)
//	QSDM_SIGNER_API_URL              QSDM node base URL, https or loopback http (default http://localhost:8080)
//	QSDM_SIGNER_KEYSTORE             path to the operator keystore JSON (required)
//	QSDM_SIGNER_PASSPHRASE_FILE      file with the keystore passphrase   (required)
//	QSDM_SIGNER_TOKEN_FILE           file containing the bearer token (preferred)
//	QSDM_SIGNER_TOKEN                bearer token fallback for compatibility
//	QSDM_SIGNER_ROLE                 required payout purpose, e.g. skyfang, referral, faucet (required)
//	QSDM_SIGNER_MAX_PAYOUT           maximum CELL in one payout (required)
//	QSDM_SIGNER_DAILY_CAP            maximum CELL (amount+fee) paid in any rolling 24h (required)
//	QSDM_SIGNER_RECIPIENT_DAILY_CAP  maximum CELL paid to one recipient in any rolling 24h (required)
//	QSDM_SIGNER_JOURNAL              path of the append-only payout journal (required)
//	QSDM_SIGNER_MIN_RESERVE          balance that the signer will never spend (default 0)
//	QSDM_SIGNER_FEE                  fee (CELL) to stamp on each transfer (default 0)
//	QSDM_SIGNER_HTTP_TIMEOUT         per-request timeout to the node       (default 10s)
//
// Endpoints (all JSON):
//
//	GET  /healthz                         -> {status, address, role, caps, window spend, ...}
//	GET  /v1/balance?address=...          -> {address, balance} (proxied)
//	POST /v1/pay  {request_id, purpose, recipient, amount}
//	                                      -> {transaction_id, nonce, sender, duplicate, status} (Bearer required)
//	POST /v1/resync                       -> {sender, nonce}                  (Bearer required)
//	POST /v1/verify {message, signature, public_key} -> {valid, address}     (Bearer required)
//
// Security: keep the token secret, protect the keystore + passphrase file like
// any hot wallet, keep only a small balance in it, and keep the journal on
// persistent storage owned by the signer's OS user.
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
	"regexp"
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
	apiURL string
	token  string
	role   string
	http   *http.Client
	sender string
	pubHex string
	sk     *mldsa87.PrivateKey

	// Policy, in dust (1 CELL = 1e8 dust).
	maxPayDust       int64
	dailyCapDust     int64
	recipientCapDust int64
	reserveDust      int64
	feeDust          int64
	wireFee          float64

	journal *payoutJournal
	now     func() time.Time

	mu    sync.Mutex // serializes payouts, resync and journal access
	nonce uint64     // informational: next nonce seen at the last resync/payout
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "qsdm-game-signer:", err)
		os.Exit(1)
	}
}

// config holds the resolved sidecar settings (from env in run()).
type config struct {
	listen       string
	apiURL       string
	ksPath       string
	passFile     string
	token        string
	fee          float64
	role         string
	maxPay       float64
	dailyCap     float64
	recipientCap float64
	reserve      float64
	journalPath  string
	timeout      time.Duration
}

var purposePattern = regexp.MustCompile(`^[a-z0-9_-]{2,32}$`)

func configFromEnv() (config, error) {
	c := config{
		listen:      env("QSDM_SIGNER_LISTEN", "127.0.0.1:8899"),
		apiURL:      strings.TrimRight(env("QSDM_SIGNER_API_URL", "http://localhost:8080"), "/"),
		ksPath:      os.Getenv("QSDM_SIGNER_KEYSTORE"),
		passFile:    os.Getenv("QSDM_SIGNER_PASSPHRASE_FILE"),
		token:       os.Getenv("QSDM_SIGNER_TOKEN"),
		role:        strings.ToLower(strings.TrimSpace(os.Getenv("QSDM_SIGNER_ROLE"))),
		journalPath: strings.TrimSpace(os.Getenv("QSDM_SIGNER_JOURNAL")),
		timeout:     10 * time.Second,
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
		if err != nil || f < 0 {
			return c, errors.New("QSDM_SIGNER_FEE must be a non-negative number")
		}
		c.fee = f
	}
	if v := strings.TrimSpace(os.Getenv("QSDM_SIGNER_HTTP_TIMEOUT")); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return c, errors.New("QSDM_SIGNER_HTTP_TIMEOUT must be a positive duration")
		}
		c.timeout = d
	}
	for _, p := range []struct {
		key string
		dst *float64
	}{
		{"QSDM_SIGNER_MAX_PAYOUT", &c.maxPay},
		{"QSDM_SIGNER_DAILY_CAP", &c.dailyCap},
		{"QSDM_SIGNER_RECIPIENT_DAILY_CAP", &c.recipientCap},
	} {
		v := strings.TrimSpace(os.Getenv(p.key))
		if v == "" {
			continue
		}
		amount, err := strconv.ParseFloat(v, 64)
		if err != nil || amount <= 0 {
			return c, fmt.Errorf("%s must be positive", p.key)
		}
		*p.dst = amount
	}
	if v := strings.TrimSpace(os.Getenv("QSDM_SIGNER_MIN_RESERVE")); v != "" {
		amount, err := strconv.ParseFloat(v, 64)
		if err != nil || amount < 0 {
			return c, errors.New("QSDM_SIGNER_MIN_RESERVE cannot be negative")
		}
		c.reserve = amount
	}
	if err := c.validatePolicy(); err != nil {
		return c, err
	}
	return c, nil
}

// validatePolicy enforces the hardening rules that apply to EVERY role.
func (c config) validatePolicy() error {
	var missing []string
	if c.role == "" {
		missing = append(missing, "QSDM_SIGNER_ROLE")
	}
	if c.maxPay <= 0 {
		missing = append(missing, "QSDM_SIGNER_MAX_PAYOUT")
	}
	if c.dailyCap <= 0 {
		missing = append(missing, "QSDM_SIGNER_DAILY_CAP")
	}
	if c.recipientCap <= 0 {
		missing = append(missing, "QSDM_SIGNER_RECIPIENT_DAILY_CAP")
	}
	if c.journalPath == "" {
		missing = append(missing, "QSDM_SIGNER_JOURNAL")
	}
	if len(missing) > 0 {
		return fmt.Errorf("required for every signer role: %s", strings.Join(missing, ", "))
	}
	if !purposePattern.MatchString(c.role) {
		return errors.New("QSDM_SIGNER_ROLE must use 2-32 lowercase letters, digits, '_' or '-'")
	}
	maxPay, err := cellToDust(c.maxPay)
	if err != nil {
		return fmt.Errorf("QSDM_SIGNER_MAX_PAYOUT: %w", err)
	}
	daily, err := cellToDust(c.dailyCap)
	if err != nil {
		return fmt.Errorf("QSDM_SIGNER_DAILY_CAP: %w", err)
	}
	perRecipient, err := cellToDust(c.recipientCap)
	if err != nil {
		return fmt.Errorf("QSDM_SIGNER_RECIPIENT_DAILY_CAP: %w", err)
	}
	if maxPay > perRecipient || perRecipient > daily {
		return errors.New("caps must satisfy QSDM_SIGNER_MAX_PAYOUT <= QSDM_SIGNER_RECIPIENT_DAILY_CAP <= QSDM_SIGNER_DAILY_CAP")
	}
	if err := validateLoopbackListen(c.listen); err != nil {
		return err
	}
	return validateSecureNodeURL(c.apiURL)
}

// loadSigner loads + decrypts the operator keystore, opens the payout journal
// and builds a *signer ready to serve. Factored out of run() so tests can
// construct a signer against a fake node without binding a socket.
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

	s := &signer{
		apiURL: c.apiURL,
		token:  c.token,
		role:   c.role,
		http:   &http.Client{Timeout: c.timeout},
		sender: hex.EncodeToString(sum[:]),
		pubHex: ks.PublicKey,
		sk:     &sk,
		now:    time.Now,
	}
	for _, f := range []struct {
		name string
		v    float64
		dst  *int64
	}{
		{"max payout", c.maxPay, &s.maxPayDust},
		{"daily cap", c.dailyCap, &s.dailyCapDust},
		{"recipient daily cap", c.recipientCap, &s.recipientCapDust},
		{"min reserve", c.reserve, &s.reserveDust},
		{"fee", c.fee, &s.feeDust},
	} {
		if f.v == 0 {
			continue
		}
		d, err := cellToDust(f.v)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f.name, err)
		}
		*f.dst = d
	}
	if s.wireFee, err = dustToWire(s.feeDust); err != nil {
		return nil, fmt.Errorf("fee: %w", err)
	}
	if s.journal, err = openJournal(c.journalPath); err != nil {
		return nil, err
	}
	return s, nil
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
	defer s.journal.close()
	if err := s.resyncNonce(); err != nil {
		return fmt.Errorf("initial nonce sync (is the node at %s on v0.4.1+?): %w", c.apiURL, err)
	}

	fmt.Fprintf(os.Stderr, "qsdm-game-signer: sender=%s role=%s node=%s listen=%s nonce=%d max_payout=%.8f daily_cap=%.8f recipient_daily_cap=%.8f reserve=%.8f journal=%s live_attempts=%d\n",
		s.sender, c.role, c.apiURL, c.listen, s.nonce, c.maxPay, c.dailyCap, c.recipientCap, c.reserve,
		c.journalPath, len(s.journal.liveAttempts()))
	srv := &http.Server{Addr: c.listen, Handler: s.routes(), ReadHeaderTimeout: 5 * time.Second}
	return srv.ListenAndServe()
}

func (s *signer) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/v1/balance", s.handleBalance)
	mux.HandleFunc("/v1/pay", s.requireToken(s.handlePay))
	mux.HandleFunc("/v1/resync", s.requireToken(s.handleResync))
	mux.HandleFunc("/v1/verify", s.requireToken(s.handleVerify))
	return mux
}

// ---- handlers ---------------------------------------------------------------

func (s *signer) handleHealth(w http.ResponseWriter, r *http.Request) {
	body := map[string]any{
		"status": "ok", "address": s.sender, "role": s.role,
		"max_payout":          float64(s.maxPayDust) / dustPerCell,
		"min_reserve":         float64(s.reserveDust) / dustPerCell,
		"daily_cap":           float64(s.dailyCapDust) / dustPerCell,
		"recipient_daily_cap": float64(s.recipientCapDust) / dustPerCell,
		"cap_window_seconds":  int(capWindow / time.Second),
		"payout_idempotency":  "request-id-v1",
		"journal":             "durable-v1",
	}
	// A payout may hold the lock across node round-trips; health must not wait.
	if s.mu.TryLock() {
		total, _ := s.journal.windowSpend("", s.now())
		live := s.journal.liveAttempts()
		s.mu.Unlock()
		body["window_spent"] = float64(total) / dustPerCell
		body["window_remaining"] = float64(max(s.dailyCapDust-total, 0)) / dustPerCell
		body["live_payouts"] = len(live)
	} else {
		body["payout_in_progress"] = true
	}
	writeJSON(w, http.StatusOK, body)
}

func (s *signer) handleBalance(w http.ResponseWriter, r *http.Request) {
	addr := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("address")))
	if addr == "" {
		addr = s.sender
	}
	if err := validateWalletAddress(addr); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid wallet address"})
		return
	}
	body, code, err := s.nodeGET("/api/v1/wallet/balance?address=" + url.QueryEscape(addr))
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
	Status        string  `json:"status,omitempty"`
}

func validRequestID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, r := range id {
		if r < 0x21 || r > 0x7e {
			return false
		}
	}
	return true
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
	amountDust, amountErr := cellToDust(req.Amount)
	if err := validateWalletAddress(req.Recipient); err != nil || amountErr != nil || req.Recipient == s.sender {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "recipient and positive amount (max 8 decimals) required", "code": "invalid_request"})
		return
	}
	if !validRequestID(req.RequestID) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "request_id is required (1-128 printable ASCII characters)", "code": "invalid_request"})
		return
	}
	if req.Purpose != s.role {
		s.mu.Lock()
		s.journal.refused(req.RequestID, truncate(req.Purpose, 32), req.Recipient, amountDust, "purpose_mismatch", s.now())
		s.mu.Unlock()
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "payout purpose is not allowed by this signer", "code": "purpose_mismatch"})
		return
	}
	if amountDust > s.maxPayDust {
		s.mu.Lock()
		s.journal.refused(req.RequestID, req.Purpose, req.Recipient, amountDust, "max_payout_exceeded", s.now())
		s.mu.Unlock()
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "payout exceeds signer maximum", "code": "max_payout_exceeded"})
		return
	}

	s.mu.Lock()
	resp, perr := s.payLocked(req, amountDust)
	s.mu.Unlock()
	if perr != nil {
		if perr.retryAfter > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(perr.retryAfter))
		}
		writeJSON(w, perr.status, map[string]any{"error": perr.msg, "code": perr.code})
		return
	}
	writeJSON(w, http.StatusOK, resp)
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

// ---- node reads ---------------------------------------------------------------

func (s *signer) balance() (float64, error) {
	body, code, err := s.nodeGET("/api/v1/wallet/balance?address=" + s.sender)
	if err != nil {
		return 0, err
	}
	if code != http.StatusOK {
		return 0, fmt.Errorf("balance HTTP %d: %s", code, truncate(string(body), 200))
	}
	var response struct {
		Balance float64 `json:"balance"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return 0, fmt.Errorf("decode balance: %w", err)
	}
	return response.Balance, nil
}

func (s *signer) fetchNonce() (nonceResponse, error) {
	body, code, err := s.nodeGET("/api/v1/wallet/nonce?sender=" + s.sender)
	if err != nil {
		return nonceResponse{}, err
	}
	if code != http.StatusOK {
		return nonceResponse{}, fmt.Errorf("nonce HTTP %d: %s", code, truncate(string(body), 200))
	}
	var nr nonceResponse
	if err := json.Unmarshal(body, &nr); err != nil {
		return nonceResponse{}, fmt.Errorf("decode nonce: %w", err)
	}
	if nr.Sender != s.sender {
		return nonceResponse{}, fmt.Errorf("node echoed wrong sender: want %q got %q", s.sender, nr.Sender)
	}
	return nr, nil
}

// lastAppliedNonce is the last envelope nonce the chain applied for the wallet.
func (s *signer) lastAppliedNonce() (uint64, error) {
	nr, err := s.fetchNonce()
	if err != nil {
		return 0, err
	}
	return nr.Nonce, nil
}

// nextNonce is the only envelope nonce a validator will admit right now.
func (s *signer) nextNonce() (uint64, error) {
	nr, err := s.fetchNonce()
	if err != nil {
		return 0, err
	}
	s.nonce = nr.Next
	return nr.Next, nil
}

func (s *signer) resyncNonce() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.resyncNonceLocked()
}

func (s *signer) resyncNonceLocked() error {
	_, err := s.nextNonce()
	return err
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
		return fmt.Errorf("QSDM_SIGNER_LISTEN must be a loopback host:port: %w", err)
	}
	host = strings.ToLower(strings.TrimSpace(host))
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return errors.New("QSDM_SIGNER_LISTEN must use localhost or a loopback IP")
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
		return errors.New("plain HTTP QSDM_SIGNER_API_URL must use a loopback host")
	}
	return nil
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
