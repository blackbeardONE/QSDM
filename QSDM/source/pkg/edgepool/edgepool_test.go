package edgepool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testToken() []byte {
	return []byte("0123456789abcdef0123456789abcdef")
}

func testMotherToken() []byte {
	return []byte("abcdef0123456789abcdef0123456789")
}

func TestRequestSignatureBindsEveryRequestField(t *testing.T) {
	body := []byte(`{"hello":"world"}`)
	signature := RequestSignature(testToken(), http.MethodPost, "/v1/register", "123", "nonce-1234567890", "worker-a", body)
	if !VerifyRequestSignature(testToken(), signature, http.MethodPost, "/v1/register", "123", "nonce-1234567890", "worker-a", body) {
		t.Fatal("valid request signature did not verify")
	}
	if VerifyRequestSignature(testToken(), signature, http.MethodPost, "/v1/jobs/lease", "123", "nonce-1234567890", "worker-a", body) {
		t.Fatal("signature verified after request path was changed")
	}
	if VerifyRequestSignature(testToken(), signature, http.MethodPost, "/v1/register", "123", "nonce-1234567890", "worker-b", body) {
		t.Fatal("signature verified after worker identity was changed")
	}
}

func TestComputeAndVerifyResourceDigests(t *testing.T) {
	jobs := []Job{
		{
			Version: ProtocolVersion, ID: "cpu-job", WorkerID: "worker-a",
			Resource: ResourceCPU, Algorithm: AlgorithmCPU,
			Seed:  "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f",
			Units: 100,
		},
		{
			Version: ProtocolVersion, ID: "gpu-job", WorkerID: "worker-a",
			Resource: ResourceGPU, Algorithm: AlgorithmGPU,
			Seed:  "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f",
			Units: 100,
		},
		{
			Version: ProtocolVersion, ID: "ram-job", WorkerID: "worker-a",
			Resource: ResourceRAM, Algorithm: AlgorithmRAM,
			Seed:  "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f",
			Units: 1024 * 1024, MemoryMiB: 1,
		},
	}
	for _, job := range jobs {
		t.Run(string(job.Resource), func(t *testing.T) {
			digest, err := ComputeJobDigest(job)
			if err != nil {
				t.Fatal(err)
			}
			result := JobResult{
				Version: ProtocolVersion, JobID: job.ID, WorkerID: job.WorkerID,
				Resource: job.Resource, Algorithm: job.Algorithm, Digest: digest,
				Units: job.Units, MemoryMiB: job.MemoryMiB, DurationMS: 1,
				Completed: time.Now().UTC().Format(time.RFC3339Nano),
			}
			if err := VerifyJobResult(job, result); err != nil {
				t.Fatalf("valid result was rejected: %v", err)
			}
			result.Digest = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
			if err := VerifyJobResult(job, result); err == nil {
				t.Fatal("tampered result digest was accepted")
			}
		})
	}
}

func TestAgentRAMCapacityIsIndependentFromTheSingleJobLimit(t *testing.T) {
	const offeredMiB = 16 * 1024 * 40 / 100
	agent, err := NewAgent(AgentConfig{
		RelayURL:  "https://relay.example.test",
		Token:     testToken(),
		WorkerID:  "large-memory-worker",
		Resources: []ResourceKind{ResourceRAM},
		RAMMiB:    offeredMiB,
	})
	if err != nil {
		t.Fatal(err)
	}
	if agent.capabilities.RAMMiB != offeredMiB {
		t.Fatalf("advertised RAM = %d MiB, want %d MiB", agent.capabilities.RAMMiB, offeredMiB)
	}

	job := Job{
		Version: ProtocolVersion, ID: "oversized-ram-job", WorkerID: "large-memory-worker",
		Resource: ResourceRAM, Algorithm: AlgorithmRAM,
		Seed:  "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f",
		Units: (MaxRAMMiB + 1) * 1024 * 1024, MemoryMiB: MaxRAMMiB + 1,
	}
	if err := ValidateJob(job); err == nil {
		t.Fatal("single RAM job exceeded the safety ceiling")
	}
}

