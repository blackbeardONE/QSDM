package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// validatorNode emulates the validator (mempool) path of POST
// /api/v1/wallet/submit-signed as of QSDM main 846e8c92, including the
// properties the hardening exists for:
//
//   - the nonce gate admits only envelope nonce == last APPLIED + 1, so a
//     second transfer from the same wallet waits for the next block;
//   - ErrDuplicateTx and ErrNonceAlreadyPending both answer 409
//     {"status":"duplicate"}, so "duplicate" does not prove the caller's own
//     envelope is pending;
//   - tx ids are only de-duplicated while in the mempool: after a block, the
//     same id at the next nonce is a new transfer (and replaces the receipt).
type validatorNode struct {
	mu            sync.Mutex
	t             *testing.T
	balances      map[string]int64
	applied       map[string]uint64
	mempool       []txEnvelope
	receipts      map[string]map[string]any
	appliedTxs    []txEnvelope
	submissions   []txEnvelope
	acceptThen500 int // accept into the mempool but answer 500 (lost response)
	drop500       int // answer 500 without admitting
	forceStatus   int // answer this status once without admitting
}

func newValidatorNode(t *testing.T) *validatorNode {
	return &validatorNode{
		t: t, balances: map[string]int64{}, applied: map[string]uint64{},
		receipts: map[string]map[string]any{},
	}
}

func (v *validatorNode) fund(addr string, cell float64) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.balances[addr] += int64(math.Round(cell * dustPerCell))
}

func (v *validatorNode) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/wallet/nonce", func(w http.ResponseWriter, r *http.Request) {
		sender := r.URL.Query().Get("sender")
		v.mu.Lock()
		n := v.applied[sender]
		v.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"sender": sender, "nonce": n, "next": n + 1})
	})
	mux.HandleFunc("/api/v1/wallet/balance", func(w http.ResponseWriter, r *http.Request) {
		addr := r.URL.Query().Get("address")
		v.mu.Lock()
		b := v.balances[addr]
		v.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"address": addr, "balance": float64(b) / dustPerCell})
	})
	mux.HandleFunc("/api/v1/wallet/submit-signed", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var env txEnvelope
		if err := json.Unmarshal(body, &env); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		// Shape checks run before the signature and nonce gates in Core.
		if env.ID == "" {
			http.Error(w, `{"error":"envelope.id is required"}`, http.StatusBadRequest)
			return
		}
		if len(env.Recipient) != 64 || env.Recipient != strings.ToLower(env.Recipient) {
			http.Error(w, `{"error":"recipient must be a lowercase 64-character wallet address"}`, http.StatusBadRequest)
			return
		}
		if err := verifyEnvelope(env); err != nil {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		v.mu.Lock()
		defer v.mu.Unlock()
		v.submissions = append(v.submissions, env)
		if v.forceStatus != 0 {
			code := v.forceStatus
			v.forceStatus = 0
			w.WriteHeader(code)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "forced"})
			return
		}
		if v.drop500 > 0 {
			v.drop500--
			http.Error(w, "upstream reset", http.StatusInternalServerError)
			return
		}
		last := v.applied[env.Sender]
		if env.Nonce <= last {
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": fmt.Sprintf("nonce replay: envelope nonce %d <= last-seen %d", env.Nonce, last)})
			return
		}
		if env.Nonce != last+1 {
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": fmt.Sprintf("nonce gap: envelope nonce %d; next required nonce is %d", env.Nonce, last+1)})
			return
		}
		if v.balances[env.Sender] < chainFloorDust(env.Amount)+chainFloorDust(env.Fee) {
			w.WriteHeader(http.StatusPaymentRequired)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "insufficient canonical CELL balance for amount + fee"})
			return
		}
		for _, p := range v.mempool {
			if p.ID == env.ID || (p.Sender == env.Sender && p.Nonce == env.Nonce) {
				w.WriteHeader(http.StatusConflict)
				_ = json.NewEncoder(w).Encode(map[string]any{"transaction_id": env.ID, "status": "duplicate", "broadcast": "block-pending"})
				return
			}
		}
		v.mempool = append(v.mempool, env)
		if v.acceptThen500 > 0 {
			v.acceptThen500--
			http.Error(w, "gateway timeout after admission", http.StatusGatewayTimeout)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{"transaction_id": env.ID, "status": "pending", "broadcast": "block-pending"})
	})
	mux.HandleFunc("/api/v1/receipts/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/api/v1/receipts/")
		v.mu.Lock()
		rec, ok := v.receipts[id]
		v.mu.Unlock()
		if !ok {
			http.Error(w, "receipt not found", http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(rec)
	})
	return mux
}

