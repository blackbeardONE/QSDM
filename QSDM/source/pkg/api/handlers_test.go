package api

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/internal/logging"
	"github.com/blackbeardONE/QSDM/pkg/branding"
	"github.com/blackbeardONE/QSDM/pkg/monitoring"
	"github.com/blackbeardONE/QSDM/pkg/storage"
	"github.com/blackbeardONE/QSDM/pkg/submesh"
	"github.com/blackbeardONE/QSDM/pkg/wallet"
)

// mockStorage is a simple mock storage for testing.
//
// v0.4.1 (Session 100): extended with `nonces` map and an in-memory
// ApplyTransferAtomic implementation that mirrors the sentinel
// posture of pkg/storage.Storage. The handler now exercises both
// paths through this mock, so the unit-test coverage of
// SubmitSignedTransaction stays meaningful without spinning up a
// real SQLite database. Tests that need to inject a storage-side
// fault (e.g. TestSubmitSigned_NonceLookupFailed) can override
// `getNonceErr` / `applyTransferErr` directly on the instance.
type mockStorage struct {
	transactions     map[string][]byte
	balances         map[string]float64
	nonces           map[string]uint64
	readyErr         error
	getNonceErr      error // injectable: makes GetNonce return this error verbatim
	applyTransferErr error // injectable: makes ApplyTransferAtomic return this error verbatim
}

func newMockStorage() *mockStorage {
	return &mockStorage{
		transactions: make(map[string][]byte),
		balances:     make(map[string]float64),
		nonces:       make(map[string]uint64),
	}
}

func (m *mockStorage) StoreTransaction(data []byte) error {
	// v0.4.0 (Session 95): index by the envelope's tx_id when
	// present so the /wallet/submit-signed idempotency tests can
	// exercise the GetTransaction lookup path. Falls back to the
	// legacy "test" key for older payload shapes (auth login mint
	// envelopes etc.) that don't carry an `id`.
	var probe map[string]interface{}
	if err := json.Unmarshal(data, &probe); err == nil {
		if id, ok := probe["id"].(string); ok && id != "" {
			m.transactions[id] = data
			return nil
		}
	}
	m.transactions["test"] = data
	return nil
}

func (m *mockStorage) Close() error {
	return nil
}

func (m *mockStorage) Ready() error {
	return m.readyErr
}

func (m *mockStorage) GetBalance(address string) (float64, error) {
	if balance, ok := m.balances[address]; ok {
		return balance, nil
	}
	return 0.0, nil
}

func (m *mockStorage) UpdateBalance(address string, amount float64) error {
	m.balances[address] = m.balances[address] + amount
	return nil
}

func (m *mockStorage) SetBalance(address string, balance float64) error {
	m.balances[address] = balance
	return nil
}

func (m *mockStorage) GetRecentTransactions(address string, limit int) ([]map[string]interface{}, error) {
	return []map[string]interface{}{
		{"id": "tx1", "sender": address, "amount": 10.0},
	}, nil
}

func (m *mockStorage) GetTransaction(txID string) (map[string]interface{}, error) {
	// v0.4.0 (Session 95): do a real lookup against the indexed
	// store so /wallet/submit-signed idempotency tests can
	// distinguish "first send" (404-equivalent error) from
	// "duplicate send" (200 with envelope). Prior to v0.4.0 this
	// always returned a stub map — fine for the only previous
	// caller (a single-purpose response-shape test) but a
	// foot-gun for the new handler.
	raw, ok := m.transactions[txID]
	if !ok {
		return nil, fmt.Errorf("transaction not found: %s", txID)
	}
	var tx map[string]interface{}
	if err := json.Unmarshal(raw, &tx); err != nil {
		return nil, err
	}
	return tx, nil
}

// GetNonce returns the per-account nonce stored against `address`.
// v0.4.1 (Session 100). Honours the injectable fault `getNonceErr`
// so tests can exercise the "nonce lookup failed" handler branch
// without an external fixture.
func (m *mockStorage) GetNonce(address string) (uint64, error) {
	if m.getNonceErr != nil {
		return 0, m.getNonceErr
	}
	return m.nonces[address], nil
}

// ApplyTransferAtomic mirrors pkg/storage/sqlite_v041.go's invariants
// in pure Go so the handler's v0.4.1 path is exercised end-to-end
// in the test suite. Enforces (in order):
//   - injectable `applyTransferErr` override
//   - tx_id uniqueness                  → storage.ErrTxAlreadyExists
//   - envelopeNonce vs stored nonce CAS → storage.ErrNonceConflict
//     (only when envelopeNonce >= 1; envelopeNonce == 0 is the
//     legacy v0.4.0 path — no nonce check, no nonce bump)
//   - balance >= amount + fee           → storage.ErrInsufficientBalance
//
// All three sentinels are package-level vars in pkg/storage; the
// handler maps them onto HTTP codes + monitoring tags.
func (m *mockStorage) ApplyTransferAtomic(
	ctx context.Context,
	sender, recipient string,
	amount, fee float64,
	envelopeNonce uint64,
	txID string,
	rawEnvelope []byte,
) error {
	if m.applyTransferErr != nil {
		return m.applyTransferErr
	}
	if _, dup := m.transactions[txID]; dup {
		return storage.ErrTxAlreadyExists
	}
	if envelopeNonce >= 1 {
		want := m.nonces[sender] + 1
		if envelopeNonce != want {
			return storage.ErrNonceConflict
		}
	}
	total := amount + fee
	if m.balances[sender] < total {
		return storage.ErrInsufficientBalance
	}
	m.balances[sender] -= total
	m.balances[recipient] += amount
	if envelopeNonce >= 1 {
		m.nonces[sender] = envelopeNonce
	}
	m.transactions[txID] = append([]byte(nil), rawEnvelope...)
	return nil
}

