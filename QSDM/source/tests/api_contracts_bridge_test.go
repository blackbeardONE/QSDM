package tests

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/internal/logging"
	"github.com/blackbeardONE/QSDM/pkg/api"
	"github.com/blackbeardONE/QSDM/pkg/config"
	"github.com/blackbeardONE/QSDM/pkg/contracts"
	"github.com/blackbeardONE/QSDM/pkg/storage"
)

// cbTestRig bundles the per-test fixture so call sites can sign
// outbound requests with the same *api.RequestSigner the server
// uses to verify them. Without this, the test client and the
// server can disagree on backend choice — for example, the
// client always wanting HMAC-SHA256 while the server speaks
// ML-DSA-87 because crypto.NewDilithium() returned a real
// handle (CGO+liboqs build, or non-CGO with the
// dilithium_circl tag). Sharing the same RequestSigner makes
// the round-trip backend-agnostic.
type cbTestRig struct {
	ts        *httptest.Server
	apiServer *api.Server
	cleanup   func()
}

func setupContractsBridgeTestServer(t *testing.T) *cbTestRig {
	t.Helper()
	cfg := &config.Config{
		APIPort:   0,
		EnableTLS: false,
		LogFile:   "test_cb.log",
	}
	logger := logging.NewLogger(cfg.LogFile, false)
	testStorage, err := storage.NewFileStorage("test_cb_storage")
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	apiServer, err := api.NewServer(cfg, logger, nil, testStorage, nil, nil)
	if err != nil {
		t.Fatalf("api.NewServer: %v", err)
	}

	// Attach contract engine (no WASM runtime — uses simulation)
	ce := contracts.NewContractEngine(nil)
	apiServer.SetContractEngine(ce)

	// Bridge/AtomicSwap require Dilithium AND an explicit
	// SetBridgeProtocol wiring. We deliberately do NOT call
	// SetBridgeProtocol here so the bridge endpoints surface a
	// 503 ("bridge protocol not available") regardless of which
	// signature backend the build links — that's what
	// TestBridgeEndpoints503WhenUnavailable asserts.

	ts := httptest.NewServer(apiServer.SetupTestHandler())
	cleanup := func() {
		ts.Close()
		testStorage.Close()
		os.RemoveAll("test_cb_storage")
		os.Remove("test_cb.log")
	}
	return &cbTestRig{ts: ts, apiServer: apiServer, cleanup: cleanup}
}

// registerAndLogin registers a user and returns a valid access token.
func registerAndLogin(t *testing.T, baseURL string) string {
	t.Helper()
	addr := "aabbccdd11223344aabbccdd11223344"
	pass := "StrongPass123!@#"

	// Register
	regBody, _ := json.Marshal(map[string]string{"address": addr, "password": pass})
	resp, err := http.Post(baseURL+"/api/v1/auth/register", "application/json", bytes.NewReader(regBody))
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusConflict {
		t.Fatalf("register status = %d", resp.StatusCode)
	}

	// Login
	loginBody, _ := json.Marshal(map[string]string{"address": addr, "password": pass})
	resp, err = http.Post(baseURL+"/api/v1/auth/login", "application/json", bytes.NewReader(loginBody))
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d", resp.StatusCode)
	}
	var loginResp map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&loginResp)
	token, _ := loginResp["access_token"].(string)
	if token == "" {
		t.Fatal("no access_token in login response")
	}
	return token
}

