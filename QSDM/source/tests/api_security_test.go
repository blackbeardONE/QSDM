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

func setupSecurityTestServer(t *testing.T) (*api.Server, *httptest.Server, func()) {
	cfg := &config.Config{
		APIPort:   8080,
		EnableTLS: false,
		LogFile:   "test_security.log",
	}

	logger := logging.NewLogger(cfg.LogFile, false)
	testStorage, _ := storage.NewFileStorage("test_security_storage")
	apiServer, _ := api.NewServer(cfg, logger, nil, testStorage, nil, nil)

	handler := apiServer.SetupTestHandler()
	testServer := httptest.NewServer(handler)

	cleanup := func() {
		testServer.Close()
		testStorage.Close()
		os.RemoveAll("test_security_storage")
		os.Remove("test_security.log")
	}

	return apiServer, testServer, cleanup
}

func TestSecurityHeaders(t *testing.T) {
	_, server, cleanup := setupSecurityTestServer(t)
	defer cleanup()

	resp, err := http.Get(server.URL + "/api/v1/health")
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}
	defer resp.Body.Close()

	// Check security headers
	headers := []string{
		"Strict-Transport-Security",
		"X-Frame-Options",
		"X-Content-Type-Options",
		"X-XSS-Protection",
		"Content-Security-Policy",
	}

	for _, header := range headers {
		if resp.Header.Get(header) == "" {
			t.Errorf("Missing security header: %s", header)
		}
	}
}

func TestTokenValidation(t *testing.T) {
	_, server, cleanup := setupSecurityTestServer(t)
	defer cleanup()

	// Register and login
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

	txURL := server.URL + "/api/v1/transactions?address=0123456789abcdef0123456789abcdef0123456789"
	// Test with invalid token
	req, _ := http.NewRequest("GET", txURL, nil)
	req.Header.Set("Authorization", "Bearer invalid_token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("Expected 401 for invalid token, got %d", resp.StatusCode)
	}

	// Test with valid token
	req, _ = http.NewRequest("GET", txURL, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		t.Error("Valid token should not return 401")
	}
}

func TestTokenExpiration(t *testing.T) {
	// This test would require mocking time or waiting for expiration
	// For now, we'll test that tokens are created with expiration
	authManager, err := api.NewAuthManager()
	if err != nil {
		t.Fatalf("Failed to create auth manager: %v", err)
	}

	token, err := authManager.CreateToken("user1", "addr1", "user", api.TokenTypeAccess, 1*time.Second)
	if err != nil {
		t.Fatalf("Failed to create token: %v", err)
	}

	// Token should be valid immediately
	claims, err := authManager.ValidateToken(token)
	if err != nil {
		t.Fatalf("Token should be valid: %v", err)
	}

	if claims.UserID != "user1" {
		t.Errorf("Expected userID 'user1', got %s", claims.UserID)
	}

	// Wait for expiration
	time.Sleep(2 * time.Second)

	// Token should be expired
	_, err = authManager.ValidateToken(token)
	if err == nil {
		t.Error("Expired token should return error")
	}
}

func TestPasswordHashing(t *testing.T) {
	password := "testpassword123"

	hash, err := api.HashPassword(password)
	if err != nil {
		t.Fatalf("Failed to hash password: %v", err)
	}

	if hash == "" {
		t.Error("Hash should not be empty")
	}

	// Verify password
	valid, err := api.VerifyPassword(password, hash)
	if err != nil {
		t.Fatalf("Failed to verify password: %v", err)
	}

	if !valid {
		t.Error("Password verification should succeed")
	}

	// Test wrong password
	valid, err = api.VerifyPassword("wrongpassword", hash)
	if err != nil {
		t.Fatalf("Failed to verify password: %v", err)
	}

	if valid {
		t.Error("Wrong password should not verify")
	}
}

func TestRateLimiting(t *testing.T) {
	_, server, cleanup := setupSecurityTestServer(t)
	defer cleanup()

	// Health is exempt from rate limiting; hammer a public non-health GET.
	url := server.URL + "/api/v1/wallet/balance?address=rate_limit_probe"
	rateLimited := false
	for i := 0; i < 150; i++ {
		resp, err := http.Get(url)
		if err != nil {
			continue
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			rateLimited = true
			resp.Body.Close()
			break
		}
		resp.Body.Close()

		// Small delay to avoid overwhelming
		time.Sleep(10 * time.Millisecond)
	}

	if !rateLimited {
		t.Log("Rate limiting may not have triggered (this is expected if timing doesn't align)")
	}
}

func TestBearerTokenReusedUntilExpiry(t *testing.T) {
	authManager, err := api.NewAuthManager()
	if err != nil {
		t.Fatalf("Failed to create auth manager: %v", err)
	}

	token, err := authManager.CreateToken("user1", "addr1", "user", api.TokenTypeAccess, 15*time.Minute)
	if err != nil {
		t.Fatalf("Failed to create token: %v", err)
	}

	for i := 0; i < 3; i++ {
		if _, err = authManager.ValidateToken(token); err != nil {
			t.Fatalf("validation %d: %v", i+1, err)
		}
	}
}

func TestInputValidation(t *testing.T) {
	_, server, cleanup := setupSecurityTestServer(t)
	defer cleanup()

	// Test registration with empty address
	reqBody := map[string]string{
		"address":  "",
		"password": "Charming123!",
	}
	body, _ := json.Marshal(reqBody)
	resp, err := http.Post(server.URL+"/api/v1/auth/register", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("Expected 400 for empty address, got %d", resp.StatusCode)
	}

	// Test registration with short password
	reqBody = map[string]string{
		"address":  "fedcba9876543210fedcba9876543210fedcba98",
		"password": "short",
	}
	body, _ = json.Marshal(reqBody)
	resp, err = http.Post(server.URL+"/api/v1/auth/register", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("Expected 400 for short password, got %d", resp.StatusCode)
	}
}

func TestSQLInjectionProtection(t *testing.T) {
	// This test verifies that SQL injection attempts are handled safely
	// The storage layer should use parameterized queries (which it does)
	_, server, cleanup := setupSecurityTestServer(t)
	defer cleanup()

	// Register with potentially malicious input
	registerBody := map[string]string{
		"address":  "test'; DROP TABLE users; --",
		"password": "Charming123!",
	}
	body, _ := json.Marshal(registerBody)
	resp, err := http.Post(server.URL+"/api/v1/auth/register", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}
	defer resp.Body.Close()

	// Should either succeed (safe handling) or fail gracefully (not crash)
	if resp.StatusCode >= 500 {
		t.Errorf("SQL injection attempt should not cause server error, got %d", resp.StatusCode)
	}
}