func setupTestHandlers() *Handlers {
	logger := logging.NewLogger("test.log", false)
	authManager, _ := NewAuthManager()
	userStore := NewUserStore()
	mockStorage := newMockStorage()

	return NewHandlers(authManager, userStore, nil, mockStorage, logger, "", false, 0, "", "", false, 0, false, nil)
}

func setupTestHandlersNvidiaLock() *Handlers {
	logger := logging.NewLogger("test.log", false)
	authManager, _ := NewAuthManager()
	userStore := NewUserStore()
	mockStorage := newMockStorage()
	return NewHandlers(authManager, userStore, nil, mockStorage, logger, "", true, time.Hour, "", "", false, 0, false, nil)
}

func setupTestHandlersNvidiaLockNode(expectedNodeID string) *Handlers {
	logger := logging.NewLogger("test.log", false)
	authManager, _ := NewAuthManager()
	userStore := NewUserStore()
	mockStorage := newMockStorage()
	return NewHandlers(authManager, userStore, nil, mockStorage, logger, "", true, time.Hour, expectedNodeID, "", false, 0, false, nil)
}

func setupTestHandlersNvidiaLockHMAC(hmacSecret string) *Handlers {
	logger := logging.NewLogger("test.log", false)
	authManager, _ := NewAuthManager()
	userStore := NewUserStore()
	mockStorage := newMockStorage()
	return NewHandlers(authManager, userStore, nil, mockStorage, logger, "", true, time.Hour, "", hmacSecret, false, 0, false, nil)
}

func setupTestHandlersNvidiaLockIngestNonce(hmacSecret string, nonceTTL time.Duration) *Handlers {
	logger := logging.NewLogger("test.log", false)
	authManager, _ := NewAuthManager()
	userStore := NewUserStore()
	mockStorage := newMockStorage()
	return NewHandlers(authManager, userStore, nil, mockStorage, logger, "", true, time.Hour, "", hmacSecret, true, nonceTTL, false, nil)
}

func ngcProofBundleWithHMAC(secret, cudaHash, tsUTC, nodeID, ingestNonce string) []byte {
	m := map[string]interface{}{
		"architecture":     "NVIDIA-Locked QSDM test",
		"cuda_proof_hash":  cudaHash,
		"timestamp_utc":    tsUTC,
		"qsdm_node_id": nodeID,
		"gpu_fingerprint":  map[string]interface{}{"available": true, "devices": []interface{}{map[string]interface{}{"name": "G", "index": "0"}}},
	}
	if strings.TrimSpace(ingestNonce) != "" {
		m["qsdm_ingest_nonce"] = ingestNonce
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(monitoring.NGCProofHMACPayload(m)))
	m["qsdm_proof_hmac"] = hex.EncodeToString(mac.Sum(nil))
	raw, _ := json.Marshal(m)
	return raw
}