// authedRequest builds and dispatches a token-authenticated
// request, signed with the SAME *api.RequestSigner the server
// will use to verify it. Sharing the signer makes the test
// round-trip backend-agnostic: the request middleware and the
// client agree on Dilithium-vs-HMAC because they consult the
// same handle.
//
// signer must be the rig's apiServer.RequestSigner(); passing a
// fresh RequestSigner would break the round-trip in any build
// where the backend generates a per-process keypair (every
// non-stub build).
func authedRequest(rig *cbTestRig, method, url, token string, body interface{}) (*http.Response, error) {
	var bodyBytes []byte
	if body != nil {
		bodyBytes, _ = json.Marshal(body)
	}
	req, err := http.NewRequest(method, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	// Add request signing headers for non-GET requests (required by RequestSigningMiddleware)
	if method != http.MethodGet {
		ts := time.Now().Unix()
		nonceBytes := make([]byte, 16)
		rand.Read(nonceBytes)
		nonce := hex.EncodeToString(nonceBytes)
		sig, signErr := rig.apiServer.RequestSigner().SignRequest(bodyBytes, ts, nonce)
		if signErr != nil {
			return nil, fmt.Errorf("authedRequest: signer.SignRequest: %w", signErr)
		}
		req.Header.Set("X-Timestamp", fmt.Sprintf("%d", ts))
		req.Header.Set("X-Nonce", nonce)
		req.Header.Set("X-Signature", sig)
	}
	return http.DefaultClient.Do(req)
}

func decodeJSON(t *testing.T, resp *http.Response) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&m)
	return m
}

// ---------- Contract API E2E ----------

