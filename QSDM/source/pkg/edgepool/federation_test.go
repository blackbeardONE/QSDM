package edgepool

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func TestFederationCredentialIsDerivedScopedAndExpiring(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	master := bytes.Repeat([]byte{0x61}, 32)
	encoded, normalized, err := EncodeFederationContext(FederationContext{
		Version:      FederationContextVersion,
		RelayURL:     "https://relay.example.test",
		OfferID:      "offer-test-1",
		ProviderName: "Test Relay",
		ExpiresAt:    now.Add(time.Hour).Format(time.RFC3339),
		WorkloadIDs:  []string{WorkloadRAMMemoryScan, WorkloadCPUHashChain},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	derived, decoded, err := DeriveFederationToken(master, encoded, now)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(derived, master) {
		t.Fatal("derived federation token exposed the Mother Hive master token")
	}
	if decoded.OfferID != normalized.OfferID || !decoded.AllowsResource(ResourceCPU) || decoded.AllowsResource(ResourceGPU) {
		t.Fatalf("unexpected federation scope: %+v", decoded)
	}
	if _, _, err := DeriveFederationToken(master, encoded, now.Add(2*time.Hour)); err == nil {
		t.Fatal("expired federation context was accepted")
	}
}

func TestFederationContextRejectsOverlongInvitation(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	_, _, err := EncodeFederationContext(FederationContext{
		Version:      FederationContextVersion,
		RelayURL:     "https://relay.example.test",
		OfferID:      "offer-too-long",
		ProviderName: "Test Relay",
		ExpiresAt:    now.Add(MaximumFederationInvitationLifetime + time.Second).Format(time.RFC3339),
		WorkloadIDs:  []string{WorkloadCPUHashChain},
	}, now)
	if err == nil {
		t.Fatal("overlong federation invitation was accepted")
	}
}

func TestRelayAcceptsDerivedFederationCredentialAndEnforcesWorkloads(t *testing.T) {
	now := time.Now().UTC()
	motherToken := bytes.Repeat([]byte{0x41}, 32)
	relay, err := NewRelay(RelayConfig{
		ID: "relay-federation", AgentToken: testToken(), MotherToken: motherToken,
		StateDir: t.TempDir(), CPUUnits: 100, JobTTL: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(relay.Handler())
	defer server.Close()
	encoded, _, err := EncodeFederationContext(FederationContext{
		Version:      FederationContextVersion,
		RelayURL:     "https://relay.example.test",
		OfferID:      "offer-federation",
		ProviderName: "Test Relay",
		ExpiresAt:    now.Add(time.Hour).Format(time.RFC3339),
		WorkloadIDs:  []string{WorkloadCPUHashChain},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	derived, _, err := DeriveFederationToken(motherToken, encoded, now)
	if err != nil {
		t.Fatal(err)
	}

	statusRequest := signedFederationRequest(t, server, http.MethodGet, "/v1/status", derived, encoded, nil)
	if statusRequest.Code != http.StatusOK {
		t.Fatalf("federation status returned %d: %s", statusRequest.Code, statusRequest.Body.String())
	}

	gpuBody := []byte(`{"version":"qsdm-compute-gateway/v1","client_request_id":"federation-gpu-1","resource":"gpu","units":1000}`)
	gpuRequest := signedFederationRequest(t, server, http.MethodPost, "/v1/compute/jobs", derived, encoded, gpuBody)
	if gpuRequest.Code != http.StatusForbidden {
		t.Fatalf("disallowed federation workload returned %d: %s", gpuRequest.Code, gpuRequest.Body.String())
	}
}

func signedFederationRequest(t *testing.T, server *httptest.Server, method, requestPath string, token []byte, context string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, server.URL+requestPath, bytes.NewReader(body))
	timestamp := strconv.FormatInt(time.Now().UTC().Unix(), 10)
	nonceBytes := sha256.Sum256([]byte(method + "\n" + requestPath))
	nonce := hex.EncodeToString(nonceBytes[:16])
	workerID := "federated-hive"
	request.Header.Set(HeaderWorkerID, workerID)
	request.Header.Set(HeaderTimestamp, timestamp)
	request.Header.Set(HeaderNonce, nonce)
	request.Header.Set(HeaderFederationContext, context)
	request.Header.Set(HeaderSignature, RequestSignature(token, method, requestPath, timestamp, nonce, workerID, body))
	recorder := httptest.NewRecorder()
	server.Config.Handler.ServeHTTP(recorder, request)
	return recorder
}
