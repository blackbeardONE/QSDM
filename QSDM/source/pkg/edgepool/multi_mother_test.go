package edgepool

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestLocalMotherCredentialsAreUniqueAndRevocable(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Now().UTC().Truncate(time.Second)
	firstContext, firstToken, first, err := CreateMotherTenantCredential(
		stateDir,
		testMotherToken(),
		"Office Hive A",
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	secondContext, secondToken, second, err := CreateMotherTenantCredential(
		stateDir,
		testMotherToken(),
		"Office Hive B",
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if first.MotherID == second.MotherID || firstContext == secondContext || string(firstToken) == string(secondToken) {
		t.Fatal("separate Mother Hives received a shared identity or credential")
	}
	if authorized, err := authorizeMotherTenant(stateDir, firstContext); err != nil || authorized.MotherID != first.MotherID {
		t.Fatalf("first Mother Hive was not authorized: %+v, %v", authorized, err)
	}
	if err := RevokeMotherTenant(stateDir, first.MotherID, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := authorizeMotherTenant(stateDir, firstContext); err == nil || !strings.Contains(err.Error(), "revoked") {
		t.Fatalf("revoked Mother Hive remained authorized: %v", err)
	}
	if _, err := authorizeMotherTenant(stateDir, secondContext); err != nil {
		t.Fatalf("revoking one Mother Hive affected another: %v", err)
	}
}

func TestRotatingMotherKeyRevokesAllDerivedIdentities(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Now().UTC().Truncate(time.Second)
	firstContext, _, first, err := CreateMotherTenantCredential(stateDir, testMotherToken(), "Hive A", now)
	if err != nil {
		t.Fatal(err)
	}
	secondContext, _, second, err := CreateMotherTenantCredential(stateDir, testMotherToken(), "Hive B", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := RevokeAllMotherTenants(stateDir, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []struct {
		id      string
		context string
	}{{first.MotherID, firstContext}, {second.MotherID, secondContext}} {
		if _, err := authorizeMotherTenant(stateDir, candidate.context); err == nil || !strings.Contains(err.Error(), "revoked") {
			t.Fatalf("Mother Hive %s remained authorized after key rotation: %v", candidate.id, err)
		}
	}
}

func TestRelayAuthenticatesAndRevokesScopedMotherContext(t *testing.T) {
	stateDir := t.TempDir()
	relay, err := NewRelay(RelayConfig{
		ID: "relay-mother-auth", AgentToken: testToken(), MotherToken: testMotherToken(),
		StateDir: stateDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	contextValue, token, tenant, err := CreateMotherTenantCredential(
		stateDir,
		testMotherToken(),
		"Authenticated Hive",
		time.Now().UTC(),
	)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(relay.Handler())
	defer server.Close()

	response := signedScopedMotherRequest(t, server, http.MethodGet, "/v1/status", token, contextValue, nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("scoped Mother Hive status returned HTTP %d", response.StatusCode)
	}
	var status PoolStatus
	if err := json.NewDecoder(response.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	if status.MotherID != tenant.MotherID || status.MotherName != tenant.MotherName || len(status.MotherHives) != 0 {
		t.Fatalf("scoped status exposed the wrong Mother Hive data: %+v", status)
	}
	if err := RevokeMotherTenant(stateDir, tenant.MotherID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	denied := signedScopedMotherRequest(t, server, http.MethodGet, "/v1/status", token, contextValue, nil)
	defer denied.Body.Close()
	if denied.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked Mother Hive status returned HTTP %d", denied.StatusCode)
	}
}

func TestComputeJobsAreIsolatedByMotherHive(t *testing.T) {
	relay, err := NewRelay(RelayConfig{
		ID: "relay-multi-compute", AgentToken: testToken(), MotherToken: testMotherToken(),
		StateDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	firstID := "mother-aaaaaaaaaaaaaaaaaaaaaaaa"
	secondID := "mother-bbbbbbbbbbbbbbbbbbbbbbbb"
	request := ComputeJobSubmitRequest{
		Version: ComputeProtocolVersion, ClientRequestID: "shared-request-id",
		Resource: ResourceCPU, Units: 100,
	}
	first, err := relay.SubmitComputeJobForMother(firstID, request, now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := relay.SubmitComputeJobForMother(secondID, request, now)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == second.ID {
		t.Fatal("two Mother Hives were given the same compute job")
	}
	if jobs := relay.ListComputeJobsForMother(firstID, 10, now); len(jobs) != 1 || jobs[0].ID != first.ID {
		t.Fatalf("first Mother Hive saw the wrong queue: %+v", jobs)
	}
	if _, err := relay.ComputeJobForMother(firstID, second.ID, now); !errors.Is(err, errComputeJobNotFound) {
		t.Fatalf("first Mother Hive read another Hive's job: %v", err)
	}
	if _, err := relay.CancelComputeJobForMother(firstID, second.ID, now); !errors.Is(err, errComputeJobNotFound) {
		t.Fatalf("first Mother Hive cancelled another Hive's job: %v", err)
	}
}

func TestRevokedMotherJobsAreCancelledNowAndAfterRestart(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Now().UTC().Truncate(time.Second)
	_, _, tenant, err := CreateMotherTenantCredential(stateDir, testMotherToken(), "Revoked Hive", now)
	if err != nil {
		t.Fatal(err)
	}
	relay, err := NewRelay(RelayConfig{
		ID: "relay-revoke-jobs", AgentToken: testToken(), MotherToken: testMotherToken(), StateDir: stateDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := ComputeJobSubmitRequest{
		Version: ComputeProtocolVersion, ClientRequestID: "revoke-job-one",
		Resource: ResourceCPU, Units: 100,
	}
	first, err := relay.SubmitComputeJobForMother(tenant.MotherID, request, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := RevokeMotherTenant(stateDir, tenant.MotherID, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if count, err := relay.CancelComputeJobsForMother(tenant.MotherID, now.Add(time.Minute)); err != nil || count != 1 {
		t.Fatalf("revocation cancelled %d jobs: %v", count, err)
	}
	cancelled, err := relay.ComputeJobForMother(tenant.MotherID, first.ID, now.Add(time.Minute))
	if err != nil || cancelled.State != ComputeJobCancelled {
		t.Fatalf("revoked job was not cancelled: %+v, %v", cancelled, err)
	}

	request.ClientRequestID = "revoke-job-two"
	second, err := relay.SubmitComputeJobForMother(tenant.MotherID, request, now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := NewRelay(RelayConfig{
		ID: "relay-revoke-jobs", AgentToken: testToken(), MotherToken: testMotherToken(), StateDir: stateDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	afterRestart, err := restarted.ComputeJobForMother(tenant.MotherID, second.ID, now.Add(3*time.Minute))
	if err != nil || afterRestart.State != ComputeJobCancelled {
		t.Fatalf("Relay restart restored a revoked Hive job: %+v, %v", afterRestart, err)
	}
}

func TestSettlementReceiptsAreIsolatedByMotherHive(t *testing.T) {
	relay, err := NewRelay(RelayConfig{
		ID: "relay-multi-settlement", AgentToken: testToken(), MotherToken: testMotherToken(),
		StateDir: t.TempDir(), ProofWindow: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	firstID := "mother-aaaaaaaaaaaaaaaaaaaaaaaa"
	secondID := "mother-bbbbbbbbbbbbbbbbbbbbbbbb"
	firstReceipt := relay.makeReceiptForJob(Job{MotherID: firstID}, settlementTestResult("job-first", now))
	secondReceipt := relay.makeReceiptForJob(Job{MotherID: secondID}, settlementTestResult("job-second", now))
	firstReceipt.AcceptedAt = now.Format(time.RFC3339Nano)
	secondReceipt.AcceptedAt = now.Format(time.RFC3339Nano)
	tampered := firstReceipt
	tampered.MotherID = secondID
	if err := relay.validateStoredReceipt(tampered); err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("receipt Mother Hive attribution was not protected: %v", err)
	}
	relay.mu.Lock()
	relay.receipts = append(relay.receipts, firstReceipt, secondReceipt)
	relay.receiptsByJob[firstReceipt.JobID] = firstReceipt
	relay.receiptsByJob[secondReceipt.JobID] = secondReceipt
	relay.mu.Unlock()

	firstBinding := SettlementBindRequest{
		Version: SettlementProtocolVersion, ContributorWallet: strings.Repeat("a", 64),
		MotherHiveWallet: strings.Repeat("b", 64), EcosystemWallet: ProductionEcosystemWallet,
	}
	secondBinding := SettlementBindRequest{
		Version: SettlementProtocolVersion, ContributorWallet: strings.Repeat("c", 64),
		MotherHiveWallet: strings.Repeat("d", 64), EcosystemWallet: ProductionEcosystemWallet,
	}
	if _, err := relay.BindSettlementForMother(firstID, firstBinding, now); err != nil {
		t.Fatal(err)
	}
	if _, err := relay.BindSettlementForMother(secondID, secondBinding, now); err != nil {
		t.Fatal(err)
	}
	firstProof, err := relay.LatestSettlementProofForMother(firstID, ResourceCPU, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	secondProof, err := relay.LatestSettlementProofForMother(secondID, ResourceCPU, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(firstProof.ReceiptIDs) != 1 || firstProof.ReceiptIDs[0] != firstReceipt.ReceiptID || firstProof.MotherHiveWallet != firstBinding.MotherHiveWallet {
		t.Fatalf("first Mother Hive received another settlement: %+v", firstProof)
	}
	if len(secondProof.ReceiptIDs) != 1 || secondProof.ReceiptIDs[0] != secondReceipt.ReceiptID || secondProof.MotherHiveWallet != secondBinding.MotherHiveWallet {
		t.Fatalf("second Mother Hive received another settlement: %+v", secondProof)
	}
	if _, err := relay.AcknowledgeSettlementProofForMother(firstID, SettlementAckRequest{
		Version: SettlementProtocolVersion,
		ProofID: secondProof.ProofID,
	}, now.Add(2*time.Second)); !errors.Is(err, errSettlementProofNotFound) {
		t.Fatalf("one Mother Hive acknowledged another Hive's proof: %v", err)
	}
}

func settlementTestResult(jobID string, completed time.Time) JobResult {
	return JobResult{
		Version: ProtocolVersion, JobID: jobID, WorkerID: "agent-a",
		Resource: ResourceCPU, Algorithm: AlgorithmCPU,
		Digest: strings.Repeat("a", 64), Units: 100, DurationMS: 1,
		Completed: completed.Format(time.RFC3339Nano),
	}
}

func signedScopedMotherRequest(t *testing.T, server *httptest.Server, method, requestPath string, token []byte, contextValue string, body []byte) *http.Response {
	t.Helper()
	timestamp := strconv.FormatInt(time.Now().UTC().Unix(), 10)
	nonceBytes := make([]byte, 16)
	if _, err := rand.Read(nonceBytes); err != nil {
		t.Fatal(err)
	}
	nonce := hex.EncodeToString(nonceBytes)
	workerID := "mother-context-test"
	request, err := http.NewRequest(method, server.URL+requestPath, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set(HeaderWorkerID, workerID)
	request.Header.Set(HeaderTimestamp, timestamp)
	request.Header.Set(HeaderNonce, nonce)
	request.Header.Set(HeaderMotherContext, contextValue)
	request.Header.Set(HeaderSignature, RequestSignature(token, method, requestPath, timestamp, nonce, workerID, body))
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}