func TestHealthCheck(t *testing.T) {
	handlers := setupTestHandlers()
	req := httptest.NewRequest("GET", "/api/v1/health", nil)
	w := httptest.NewRecorder()

	handlers.HealthCheck(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if response["status"] != "healthy" {
		t.Errorf("Expected status 'healthy', got %v", response["status"])
	}
}

func TestHealthLive(t *testing.T) {
	h := setupTestHandlers()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/health/live", nil)
	w := httptest.NewRecorder()
	h.HealthLive(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("live: want 200, got %d", w.Code)
	}
}

func TestHealthReady(t *testing.T) {
	h := setupTestHandlers()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/health/ready", nil)
	w := httptest.NewRecorder()
	h.HealthReady(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ready: want 200, got %d", w.Code)
	}
	var response map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if response["status"] != "ready" {
		t.Fatalf("expected status ready, got %v", response["status"])
	}
}

func TestHealthReadyStorageDown(t *testing.T) {
	logger := logging.NewLogger("test.log", false)
	authManager, _ := NewAuthManager()
	userStore := NewUserStore()
	ms := newMockStorage()
	ms.readyErr = errors.New("db down")
	h := NewHandlers(authManager, userStore, nil, ms, logger, "", false, 0, "", "", false, 0, false, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health/ready", nil)
	w := httptest.NewRecorder()
	h.HealthReady(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d", w.Code)
	}
}

func TestRegister(t *testing.T) {
	handlers := setupTestHandlers()
	validAddr := "0123456789abcdef0123456789abcdef0123456789"
	validPass := "Charming123!"

	// Test successful registration
	reqBody := map[string]string{
		"address":  validAddr,
		"password": validPass,
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/api/v1/auth/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handlers.Register(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("Expected status 201, got %d", w.Code)
	}

	// Test duplicate registration (new Body reader — first request drained r.Body)
	dupBody, _ := json.Marshal(reqBody)
	dupReq := httptest.NewRequest("POST", "/api/v1/auth/register", bytes.NewReader(dupBody))
	dupReq.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	handlers.Register(w2, dupReq)
	if w2.Code != http.StatusConflict {
		t.Errorf("Expected status 409 for duplicate, got %d", w2.Code)
	}

	// Test invalid password (too short) — fresh hex address so we do not hit "user exists"
	shortPassBody := map[string]string{
		"address":  "fedcba9876543210fedcba9876543210fedcba98",
		"password": "short",
	}
	body, _ = json.Marshal(shortPassBody)
	req = httptest.NewRequest("POST", "/api/v1/auth/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w3 := httptest.NewRecorder()
	handlers.Register(w3, req)
	if w3.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400 for short password, got %d", w3.Code)
	}
}

func TestLogin(t *testing.T) {
	handlers := setupTestHandlers()

	// Register a user first
	reqBody := map[string]string{
		"address":  "0123456789abcdef0123456789abcdef0123456789",
		"password": "Charming123!",
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/api/v1/auth/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handlers.Register(w, req)

	// Test successful login
	loginBody, _ := json.Marshal(reqBody)
	loginReq := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewReader(loginBody))
	loginReq.Header.Set("Content-Type", "application/json")
	loginW := httptest.NewRecorder()

	handlers.Login(loginW, loginReq)

	if loginW.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", loginW.Code)
	}

	var response LoginResponse
	if err := json.NewDecoder(loginW.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if response.AccessToken == "" {
		t.Error("Expected access token, got empty string")
	}
	if response.RefreshToken == "" {
		t.Error("Expected refresh token, got empty string")
	}

	// Test invalid password
	invalidBody := map[string]string{
		"address":  "0123456789abcdef0123456789abcdef0123456789",
		"password": "Wrongpass999!",
	}
	invalidBodyBytes, _ := json.Marshal(invalidBody)
	invalidReq := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewReader(invalidBodyBytes))
	invalidReq.Header.Set("Content-Type", "application/json")
	invalidW := httptest.NewRecorder()
	handlers.Login(invalidW, invalidReq)

	if invalidW.Code != http.StatusUnauthorized {
		t.Errorf("Expected status 401 for invalid password, got %d", invalidW.Code)
	}
}

func TestGetBalance(t *testing.T) {
	handlers := setupTestHandlers()
	mockStorage := handlers.storage.(*mockStorage)
	mockStorage.SetBalance("test_address", 100.0)

	// Create a token for authentication
	authManager, _ := NewAuthManager()
	token, _ := authManager.CreateToken("test_user", "test_address", "user", TokenTypeAccess, 15*60*1000000000) // 15 minutes in nanoseconds

	req := httptest.NewRequest("GET", "/api/v1/wallet/balance?address=test_address", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	// We need to add claims to context manually for testing
	// This is a simplified test - in real integration tests, middleware would handle this
	// For now, we'll test the handler logic directly by calling it with proper setup
	handlers.GetBalance(w, req)

	// Note: This test will fail without proper middleware setup
	// Full integration tests should be in tests/api_integration_test.go
}

// TestWalletMint_410Gone documents the v0.3.3+ posture of
// POST /api/v1/wallet/mint: removed (see handlers.go::MintMainCoin
// for the why). The previous 8 mint tests (TestNvidiaLockMintMainCoin_*
// and TestSubmeshMintMainCoin_*) were deleted along with the
// real handler body — the NVIDIA-lock / HMAC / ingest-nonce /
// submesh-privileged-payload code paths they exercised are still
// covered by the other consumers (`/api/v1/wallet/send`,
// `/api/v1/tokens/mint`, etc.) so removing the mint-specific tests
// does not regress the gate coverage.
func TestWalletMint_410Gone(t *testing.T) {
	h := setupTestHandlersNvidiaLock()
	body := []byte(`{"recipient":"0123456789abcdef0123456789abcdef0123456789","amount":1}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/wallet/mint", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.MintMainCoin(w, req)

	if w.Code != http.StatusGone {
		t.Fatalf("expected 410 Gone, got %d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode 410 body: %v (raw=%s)", err, w.Body.String())
	}
	if status, _ := resp["status"].(string); status != "gone" {
		t.Errorf(`status = %q; want "gone" (raw=%s)`, status, w.Body.String())
	}
	if _, ok := resp["migration"].(map[string]interface{}); !ok {
		t.Errorf("missing `migration` block in 410 body: %s", w.Body.String())
	}
	if reason, _ := resp["reason"].(string); reason == "" {
		t.Errorf("empty `reason` field in 410 body: %s", w.Body.String())
	}
}

// TestWalletMint_410GoneMethodNotAllowed asserts the
// method-not-allowed branch still wins over the 410 — a GET
// /api/v1/wallet/mint returns 405, not 410, because surfacing
// 405 first matches the rest of the handler conventions and
// keeps caller-side method-routing diagnostics clean.
func TestWalletMint_410GoneMethodNotAllowed(t *testing.T) {
	h := setupTestHandlersNvidiaLock()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/wallet/mint", nil)
	w := httptest.NewRecorder()
	h.MintMainCoin(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 on GET, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestNGCIngestChallenge_notFoundWhenDisabled(t *testing.T) {
	logger := logging.NewLogger("test.log", false)
	authManager, _ := NewAuthManager()
	userStore := NewUserStore()
	mockStorage := newMockStorage()
	h := NewHandlers(authManager, userStore, nil, mockStorage, logger, "Charming123", true, time.Hour, "", "Charming123", false, 0, false, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/monitoring/ngc-challenge", nil)
	req.Header.Set(branding.NGCSecretHeaderPreferred, "Charming123")
	w := httptest.NewRecorder()
	h.NGCIngestChallenge(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestNGCIngestChallenge_okWhenEnabled(t *testing.T) {
	monitoring.ResetNGCIngestNoncesForTest()
	t.Cleanup(monitoring.ResetNGCIngestNoncesForTest)

	logger := logging.NewLogger("test.log", false)
	authManager, _ := NewAuthManager()
	userStore := NewUserStore()
	mockStorage := newMockStorage()
	h := NewHandlers(authManager, userStore, nil, mockStorage, logger, "Charming123", true, time.Hour, "", "Charming123", true, time.Hour, false, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/monitoring/ngc-challenge", nil)
	req.Header.Set(branding.NGCSecretHeaderPreferred, "Charming123")
	w := httptest.NewRecorder()
	h.NGCIngestChallenge(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	ns, _ := resp["qsdm_ingest_nonce"].(string)
	if ns == "" {
		t.Fatalf("missing nonce: %#v", resp)
	}
}

func setupTestHandlersWithSubmesh(dm *submesh.DynamicSubmeshManager, ws *wallet.WalletService) *Handlers {
	logger := logging.NewLogger("test.log", false)
	authManager, _ := NewAuthManager()
	userStore := NewUserStore()
	mockStorage := newMockStorage()
	return NewHandlers(authManager, userStore, ws, mockStorage, logger, "", false, 0, "", "", false, 0, false, dm)
}

// TestSubmeshMintMainCoin_422OversizedPayload was deleted in
// v0.3.3 (Session 91): the submesh privileged-payload gate is no
// longer reachable via /api/v1/wallet/mint (it returns 410 Gone
// before the submesh check). Equivalent submesh coverage is in
// TestSubmeshSendTransaction_422NoMatchingRoute and the broader
// pkg/submesh test suite.

func TestSubmeshSendTransaction_422NoMatchingRoute(t *testing.T) {
	ws, err := wallet.NewWalletService()
	if err != nil {
		t.Fatal(err)
	}
	dm := submesh.NewDynamicSubmeshManager()
	dm.AddOrUpdateSubmesh(&submesh.DynamicSubmesh{
		Name: "us", FeeThreshold: 0.001, PriorityLevel: 1, GeoTags: []string{"US"}, MaxPayloadBytes: 100000,
	})
	h := setupTestHandlersWithSubmesh(dm, ws)
	recipient := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	body := map[string]interface{}{
		"recipient":    recipient,
		"amount":       1.0,
		"fee":          0.01,
		"geotag":       "EU",
		"parent_cells": []string{},
	}
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/wallet/send", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	ctx := ContextWithClaims(req.Context(), &Claims{Address: ws.GetAddress(), Role: "user"})
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	h.SendTransaction(w, req)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestSendTransaction_meshCompanionSecondBroadcast(t *testing.T) {
	ws, err := wallet.NewWalletService()
	if err != nil {
		t.Skip("wallet requires CGO / Dilithium")
	}
	t.Setenv("QSDM_PUBLISH_MESH_COMPANION", "1")
	before := monitoring.MeshCompanionPublishCount()

	dm := submesh.NewDynamicSubmeshManager()
	dm.AddOrUpdateSubmesh(&submesh.DynamicSubmesh{
		Name: "us", FeeThreshold: 0, PriorityLevel: 1, GeoTags: []string{"US"}, MaxPayloadBytes: 1_000_000,
	})
	h := setupTestHandlersWithSubmesh(dm, ws)

	var payloads [][]byte
	h.SetP2PTxBroadcast(func(b []byte) error {
		payloads = append(payloads, append([]byte(nil), b...))
		return nil
	})

	recipient := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	p1 := strings.Repeat("a", 32)
	p2 := strings.Repeat("b", 32)
	body := map[string]interface{}{
		"recipient":    recipient,
		"amount":       1.0,
		"fee":          0.01,
		"geotag":       "US",
		"parent_cells": []string{p1, p2},
	}
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/wallet/send", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	ctx := ContextWithClaims(req.Context(), &Claims{Address: ws.GetAddress(), Role: "user"})
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	h.SendTransaction(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", w.Code, w.Body.String())
	}
	if len(payloads) != 2 {
		t.Fatalf("expected 2 P2P broadcasts (wallet + mesh companion), got %d", len(payloads))
	}
	var wire map[string]interface{}
	if err := json.Unmarshal(payloads[1], &wire); err != nil {
		t.Fatal(err)
	}
	if wire["kind"] != "qsdm_mesh3d_v1" {
		t.Fatalf("companion kind = %v", wire["kind"])
	}
	if got := monitoring.MeshCompanionPublishCount(); got != before+1 {
		t.Fatalf("mesh_companion_publish_total: before=%d after=%d want +1", before, got)
	}
}

// ============================================================
// v0.4.0 (Session 95) — POST /api/v1/wallet/submit-signed tests
// ============================================================
//
// These tests exercise the self-custody signed-envelope handler
// added in v0.4.0. Every test builds a fresh ML-DSA-87 keypair
// (via wallet.NewWalletService — circl backend on non-CGO,
// liboqs on CGO), constructs a TransactionData envelope, signs
// it with the correct canonical-payload (signature + public_key
// fields cleared, then re-marshalled in struct field order), and
// asserts on the handler's terminal posture.

// buildSignedEnvelope is the v0.4.0 test fixture: produce a
// wire-correct, ML-DSA-87-signed wallet.TransactionData ready
// for POST /api/v1/wallet/submit-signed. Caller can mutate the
// returned envelope before re-marshalling to test bad-input
// cases (sender mismatch, corrupted signature, etc.).
func buildSignedEnvelope(t *testing.T, ws *wallet.WalletService, recipient string, amount, fee float64, parents []string) wallet.TransactionData {
	t.Helper()
	pubKey := ws.GetPublicKey()
	if pubKey == nil {
		t.Fatal("ws.GetPublicKey returned nil; cannot build signed envelope")
	}
	addrHash := sha256.Sum256(pubKey)
	sender := hex.EncodeToString(addrHash[:])

	now := time.Now().UTC()
	txIDSeed := sha256.Sum256([]byte(sender + recipient + now.Format(time.RFC3339Nano)))
	env := wallet.TransactionData{
		ID:          hex.EncodeToString(txIDSeed[:16]),
		Sender:      sender,
		Recipient:   recipient,
		Amount:      amount,
		Fee:         fee,
		GeoTag:      "US",
		ParentCells: parents,
		Timestamp:   now.Format(time.RFC3339),
	}
	canonical, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal canonical envelope: %v", err)
	}
	sig, err := ws.SignData(canonical)
	if err != nil {
		t.Fatalf("sign canonical envelope: %v", err)
	}
	env.Signature = hex.EncodeToString(sig)
	env.PublicKey = hex.EncodeToString(pubKey)
	return env
}

// postSubmitSigned is a tiny helper that wires the request shape
// the handler expects.
func postSubmitSigned(t *testing.T, h *Handlers, env wallet.TransactionData) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/wallet/submit-signed", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.SubmitSignedTransaction(w, req)
	return w
}

func TestSubmitSigned_HappyPath(t *testing.T) {
	ws, err := wallet.NewWalletService()
	if err != nil {
		t.Skipf("wallet requires CGO / Dilithium: %v", err)
	}
	h := setupTestHandlersWithSubmesh(nil, ws)

	// v0.4.1: the atomic-transfer primitive now enforces
	// balance >= amount + fee unconditionally (v0.4.0's
	// pre-flight check skipped balance==0). Pre-fund the
	// sender so the happy-path still asserts the success
	// posture rather than the new InsufficientBalance branch.
	pubKey := ws.GetPublicKey()
	addrHash := sha256.Sum256(pubKey)
	sender := hex.EncodeToString(addrHash[:])
	h.storage.(*mockStorage).balances[sender] = 5.0

	recipient := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	env := buildSignedEnvelope(t, ws, recipient, 1.0, 0.01, []string{
		strings.Repeat("a", 32), strings.Repeat("b", 32),
	})

	w := postSubmitSigned(t, h, env)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp SubmitSignedTransactionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.TransactionID != env.ID {
		t.Fatalf("tx_id mismatch: want %q got %q", env.ID, resp.TransactionID)
	}
	if resp.Status != "accepted" {
		t.Fatalf("status: want 'accepted' got %q", resp.Status)
	}
}

func TestSubmitSigned_MethodNotAllowed(t *testing.T) {
	ws, err := wallet.NewWalletService()
	if err != nil {
		t.Skipf("wallet requires CGO / Dilithium: %v", err)
	}
	h := setupTestHandlersWithSubmesh(nil, ws)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/wallet/submit-signed", nil)
	w := httptest.NewRecorder()
	h.SubmitSignedTransaction(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestSubmitSigned_MalformedJSON(t *testing.T) {
	ws, err := wallet.NewWalletService()
	if err != nil {
		t.Skipf("wallet requires CGO / Dilithium: %v", err)
	}
	h := setupTestHandlersWithSubmesh(nil, ws)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/wallet/submit-signed", bytes.NewReader([]byte("{not json")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.SubmitSignedTransaction(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestSubmitSigned_SenderMismatch(t *testing.T) {
	ws, err := wallet.NewWalletService()
	if err != nil {
		t.Skipf("wallet requires CGO / Dilithium: %v", err)
	}
	h := setupTestHandlersWithSubmesh(nil, ws)

	recipient := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	env := buildSignedEnvelope(t, ws, recipient, 1.0, 0.01, []string{
		strings.Repeat("a", 32), strings.Repeat("b", 32),
	})
	env.Sender = strings.Repeat("f", 64) // valid hex64 shape, wrong identity

	w := postSubmitSigned(t, h, env)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "sender does not match") {
		t.Fatalf("expected sender-mismatch error, got body=%s", w.Body.String())
	}
}

func TestSubmitSigned_BadSignature(t *testing.T) {
	ws, err := wallet.NewWalletService()
	if err != nil {
		t.Skipf("wallet requires CGO / Dilithium: %v", err)
	}
	h := setupTestHandlersWithSubmesh(nil, ws)

	recipient := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	env := buildSignedEnvelope(t, ws, recipient, 1.0, 0.01, []string{
		strings.Repeat("a", 32), strings.Repeat("b", 32),
	})
	// Flip one byte of the signature (preserving hex length).
	sig := []byte(env.Signature)
	if sig[0] == '0' {
		sig[0] = '1'
	} else {
		sig[0] = '0'
	}
	env.Signature = string(sig)

	w := postSubmitSigned(t, h, env)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestSubmitSigned_DuplicateTxID(t *testing.T) {
	ws, err := wallet.NewWalletService()
	if err != nil {
		t.Skipf("wallet requires CGO / Dilithium: %v", err)
	}
	h := setupTestHandlersWithSubmesh(nil, ws)

	// Same pre-fund requirement as TestSubmitSigned_HappyPath:
	// the first submit must succeed before the second can hit
	// the duplicate-tx_id branch.
	pubKey := ws.GetPublicKey()
	addrHash := sha256.Sum256(pubKey)
	sender := hex.EncodeToString(addrHash[:])
	h.storage.(*mockStorage).balances[sender] = 5.0

	recipient := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	env := buildSignedEnvelope(t, ws, recipient, 1.0, 0.01, []string{
		strings.Repeat("a", 32), strings.Repeat("b", 32),
	})
	w1 := postSubmitSigned(t, h, env)
	if w1.Code != http.StatusOK {
		t.Fatalf("first submit: expected 200, got %d body=%s", w1.Code, w1.Body.String())
	}
	w2 := postSubmitSigned(t, h, env)
	if w2.Code != http.StatusConflict {
		t.Fatalf("second submit: expected 409 duplicate, got %d body=%s", w2.Code, w2.Body.String())
	}
	var resp SubmitSignedTransactionResponse
	if err := json.Unmarshal(w2.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode duplicate response: %v", err)
	}
	if resp.Status != "duplicate" {
		t.Fatalf("status: want 'duplicate' got %q", resp.Status)
	}
}

func TestSubmitSigned_InsufficientBalance(t *testing.T) {
	ws, err := wallet.NewWalletService()
	if err != nil {
		t.Skipf("wallet requires CGO / Dilithium: %v", err)
	}
	h := setupTestHandlersWithSubmesh(nil, ws)

	pubKey := ws.GetPublicKey()
	addrHash := sha256.Sum256(pubKey)
	sender := hex.EncodeToString(addrHash[:])

	// Pre-fund the sender with 0.5 CELL — below the 1.0 + 0.01 = 1.01 ask.
	// We have to reach into the mock directly because StorageInterface
	// doesn't expose SetBalance.
	mock := h.storage.(*mockStorage)
	mock.balances[sender] = 0.5

	recipient := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	env := buildSignedEnvelope(t, ws, recipient, 1.0, 0.01, []string{
		strings.Repeat("a", 32), strings.Repeat("b", 32),
	})
	w := postSubmitSigned(t, h, env)
	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("expected 402, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestSubmitSigned_NoWalletService(t *testing.T) {
	logger := logging.NewLogger("test.log", false)
	authManager, _ := NewAuthManager()
	userStore := NewUserStore()
	mock := newMockStorage()
	h := NewHandlers(authManager, userStore, nil, mock, logger, "", false, 0, "", "", false, 0, false, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/wallet/submit-signed", bytes.NewReader([]byte("{}")))
	w := httptest.NewRecorder()
	h.SubmitSignedTransaction(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 (no wallet service), got %d body=%s", w.Code, w.Body.String())
	}
}

// =============================================================
// v0.4.1 (Session 100) tests for the replay-protection +
// atomic-debit path on POST /api/v1/wallet/submit-signed.
//
// All five tests below pivot on the new Nonce field added to
// wallet.TransactionData in Session 99 (commit ecfa121). The
// envelope canonicalisation contract is unchanged structurally
// (json.Marshal of the struct minus signature + public_key);
// Nonce just appears as a new field with json tag "nonce".
//
// Coverage:
//   - TestSubmitSigned_HappyPath_WithNonce       Nonce=1 round-trip
//   - TestSubmitSigned_LegacyV040Envelope        Nonce=0 backward-compat
//   - TestSubmitSigned_NonceReplay               Nonce <= last-seen → 409
//   - TestSubmitSigned_NonceConflict             concurrent-CAS → 409
//   - TestSubmitSigned_NonceLookupFailed         storage-side error → 500
// =============================================================

// buildSignedEnvelopeWithNonce is the v0.4.1 fixture: like
// buildSignedEnvelope but stamps a non-zero Nonce onto the
// canonical bytes before signing. Crucially, the sign-then-marshal
// sequence ensures the signature covers the new field — exactly
// matching what the WASM/CLI signer produces (the omitempty tag
// on Nonce means a zero value drops out of the canonical bytes
// entirely, preserving v0.4.0 wire compatibility).
func buildSignedEnvelopeWithNonce(t *testing.T, ws *wallet.WalletService, recipient string, amount, fee float64, parents []string, nonce uint64) wallet.TransactionData {
	t.Helper()
	env := buildSignedEnvelope(t, ws, recipient, amount, fee, parents)
	// Re-sign with the nonce field set so the signature covers it.
	env.Signature = ""
	env.PublicKey = ""
	env.Nonce = nonce
	canonical, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal canonical envelope w/ nonce: %v", err)
	}
	sig, err := ws.SignData(canonical)
	if err != nil {
		t.Fatalf("sign canonical envelope w/ nonce: %v", err)
	}
	env.Signature = hex.EncodeToString(sig)
	env.PublicKey = hex.EncodeToString(ws.GetPublicKey())
	return env
}

// TestSubmitSigned_HappyPath_WithNonce asserts that an envelope
// with Nonce=1 takes the v0.4.1 path: GetNonce returns 0 (new
// sender), 1 > 0 so the replay gate clears, ApplyTransferAtomic
// debits + credits + bumps the stored nonce to 1.
func TestSubmitSigned_HappyPath_WithNonce(t *testing.T) {
	ws, err := wallet.NewWalletService()
	if err != nil {
		t.Skipf("wallet requires CGO / Dilithium: %v", err)
	}
	h := setupTestHandlersWithSubmesh(nil, ws)
	mock := h.storage.(*mockStorage)

	pubKey := ws.GetPublicKey()
	addrHash := sha256.Sum256(pubKey)
	sender := hex.EncodeToString(addrHash[:])
	mock.balances[sender] = 5.0

	recipient := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	env := buildSignedEnvelopeWithNonce(t, ws, recipient, 1.0, 0.01, []string{
		strings.Repeat("a", 32), strings.Repeat("b", 32),
	}, 1)

	w := postSubmitSigned(t, h, env)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if mock.nonces[sender] != 1 {
		t.Fatalf("stored nonce: want 1 got %d", mock.nonces[sender])
	}
	if got := mock.balances[sender]; got != 5.0-1.01 {
		t.Fatalf("sender balance: want %v got %v", 5.0-1.01, got)
	}
	if got := mock.balances[recipient]; got != 1.0 {
		t.Fatalf("recipient balance: want 1.0 got %v", got)
	}
}

// TestSubmitSigned_LegacyV040Envelope asserts the backward-compat
// promise from V041_REPLAY_PROTECTION_DESIGN.md §2.3: a v0.4.0
// envelope (no Nonce field, omitempty drops it from canonical
// bytes) still serves 200 through the new handler. The stored
// nonce stays at 0 because legacy envelopes never bump it.
func TestSubmitSigned_LegacyV040Envelope(t *testing.T) {
	ws, err := wallet.NewWalletService()
	if err != nil {
		t.Skipf("wallet requires CGO / Dilithium: %v", err)
	}
	h := setupTestHandlersWithSubmesh(nil, ws)
	mock := h.storage.(*mockStorage)

	pubKey := ws.GetPublicKey()
	addrHash := sha256.Sum256(pubKey)
	sender := hex.EncodeToString(addrHash[:])
	mock.balances[sender] = 5.0

	recipient := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	env := buildSignedEnvelope(t, ws, recipient, 1.0, 0.01, []string{
		strings.Repeat("a", 32), strings.Repeat("b", 32),
	})
	if env.Nonce != 0 {
		t.Fatalf("legacy envelope must have Nonce==0, got %d", env.Nonce)
	}

	w := postSubmitSigned(t, h, env)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for legacy envelope, got %d body=%s", w.Code, w.Body.String())
	}
	if got := mock.nonces[sender]; got != 0 {
		t.Fatalf("legacy path must NOT bump nonce; got stored nonce %d", got)
	}
}

// TestSubmitSigned_NonceReplay asserts that re-submitting an
// envelope with a nonce <= the last-seen stored nonce returns
// HTTP 409 and bumps qsdm_wallet_send_total{result=nonce_replay}.
// The handler must short-circuit BEFORE the atomic-debit call
// so a replay never even touches the balance ledger.
func TestSubmitSigned_NonceReplay(t *testing.T) {
	ws, err := wallet.NewWalletService()
	if err != nil {
		t.Skipf("wallet requires CGO / Dilithium: %v", err)
	}
	h := setupTestHandlersWithSubmesh(nil, ws)
	mock := h.storage.(*mockStorage)

	pubKey := ws.GetPublicKey()
	addrHash := sha256.Sum256(pubKey)
	sender := hex.EncodeToString(addrHash[:])
	mock.balances[sender] = 5.0
	// Simulate a prior successful send: stored nonce = 1.
	mock.nonces[sender] = 1

	recipient := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	// Replay with the same Nonce=1 the sender already used.
	env := buildSignedEnvelopeWithNonce(t, ws, recipient, 1.0, 0.01, []string{
		strings.Repeat("a", 32), strings.Repeat("b", 32),
	}, 1)

	before := monitoring.WalletSendCounts()
	w := postSubmitSigned(t, h, env)
	after := monitoring.WalletSendCounts()

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 nonce_replay, got %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "nonce replay") {
		t.Fatalf("expected nonce-replay error, got body=%s", w.Body.String())
	}
	if delta := walletSendDelta(before, after, monitoring.WalletSendResultNonceReplay); delta != 1 {
		t.Fatalf("qsdm_wallet_send_total{result=nonce_replay} delta: want 1 got %d", delta)
	}
	// Balances must not have moved.
	if mock.balances[sender] != 5.0 {
		t.Fatalf("replay must not debit sender; balance now %v", mock.balances[sender])
	}
	if _, exists := mock.balances[recipient]; exists {
		t.Fatalf("replay must not credit recipient; balance entry created: %v", mock.balances[recipient])
	}
}

// TestSubmitSigned_NonceConflict asserts that when the GetNonce
// pre-flight read agrees but the storage-side CAS rejects (a
// concurrent submit raced our envelope), the handler maps the
// storage.ErrNonceConflict sentinel to HTTP 409 with the
// nonce_conflict result tag. Distinct from nonce_replay: the
// caller's nonce is structurally correct but the storage state
// moved between our read and our write.
func TestSubmitSigned_NonceConflict(t *testing.T) {
	ws, err := wallet.NewWalletService()
	if err != nil {
		t.Skipf("wallet requires CGO / Dilithium: %v", err)
	}
	h := setupTestHandlersWithSubmesh(nil, ws)
	mock := h.storage.(*mockStorage)

	pubKey := ws.GetPublicKey()
	addrHash := sha256.Sum256(pubKey)
	sender := hex.EncodeToString(addrHash[:])
	mock.balances[sender] = 5.0
	// Stored nonce starts at 0. The envelope below will pass the
	// pre-flight (1 > 0) but the injected applyTransferErr forces
	// the CAS-conflict path inside ApplyTransferAtomic.
	mock.applyTransferErr = storage.ErrNonceConflict

	recipient := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	env := buildSignedEnvelopeWithNonce(t, ws, recipient, 1.0, 0.01, []string{
		strings.Repeat("a", 32), strings.Repeat("b", 32),
	}, 1)

	before := monitoring.WalletSendCounts()
	w := postSubmitSigned(t, h, env)
	after := monitoring.WalletSendCounts()

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 nonce_conflict, got %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "nonce conflict") {
		t.Fatalf("expected nonce-conflict error, got body=%s", w.Body.String())
	}
	if delta := walletSendDelta(before, after, monitoring.WalletSendResultNonceConflict); delta != 1 {
		t.Fatalf("qsdm_wallet_send_total{result=nonce_conflict} delta: want 1 got %d", delta)
	}
}

// TestSubmitSigned_NonceLookupFailed asserts that when the
// storage-side GetNonce call returns an error (db disk failure,
// connection-pool exhaustion, etc.), the handler returns HTTP 500
// and bumps qsdm_wallet_send_total{result=nonce_lookup_failed}.
// This is the only branch where the handler must fail closed —
// silently allowing replays on a storage fault would defeat the
// whole point of v0.4.1.
func TestSubmitSigned_NonceLookupFailed(t *testing.T) {
	ws, err := wallet.NewWalletService()
	if err != nil {
		t.Skipf("wallet requires CGO / Dilithium: %v", err)
	}
	h := setupTestHandlersWithSubmesh(nil, ws)
	mock := h.storage.(*mockStorage)
	mock.getNonceErr = errors.New("simulated storage I/O failure")

	pubKey := ws.GetPublicKey()
	addrHash := sha256.Sum256(pubKey)
	sender := hex.EncodeToString(addrHash[:])
	mock.balances[sender] = 5.0

	recipient := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	env := buildSignedEnvelopeWithNonce(t, ws, recipient, 1.0, 0.01, []string{
		strings.Repeat("a", 32), strings.Repeat("b", 32),
	}, 1)

	before := monitoring.WalletSendCounts()
	w := postSubmitSigned(t, h, env)
	after := monitoring.WalletSendCounts()

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 nonce_lookup_failed, got %d body=%s", w.Code, w.Body.String())
	}
	if delta := walletSendDelta(before, after, monitoring.WalletSendResultNonceLookupFailed); delta != 1 {
		t.Fatalf("qsdm_wallet_send_total{result=nonce_lookup_failed} delta: want 1 got %d", delta)
	}
}

// =============================================================
// GET /api/v1/wallet/nonce  (v0.4.1, Session 100)
// =============================================================

// postGetNonce wires the request shape the handler expects. We
// use httptest.NewRequest with a query param rather than a
// path-param parser because /wallet/nonce is registered with
// `mux.HandleFunc` (exact-match, no trailing slash) — symmetric
// with /wallet/balance.
func getWalletNonce(t *testing.T, h *Handlers, sender string) *httptest.ResponseRecorder {
	t.Helper()
	url := "/api/v1/wallet/nonce"
	if sender != "" {
		url += "?sender=" + sender
	}
	req := httptest.NewRequest(http.MethodGet, url, nil)
	w := httptest.NewRecorder()
	h.GetWalletNonce(w, req)
	return w
}

func TestGetWalletNonce_HappyPath_New(t *testing.T) {
	h := setupTestHandlers()
	sender := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	w := getWalletNonce(t, h, sender)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp GetWalletNonceResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Sender != sender {
		t.Fatalf("sender echo: want %q got %q", sender, resp.Sender)
	}
	if resp.Nonce != 0 {
		t.Fatalf("new-sender nonce: want 0 got %d", resp.Nonce)
	}
	if resp.Next != 1 {
		t.Fatalf("new-sender next: want 1 got %d", resp.Next)
	}
}

func TestGetWalletNonce_HappyPath_AfterSubmit(t *testing.T) {
	h := setupTestHandlers()
	sender := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	// Simulate a prior successful v0.4.1 submit by setting the
	// stored nonce directly. End-to-end coverage that the handler
	// observes the bump happens in TestGetWalletNonce_E2EBump.
	h.storage.(*mockStorage).nonces[sender] = 7

	w := getWalletNonce(t, h, sender)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp GetWalletNonceResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Nonce != 7 {
		t.Fatalf("nonce: want 7 got %d", resp.Nonce)
	}
	if resp.Next != 8 {
		t.Fatalf("next: want 8 got %d", resp.Next)
	}
}

func TestGetWalletNonce_MethodNotAllowed(t *testing.T) {
	h := setupTestHandlers()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/wallet/nonce?sender=abc", nil)
	w := httptest.NewRecorder()
	h.GetWalletNonce(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestGetWalletNonce_MissingSender(t *testing.T) {
	h := setupTestHandlers()
	w := getWalletNonce(t, h, "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "sender") {
		t.Fatalf("expected 'sender' in body, got %s", w.Body.String())
	}
}

func TestGetWalletNonce_InvalidSender(t *testing.T) {
	h := setupTestHandlers()
	// Too short → fails ValidateAddress's hex64 shape check.
	w := getWalletNonce(t, h, "abc")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestGetWalletNonce_StorageError(t *testing.T) {
	h := setupTestHandlers()
	mock := h.storage.(*mockStorage)
	mock.getNonceErr = errors.New("simulated storage I/O failure")

	sender := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	w := getWalletNonce(t, h, sender)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 (fail-closed on storage error), got %d body=%s", w.Code, w.Body.String())
	}
}

// TestGetWalletNonce_E2EBump asserts that a successful
// /wallet/submit-signed bumps the nonce visible via
// /wallet/nonce. This is the integration that lets a client
// build envelope N+1 immediately after envelope N is accepted.
func TestGetWalletNonce_E2EBump(t *testing.T) {
	ws, err := wallet.NewWalletService()
	if err != nil {
		t.Skipf("wallet requires CGO / Dilithium: %v", err)
	}
	h := setupTestHandlersWithSubmesh(nil, ws)
	mock := h.storage.(*mockStorage)

	pubKey := ws.GetPublicKey()
	addrHash := sha256.Sum256(pubKey)
	sender := hex.EncodeToString(addrHash[:])
	mock.balances[sender] = 100.0

	// Step 1: pre-submit nonce should be 0/1.
	w1 := getWalletNonce(t, h, sender)
	if w1.Code != http.StatusOK {
		t.Fatalf("pre-submit GET nonce: want 200 got %d body=%s", w1.Code, w1.Body.String())
	}
	var pre GetWalletNonceResponse
	_ = json.Unmarshal(w1.Body.Bytes(), &pre)
	if pre.Nonce != 0 || pre.Next != 1 {
		t.Fatalf("pre-submit: want nonce=0 next=1 got nonce=%d next=%d", pre.Nonce, pre.Next)
	}

	// Step 2: submit a v0.4.1 envelope with Nonce=pre.Next.
	recipient := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	env := buildSignedEnvelopeWithNonce(t, ws, recipient, 1.0, 0.01, []string{
		strings.Repeat("a", 32), strings.Repeat("b", 32),
	}, pre.Next)
	w2 := postSubmitSigned(t, h, env)
	if w2.Code != http.StatusOK {
		t.Fatalf("submit-signed: want 200 got %d body=%s", w2.Code, w2.Body.String())
	}

	// Step 3: post-submit nonce should be bumped to pre.Next.
	w3 := getWalletNonce(t, h, sender)
	var post GetWalletNonceResponse
	_ = json.Unmarshal(w3.Body.Bytes(), &post)
	if post.Nonce != pre.Next {
		t.Fatalf("post-submit nonce: want %d got %d", pre.Next, post.Nonce)
	}
	if post.Next != pre.Next+1 {
		t.Fatalf("post-submit next: want %d got %d", pre.Next+1, post.Next)
	}
}

// walletSendDelta scans the WalletSendCounts() slice for the
// counter named `result` and returns (after - before). Avoids
// hard-coding indices and survives reordering of the result-tag
// table in pkg/monitoring/wallet_metrics.go. The return type is
// int64 (not uint64) so an "after < before" surprise reads as a
// negative diff rather than silently wrapping.
func walletSendDelta(before, after []struct {
	Result string
	Count  uint64
}, result string) int64 {
	var b, a uint64
	for _, c := range before {
		if c.Result == result {
			b = c.Count
		}
	}
	for _, c := range after {
		if c.Result == result {
			a = c.Count
		}
	}
	return int64(a) - int64(b)
}
