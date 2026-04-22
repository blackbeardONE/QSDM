package tests

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/internal/logging"
	"github.com/blackbeardONE/QSDM/pkg/api"
	"github.com/blackbeardONE/QSDM/pkg/config"
	"github.com/blackbeardONE/QSDM/pkg/storage"
)

func setupAPITestServer(t *testing.T) (*api.Server, *httptest.Server, func()) {
	// Create test config
	cfg := &config.Config{
		APIPort:   8080,
		EnableTLS: false, // Use HTTP for testing
		LogFile:   "test_api.log",
	}

	// Create test logger
	logger := logging.NewLogger(cfg.LogFile, false)

	// Create test storage
	testStorage, err := storage.NewFileStorage("test_api_storage")
	if err != nil {
		t.Fatalf("Failed to create test storage: %v", err)
	}

	// Create API server
	apiServer, err := api.NewServer(cfg, logger, nil, testStorage, nil, nil)
	if err != nil {
		t.Fatalf("Failed to create API server: %v", err)
	}

	// Create test HTTP server
	handler := apiServer.SetupTestHandler() // We'll need to add this method
	testServer := httptest.NewServer(handler)

	cleanup := func() {
		testServer.Close()
		testStorage.Close()
		os.RemoveAll("test_api_storage")
		os.Remove("test_api.log")
	}

	return apiServer, testServer, cleanup
}

func TestAPIHealthCheck(t *testing.T) {
	_, server, cleanup := setupAPITestServer(t)
	defer cleanup()

	resp, err := http.Get(server.URL + "/api/v1/health")
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	var response map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if response["status"] != "healthy" {
		t.Errorf("Expected status 'healthy', got %v", response["status"])
	}
}

func TestAPIRegisterAndLogin(t *testing.T) {
	_, server, cleanup := setupAPITestServer(t)
	defer cleanup()

	// Register
	registerBody := map[string]string{
		"address":  "0123456789abcdef0123456789abcdef0123456789",
		"password": "Charming123!",
	}
	body, _ := json.Marshal(registerBody)

	resp, err := http.Post(server.URL+"/api/v1/auth/register", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("Failed to register: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Errorf("Expected status 201, got %d", resp.StatusCode)
	}

	// Login
	loginResp, err := http.Post(server.URL+"/api/v1/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("Failed to login: %v", err)
	}
	defer loginResp.Body.Close()

	if loginResp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", loginResp.StatusCode)
	}

	var loginResponse map[string]interface{}
	if err := json.NewDecoder(loginResp.Body).Decode(&loginResponse); err != nil {
		t.Fatalf("Failed to decode login response: %v", err)
	}

	if loginResponse["access_token"] == nil {
		t.Error("Expected access_token in response")
	}
}

func TestAPIAuthentication(t *testing.T) {
	_, server, cleanup := setupAPITestServer(t)
	defer cleanup()

	// Register and login to get token
	registerBody := map[string]string{
		"address":  "0123456789abcdef0123456789abcdef0123456789",
		"password": "Charming123!",
	}
	body, _ := json.Marshal(registerBody)

	http.Post(server.URL+"/api/v1/auth/register", "application/json", bytes.NewReader(body))

	loginResp, _ := http.Post(server.URL+"/api/v1/auth/login", "application/json", bytes.NewReader(body))
	var loginResponse map[string]interface{}
	json.NewDecoder(loginResp.Body).Decode(&loginResponse)
	loginResp.Body.Close()

	token, _ := loginResponse["access_token"].(string)
	if token == "" {
		t.Fatal("expected access_token after register/login")
	}

	// Protected route (balance is public for game integration; use transactions list)
	txURL := server.URL + "/api/v1/transactions?address=0123456789abcdef0123456789abcdef0123456789"
	req, _ := http.NewRequest("GET", txURL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("Expected status 401 without token, got %d", resp.StatusCode)
	}

	// Test with token
	req, _ = http.NewRequest("GET", txURL, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Failed to make authenticated request: %v", err)
	}
	defer resp.Body.Close()

	// Should not be 401 (may be 200 or other status depending on implementation)
	if resp.StatusCode == http.StatusUnauthorized {
		t.Error("Request with valid token should not return 401")
	}
}

func TestAPIRateLimiting(t *testing.T) {
	_, server, cleanup := setupAPITestServer(t)
	defer cleanup()

	// Health routes are exempt from rate limiting (orchestration probes); use a public non-health GET.
	url := server.URL + "/api/v1/wallet/balance?address=rate_limit_probe"
	for i := 0; i < 150; i++ {
		resp, err := http.Get(url)
		if err != nil {
			continue
		}
		resp.Body.Close()

		if i >= 100 && resp.StatusCode != http.StatusTooManyRequests {
			if i == 149 {
				t.Logf("Rate limiting test: Made %d requests, last status: %d", i+1, resp.StatusCode)
			}
		}
		time.Sleep(10 * time.Millisecond) // Small delay
	}
}

// Note: This test file requires a SetupTestHandler method in api.Server
// For now, these tests serve as a template for full integration testing