// produceBlock applies the mempool in sender-nonce order, like the producer.
func (v *validatorNode) produceBlock() {
	v.mu.Lock()
	defer v.mu.Unlock()
	pending := v.mempool
	v.mempool = nil
	for _, env := range pending {
		status, topic := 1, "TxApplied"
		amount, fee := chainFloorDust(env.Amount), chainFloorDust(env.Fee)
		if env.Nonce != v.applied[env.Sender]+1 || v.balances[env.Sender] < amount+fee {
			status, topic = 0, "TxFailed"
		} else {
			v.balances[env.Sender] -= amount + fee
			v.balances[env.Recipient] += amount
			v.applied[env.Sender] = env.Nonce
			v.appliedTxs = append(v.appliedTxs, env)
		}
		v.receipts[env.ID] = map[string]any{
			"tx_id": env.ID, "status": status,
			"logs": []any{map[string]any{"topic": topic, "data": map[string]any{
				"sender": env.Sender, "recipient": env.Recipient, "amount": env.Amount}}},
		}
	}
}

func (v *validatorNode) counts() (submissions, mempool, applied int) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return len(v.submissions), len(v.mempool), len(v.appliedTxs)
}

// ---- harness ------------------------------------------------------------------

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

type harness struct {
	t        *testing.T
	node     *validatorNode
	nodeSrv  *httptest.Server
	ksPath   string
	passPath string
	journal  string
	clock    *testClock
	caps     [3]float64 // max, recipient, daily
	s        *signer
	srv      *httptest.Server
}

func newHarness(t *testing.T, maxPay, recipientCap, dailyCap float64) *harness {
	t.Helper()
	h := &harness{t: t, node: newValidatorNode(t), clock: &testClock{now: time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)},
		caps: [3]float64{maxPay, recipientCap, dailyCap}}
	h.nodeSrv = httptest.NewServer(h.node.handler())
	t.Cleanup(h.nodeSrv.Close)
	var sender string
	h.ksPath, h.passPath, sender = newTestKeystore(t)
	h.journal = filepath.Join(t.TempDir(), "payouts.jsonl")
	h.node.fund(sender, 1000)
	h.start()
	return h
}

// start (re)builds the signer process state from the keystore and journal.
func (h *harness) start() {
	h.t.Helper()
	if h.srv != nil {
		h.srv.Close()
		_ = h.s.journal.close()
	}
	s, err := loadSigner(config{
		apiURL: h.nodeSrv.URL, ksPath: h.ksPath, passFile: h.passPath, token: "test-token",
		role: "skyfang", maxPay: h.caps[0], recipientCap: h.caps[1], dailyCap: h.caps[2],
		journalPath: h.journal, timeout: 5 * time.Second,
	})
	if err != nil {
		h.t.Fatalf("loadSigner: %v", err)
	}
	s.now = h.clock.Now
	h.s = s
	h.srv = httptest.NewServer(s.routes())
	h.t.Cleanup(func() {
		h.srv.Close()
		_ = s.journal.close()
	})
}

type payReply struct {
	status     int
	retryAfter string
	ok         payResponse
	code       string
	err        string
}

