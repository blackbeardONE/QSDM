package edgepool

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pbnjay/memory"
)

type AgentConfig struct {
	RelayURL string
	// CoordinatorURL is the legacy name retained for existing configs.
	CoordinatorURL string
	Token          []byte
	WorkerID       string
	AgentVersion   string
	Resources      []ResourceKind
	RAMMiB         uint64
	CPUUnits       uint64
	GPUUnits       uint64
	GPUHelperPath  string
	PollInterval   time.Duration
	HTTPClient     *http.Client
	Logger         *log.Logger
}

type Agent struct {
	config       AgentConfig
	baseURL      *url.URL
	httpClient   *http.Client
	logger       *log.Logger
	capabilities Capabilities
	registerMu   sync.Mutex
}

// CoordinatorHTTPError preserves the response status so the agent can recover
// from coordinator lifecycle events without relying on error-message parsing.
type CoordinatorHTTPError struct {
	StatusCode int
	Message    string
}

func (e *CoordinatorHTTPError) Error() string {
	return fmt.Sprintf("coordinator returned HTTP %d: %s", e.StatusCode, e.Message)
}

func coordinatorHTTPStatus(err error, status int) bool {
	var responseErr *CoordinatorHTTPError
	return errors.As(err, &responseErr) && responseErr.StatusCode == status
}

func coordinatorHTTPStatusContains(err error, status int, text string) bool {
	var responseErr *CoordinatorHTTPError
	return errors.As(err, &responseErr) &&
		responseErr.StatusCode == status &&
		strings.Contains(strings.ToLower(responseErr.Message), strings.ToLower(text))
}

func NewAgent(config AgentConfig) (*Agent, error) {
	if len(config.Token) < 32 {
		return nil, errors.New("agent token must contain at least 32 bytes")
	}
	if err := ValidateWorkerID(config.WorkerID); err != nil {
		return nil, err
	}
	relayURL := strings.TrimSpace(config.RelayURL)
	if relayURL == "" {
		relayURL = strings.TrimSpace(config.CoordinatorURL)
	}
	baseURL, err := url.Parse(relayURL)
	if err != nil || (baseURL.Scheme != "http" && baseURL.Scheme != "https") || baseURL.Host == "" || baseURL.User != nil {
		return nil, errors.New("relay URL must be an absolute HTTP(S) URL without user information")
	}
	baseURL.Path = strings.TrimRight(baseURL.Path, "/")
	baseURL.RawQuery = ""
	baseURL.Fragment = ""
	if config.AgentVersion == "" {
		config.AgentVersion = "dev"
	}
	if len(config.Resources) == 0 {
		config.Resources = []ResourceKind{ResourceCPU, ResourceRAM}
	}
	config.Resources, err = normalizeResources(config.Resources)
	if err != nil {
		return nil, err
	}
	if config.RAMMiB == 0 {
		totalMiB := memory.TotalMemory() / 1024 / 1024
		config.RAMMiB = totalMiB / 8
		if config.RAMMiB > 256 {
			config.RAMMiB = 256
		}
		if config.RAMMiB < 32 {
			config.RAMMiB = 32
		}
	}
	if config.RAMMiB > MaxAgentRAMMiB {
		return nil, fmt.Errorf("agent RAM capacity exceeds %d MiB", MaxAgentRAMMiB)
	}
	if config.CPUUnits == 0 || config.CPUUnits > MaxCPUUnits {
		config.CPUUnits = 500_000
	}
	if config.GPUUnits == 0 || config.GPUUnits > MaxGPUUnits {
		config.GPUUnits = 10_000_000
	}
	if config.PollInterval <= 0 {
		config.PollInterval = 5 * time.Second
	}
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	if config.Logger == nil {
		config.Logger = log.New(io.Discard, "", log.LstdFlags|log.LUTC)
	}

	capabilities := Capabilities{
		CPUThreads: runtime.NumCPU(),
		RAMMiB:     config.RAMMiB,
		Resources:  append([]ResourceKind(nil), config.Resources...),
	}
	if containsResource(config.Resources, ResourceGPU) {
		if strings.TrimSpace(config.GPUHelperPath) == "" {
			return nil, errors.New("GPU resource requested but --gpu-helper is not configured")
		}
		gpus, detectErr := DetectNVIDIAGPUs(config.GPUHelperPath)
		if detectErr != nil {
			return nil, detectErr
		}
		capabilities.GPUs = gpus
	}
	if !containsResource(config.Resources, ResourceRAM) {
		capabilities.RAMMiB = 0
	}

	return &Agent{
		config:       config,
		baseURL:      baseURL,
		httpClient:   config.HTTPClient,
		logger:       config.Logger,
		capabilities: capabilities,
	}, nil
}