func TestCUDAHelperMatchesCoordinatorVerifier(t *testing.T) {
	helperPath := os.Getenv("QSDM_GPU_HELPER_TEST_PATH")
	if helperPath == "" {
		t.Skip("QSDM_GPU_HELPER_TEST_PATH is not configured")
	}
	job := Job{
		Version: ProtocolVersion, ID: "gpu-helper-job", WorkerID: "worker-gpu",
		Resource: ResourceGPU, Algorithm: AlgorithmGPU,
		Seed:  "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f",
		Units: 100_000,
	}
	result, err := ExecuteJob(context.Background(), job, helperPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyJobResult(job, result); err != nil {
		t.Fatalf("CUDA result did not match the coordinator verifier: %v", err)
	}
	if result.Metadata["gpu_name"] == "" || result.Metadata["gpu_helper_sha256"] == "" {
		t.Fatalf("CUDA result lacks device/helper identity: %+v", result.Metadata)
	}
}

func TestCoordinatorAgentCPUReceiptAndProof(t *testing.T) {
	coordinator, err := NewCoordinator(CoordinatorConfig{
		ID: "lab-a", Token: testToken(), StateDir: t.TempDir(),
		CPUUnits: 1000, RAMMiB: 1, JobTTL: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(coordinator.Handler())
	defer server.Close()

	agent, err := NewAgent(AgentConfig{
		CoordinatorURL: server.URL,
		Token:          testToken(),
		WorkerID:       "lab-pc-02",
		AgentVersion:   "test",
		Resources:      []ResourceKind{ResourceCPU},
		CPUUnits:       1000,
		HTTPClient:     server.Client(),
		Logger:         log.New(io.Discard, "", 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := agent.register(ctx); err != nil {
		t.Fatal(err)
	}
	job, err := agent.lease(ctx, ResourceCPU)
	if err != nil {
		t.Fatal(err)
	}
	result, err := ExecuteJob(ctx, job, "")
	if err != nil {
		t.Fatal(err)
	}
	var receipt Receipt
	if err := agent.doJSON(ctx, http.MethodPost, "/v1/jobs/complete", result, &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.WorkerID != "lab-pc-02" || receipt.Resource != ResourceCPU {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
	proof := coordinator.LatestProof(ResourceCPU, time.Now().UTC())
	if proof.JobCount != 1 || proof.WorkerCount != 1 || proof.TotalUnits != 1000 {
		t.Fatalf("unexpected aggregate proof: %+v", proof)
	}
	if proof.ReceiptRoot == "" || proof.ProofID == "" {
		t.Fatal("aggregate proof is missing its cryptographic identity")
	}
	if !VerifyPoolProof(testToken(), proof) {
		t.Fatal("coordinator aggregate proof signature did not verify")
	}
	proof.TotalUnits++
	if VerifyPoolProof(testToken(), proof) {
		t.Fatal("tampered aggregate proof signature verified")
	}
}

func TestRelaySeparatesAgentAndMotherHiveCredentials(t *testing.T) {
	relay, err := NewRelay(RelayConfig{
		ID: "relay-a", AgentToken: testToken(), MotherToken: testMotherToken(),
		StateDir: t.TempDir(), CPUUnits: 100, JobTTL: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(relay.Handler())
	defer server.Close()

	agent, err := NewAgent(AgentConfig{
		RelayURL: server.URL, Token: testToken(), WorkerID: "lab-agent",
		Resources: []ResourceKind{ResourceCPU}, CPUUnits: 100,
		HTTPClient: server.Client(), Logger: log.New(io.Discard, "", 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := agent.register(context.Background()); err != nil {
		t.Fatalf("agent credential could not register: %v", err)
	}
	if _, err := QueryStatus(context.Background(), server.URL, "agent-status", testToken()); !coordinatorHTTPStatus(err, http.StatusUnauthorized) {
		t.Fatalf("agent credential read Mother Hive status: %v", err)
	}
	status, err := QueryStatus(context.Background(), server.URL, "mother-status", testMotherToken())
	if err != nil {
		t.Fatalf("Mother Hive credential could not read relay status: %v", err)
	}
	if status.Role != "relay" || status.RelayID != "relay-a" || status.MotherSeenAt == "" {
		t.Fatalf("unexpected relay status: %+v", status)
	}

	impersonator, err := NewAgent(AgentConfig{
		RelayURL: server.URL, Token: testMotherToken(), WorkerID: "mother-as-agent",
		Resources: []ResourceKind{ResourceCPU}, HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := impersonator.register(context.Background()); !coordinatorHTTPStatus(err, http.StatusUnauthorized) {
		t.Fatalf("Mother Hive credential registered as an agent: %v", err)
	}

	proof := relay.LatestProof(ResourceCPU, time.Now().UTC())
	if !VerifyPoolProof(testMotherToken(), proof) {
		t.Fatal("relay proof was not signed for Mother Hive")
	}
	if VerifyPoolProof(testToken(), proof) {
		t.Fatal("agent credential verified a Mother Hive relay proof")
	}
}

func TestRelayPolicyCapsLeasedResources(t *testing.T) {
	relay, err := NewRelay(RelayConfig{
		ID: "relay-policy", Token: testToken(), StateDir: t.TempDir(),
		CPUUnits: 1000, CPUPercent: 25,
		RAMMiB: 100, RAMPercent: 40,
		GPUUnits: 2000, GPUPercent: 50,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(relay.Handler())
	defer server.Close()
	agent, err := NewAgent(AgentConfig{
		RelayURL: server.URL, Token: testToken(), WorkerID: "policy-worker",
		Resources: []ResourceKind{ResourceCPU, ResourceRAM},
		CPUUnits:  1000, RAMMiB: 100, HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := agent.register(ctx); err != nil {
		t.Fatal(err)
	}
	cpuJob, err := agent.lease(ctx, ResourceCPU)
	if err != nil {
		t.Fatal(err)
	}
	if cpuJob.Units != 250 {
		t.Fatalf("CPU relay cap = %d units, want 250", cpuJob.Units)
	}
	ramJob, err := agent.lease(ctx, ResourceRAM)
	if err != nil {
		t.Fatal(err)
	}
	if ramJob.MemoryMiB != 40 || ramJob.Units != 40*1024*1024 {
		t.Fatalf("RAM relay cap was not enforced: %+v", ramJob)
	}
	status := relay.Status()
	if status.Policy.CPUPercent != 25 || status.Policy.CPUUnits != 250 ||
		status.Policy.GPUPercent != 50 || status.Policy.GPUUnits != 1000 ||
		status.Policy.RAMPercent != 40 || status.Policy.RAMMiB != 40 {
		t.Fatalf("unexpected relay policy: %+v", status.Policy)
	}
}

func TestRelayRejectsInvalidResourcePercent(t *testing.T) {
	_, err := NewRelay(RelayConfig{
		ID: "relay-invalid", Token: testToken(), StateDir: t.TempDir(), CPUPercent: 101,
	})
	if err == nil || !strings.Contains(err.Error(), "CPU percent") {
		t.Fatalf("invalid relay percentage returned %v", err)
	}
}

func TestNewRelayUsesRelayIdentifierForFreshState(t *testing.T) {
	relay, err := NewRelay(RelayConfig{Token: testToken(), StateDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(relay.Status().RelayID, "relay-") {
		t.Fatalf("fresh Relay ID = %q", relay.Status().RelayID)
	}
}

func TestCoordinatorRejectsReplayedRequestNonce(t *testing.T) {
	coordinator, err := NewCoordinator(CoordinatorConfig{
		ID: "lab-a", Token: testToken(), StateDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(coordinator.Handler())
	defer server.Close()

	registration := RegisterRequest{
		Version: ProtocolVersion, WorkerID: "lab-pc-03", AgentVersion: "test",
		Capabilities: Capabilities{CPUThreads: 1, Resources: []ResourceKind{ResourceCPU}},
	}
	body, _ := json.Marshal(registration)
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	nonce := "0123456789abcdef0123456789abcdef"
	signature := RequestSignature(testToken(), http.MethodPost, "/v1/register", timestamp, nonce, "lab-pc-03", body)

	request := func() *http.Request {
		req, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/register", bytes.NewReader(body))
		req.Header.Set(HeaderWorkerID, "lab-pc-03")
		req.Header.Set(HeaderTimestamp, timestamp)
		req.Header.Set(HeaderNonce, nonce)
		req.Header.Set(HeaderSignature, signature)
		return req
	}
	response, err := server.Client().Do(request())
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("first request failed with %d", response.StatusCode)
	}
	replayed, err := server.Client().Do(request())
	if err != nil {
		t.Fatal(err)
	}
	replayed.Body.Close()
	if replayed.StatusCode != http.StatusConflict {
		t.Fatalf("replayed nonce returned %d, want %d", replayed.StatusCode, http.StatusConflict)
	}
}

func TestCoordinatorCompletionIsIdempotentAcrossRestart(t *testing.T) {
	stateDir := t.TempDir()
	coordinator, err := NewCoordinator(CoordinatorConfig{
		ID: "lab-a", Token: testToken(), StateDir: stateDir,
		CPUUnits: 100, JobTTL: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(coordinator.Handler())

	agent, err := NewAgent(AgentConfig{
		CoordinatorURL: server.URL,
		Token:          testToken(),
		WorkerID:       "lab-pc-idempotent",
		Resources:      []ResourceKind{ResourceCPU},
		CPUUnits:       100,
		HTTPClient:     server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := agent.register(ctx); err != nil {
		t.Fatal(err)
	}
	job, err := agent.lease(ctx, ResourceCPU)
	if err != nil {
		t.Fatal(err)
	}
	result, err := ExecuteJob(ctx, job, "")
	if err != nil {
		t.Fatal(err)
	}
	var first Receipt
	if err := agent.doJSON(ctx, http.MethodPost, "/v1/jobs/complete", result, &first); err != nil {
		t.Fatal(err)
	}
	var duplicate Receipt
	if err := agent.doJSON(ctx, http.MethodPost, "/v1/jobs/complete", result, &duplicate); err != nil {
		t.Fatalf("duplicate completion was not acknowledged: %v", err)
	}
	if duplicate.ReceiptID != first.ReceiptID {
		t.Fatalf("duplicate completion returned a different receipt: %s != %s", duplicate.ReceiptID, first.ReceiptID)
	}
	if proof := coordinator.LatestProof(ResourceCPU, time.Now().UTC()); proof.JobCount != 1 || proof.TotalUnits != 100 {
		t.Fatalf("duplicate completion inflated the proof: %+v", proof)
	}
	server.Close()

	restarted, err := NewCoordinator(CoordinatorConfig{
		ID: "lab-a", Token: testToken(), StateDir: stateDir,
		CPUUnits: 100, JobTTL: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	restartedServer := httptest.NewServer(restarted.Handler())
	defer restartedServer.Close()
	restartedAgent, err := NewAgent(AgentConfig{
		CoordinatorURL: restartedServer.URL,
		Token:          testToken(),
		WorkerID:       "lab-pc-idempotent",
		Resources:      []ResourceKind{ResourceCPU},
		HTTPClient:     restartedServer.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	var afterRestart Receipt
	if err := restartedAgent.doJSON(ctx, http.MethodPost, "/v1/jobs/complete", result, &afterRestart); err != nil {
		t.Fatalf("persisted completion was not acknowledged after restart: %v", err)
	}
	if afterRestart.ReceiptID != first.ReceiptID {
		t.Fatal("coordinator restart changed the persisted receipt identity")
	}
	if proof := restarted.LatestProof(ResourceCPU, time.Now().UTC()); proof.JobCount != 1 || proof.TotalUnits != 100 {
		t.Fatalf("reloaded proof was not exactly-once: %+v", proof)
	}
}

func TestCoordinatorRetainsLeaseWhenReceiptPersistenceFails(t *testing.T) {
	stateDir := t.TempDir()
	coordinator, err := NewCoordinator(CoordinatorConfig{
		ID: "lab-a", Token: testToken(), StateDir: stateDir,
		CPUUnits: 100, JobTTL: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(coordinator.Handler())
	defer server.Close()
	agent, err := NewAgent(AgentConfig{
		CoordinatorURL: server.URL,
		Token:          testToken(),
		WorkerID:       "lab-pc-persist",
		Resources:      []ResourceKind{ResourceCPU},
		CPUUnits:       100,
		HTTPClient:     server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := agent.register(ctx); err != nil {
		t.Fatal(err)
	}
	job, err := agent.lease(ctx, ResourceCPU)
	if err != nil {
		t.Fatal(err)
	}
	result, err := ExecuteJob(ctx, job, "")
	if err != nil {
		t.Fatal(err)
	}
	receiptPath := filepath.Join(stateDir, "receipts.jsonl")
	if err := os.Mkdir(receiptPath, 0o700); err != nil {
		t.Fatal(err)
	}
	var receipt Receipt
	err = agent.doJSON(ctx, http.MethodPost, "/v1/jobs/complete", result, &receipt)
	if !coordinatorHTTPStatus(err, http.StatusInternalServerError) {
		t.Fatalf("persistence failure returned %v, want HTTP 500", err)
	}
	status := coordinator.Status()
	if status.ActiveLeases != 1 || status.ReceiptCounts[ResourceCPU] != 0 {
		t.Fatalf("failed persistence committed in-memory state: %+v", status)
	}
	if err := os.Remove(receiptPath); err != nil {
		t.Fatal(err)
	}
	if err := agent.doJSON(ctx, http.MethodPost, "/v1/jobs/complete", result, &receipt); err != nil {
		t.Fatalf("completion did not recover after persistence was restored: %v", err)
	}
	status = coordinator.Status()
	if status.ActiveLeases != 0 || status.ReceiptCounts[ResourceCPU] != 1 {
		t.Fatalf("recovered completion did not commit exactly once: %+v", status)
	}
}

func TestCoordinatorRejectsTamperedReceiptJournal(t *testing.T) {
	stateDir := t.TempDir()
	coordinator, err := NewCoordinator(CoordinatorConfig{
		ID: "lab-a", Token: testToken(), StateDir: stateDir,
		CPUUnits: 100, JobTTL: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(coordinator.Handler())
	agent, err := NewAgent(AgentConfig{
		CoordinatorURL: server.URL,
		Token:          testToken(),
		WorkerID:       "lab-pc-journal",
		Resources:      []ResourceKind{ResourceCPU},
		CPUUnits:       100,
		HTTPClient:     server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := agent.register(ctx); err != nil {
		t.Fatal(err)
	}
	job, err := agent.lease(ctx, ResourceCPU)
	if err != nil {
		t.Fatal(err)
	}
	result, err := ExecuteJob(ctx, job, "")
	if err != nil {
		t.Fatal(err)
	}
	var receipt Receipt
	if err := agent.doJSON(ctx, http.MethodPost, "/v1/jobs/complete", result, &receipt); err != nil {
		t.Fatal(err)
	}
	server.Close()

	receipt.MemoryMiB = 1024
	raw, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "receipts.jsonl"), append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewCoordinator(CoordinatorConfig{ID: "lab-a", Token: testToken(), StateDir: stateDir}); err == nil {
		t.Fatal("coordinator accepted a tampered CPU receipt journal")
	}
}

func TestAgentReregistersAfterCoordinatorRestart(t *testing.T) {
	first, err := NewCoordinator(CoordinatorConfig{
		ID: "lab-a", Token: testToken(), StateDir: t.TempDir(),
		CPUUnits: 100, JobTTL: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewCoordinator(CoordinatorConfig{
		ID: "lab-a", Token: testToken(), StateDir: t.TempDir(),
		CPUUnits: 100, JobTTL: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	var active atomic.Value
	active.Store(first.Handler())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		active.Load().(http.Handler).ServeHTTP(w, r)
	}))
	defer server.Close()
	agent, err := NewAgent(AgentConfig{
		CoordinatorURL: server.URL,
		Token:          testToken(),
		WorkerID:       "lab-pc-restart",
		AgentVersion:   "test",
		Resources:      []ResourceKind{ResourceCPU},
		CPUUnits:       100,
		PollInterval:   10 * time.Millisecond,
		HTTPClient:     server.Client(),
		Logger:         log.New(io.Discard, "", 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- agent.Run(ctx) }()
	waitForCondition(t, 3*time.Second, func() bool {
		return first.Status().ReceiptCounts[ResourceCPU] > 0
	})
	active.Store(second.Handler())
	waitForCondition(t, 3*time.Second, func() bool {
		status := second.Status()
		return len(status.Workers) == 1 && status.ReceiptCounts[ResourceCPU] > 0
	})
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("agent returned an error after cancellation: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("agent did not stop after cancellation")
	}
}

func TestComputeJobDigestHonorsCancellation(t *testing.T) {
	job := Job{
		Version: ProtocolVersion, ID: "cancelled-job", WorkerID: "worker-a",
		Resource: ResourceCPU, Algorithm: AlgorithmCPU,
		Seed:  "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f",
		Units: MaxCPUUnits,
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ComputeJobDigestContext(ctx, job); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled computation returned %v", err)
	}
}

func waitForCondition(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition was not satisfied before timeout")
}