func TestContractDeployAndExecuteE2E(t *testing.T) {
	rig := setupContractsBridgeTestServer(t)
	defer rig.cleanup()
	token := registerAndLogin(t, rig.ts.URL)

	// List templates (public)
	resp, _ := authedRequest(rig, "GET", rig.ts.URL+"/api/v1/contracts/templates", token, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("templates status = %d", resp.StatusCode)
	}
	tmplResp := decodeJSON(t, resp)
	count, _ := tmplResp["count"].(float64)
	if count < 3 {
		t.Fatalf("expected >=3 templates, got %v", count)
	}

	// Deploy SimpleToken from template
	resp, _ = authedRequest(rig, "POST", rig.ts.URL+"/api/v1/contracts/deploy", token, map[string]string{
		"contract_id": "my_token",
		"template":    "SimpleToken",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		body := decodeJSON(t, resp)
		t.Fatalf("deploy status = %d: %v", resp.StatusCode, body)
	}

	// Execute: transfer tokens
	resp, _ = authedRequest(rig, "POST", rig.ts.URL+"/api/v1/contracts/my_token/execute", token, map[string]interface{}{
		"function": "transfer",
		"args":     map[string]interface{}{"to": "bob", "amount": 42},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body := decodeJSON(t, resp)
		t.Fatalf("execute status = %d: %v", resp.StatusCode, body)
	}
	execResp := decodeJSON(t, resp)
	if execResp["success"] != true {
		t.Fatalf("execute success = %v, want true", execResp["success"])
	}

	// Execute: balanceOf
	resp, _ = authedRequest(rig, "POST", rig.ts.URL+"/api/v1/contracts/my_token/execute", token, map[string]interface{}{
		"function": "balanceOf",
		"args":     map[string]interface{}{"address": "bob"},
	})
	defer resp.Body.Close()
	execResp = decodeJSON(t, resp)
	output, ok := execResp["output"].(map[string]interface{})
	if !ok {
		t.Fatalf("output type = %T", execResp["output"])
	}
	if output["balance"] != float64(42) {
		t.Errorf("balance = %v, want 42", output["balance"])
	}

	// List contracts
	resp, _ = authedRequest(rig, "GET", rig.ts.URL+"/api/v1/contracts/list", token, nil)
	defer resp.Body.Close()
	listResp := decodeJSON(t, resp)
	if listResp["count"] != float64(1) {
		t.Errorf("count = %v, want 1", listResp["count"])
	}

	// Get contract
	resp, _ = authedRequest(rig, "GET", rig.ts.URL+"/api/v1/contracts/my_token", token, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get contract status = %d", resp.StatusCode)
	}
	getResp := decodeJSON(t, resp)
	if getResp["contract_id"] != "my_token" {
		t.Errorf("contract_id = %v", getResp["contract_id"])
	}
}

func TestContractDeployDuplicateE2E(t *testing.T) {
	rig := setupContractsBridgeTestServer(t)
	defer rig.cleanup()
	token := registerAndLogin(t, rig.ts.URL)

	deploy := func() int {
		resp, _ := authedRequest(rig, "POST", rig.ts.URL+"/api/v1/contracts/deploy", token, map[string]string{
			"contract_id": "dup_test",
			"template":    "Voting",
		})
		defer resp.Body.Close()
		return resp.StatusCode
	}

	if s := deploy(); s != http.StatusCreated {
		t.Fatalf("first deploy = %d, want 201", s)
	}
	if s := deploy(); s != http.StatusConflict {
		t.Fatalf("second deploy = %d, want 409", s)
	}
}

func TestContractVotingE2E(t *testing.T) {
	rig := setupContractsBridgeTestServer(t)
	defer rig.cleanup()
	token := registerAndLogin(t, rig.ts.URL)

	// Deploy Voting contract
	authedRequest(rig, "POST", rig.ts.URL+"/api/v1/contracts/deploy", token, map[string]string{
		"contract_id": "gov_vote",
		"template":    "Voting",
	})

	// Cast 3 yes votes, 1 no vote
	for i := 0; i < 3; i++ {
		authedRequest(rig, "POST", rig.ts.URL+"/api/v1/contracts/gov_vote/execute", token, map[string]interface{}{
			"function": "vote",
			"args":     map[string]interface{}{"proposal": "proposal_A", "choice": true},
		})
	}
	authedRequest(rig, "POST", rig.ts.URL+"/api/v1/contracts/gov_vote/execute", token, map[string]interface{}{
		"function": "vote",
		"args":     map[string]interface{}{"proposal": "proposal_A", "choice": false},
	})

	// Check results
	resp, _ := authedRequest(rig, "POST", rig.ts.URL+"/api/v1/contracts/gov_vote/execute", token, map[string]interface{}{
		"function": "getResults",
		"args":     map[string]interface{}{"proposal": "proposal_A"},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body := decodeJSON(t, resp)
		t.Fatalf("getResults status = %d: %v", resp.StatusCode, body)
	}
	execResp := decodeJSON(t, resp)
	output, ok := execResp["output"].(map[string]interface{})
	if !ok {
		t.Fatalf("output = %T (%v), want map", execResp["output"], execResp["output"])
	}
	if output["yes"] != float64(3) {
		t.Errorf("yes = %v, want 3", output["yes"])
	}
	if output["no"] != float64(1) {
		t.Errorf("no = %v, want 1", output["no"])
	}
}

func TestContractMissingReturns404(t *testing.T) {
	rig := setupContractsBridgeTestServer(t)
	defer rig.cleanup()
	token := registerAndLogin(t, rig.ts.URL)

	resp, _ := authedRequest(rig, "GET", rig.ts.URL+"/api/v1/contracts/nonexistent", token, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

// ---------- Bridge API E2E ----------

// TestBridgeEndpoints503WhenUnavailable verifies the bridge
// endpoints return 503 when no *bridge.BridgeProtocol has been
// wired into the api.Server. Pre-circl, the failure mode was
// "bridge construction errored because Dilithium was nil"; now
// any backend (CGO+liboqs, circl pure-Go, or stub) builds a
// non-nil signer, so the only remaining 503 trigger is the
// explicit no-SetBridgeProtocol case the test fixture
// constructs. Either way the user-facing contract holds: a
// validator that has not opted into bridge functionality
// returns 503 from every bridge endpoint.
func TestBridgeEndpoints503WhenUnavailable(t *testing.T) {
	rig := setupContractsBridgeTestServer(t)
	defer rig.cleanup()
	token := registerAndLogin(t, rig.ts.URL)

	endpoints := []struct {
		method string
		path   string
	}{
		{"POST", "/api/v1/bridge/lock"},
		{"GET", "/api/v1/bridge/locks"},
		{"POST", "/api/v1/bridge/swap"},
		{"GET", "/api/v1/bridge/swaps"},
	}

	for _, ep := range endpoints {
		t.Run(fmt.Sprintf("%s %s", ep.method, ep.path), func(t *testing.T) {
			resp, _ := authedRequest(rig, ep.method, rig.ts.URL+ep.path, token, map[string]interface{}{})
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusServiceUnavailable {
				t.Errorf("status = %d, want 503 (no SetBridgeProtocol → bridge protocol not available)", resp.StatusCode)
			}
		})
	}
}
