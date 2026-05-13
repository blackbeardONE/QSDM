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
	"github.com/blackbeardONE/QSDM/pkg/submesh"
	"github.com/blackbeardONE/QSDM/pkg/wallet"
)

// mockStorage is a simple mock storage for testing
type mockStorage struct {
	transactions map[string][]byte
	balances     map[string]float64
	readyErr     error
}

func newMockStorage() *mockStorage {
	return &mockStorage{
		transactions: make(map[string][]byte),
		balances:     make(map[string]float64),
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
	ctx := context.WithValue(req.Context(), "claims", &Claims{Address: ws.GetAddress(), Role: "user"})
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
	ctx := context.WithValue(req.Context(), "claims", &Claims{Address: ws.GetAddress(), Role: "user"})
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
