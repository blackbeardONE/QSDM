package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/keystore"
	"github.com/cloudflare/circl/sign/mldsa/mldsa87"
)

// fakeNode emulates the subset of the QSDM API the signer uses, and — crucially
// — verifies submitted envelopes exactly the way pkg/api does: sender must equal
// hex(sha256(public_key)) and the ML-DSA-87 signature must verify over the
// canonical bytes (envelope with signature+public_key cleared). If the signer
// ever drifts from the server's canonicalization, this test fails.
type fakeNode struct {
	mu        sync.Mutex
	lastNonce uint64
	submitted []txEnvelope
	seen      map[string]bool
	t         *testing.T
}

func (f *fakeNode) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/wallet/nonce", func(w http.ResponseWriter, r *http.Request) {
		sender := r.URL.Query().Get("sender")
		f.mu.Lock()
		n := f.lastNonce
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"sender": sender, "nonce": n, "next": n + 1})
	})
	mux.HandleFunc("/api/v1/wallet/balance", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"address": r.URL.Query().Get("address"), "balance": 1000.0,
		})
	})
	mux.HandleFunc("/api/v1/wallet/submit-signed", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var env txEnvelope
		if err := json.Unmarshal(body, &env); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		if err := verifyEnvelope(env); err != nil {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		f.mu.Lock()
		if f.seen == nil {
			f.seen = map[string]bool{}
		}
		if f.seen[env.ID] {
			f.mu.Unlock()
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "duplicate", "transaction_id": env.ID})
			return
		}
		f.seen[env.ID] = true
		f.submitted = append(f.submitted, env)
		if env.Nonce > f.lastNonce {
			f.lastNonce = env.Nonce
		}
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"transaction_id": env.ID, "status": "accepted", "broadcast": "local-only",
		})
	})
	return mux
}

func TestTreasuryEndpointValidation(t *testing.T) {
	for _, listen := range []string{"127.0.0.1:8897", "localhost:8897", "[::1]:8897"} {
		if err := validateLoopbackListen(listen); err != nil {
			t.Fatalf("expected %q to be accepted: %v", listen, err)
		}
	}
	for _, listen := range []string{"0.0.0.0:8897", ":8897", "192.0.2.10:8897"} {
		if err := validateLoopbackListen(listen); err == nil {
			t.Fatalf("expected %q to be rejected", listen)
		}
	}

	for _, endpoint := range []string{"http://127.0.0.1:8080", "http://localhost:8080", "https://api.qsdm.tech"} {
		if err := validateSecureNodeURL(endpoint); err != nil {
			t.Fatalf("expected %q to be accepted: %v", endpoint, err)
		}
	}
	for _, endpoint := range []string{"http://192.0.2.10:8080", "ftp://127.0.0.1:8080", "api.qsdm.tech"} {
		if err := validateSecureNodeURL(endpoint); err == nil {
			t.Fatalf("expected %q to be rejected", endpoint)
		}
	}
}

// verifyEnvelope mirrors pkg/api/handlers.go SubmitSignedTransaction verification.
func verifyEnvelope(env txEnvelope) error {
	pubBytes, err := hex.DecodeString(env.PublicKey)
	if err != nil {
		return fmt.Errorf("public_key not hex")
	}
	sum := sha256.Sum256(pubBytes)
	if hex.EncodeToString(sum[:]) != env.Sender {
		return fmt.Errorf("sender != hex(sha256(public_key))")
	}
	sig, err := hex.DecodeString(env.Signature)
	if err != nil {
		return fmt.Errorf("signature not hex")
	}
	unsigned := env
	unsigned.Signature = ""
	unsigned.PublicKey = ""
	canonical, err := json.Marshal(unsigned)
	if err != nil {
		return fmt.Errorf("canonicalize: %w", err)
	}
	var pk mldsa87.PublicKey
	if err := pk.UnmarshalBinary(pubBytes); err != nil {
		return fmt.Errorf("parse public key: %w", err)
	}
	if !mldsa87.Verify(&pk, canonical, nil, sig) {
		return fmt.Errorf("signature does not verify")
	}
	return nil
}

