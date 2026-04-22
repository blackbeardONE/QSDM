package api

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	return map[string]interface{}{
		"id":        txID,
		"sender":    "sender1",
		"recipient": "recipient1",
		"amount":    10.0,
	}, nil
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
		"architecture":     "NVIDIA-Locked QSDM+ test",
		"cuda_proof_hash":  cudaHash,
		"timestamp_utc":    tsUTC,
		"qsdmplus_node_id": nodeID,
		"gpu_fingerprint":  map[string]interface{}{"available": true, "devices": []interface{}{map[string]interface{}{"name": "G", "index": "0"}}},
	}
	if strings.TrimSpace(ingestNonce) != "" {
		m["qsdmplus_ingest_nonce"] = ingestNonce
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(monitoring.NGCProofHMACPayload(m)))
	m["qsdmplus_proof_hmac"] = hex.EncodeToString(mac.Sum(nil))
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

func TestNvidiaLockMintMainCoin_403WithoutProof(t *testing.T) {
	monitoring.ResetNGCProofsForTest()
	t.Cleanup(monitoring.ResetNGCProofsForTest)

	h := setupTestHandlersNvidiaLock()
	body := []byte(`{"recipient":"0123456789abcdef0123456789abcdef0123456789","amount":1}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/wallet/mint", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.MintMainCoin(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	msg, _ := resp["message"].(string)
	if !strings.Contains(msg, "NVIDIA lock") {
		t.Fatalf("expected NVIDIA lock detail in message, got %q", msg)
	}
}

func TestNvidiaLockMintMainCoin_OKWithGPUProof(t *testing.T) {
	monitoring.ResetNGCProofsForTest()
	t.Cleanup(monitoring.ResetNGCProofsForTest)

	proof := map[string]interface{}{
		"architecture":    "NVIDIA-Locked QSDM+ test",
		"cuda_proof_hash": "integration-deadbeef",
		"gpu_fingerprint": map[string]interface{}{
			"available": true,
			"devices": []interface{}{
				map[string]interface{}{"name": "Test GPU", "index": "0"},
			},
		},
	}
	raw, err := json.Marshal(proof)
	if err != nil {
		t.Fatal(err)
	}
	if err := monitoring.RecordNGCProofBundle(raw); err != nil {
		t.Fatal(err)
	}

	h := setupTestHandlersNvidiaLock()
	body := []byte(`{"recipient":"0123456789abcdef0123456789abcdef0123456789","amount":1}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/wallet/mint", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.MintMainCoin(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestNvidiaLockMintMainCoin_403WrongProofNodeID(t *testing.T) {
	monitoring.ResetNGCProofsForTest()
	t.Cleanup(monitoring.ResetNGCProofsForTest)

	proof := map[string]interface{}{
		"architecture":     "NVIDIA-Locked QSDM+ test",
		"cuda_proof_hash":  "h1",
		"qsdmplus_node_id": "other-node",
		"gpu_fingerprint":  map[string]interface{}{"available": true},
	}
	raw, _ := json.Marshal(proof)
	if err := monitoring.RecordNGCProofBundle(raw); err != nil {
		t.Fatal(err)
	}

	h := setupTestHandlersNvidiaLockNode("expected-node")
	body := []byte(`{"recipient":"0123456789abcdef0123456789abcdef0123456789","amount":1}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/wallet/mint", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.MintMainCoin(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestNvidiaLockMintMainCoin_OKWithBoundNodeID(t *testing.T) {
	monitoring.ResetNGCProofsForTest()
	t.Cleanup(monitoring.ResetNGCProofsForTest)

	proof := map[string]interface{}{
		"architecture":     "NVIDIA-Locked QSDM+ test",
		"cuda_proof_hash":  "h2",
		"qsdmplus_node_id": "prod-validator-1",
		"gpu_fingerprint":  map[string]interface{}{"available": true, "devices": []interface{}{map[string]interface{}{"name": "T", "index": "0"}}},
	}
	raw, _ := json.Marshal(proof)
	if err := monitoring.RecordNGCProofBundle(raw); err != nil {
		t.Fatal(err)
	}

	h := setupTestHandlersNvidiaLockNode("prod-validator-1")
	body := []byte(`{"recipient":"0123456789abcdef0123456789abcdef0123456789","amount":1}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/wallet/mint", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.MintMainCoin(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestNvidiaLockMintMainCoin_403WithoutHMACWhenRequired(t *testing.T) {
	monitoring.ResetNGCProofsForTest()
	t.Cleanup(monitoring.ResetNGCProofsForTest)

	proof := map[string]interface{}{
		"architecture":    "NVIDIA-Locked QSDM+ test",
		"cuda_proof_hash": "no-hmac",
		"timestamp_utc":   "2026-04-01T12:00:00+00:00",
		"gpu_fingerprint": map[string]interface{}{"available": true},
	}
	raw, _ := json.Marshal(proof)
	if err := monitoring.RecordNGCProofBundle(raw); err != nil {
		t.Fatal(err)
	}

	h := setupTestHandlersNvidiaLockHMAC("Charming123")
	body := []byte(`{"recipient":"0123456789abcdef0123456789abcdef0123456789","amount":1}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/wallet/mint", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.MintMainCoin(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestNvidiaLockMintMainCoin_OKWithHMAC(t *testing.T) {
	monitoring.ResetNGCProofsForTest()
	t.Cleanup(monitoring.ResetNGCProofsForTest)

	secret := "Charming123"
	raw := ngcProofBundleWithHMAC(secret, "cuda-with-mac", "2026-04-01T12:00:00+00:00", "", "")
	if err := monitoring.RecordNGCProofBundle(raw); err != nil {
		t.Fatal(err)
	}

	h := setupTestHandlersNvidiaLockHMAC(secret)
	body := []byte(`{"recipient":"0123456789abcdef0123456789abcdef0123456789","amount":1}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/wallet/mint", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.MintMainCoin(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestNvidiaLockMintMainCoin_SecondMintFailsAfterProofConsumed(t *testing.T) {
	monitoring.ResetNGCProofsForTest()
	monitoring.ResetNGCIngestNoncesForTest()
	t.Cleanup(func() {
		monitoring.ResetNGCProofsForTest()
		monitoring.ResetNGCIngestNoncesForTest()
	})

	secret := "Charming123"
	nonce, _, err := monitoring.IssueNGCIngestNonce(time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	raw := ngcProofBundleWithHMAC(secret, "cuda-n1", "2026-04-02T12:00:00+00:00", "", nonce)
	if err := monitoring.RecordNGCProofBundleForIngest(raw, true, secret); err != nil {
		t.Fatal(err)
	}

	h := setupTestHandlersNvidiaLockIngestNonce(secret, time.Hour)
	body := []byte(`{"recipient":"0123456789abcdef0123456789abcdef0123456789","amount":1}`)

	req1 := httptest.NewRequest(http.MethodPost, "/api/v1/wallet/mint", bytes.NewReader(body))
	req1.Header.Set("Content-Type", "application/json")
	w1 := httptest.NewRecorder()
	h.MintMainCoin(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("first mint: expected 200, got %d %s", w1.Code, w1.Body.String())
	}

	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/wallet/mint", bytes.NewReader(body))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	h.MintMainCoin(w2, req2)
	if w2.Code != http.StatusForbidden {
		t.Fatalf("second mint: expected 403, got %d %s", w2.Code, w2.Body.String())
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
	ns, _ := resp["qsdmplus_ingest_nonce"].(string)
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

func TestSubmeshMintMainCoin_422OversizedPayload(t *testing.T) {
	dm := submesh.NewDynamicSubmeshManager()
	dm.AddOrUpdateSubmesh(&submesh.DynamicSubmesh{
		Name: "tight", FeeThreshold: 0, PriorityLevel: 1, GeoTags: []string{"US"},
		MaxPayloadBytes: 80,
	})
	h := setupTestHandlersWithSubmesh(dm, nil)
	body := []byte(`{"recipient":"0123456789abcdef0123456789abcdef0123456789","amount":1}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/wallet/mint", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.MintMainCoin(w, req)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d body=%s", w.Code, w.Body.String())
	}
}

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
	t.Setenv("QSDMPLUS_PUBLISH_MESH_COMPANION", "1")
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