func (a *Agent) Run(ctx context.Context) error {
	registerBackoff := time.Second
	for {
		if err := a.register(ctx); err != nil {
			a.logger.Printf("registration failed: %v", err)
			if !sleepContext(ctx, registerBackoff) {
				return nil
			}
			if registerBackoff < 30*time.Second {
				registerBackoff *= 2
				if registerBackoff > 30*time.Second {
					registerBackoff = 30 * time.Second
				}
			}
			continue
		}
		break
	}
	a.logger.Printf("registered worker=%s relay=%s resources=%v", a.config.WorkerID, a.baseURL.String(), a.config.Resources)

	var wg sync.WaitGroup
	for _, resource := range a.config.Resources {
		resource := resource
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				err := a.runResourceLoop(ctx, resource)
				if ctx.Err() != nil || errors.Is(err, context.Canceled) {
					return
				}
				a.logger.Printf("resource=%s loop stopped unexpectedly and will restart: %v", resource, err)
				if !sleepContext(ctx, a.config.PollInterval) {
					return
				}
			}
		}()
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-ctx.Done():
		<-done
		return nil
	case <-done:
		return nil
	}
}

func (a *Agent) register(ctx context.Context) error {
	a.registerMu.Lock()
	defer a.registerMu.Unlock()

	hostname, _ := os.Hostname()
	request := RegisterRequest{
		Version:      ProtocolVersion,
		WorkerID:     a.config.WorkerID,
		Hostname:     hostname,
		AgentVersion: a.config.AgentVersion,
		Capabilities: a.capabilities,
	}
	var response RegisterResponse
	if err := a.doJSON(ctx, http.MethodPost, "/v1/register", request, &response); err != nil {
		return fmt.Errorf("register agent: %w", err)
	}
	if !response.OK || response.WorkerID != a.config.WorkerID {
		return errors.New("relay returned an invalid registration response")
	}
	return nil
}

func (a *Agent) runResourceLoop(ctx context.Context, resource ResourceKind) error {
	backoff := a.config.PollInterval
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		job, err := a.lease(ctx, resource)
		if err != nil {
			if coordinatorHTTPStatus(err, http.StatusPreconditionRequired) {
				a.logger.Printf("resource=%s relay forgot worker registration; registering again", resource)
				if registerErr := a.register(ctx); registerErr == nil {
					backoff = a.config.PollInterval
					continue
				} else {
					a.logger.Printf("resource=%s re-registration failed: %v", resource, registerErr)
				}
			}
			a.logger.Printf("resource=%s lease failed: %v", resource, err)
			if !sleepContext(ctx, backoff) {
				return ctx.Err()
			}
			if backoff < time.Minute {
				backoff *= 2
				if backoff > time.Minute {
					backoff = time.Minute
				}
			}
			continue
		}
		backoff = a.config.PollInterval
		expiresAt, err := time.Parse(time.RFC3339Nano, job.ExpiresAt)
		if err != nil {
			return fmt.Errorf("invalid job expiry: %w", err)
		}
		jobCtx, cancel := context.WithDeadline(ctx, expiresAt)
		result, executeErr := ExecuteJob(jobCtx, job, a.config.GPUHelperPath)
		cancel()
		if executeErr != nil {
			a.logger.Printf("resource=%s job=%s failed: %v", resource, job.ID, executeErr)
			if !sleepContext(ctx, a.config.PollInterval) {
				return ctx.Err()
			}
			continue
		}
		receipt, err := a.completeJob(ctx, job, result)
		if err != nil {
			a.logger.Printf("resource=%s job=%s completion failed: %v", resource, job.ID, err)
			if !sleepContext(ctx, a.config.PollInterval) {
				return ctx.Err()
			}
			continue
		}
		a.logger.Printf("resource=%s job=%s receipt=%s units=%d duration_ms=%d", resource, job.ID, receipt.ReceiptID, result.Units, result.DurationMS)
		if !sleepContext(ctx, a.config.PollInterval) {
			return ctx.Err()
		}
	}
}