// newTestKeystore writes an encrypted keystore + passphrase file to a temp dir
// and returns their paths plus the derived sender address.
func newTestKeystore(t *testing.T) (ksPath, passPath, sender string) {
	t.Helper()
	pk, sk, err := mldsa87.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	pub, err := pk.MarshalBinary()
	if err != nil {
		t.Fatalf("pub marshal: %v", err)
	}
	priv, err := sk.MarshalBinary()
	if err != nil {
		t.Fatalf("priv marshal: %v", err)
	}
	passphrase := []byte("correct horse battery staple")
	ks, err := keystore.Encrypt(pub, priv, passphrase)
	if err != nil {
		t.Fatalf("keystore encrypt: %v", err)
	}
	data, err := keystore.Marshal(ks)
	if err != nil {
		t.Fatalf("keystore marshal: %v", err)
	}
	dir := t.TempDir()
	ksPath = filepath.Join(dir, "wallet.json")
	passPath = filepath.Join(dir, "pass.txt")
	if err := os.WriteFile(ksPath, data, 0o600); err != nil {
		t.Fatalf("write keystore: %v", err)
	}
	if err := os.WriteFile(passPath, passphrase, 0o600); err != nil {
		t.Fatalf("write passphrase: %v", err)
	}
	sum := sha256.Sum256(pub)
	return ksPath, passPath, hex.EncodeToString(sum[:])
}

func newTestSigner(t *testing.T, apiURL string) *signer {
	t.Helper()
	ksPath, passPath, _ := newTestKeystore(t)
	s, err := loadSigner(config{
		apiURL:   apiURL,
		ksPath:   ksPath,
		passFile: passPath,
		token:    "test-token",
		timeout:  5_000_000_000, // 5s
	})
	if err != nil {
		t.Fatalf("loadSigner: %v", err)
	}
	return s
}

func TestSignAndSubmit_ProducesVerifiableEnvelope(t *testing.T) {
	fn := &fakeNode{t: t}
	node := httptest.NewServer(fn.handler())
	defer node.Close()

	s := newTestSigner(t, node.URL)
	if err := s.resyncNonce(); err != nil {
		t.Fatalf("resyncNonce: %v", err)
	}
	if s.nonce != 1 {
		t.Fatalf("want initial nonce 1, got %d", s.nonce)
	}

	recipient := strings.Repeat("f", 64)
	txID, used, duplicate, err := s.signAndSubmit("test-sign-submit-01", "test", recipient, 1.5, s.nonce)
	if err != nil {
		t.Fatalf("signAndSubmit: %v", err)
	}
	if txID == "" {
		t.Fatal("empty txID")
	}
	if used != 1 {
		t.Fatalf("want used nonce 1, got %d", used)
	}
	if duplicate {
		t.Fatal("newly submitted transfer reported as duplicate")
	}
	if len(fn.submitted) != 1 {
		t.Fatalf("want 1 submitted envelope, got %d", len(fn.submitted))
	}
	got := fn.submitted[0]
	if got.Sender != s.sender {
		t.Fatalf("sender mismatch: env=%s signer=%s", got.Sender, s.sender)
	}
	if got.Recipient != recipient || got.Amount != 1.5 || got.Nonce != 1 {
		t.Fatalf("envelope fields wrong: %+v", got)
	}
}