func (h *harness) pay(requestID, recipient string, amount float64) payReply {
	h.t.Helper()
	body := fmt.Sprintf(`{"request_id":%q,"purpose":"skyfang","recipient":%q,"amount":%v}`, requestID, recipient, amount)
	req, _ := http.NewRequest(http.MethodPost, h.srv.URL+"/v1/pay", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatalf("pay: %v", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	out := payReply{status: resp.StatusCode, retryAfter: resp.Header.Get("Retry-After")}
	if resp.StatusCode == http.StatusOK {
		if err := json.Unmarshal(data, &out.ok); err != nil {
			h.t.Fatalf("decode: %v (%s)", err, data)
		}
	} else {
		var e struct {
			Error string `json:"error"`
			Code  string `json:"code"`
		}
		_ = json.Unmarshal(data, &e)
		out.code, out.err = e.Code, e.Error
	}
	return out
}

func addr(n int) string { return fmt.Sprintf("%064x", n) }

func mustStatus(t *testing.T, got payReply, want int, wantCode string) {
	t.Helper()
	if got.status != want || (wantCode != "" && got.code != wantCode) {
		t.Fatalf("want HTTP %d %s, got %d %s (%s)", want, wantCode, got.status, got.code, got.err)
	}
}

// ---- tests ----------------------------------------------------------------------

// The unhardened signer advanced its nonce in memory and treated any 409
// "duplicate" as success, so the second payout of a batch (nonce gate refuses
// it, resync, ErrNonceAlreadyPending -> "duplicate") was reported paid but
// never sent. Now it waits for the block.
func TestPay_SecondPayoutWaitsForNextBlock(t *testing.T) {
	h := newHarness(t, 5, 20, 100)

	first := h.pay("epoch-1:alice", addr(1), 1.5)
	mustStatus(t, first, http.StatusOK, "")
	if first.ok.Duplicate || first.ok.Nonce != 1 || first.ok.Status != stateSubmitted {
		t.Fatalf("first payout: %+v", first.ok)
	}

	// The unhardened signer answered a retry while pending with 502 (its
	// nonce-retry path re-signed with an empty id). Now: idempotent duplicate.
	retry := h.pay("epoch-1:alice", addr(1), 1.5)
	mustStatus(t, retry, http.StatusOK, "")
	if !retry.ok.Duplicate || retry.ok.TransactionID != first.ok.TransactionID || retry.ok.Status != stateSubmitted {
		t.Fatalf("retry while pending: %+v", retry.ok)
	}

	second := h.pay("epoch-1:bob", addr(2), 2)
	mustStatus(t, second, http.StatusServiceUnavailable, "payout_in_flight")
	if second.retryAfter == "" {
		t.Fatal("busy response lacks Retry-After")
	}
	if subs, pool, _ := h.node.counts(); subs != 1 || pool != 1 {
		t.Fatalf("busy payout must not be signed or submitted: submissions=%d mempool=%d", subs, pool)
	}

	h.node.produceBlock()
	second = h.pay("epoch-1:bob", addr(2), 2)
	mustStatus(t, second, http.StatusOK, "")
	if second.ok.Duplicate || second.ok.Nonce != 2 {
		t.Fatalf("second payout: %+v", second.ok)
	}
	h.node.produceBlock()

	if _, _, applied := h.node.counts(); applied != 2 {
		t.Fatalf("want 2 applied transfers, got %d", applied)
	}
	if got := h.node.balances[addr(1)]; got != 150_000_000 {
		t.Fatalf("alice balance %d", got)
	}
	if got := h.node.balances[addr(2)]; got != 200_000_000 {
		t.Fatalf("bob balance %d", got)
	}
}

// Retrying a request after its transfer was included must not sign a second
// transfer at the next nonce (the node would accept it: ids are not unique
// after inclusion). Holds across a signer restart.
func TestPay_RetryAfterInclusionAndRestartPaysOnce(t *testing.T) {
	h := newHarness(t, 5, 20, 100)
	first := h.pay("epoch-7:carol", addr(3), 1)
	mustStatus(t, first, http.StatusOK, "")
	h.node.produceBlock()

	again := h.pay("epoch-7:carol", addr(3), 1)
	mustStatus(t, again, http.StatusOK, "")
	if !again.ok.Duplicate || again.ok.TransactionID != first.ok.TransactionID || again.ok.Status != stateApplied {
		t.Fatalf("retry after inclusion: %+v", again.ok)
	}

	h.start() // restart: state comes back from the journal
	again = h.pay("epoch-7:carol", addr(3), 1)
	mustStatus(t, again, http.StatusOK, "")
	if !again.ok.Duplicate || again.ok.TransactionID != first.ok.TransactionID {
		t.Fatalf("retry after restart: %+v", again.ok)
	}
	h.node.produceBlock()
	if subs, _, applied := h.node.counts(); subs != 1 || applied != 1 {
		t.Fatalf("want exactly one submission and one transfer, got submissions=%d applied=%d", subs, applied)
	}
}

// A lost response (node admitted the envelope, client saw 504) is resolved by
// resubmitting the byte-identical envelope, never by re-signing.
func TestPay_LostResponseResubmitsIdenticalEnvelope(t *testing.T) {
	h := newHarness(t, 5, 20, 100)
	h.node.acceptThen500 = 1
	r := h.pay("epoch-2:dave", addr(4), 2.5)
	mustStatus(t, r, http.StatusBadGateway, "outcome_unknown")

	r = h.pay("epoch-2:dave", addr(4), 2.5)
	mustStatus(t, r, http.StatusOK, "")
	if !r.ok.Duplicate || r.ok.Nonce != 1 {
		t.Fatalf("retry: %+v", r.ok)
	}
	h.node.mu.Lock()
	subs := append([]txEnvelope(nil), h.node.submissions...)
	h.node.mu.Unlock()
	if len(subs) != 2 || subs[0].Signature != subs[1].Signature || subs[0].Nonce != subs[1].Nonce || subs[0].ID != subs[1].ID {
		t.Fatalf("retry must resubmit the identical envelope: %d submissions", len(subs))
	}
	h.node.produceBlock()
	if _, _, applied := h.node.counts(); applied != 1 {
		t.Fatalf("want 1 transfer, got %d", applied)
	}
}

// A response lost BEFORE admission is healed by the same resubmission.
func TestPay_DroppedSubmissionIsReadmitted(t *testing.T) {
	h := newHarness(t, 5, 20, 100)
	h.node.drop500 = 1
	mustStatus(t, h.pay("epoch-2:erin", addr(5), 1), http.StatusBadGateway, "outcome_unknown")
	r := h.pay("epoch-2:erin", addr(5), 1)
	mustStatus(t, r, http.StatusOK, "")
	if !r.ok.Duplicate || r.ok.Status != stateSubmitted {
		t.Fatalf("retry: %+v", r.ok)
	}
	h.node.produceBlock()
	if _, _, applied := h.node.counts(); applied != 1 {
		t.Fatalf("want 1 transfer, got %d", applied)
	}
}

func TestPay_RequestIDReuseWithDifferentParamsIsRefused(t *testing.T) {
	h := newHarness(t, 5, 20, 100)
	mustStatus(t, h.pay("epoch-3:frank", addr(6), 1), http.StatusOK, "")
	mustStatus(t, h.pay("epoch-3:frank", addr(6), 2), http.StatusConflict, "request_conflict")
	mustStatus(t, h.pay("epoch-3:frank", addr(7), 1), http.StatusConflict, "request_conflict")
}

func TestPay_DailyAndRecipientCapsUseRollingWindow(t *testing.T) {
	h := newHarness(t, 5, 8, 12)
	step := func(id string, to int, amount float64, want int, code string) {
		t.Helper()
		mustStatus(t, h.pay(id, addr(to), amount), want, code)
		if want == http.StatusOK {
			h.node.produceBlock()
		}
	}
	step("r1", 1, 5, http.StatusOK, "")
	step("r2", 1, 3, http.StatusOK, "")
	step("r3", 1, 1, http.StatusTooManyRequests, "recipient_daily_cap_exceeded")
	step("r4", 2, 4, http.StatusOK, "")
	step("r5", 3, 1, http.StatusTooManyRequests, "daily_cap_exceeded")

	// Caps are rebuilt from the journal after a restart.
	h.start()
	step("r5", 3, 1, http.StatusTooManyRequests, "daily_cap_exceeded")

	h.clock.Advance(24*time.Hour + time.Second)
	step("r5", 3, 1, http.StatusOK, "")
	step("r6", 1, 5, http.StatusOK, "")
}

// A transfer the node definitively refused can never apply, so it releases
// its cap reservation and the same request may be signed again.
func TestPay_RejectedAttemptReleasesCapAndCanBeRetried(t *testing.T) {
	h := newHarness(t, 5, 5, 5)
	h.node.forceStatus = http.StatusPaymentRequired
	mustStatus(t, h.pay("epoch-4:gina", addr(8), 5), http.StatusPaymentRequired, "insufficient_balance")
	if total, _ := h.s.journal.windowSpend(addr(8), h.clock.Now()); total != 0 {
		t.Fatalf("rejected attempt counted toward caps: %d", total)
	}
	r := h.pay("epoch-4:gina", addr(8), 5)
	mustStatus(t, r, http.StatusOK, "")
	if r.ok.Duplicate {
		t.Fatalf("re-signed attempt reported duplicate: %+v", r.ok)
	}
	h.node.produceBlock()
	if _, _, applied := h.node.counts(); applied != 1 {
		t.Fatalf("want 1 transfer, got %d", applied)
	}
	mustStatus(t, h.pay("epoch-4:hank", addr(9), 1), http.StatusTooManyRequests, "daily_cap_exceeded")
}

func TestPay_RequestValidation(t *testing.T) {
	h := newHarness(t, 5, 20, 100)
	send := func(body string) payReply {
		req, _ := http.NewRequest(http.MethodPost, h.srv.URL+"/v1/pay", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer test-token")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var e struct {
			Code string `json:"code"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&e)
		return payReply{status: resp.StatusCode, code: e.Code}
	}
	to := addr(1)
	mustStatus(t, send(fmt.Sprintf(`{"purpose":"skyfang","recipient":%q,"amount":1}`, to)), http.StatusBadRequest, "invalid_request")
	mustStatus(t, send(fmt.Sprintf(`{"request_id":"a b","purpose":"skyfang","recipient":%q,"amount":1}`, to)), http.StatusBadRequest, "invalid_request")
	mustStatus(t, send(fmt.Sprintf(`{"request_id":"x","purpose":"referral","recipient":%q,"amount":1}`, to)), http.StatusForbidden, "purpose_mismatch")
	mustStatus(t, send(fmt.Sprintf(`{"request_id":"x","recipient":%q,"amount":1}`, to)), http.StatusForbidden, "purpose_mismatch")
	mustStatus(t, send(fmt.Sprintf(`{"request_id":"x","purpose":"skyfang","recipient":%q,"amount":5.00000001}`, to)), http.StatusForbidden, "max_payout_exceeded")
	mustStatus(t, send(fmt.Sprintf(`{"request_id":"x","purpose":"skyfang","recipient":%q,"amount":0.123456789}`, to)), http.StatusBadRequest, "invalid_request")
	mustStatus(t, send(fmt.Sprintf(`{"request_id":"x","purpose":"skyfang","recipient":%q,"amount":1}`, h.s.sender)), http.StatusBadRequest, "invalid_request")
	if subs, _, _ := h.node.counts(); subs != 0 {
		t.Fatalf("invalid requests reached the node: %d", subs)
	}
}

func TestHealthz_AdvertisesHardening(t *testing.T) {
	h := newHarness(t, 5, 8, 12)
	mustStatus(t, h.pay("e:1", addr(1), 2), http.StatusOK, "")
	resp, err := http.Get(h.srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	for k, want := range map[string]any{
		"payout_idempotency": "request-id-v1", "journal": "durable-v1", "role": "skyfang",
		"max_payout": 5.0, "recipient_daily_cap": 8.0, "daily_cap": 12.0,
		"window_spent": 2.0, "window_remaining": 10.0, "live_payouts": 1.0,
	} {
		if body[k] != want {
			t.Fatalf("healthz %s = %v, want %v (%v)", k, body[k], want, body)
		}
	}
}

func TestConfig_HardeningIsRequiredForEveryRole(t *testing.T) {
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "token")
	if err := os.WriteFile(tokenFile, []byte("t0ken\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	base := map[string]string{
		"QSDM_SIGNER_KEYSTORE":            filepath.Join(dir, "ks.json"),
		"QSDM_SIGNER_PASSPHRASE_FILE":     filepath.Join(dir, "pass"),
		"QSDM_SIGNER_TOKEN_FILE":          tokenFile,
		"QSDM_SIGNER_ROLE":                "skyfang",
		"QSDM_SIGNER_MAX_PAYOUT":          "1",
		"QSDM_SIGNER_RECIPIENT_DAILY_CAP": "2",
		"QSDM_SIGNER_DAILY_CAP":           "50",
		"QSDM_SIGNER_JOURNAL":             filepath.Join(dir, "journal.jsonl"),
		"QSDM_SIGNER_LISTEN":              "127.0.0.1:8899",
		"QSDM_SIGNER_API_URL":             "https://api.qsdm.tech",
	}
	apply := func(over map[string]string) error {
		for _, k := range []string{"QSDM_SIGNER_TOKEN", "QSDM_SIGNER_MIN_RESERVE", "QSDM_SIGNER_FEE", "QSDM_SIGNER_HTTP_TIMEOUT"} {
			t.Setenv(k, "")
		}
		for k, v := range base {
			t.Setenv(k, v)
		}
		for k, v := range over {
			t.Setenv(k, v)
		}
		_, err := configFromEnv()
		return err
	}
	if err := apply(nil); err != nil {
		t.Fatalf("complete config rejected: %v", err)
	}
	err := apply(map[string]string{"QSDM_SIGNER_ROLE": "", "QSDM_SIGNER_MAX_PAYOUT": "", "QSDM_SIGNER_DAILY_CAP": "",
		"QSDM_SIGNER_RECIPIENT_DAILY_CAP": "", "QSDM_SIGNER_JOURNAL": ""})
	for _, key := range []string{"QSDM_SIGNER_ROLE", "QSDM_SIGNER_MAX_PAYOUT", "QSDM_SIGNER_DAILY_CAP", "QSDM_SIGNER_RECIPIENT_DAILY_CAP", "QSDM_SIGNER_JOURNAL"} {
		if err == nil || !strings.Contains(err.Error(), key) {
			t.Fatalf("missing %s not reported: %v", key, err)
		}
	}
	for name, over := range map[string]map[string]string{
		"game role on all interfaces":   {"QSDM_SIGNER_LISTEN": "0.0.0.0:8899"},
		"game role on a LAN address":    {"QSDM_SIGNER_LISTEN": "192.0.2.10:8899"},
		"plain http to a remote node":   {"QSDM_SIGNER_API_URL": "http://192.0.2.10:8080"},
		"per-payout above recipient":    {"QSDM_SIGNER_MAX_PAYOUT": "3"},
		"recipient cap above daily":     {"QSDM_SIGNER_RECIPIENT_DAILY_CAP": "60"},
		"bad role":                      {"QSDM_SIGNER_ROLE": "Sky Fang"},
		"negative fee":                  {"QSDM_SIGNER_FEE": "-1"},
		"daily cap with 9 decimals":     {"QSDM_SIGNER_DAILY_CAP": "50.000000001"},
		"zero max payout":               {"QSDM_SIGNER_MAX_PAYOUT": "0"},
		"referral role is no exception": {"QSDM_SIGNER_ROLE": "referral", "QSDM_SIGNER_LISTEN": ":8899"},
	} {
		if err := apply(over); err == nil {
			t.Fatalf("%s: config accepted", name)
		}
	}
}

func TestJournal_TornTailIsTruncatedAndCorruptionIsFatal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "j.jsonl")
	j, err := openJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	a := &payoutAttempt{RequestID: "r1", Purpose: "skyfang", Recipient: addr(1), AmountDust: 5, WireAmount: 5e-8,
		TxID: "tx1", Nonce: 1, Timestamp: "t", Signature: "ab", SignedAt: now}
	if err := j.recordSigned(a, now); err != nil {
		t.Fatal(err)
	}
	if err := j.recordSigned(&payoutAttempt{RequestID: "r1", TxID: "tx2", Signature: "cd"}, now); err == nil {
		t.Fatal("second envelope for a live request was journaled")
	}
	_ = j.close()
	good, _ := os.ReadFile(path)
	if err := os.WriteFile(path, append(append([]byte{}, good...), []byte(`{"v":1,"event":"subm`)...), 0o600); err != nil {
		t.Fatal(err)
	}
	j, err = openJournal(path)
	if err != nil {
		t.Fatalf("torn tail: %v", err)
	}
	if got := j.byRequest["r1"]; got == nil || got.State != stateSigned {
		t.Fatalf("replayed state: %+v", got)
	}
	_ = j.close()
	if after, _ := os.ReadFile(path); string(after) != string(good) {
		t.Fatalf("torn tail not truncated:\n%q", after)
	}

	if err := os.WriteFile(path, append([]byte("{not json}\n"), good...), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := openJournal(path); err == nil {
		t.Fatal("corrupt journal line accepted")
	}
}

func TestAmounts_DustExactOnChain(t *testing.T) {
	for _, d := range []int64{1, 10_000_000, 29_000_000, 70_000_000, 123_456_789, 100_000_000_001, 2_900_000_000_000} {
		w, err := dustToWire(d)
		if err != nil {
			t.Fatalf("%d: %v", d, err)
		}
		if got := chainFloorDust(w); got != d {
			t.Fatalf("dust %d -> wire %v -> chain credits %d", d, w, got)
		}
		back, err := cellToDust(w)
		if err != nil || back != d {
			t.Fatalf("dust %d -> wire %v -> parsed %d (%v)", d, w, back, err)
		}
	}
	for _, bad := range []float64{0, -1, math.NaN(), math.Inf(1), 0.123456789, 1e-9} {
		if _, err := cellToDust(bad); err == nil {
			t.Fatalf("%v accepted", bad)
		}
	}
}