func (a *Agent) completeJob(ctx context.Context, job Job, result JobResult) (Receipt, error) {
	expiresAt, err := time.Parse(time.RFC3339Nano, job.ExpiresAt)
	if err != nil {
		return Receipt{}, fmt.Errorf("invalid job expiry: %w", err)
	}
	backoff := a.config.PollInterval
	if backoff <= 0 {
		backoff = time.Second
	}
	for {
		var receipt Receipt
		err = a.doJSON(ctx, http.MethodPost, "/v1/jobs/complete", result, &receipt)
		if err == nil {
			return receipt, nil
		}
		if coordinatorHTTPStatus(err, http.StatusPreconditionRequired) {
			if registerErr := a.register(ctx); registerErr != nil {
				a.logger.Printf("job=%s re-registration before completion retry failed: %v", job.ID, registerErr)
			}
		}
		if coordinatorHTTPStatus(err, http.StatusGone) ||
			coordinatorHTTPStatus(err, http.StatusUnprocessableEntity) ||
			coordinatorHTTPStatusContains(err, http.StatusConflict, "missing or expired") ||
			(coordinatorHTTPStatus(err, http.StatusConflict) && time.Now().UTC().After(expiresAt)) {
			return Receipt{}, err
		}
		if time.Now().UTC().Add(backoff).After(expiresAt) {
			return Receipt{}, fmt.Errorf("job completion could not be acknowledged before lease expiry: %w", err)
		}
		a.logger.Printf("job=%s completion acknowledgement failed; retrying the same result: %v", job.ID, err)
		if !sleepContext(ctx, backoff) {
			return Receipt{}, ctx.Err()
		}
		if backoff < 30*time.Second {
			backoff *= 2
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
		}
	}
}

func (a *Agent) lease(ctx context.Context, resource ResourceKind) (Job, error) {
	request := LeaseRequest{
		Version:  ProtocolVersion,
		WorkerID: a.config.WorkerID,
		Resource: resource,
	}
	switch resource {
	case ResourceCPU:
		request.MaxUnits = a.config.CPUUnits
	case ResourceGPU:
		request.MaxUnits = a.config.GPUUnits
	case ResourceRAM:
		request.MaxMemoryMiB = a.config.RAMMiB
	}
	var job Job
	if err := a.doJSON(ctx, http.MethodPost, "/v1/jobs/lease", request, &job); err != nil {
		return Job{}, err
	}
	if job.WorkerID != a.config.WorkerID || job.Resource != resource || !VerifyJob(a.config.Token, job) {
		return Job{}, errors.New("relay job signature or lease identity did not verify")
	}
	if err := ValidateJob(job); err != nil {
		return Job{}, err
	}
	return job, nil
}