func TestHandlePay_AuthAndNonceAdvance(t *testing.T) {
	fn := &fakeNode{t: t}
	node := httptest.NewServer(fn.handler())
	defer node.Close()

	s := newTestSigner(t, node.URL)
	if err := s.resyncNonce(); err != nil {
		t.Fatalf("resyncNonce: %v", err)
	}
	srv := httptest.NewServer(payMux(s))
	defer srv.Close()

	// Missing token -> 401.
	resp, err := http.Post(srv.URL+"/v1/pay", "application/json",
		strings.NewReader(`{"recipient":"00 aa","amount":1}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401 without token, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Two authorized payouts -> nonces 1 then 2, both verify at the node.
	for i, want := range []uint64{1, 2} {
		recipient := fmt.Sprintf("%064x", i+1)
		body := fmt.Sprintf(`{"recipient":%q,"amount":%d}`, recipient, i+1)
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/pay", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer test-token")
		req.Header.Set("Content-Type", "application/json")
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("pay %d: %v", i, err)
		}
		if r.StatusCode != http.StatusOK {
			b, _ := io.ReadAll(r.Body)
			r.Body.Close()
			t.Fatalf("pay %d: want 200 got %d: %s", i, r.StatusCode, string(b))
		}
		var pr payResponse
		_ = json.NewDecoder(r.Body).Decode(&pr)
		r.Body.Close()
		if pr.Nonce != want {
			t.Fatalf("pay %d: want nonce %d got %d", i, want, pr.Nonce)
		}
	}
	if len(fn.submitted) != 2 {
		t.Fatalf("want 2 submitted, got %d", len(fn.submitted))
	}
}

func TestHandlePay_RequestIDIsIdempotent(t *testing.T) {
	fn := &fakeNode{t: t}
	node := httptest.NewServer(fn.handler())
	defer node.Close()

	s := newTestSigner(t, node.URL)
	s.role = "referral"
	s.maxPay = 5
	if err := s.resyncNonce(); err != nil {
		t.Fatalf("resyncNonce: %v", err)
	}
	srv := httptest.NewServer(payMux(s))
	defer srv.Close()

	call := func() payResponse {
		body := `{"request_id":"referral-42","purpose":"referral","recipient":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","amount":5}`
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/pay", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer test-token")
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			data, _ := io.ReadAll(resp.Body)
			t.Fatalf("status=%d body=%s", resp.StatusCode, data)
		}
		var out payResponse
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		return out
	}

	first := call()
	second := call()
	if first.TransactionID != second.TransactionID || !second.Duplicate {
		t.Fatalf("idempotency failed: first=%+v second=%+v", first, second)
	}
	if len(fn.submitted) != 1 {
		t.Fatalf("request was paid %d times", len(fn.submitted))
	}
}

func TestHandleVerify_LinkChallenge(t *testing.T) {
	// Generate a player keypair and sign a link challenge the way the wallet would.
	pk, sk, err := mldsa87.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	pub, _ := pk.MarshalBinary()
	challenge := "qsdm-link:user=42:nonce=deadbeef"
	sig := make([]byte, mldsa87.SignatureSize)
	if err := mldsa87.SignTo(sk, []byte(challenge), nil, true, sig); err != nil {
		t.Fatalf("sign: %v", err)
	}
	sum := sha256.Sum256(pub)
	wantAddr := hex.EncodeToString(sum[:])

	// A signer with no node needed for pure verification.
	s := newTestSigner(t, "http://127.0.0.1:0")
	srv := httptest.NewServer(payMux(s))
	defer srv.Close()

	call := func(body string) (int, verifyResponse) {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/verify", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer test-token")
		req.Header.Set("Content-Type", "application/json")
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("verify call: %v", err)
		}
		defer r.Body.Close()
		var vr verifyResponse
		_ = json.NewDecoder(r.Body).Decode(&vr)
		return r.StatusCode, vr
	}

	// Valid signature -> valid=true, address matches.
	good := fmt.Sprintf(`{"message":%q,"signature":%q,"public_key":%q}`,
		challenge, hex.EncodeToString(sig), hex.EncodeToString(pub))
	code, vr := call(good)
	if code != http.StatusOK || !vr.Valid {
		t.Fatalf("want 200 valid=true, got %d valid=%v", code, vr.Valid)
	}
	if vr.Address != wantAddr {
		t.Fatalf("address mismatch: want %s got %s", wantAddr, vr.Address)
	}

	// Tampered message -> valid=false.
	bad := fmt.Sprintf(`{"message":%q,"signature":%q,"public_key":%q}`,
		challenge+"-tampered", hex.EncodeToString(sig), hex.EncodeToString(pub))
	code, vr = call(bad)
	if code != http.StatusOK || vr.Valid {
		t.Fatalf("want 200 valid=false for tampered message, got %d valid=%v", code, vr.Valid)
	}
}

// payMux builds the same routes run() registers, for httptest.
func payMux(s *signer) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/v1/balance", s.handleBalance)
	mux.HandleFunc("/v1/pay", s.requireToken(s.handlePay))
	mux.HandleFunc("/v1/resync", s.requireToken(s.handleResync))
	mux.HandleFunc("/v1/verify", s.requireToken(s.handleVerify))
	return mux
}
