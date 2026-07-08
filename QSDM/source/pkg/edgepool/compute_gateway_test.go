package edgepool

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func TestComputeGatewayQueuesCompletesAndPersistsApplicationJob(t *testing.T) {
	stateDir := t.TempDir()
	relay, err := NewRelay(RelayConfig{
		ID: "relay-compute", AgentToken: testToken(), MotherToken: testMotherToken(),
		StateDir: stateDir, CPUUnits: 1000, CPUPercent: 100, JobTTL: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	request := ComputeJobSubmitRequest{
		Version: ComputeProtocolVersion, ClientRequestID: "application-job-0001",
		Resource: ResourceCPU, Units: 250, DeadlineSeconds: 300,
	}
	queued, err := relay.SubmitComputeJob(request, now)
	if err != nil {
		t.Fatal(err)
	}
	if queued.State != ComputeJobQueued || queued.Algorithm != AlgorithmCPU || queued.Units != 250 {
		t.Fatalf("unexpected queued compute job: %+v", queued)
	}
	idempotent, err := relay.SubmitComputeJob(request, now.Add(time.Second))
	if err != nil || idempotent.ID != queued.ID {
		t.Fatalf("idempotent submission changed identity: %+v, %v", idempotent, err)
	}
	changedDeadline := request
	changedDeadline.DeadlineSeconds = 600
	if _, err := relay.SubmitComputeJob(changedDeadline, now.Add(2*time.Second)); !errors.Is(err, errComputeJobConflict) {
		t.Fatalf("changed deadline reused an idempotency key without conflict: %v", err)
	}

	server := httptest.NewServer(relay.Handler())
	agent, err := NewAgent(AgentConfig{
		RelayURL: server.URL, Token: testToken(), WorkerID: "compute-agent",
		Resources: []ResourceKind{ResourceCPU}, CPUUnits: 1000,
		HTTPClient: server.Client(), Logger: log.New(io.Discard, "", 0),
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
	if job.ID != queued.ID || job.Units != queued.Units {
		t.Fatalf("Agent leased the wrong application job: %+v", job)
	}
	leasing, err := relay.ComputeJob(queued.ID, time.Now().UTC())
	if err != nil || leasing.State != ComputeJobLeased || leasing.WorkerID != "compute-agent" {
		t.Fatalf("leased state was not exposed: %+v, %v", leasing, err)
	}
	result, err := ExecuteJob(ctx, job, "")
	if err != nil {
		t.Fatal(err)
	}
	var receipt Receipt
	if err := agent.doJSON(ctx, http.MethodPost, "/v1/jobs/complete", result, &receipt); err != nil {
		t.Fatal(err)
	}
	completed, err := relay.ComputeJob(queued.ID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if completed.State != ComputeJobCompleted || completed.ReceiptID != receipt.ReceiptID || completed.Result == nil || completed.Result.Digest != result.Digest {
		t.Fatalf("completed application result is incomplete: %+v", completed)
	}
	server.Close()

	restarted, err := NewRelay(RelayConfig{
		ID: "relay-compute", AgentToken: testToken(), MotherToken: testMotherToken(),
		StateDir: stateDir, CPUUnits: 1000, CPUPercent: 100, JobTTL: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	restored, err := restarted.ComputeJob(queued.ID, time.Now().UTC())
	if err != nil || restored.State != ComputeJobCompleted || restored.ReceiptID != receipt.ReceiptID {
		t.Fatalf("restart lost completed application work: %+v, %v", restored, err)
	}
	if status := restarted.Status().ComputeQueue; status.Completed != 1 || status.Queued != 0 || status.Leased != 0 {
		t.Fatalf("unexpected restored compute queue status: %+v", status)
	}
}

func TestComputeGatewayCannotCancelCompletionInProgress(t *testing.T) {
	relay, err := NewRelay(RelayConfig{
		ID: "relay-cancel-race", AgentToken: testToken(), MotherToken: testMotherToken(),
		StateDir: t.TempDir(), CPUUnits: 1000, CPUPercent: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	queued, err := relay.SubmitComputeJob(ComputeJobSubmitRequest{
		Version: ComputeProtocolVersion, ClientRequestID: "application-completing-job",
		Resource: ResourceCPU, Units: 100, DeadlineSeconds: 300,
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	relay.mu.Lock()
	relay.completing[queued.ID] = struct{}{}
	relay.mu.Unlock()

	if _, err := relay.CancelComputeJob(queued.ID, time.Now().UTC()); !errors.Is(err, errComputeJobCompleting) {
		t.Fatalf("completion-in-progress cancellation returned %v", err)
	}
	status, err := relay.ComputeJob(queued.ID, time.Now().UTC())
	if err != nil || status.State != ComputeJobQueued {
		t.Fatalf("failed cancellation changed the queued job: %+v, %v", status, err)
	}
}

func TestComputeGatewayHTTPRequiresMotherCredential(t *testing.T) {
	relay, err := NewRelay(RelayConfig{
		ID: "relay-auth", AgentToken: testToken(), MotherToken: testMotherToken(),
		StateDir: t.TempDir(), CPUUnits: 1000, CPUPercent: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(relay.Handler())
	defer server.Close()
	payload := ComputeJobSubmitRequest{
		Version: ComputeProtocolVersion, ClientRequestID: "application-job-auth",
		Resource: ResourceCPU, Units: 100, DeadlineSeconds: 300,
	}
	body, _ := json.Marshal(payload)

	agentResponse := signedComputeRequest(t, server, http.MethodPost, "/v1/compute/jobs", testToken(), body)
	defer agentResponse.Body.Close()
	if agentResponse.StatusCode != http.StatusUnauthorized {
		t.Fatalf("Agent credential submitted application work with HTTP %d", agentResponse.StatusCode)
	}

	motherResponse := signedComputeRequest(t, server, http.MethodPost, "/v1/compute/jobs", testMotherToken(), body)
	defer motherResponse.Body.Close()
	if motherResponse.StatusCode != http.StatusAccepted {
		raw, _ := io.ReadAll(motherResponse.Body)
		t.Fatalf("Mother credential submission returned HTTP %d: %s", motherResponse.StatusCode, raw)
	}
	var queued ComputeJobRecord
	if err := json.NewDecoder(motherResponse.Body).Decode(&queued); err != nil {
		t.Fatal(err)
	}
	if decoded, err := hex.DecodeString(queued.ID); err != nil || len(decoded) != 16 {
		t.Fatalf("Relay returned an invalid application job id %q", queued.ID)
	}

	statusResponse := signedComputeRequest(t, server, http.MethodGet, "/v1/compute/jobs/"+queued.ID, testMotherToken(), nil)
	defer statusResponse.Body.Close()
	if statusResponse.StatusCode != http.StatusOK {
		t.Fatalf("Mother credential could not read application job: HTTP %d", statusResponse.StatusCode)
	}

	cancelResponse := signedComputeRequest(t, server, http.MethodDelete, "/v1/compute/jobs/"+queued.ID, testMotherToken(), nil)
	defer cancelResponse.Body.Close()
	if cancelResponse.StatusCode != http.StatusOK {
		t.Fatalf("Mother credential could not cancel application job: HTTP %d", cancelResponse.StatusCode)
	}
	var cancelled ComputeJobRecord
	if err := json.NewDecoder(cancelResponse.Body).Decode(&cancelled); err != nil {
		t.Fatal(err)
	}
	if cancelled.State != ComputeJobCancelled {
		t.Fatalf("cancelled application job state = %q", cancelled.State)
	}
}

func TestComputeGatewayRejectsWorkAboveRelayPolicy(t *testing.T) {
	relay, err := NewRelay(RelayConfig{
		ID: "relay-budget", AgentToken: testToken(), MotherToken: testMotherToken(),
		StateDir: t.TempDir(), CPUUnits: 1000, CPUPercent: 25,
		RAMMiB: 100, RAMPercent: 40,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = relay.SubmitComputeJob(ComputeJobSubmitRequest{
		Version: ComputeProtocolVersion, ClientRequestID: "application-too-large",
		Resource: ResourceCPU, Units: 251,
	}, time.Now().UTC())
	if err == nil {
		t.Fatal("compute gateway accepted CPU work above the Relay policy")
	}
	_, err = relay.SubmitComputeJob(ComputeJobSubmitRequest{
		Version: ComputeProtocolVersion, ClientRequestID: "application-ram-large",
		Resource: ResourceRAM, MemoryMiB: 41,
	}, time.Now().UTC())
	if err == nil {
		t.Fatal("compute gateway accepted RAM work above the Relay policy")
	}
}

func signedComputeRequest(t *testing.T, server *httptest.Server, method, requestPath string, token, body []byte) *http.Response {
	t.Helper()
	timestamp := strconv.FormatInt(time.Now().UTC().Unix(), 10)
	nonceBytes := make([]byte, 16)
	if _, err := rand.Read(nonceBytes); err != nil {
		t.Fatal(err)
	}
	nonce := hex.EncodeToString(nonceBytes)
	workerID := "mother-compute-test"
	request, err := http.NewRequest(method, server.URL+requestPath, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set(HeaderWorkerID, workerID)
	request.Header.Set(HeaderTimestamp, timestamp)
	request.Header.Set(HeaderNonce, nonce)
	request.Header.Set(HeaderSignature, RequestSignature(token, method, requestPath, timestamp, nonce, workerID, body))
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}