func (a *Agent) doJSON(ctx context.Context, method, path string, requestBody, responseBody any) error {
	body := []byte(nil)
	var err error
	if requestBody != nil {
		body, err = json.Marshal(requestBody)
		if err != nil {
			return err
		}
	}
	requestURL := *a.baseURL
	requestURL.Path = strings.TrimRight(a.baseURL.Path, "/") + path
	request, err := http.NewRequestWithContext(ctx, method, requestURL.String(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	timestamp := strconv.FormatInt(time.Now().UTC().Unix(), 10)
	nonceBytes := make([]byte, 16)
	if _, err := rand.Read(nonceBytes); err != nil {
		return err
	}
	nonce := hex.EncodeToString(nonceBytes)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(HeaderWorkerID, a.config.WorkerID)
	request.Header.Set(HeaderTimestamp, timestamp)
	request.Header.Set(HeaderNonce, nonce)
	request.Header.Set(HeaderSignature, RequestSignature(a.config.Token, method, requestURL.Path, timestamp, nonce, a.config.WorkerID, body))

	response, err := a.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	responseRaw, err := io.ReadAll(io.LimitReader(response.Body, maxCoordinatorRequestBody+1))
	if err != nil {
		return err
	}
	if len(responseRaw) > maxCoordinatorRequestBody {
		return errors.New("coordinator response exceeded the size limit")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var payload struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(responseRaw, &payload)
		if payload.Error == "" {
			payload.Error = strings.TrimSpace(string(responseRaw))
		}
		return &CoordinatorHTTPError{StatusCode: response.StatusCode, Message: payload.Error}
	}
	if responseBody == nil || len(responseRaw) == 0 {
		return nil
	}
	if err := json.Unmarshal(responseRaw, responseBody); err != nil {
		return fmt.Errorf("decode coordinator response: %w", err)
	}
	return nil
}

func DetectNVIDIAGPUs(helperPath string) ([]GPUCapability, error) {
	if info, err := os.Stat(helperPath); err != nil || info.IsDir() {
		return nil, fmt.Errorf("trusted GPU helper is unavailable at %q", helperPath)
	}
	helperHash, err := fileSHA256(helperPath)
	if err != nil {
		return nil, err
	}
	command := exec.Command("nvidia-smi", "--query-gpu=name,uuid,memory.total", "--format=csv,noheader,nounits")
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("nvidia-smi GPU detection failed: %w", err)
	}
	reader := csv.NewReader(strings.NewReader(string(output)))
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("parse nvidia-smi output: %w", err)
	}
	gpus := make([]GPUCapability, 0, len(records))
	for _, record := range records {
		if len(record) < 3 {
			continue
		}
		memoryMiB, _ := strconv.ParseUint(strings.TrimSpace(record[2]), 10, 64)
		gpus = append(gpus, GPUCapability{
			Name:      truncateText(record[0], 128),
			UUID:      truncateText(record[1], 128),
			MemoryMiB: memoryMiB,
			Helper:    helperHash,
		})
	}
	if len(gpus) == 0 {
		return nil, errors.New("nvidia-smi did not report a usable NVIDIA GPU")
	}
	return gpus, nil
}

func QueryStatus(ctx context.Context, relayURL, workerID string, token []byte) (PoolStatus, error) {
	agent, err := NewAgent(AgentConfig{
		RelayURL:  relayURL,
		Token:     token,
		WorkerID:  workerID,
		Resources: []ResourceKind{ResourceCPU},
	})
	if err != nil {
		return PoolStatus{}, err
	}
	var status PoolStatus
	if err := agent.doJSON(ctx, http.MethodGet, "/v1/status", nil, &status); err != nil {
		return PoolStatus{}, err
	}
	return status, nil
}

func normalizeResources(resources []ResourceKind) ([]ResourceKind, error) {
	seen := map[ResourceKind]bool{}
	normalized := make([]ResourceKind, 0, len(resources))
	for _, resource := range resources {
		resource = ResourceKind(strings.ToLower(strings.TrimSpace(string(resource))))
		if !resource.Valid() {
			return nil, fmt.Errorf("unsupported resource %q", resource)
		}
		if !seen[resource] {
			seen[resource] = true
			normalized = append(normalized, resource)
		}
	}
	return normalized, nil
}

func containsResource(resources []ResourceKind, target ResourceKind) bool {
	for _, resource := range resources {
		if resource == target {
			return true
		}
	}
	return false
}

func sleepContext(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func DefaultAgentLogPath() string {
	if configDir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(configDir, "QSDM", "edge-agent.log")
	}
	return "qsdm-edge-agent.log"
}
