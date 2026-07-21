package edgepool

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cloudflare/circl/sign/mldsa/mldsa87"
)

const maxCoordinatorRequestBody = 64 * 1024

type CoordinatorConfig struct {
	ID            string
	ListenAddress string
	// Token is the legacy shared credential. New deployments should use
	// AgentToken and MotherToken so worker machines cannot impersonate Mother
	// Hive when reading aggregate proofs.
	Token            []byte
	AgentToken       []byte
	MotherToken      []byte
	StateDir         string
	JobTTL           time.Duration
	ProofWindow      time.Duration
	MaxClockSkew     time.Duration
	CPUUnits         uint64
	GPUUnits         uint64
	RAMMiB           uint64
	CPUPercent       int
	GPUPercent       int
	RAMPercent       int
	MaxWorkers       int
	MaxProofReceipts int
	MaxVerifications int
}

type Coordinator struct {
	config    CoordinatorConfig
	startedAt time.Time

	mu               sync.Mutex
	workers          map[string]WorkerStatus
	jobs             map[string]Job
	receipts         []Receipt
	receiptsByJob    map[string]Receipt
	completing       map[string]struct{}
	computeJobs      map[string]*ComputeJobRecord
	computeByRequest map[string]string
	computeOrder     []string
	nonces           map[string]time.Time
	receiptPersistMu sync.Mutex
	computePersistMu sync.Mutex
	settlePersistMu  sync.Mutex
	verifySlots      chan struct{}
	motherSeenAt     map[string]time.Time

	settlementSigner    *mldsa87.PrivateKey
	settlementPublicKey string
	settlementRelayID   string
	settlement          settlementState
}

type motherAuthentication struct {
	Token      []byte
	Federation *FederationContext
	Mother     *MotherContext
	MotherID   string
	MotherName string
}

// Relay and RelayConfig are the preferred names for new deployments. The
// coordinator names remain aliases so existing laboratories can upgrade
// without changing state files or automation in one step.
type Relay = Coordinator
type RelayConfig = CoordinatorConfig

func NewRelay(config RelayConfig) (*Relay, error) {
	if config.StateDir == "" {
		config.StateDir = ".qsdm-edge-pool"
	}
	if config.ID == "" {
		config.ID = persistedCoordinatorID(config.StateDir)
		if config.ID == "" {
			config.ID = defaultPoolID("relay")
		}
	}
	return NewCoordinator(config)
}

func NewCoordinator(config CoordinatorConfig) (*Coordinator, error) {
	if len(config.AgentToken) == 0 {
		config.AgentToken = config.Token
	}
	if len(config.MotherToken) == 0 {
		config.MotherToken = config.Token
		if len(config.MotherToken) == 0 {
			config.MotherToken = config.AgentToken
		}
	}
	if len(config.AgentToken) < 32 {
		return nil, errors.New("relay agent token must contain at least 32 bytes")
	}
	if len(config.MotherToken) < 32 {
		return nil, errors.New("relay Mother Hive token must contain at least 32 bytes")
	}
	if config.ID == "" {
		config.ID = defaultPoolID("coordinator")
	}
	if err := ValidateWorkerID(config.ID); err != nil {
		return nil, fmt.Errorf("invalid coordinator id: %w", err)
	}
	if config.ListenAddress == "" {
		config.ListenAddress = "127.0.0.1:7740"
	}
	if config.StateDir == "" {
		config.StateDir = ".qsdm-edge-pool"
	}
	if config.JobTTL <= 0 {
		config.JobTTL = 2 * time.Minute
	}
	if config.ProofWindow <= 0 {
		config.ProofWindow = time.Hour
	}
	if config.MaxClockSkew <= 0 {
		config.MaxClockSkew = 2 * time.Minute
	}
	if config.CPUUnits == 0 || config.CPUUnits > MaxCPUUnits {
		config.CPUUnits = 250_000
	}
	if config.GPUUnits == 0 || config.GPUUnits > MaxGPUUnits {
		config.GPUUnits = 5_000_000
	}
	if config.RAMMiB == 0 || config.RAMMiB > MaxRAMMiB {
		config.RAMMiB = 64
	}
	var err error
	if config.CPUPercent, err = normalizeRelayPercent("CPU", config.CPUPercent); err != nil {
		return nil, err
	}
	if config.GPUPercent, err = normalizeRelayPercent("GPU", config.GPUPercent); err != nil {
		return nil, err
	}
	if config.RAMPercent, err = normalizeRelayPercent("RAM", config.RAMPercent); err != nil {
		return nil, err
	}
	if config.MaxWorkers <= 0 {
		config.MaxWorkers = 256
	}
	if config.MaxProofReceipts <= 0 {
		config.MaxProofReceipts = 512
	}
	if config.MaxVerifications <= 0 {
		config.MaxVerifications = 2
	}
	if config.MaxVerifications > 16 {
		return nil, errors.New("maximum concurrent verifications cannot exceed 16")
	}
	if err := os.MkdirAll(config.StateDir, 0o700); err != nil {
		return nil, fmt.Errorf("create coordinator state directory: %w", err)
	}
	settlementSigner, settlementPublicKey, err := loadOrCreateRelaySigningKey(config.StateDir)
	if err != nil {
		return nil, err
	}
	settlementRelayID, err := SettlementRelayID(settlementPublicKey)
	if err != nil {
		return nil, fmt.Errorf("derive Relay settlement identity: %w", err)
	}
	settlement, err := loadSettlementState(config.StateDir, settlementPublicKey)
	if err != nil {
		return nil, err
	}

	coordinator := &Coordinator{
		config:              config,
		startedAt:           time.Now().UTC(),
		workers:             map[string]WorkerStatus{},
		jobs:                map[string]Job{},
		receiptsByJob:       map[string]Receipt{},
		completing:          map[string]struct{}{},
		computeJobs:         map[string]*ComputeJobRecord{},
		computeByRequest:    map[string]string{},
		nonces:              map[string]time.Time{},
		motherSeenAt:        map[string]time.Time{},
		verifySlots:         make(chan struct{}, config.MaxVerifications),
		settlementSigner:    settlementSigner,
		settlementPublicKey: settlementPublicKey,
		settlementRelayID:   settlementRelayID,
		settlement:          settlement,
	}
	if err := coordinator.loadReceipts(); err != nil {
		return nil, err
	}
	if err := coordinator.loadComputeJobs(); err != nil {
		return nil, err
	}
	return coordinator, nil
}

func defaultPoolID(prefix string) string {
	hostname, _ := os.Hostname()
	digest := sha256.Sum256([]byte(hostname))
	return prefix + "-" + hex.EncodeToString(digest[:6])
}

func persistedCoordinatorID(stateDir string) string {
	file, err := os.Open(filepath.Join(stateDir, "receipts.jsonl"))
	if err != nil {
		return ""
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var receipt Receipt
		if json.Unmarshal(scanner.Bytes(), &receipt) == nil && ValidateWorkerID(receipt.CoordinatorID) == nil {
			return receipt.CoordinatorID
		}
	}
	return ""
}

func (c *Coordinator) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", c.handleHealth)
	mux.HandleFunc("/v1/register", c.handleRegister)
	mux.HandleFunc("/v1/jobs/lease", c.handleLease)
	mux.HandleFunc("/v1/jobs/complete", c.handleComplete)
	mux.HandleFunc("/v1/compute/jobs", c.handleComputeJobs)
	mux.HandleFunc("/v1/compute/jobs/", c.handleComputeJob)
	mux.HandleFunc("/v1/status", c.handleStatus)
	mux.HandleFunc("/v1/settlement/bind", c.handleSettlementBind)
	mux.HandleFunc("/v1/proofs/ack", c.handleSettlementAck)
	mux.HandleFunc("/v1/proofs/latest", c.handleLatestProof)
	return mux
}

func (c *Coordinator) Serve(ctx context.Context) error {
	listener, err := net.Listen("tcp", c.config.ListenAddress)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", c.config.ListenAddress, err)
	}
	server := &http.Server{
		Handler:           c.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 * 1024,
	}
	done := make(chan error, 1)
	go func() {
		done <- server.Serve(listener)
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		err := <-done
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func (c *Coordinator) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writePoolError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writePoolJSON(w, http.StatusOK, map[string]any{
		"ok":             true,
		"version":        ProtocolVersion,
		"coordinator_id": c.config.ID,
		"relay_id":       c.config.ID,
		"role":           "relay",
	})
}

func (c *Coordinator) handleRegister(w http.ResponseWriter, r *http.Request) {
	body, workerID, ok := c.authenticate(w, r, c.config.AgentToken, "agent")
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		writePoolError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var request RegisterRequest
	if err := decodePoolJSON(body, &request); err != nil {
		writePoolError(w, http.StatusBadRequest, err.Error())
		return
	}
	if request.Version != ProtocolVersion || request.WorkerID != workerID {
		writePoolError(w, http.StatusBadRequest, "registration protocol or worker mismatch")
		return
	}
	if err := validateCapabilities(request.Capabilities); err != nil {
		writePoolError(w, http.StatusBadRequest, err.Error())
		return
	}
	now := time.Now().UTC()
	c.mu.Lock()
	status, exists := c.workers[workerID]
	if !exists && len(c.workers) >= c.config.MaxWorkers {
		c.mu.Unlock()
		writePoolError(w, http.StatusTooManyRequests, "coordinator worker limit reached")
		return
	}
	if status.RegisteredAt == "" {
		status.RegisteredAt = now.Format(time.RFC3339Nano)
	}
	status.WorkerID = workerID
	status.Hostname = truncateText(request.Hostname, 128)
	status.AgentVersion = truncateText(request.AgentVersion, 64)
	status.Capabilities = request.Capabilities
	status.LastSeenAt = now.Format(time.RFC3339Nano)
	c.workers[workerID] = status
	c.mu.Unlock()

	writePoolJSON(w, http.StatusOK, RegisterResponse{
		OK:          true,
		Coordinator: c.config.ID,
		Relay:       c.config.ID,
		WorkerID:    workerID,
		Registered:  status.RegisteredAt,
	})
}

func (c *Coordinator) handleLease(w http.ResponseWriter, r *http.Request) {
	body, workerID, ok := c.authenticate(w, r, c.config.AgentToken, "agent")
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		writePoolError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var request LeaseRequest
	if err := decodePoolJSON(body, &request); err != nil {
		writePoolError(w, http.StatusBadRequest, err.Error())
		return
	}
	if request.Version != ProtocolVersion || request.WorkerID != workerID || !request.Resource.Valid() {
		writePoolError(w, http.StatusBadRequest, "lease protocol, worker, or resource is invalid")
		return
	}

	now := time.Now().UTC()
	c.mu.Lock()
	c.pruneLocked(now)
	worker, exists := c.workers[workerID]
	if !exists {
		c.mu.Unlock()
		writePoolError(w, http.StatusPreconditionRequired, "worker must register before leasing jobs")
		return
	}
	if !worker.Capabilities.Supports(request.Resource) {
		c.mu.Unlock()
		writePoolError(w, http.StatusConflict, "worker did not register the requested resource")
		return
	}
	for _, active := range c.jobs {
		if active.WorkerID == workerID && active.Resource == request.Resource {
			worker.LastSeenAt = now.Format(time.RFC3339Nano)
			c.workers[workerID] = worker
			c.mu.Unlock()
			writePoolJSON(w, http.StatusOK, active)
			return
		}
	}
	if job, leased, err := c.leaseComputeJobLocked(worker, request, now); err != nil {
		c.mu.Unlock()
		writePoolError(w, http.StatusInternalServerError, "application compute job could not be leased")
		return
	} else if leased {
		worker.LastSeenAt = now.Format(time.RFC3339Nano)
		c.workers[workerID] = worker
		c.mu.Unlock()
		writePoolJSON(w, http.StatusOK, job)
		return
	}

	job, err := c.newJobLocked(worker, request, now)
	if err != nil {
		c.mu.Unlock()
		writePoolError(w, http.StatusConflict, err.Error())
		return
	}
	c.jobs[job.ID] = job
	worker.LastSeenAt = now.Format(time.RFC3339Nano)
	c.workers[workerID] = worker
	c.mu.Unlock()
	writePoolJSON(w, http.StatusOK, job)
}

func (c *Coordinator) handleComplete(w http.ResponseWriter, r *http.Request) {
	body, workerID, ok := c.authenticate(w, r, c.config.AgentToken, "agent")
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		writePoolError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var result JobResult
	if err := decodePoolJSON(body, &result); err != nil {
		writePoolError(w, http.StatusBadRequest, err.Error())
		return
	}
	if result.WorkerID != workerID {
		writePoolError(w, http.StatusBadRequest, "result worker does not match authenticated worker")
		return
	}

	c.mu.Lock()
	if existing, exists := c.receiptsByJob[result.JobID]; exists {
		c.mu.Unlock()
		if receiptMatchesResult(existing, result) {
			c.mu.Lock()
			persistErr := c.completeComputeJobLocked(existing, result)
			c.mu.Unlock()
			if persistErr != nil {
				writePoolError(w, http.StatusInternalServerError, "verified application result could not be persisted")
				return
			}
			writePoolJSON(w, http.StatusOK, existing)
			return
		}
		writePoolError(w, http.StatusConflict, "job already has a different verified receipt")
		return
	}
	job, exists := c.jobs[result.JobID]
	if _, busy := c.completing[result.JobID]; busy {
		c.mu.Unlock()
		writePoolError(w, http.StatusConflict, "job completion is already being verified")
		return
	}
	if exists {
		c.completing[result.JobID] = struct{}{}
	}
	c.mu.Unlock()
	if !exists {
		writePoolError(w, http.StatusConflict, "job lease is missing or expired")
		return
	}
	select {
	case c.verifySlots <- struct{}{}:
		defer func() { <-c.verifySlots }()
	default:
		c.clearCompleting(result.JobID)
		w.Header().Set("Retry-After", "1")
		writePoolError(w, http.StatusTooManyRequests, "coordinator verification capacity is busy")
		return
	}
	if expires, err := time.Parse(time.RFC3339Nano, job.ExpiresAt); err != nil || time.Now().UTC().After(expires) {
		c.rejectJob(workerID, result.JobID)
		writePoolError(w, http.StatusGone, "job lease expired")
		return
	}
	if err := validateResultMetadata(result.Metadata); err != nil {
		c.rejectJob(workerID, result.JobID)
		writePoolError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := VerifyJobResultContext(r.Context(), job, result); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			c.clearCompleting(result.JobID)
			writePoolError(w, http.StatusRequestTimeout, "result verification was interrupted; retry the same completion")
			return
		}
		c.rejectJob(workerID, result.JobID)
		writePoolError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	receipt := c.makeReceiptForJob(job, result)
	if err := c.appendReceipt(receipt); err != nil {
		c.clearCompleting(result.JobID)
		writePoolError(w, http.StatusInternalServerError, "verified result could not be persisted")
		return
	}
	c.mu.Lock()
	delete(c.jobs, result.JobID)
	delete(c.completing, result.JobID)
	worker := c.workers[workerID]
	worker.CompletedJobs++
	worker.LastSeenAt = receipt.AcceptedAt
	c.workers[workerID] = worker
	c.receipts = append(c.receipts, receipt)
	c.receiptsByJob[receipt.JobID] = receipt
	computePersistErr := c.completeComputeJobLocked(receipt, result)
	c.mu.Unlock()
	if computePersistErr != nil {
		writePoolError(w, http.StatusInternalServerError, "verified application result could not be persisted")
		return
	}
	writePoolJSON(w, http.StatusOK, receipt)
}

func (c *Coordinator) handleStatus(w http.ResponseWriter, r *http.Request) {
	_, _, authentication, ok := c.authenticateMother(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodGet {
		writePoolError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	c.markMotherSeen(authentication.MotherID)
	writePoolJSON(w, http.StatusOK, c.StatusForMother(authentication.MotherID, authentication.MotherName))
}

func (c *Coordinator) handleLatestProof(w http.ResponseWriter, r *http.Request) {
	_, _, authentication, ok := c.authenticateMother(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodGet {
		writePoolError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	resource := ResourceKind(strings.ToLower(strings.TrimSpace(r.URL.Query().Get("resource"))))
	if !resource.Valid() {
		writePoolError(w, http.StatusBadRequest, "resource must be cpu, gpu, or ram")
		return
	}
	if authentication.Federation != nil && !authentication.Federation.AllowsResource(resource) {
		writePoolError(w, http.StatusForbidden, "federation invitation does not allow this workload")
		return
	}
	c.markMotherSeen(authentication.MotherID)
	proof, err := c.LatestSettlementProofForMother(authentication.MotherID, resource, time.Now().UTC())
	if errors.Is(err, errSettlementNotBound) {
		writePoolError(w, http.StatusPreconditionRequired, err.Error())
		return
	}
	if errors.Is(err, errNoSettlementReceipts) {
		writePoolError(w, http.StatusNotFound, err.Error())
		return
	}
	if err != nil {
		writePoolError(w, http.StatusInternalServerError, "settlement proof could not be prepared")
		return
	}
	proof.Signature = PoolProofSignature(authentication.Token, proof)
	writePoolJSON(w, http.StatusOK, proof)
}

func (c *Coordinator) handleSettlementBind(w http.ResponseWriter, r *http.Request) {
	body, _, authentication, ok := c.authenticateMother(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		writePoolError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var request SettlementBindRequest
	if err := decodePoolJSON(body, &request); err != nil {
		writePoolError(w, http.StatusBadRequest, err.Error())
		return
	}
	if authentication.Federation != nil && authentication.Federation.ConsumerWallet != "" &&
		!strings.EqualFold(authentication.Federation.ConsumerWallet, request.MotherHiveWallet) {
		writePoolError(w, http.StatusForbidden, "federation invitation is bound to a different Mother Hive wallet")
		return
	}
	binding, err := c.BindSettlementForMother(authentication.MotherID, request, time.Now().UTC())
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errSettlementBindingConflict) {
			status = http.StatusConflict
		}
		writePoolError(w, status, err.Error())
		return
	}
	c.markMotherSeen(authentication.MotherID)
	writePoolJSON(w, http.StatusOK, binding)
}

func (c *Coordinator) handleSettlementAck(w http.ResponseWriter, r *http.Request) {
	body, _, authentication, ok := c.authenticateMother(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		writePoolError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var request SettlementAckRequest
	if err := decodePoolJSON(body, &request); err != nil {
		writePoolError(w, http.StatusBadRequest, err.Error())
		return
	}
	result, err := c.AcknowledgeSettlementProofForMother(authentication.MotherID, request, time.Now().UTC())
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errSettlementProofNotFound) {
			status = http.StatusNotFound
		}
		writePoolError(w, status, err.Error())
		return
	}
	c.markMotherSeen(authentication.MotherID)
	writePoolJSON(w, http.StatusOK, result)
}

func (c *Coordinator) Status() PoolStatus {
	return c.statusForMother(LegacyMotherID, "Legacy Mother Hive", true)
}

// StatusForMother returns shared Agent capacity plus only the authenticated
// Hive's jobs, receipts, and settlement binding.
func (c *Coordinator) StatusForMother(motherID, motherName string) PoolStatus {
	return c.statusForMother(motherID, motherName, false)
}

func (c *Coordinator) statusForMother(motherID, motherName string, includeMotherRegistry bool) PoolStatus {
	motherID = normalizeReceiptMotherID(motherID)
	var motherHives []MotherTenantStatus
	if includeMotherRegistry {
		motherHives, _ = ListMotherTenants(c.config.StateDir)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pruneLocked(time.Now().UTC())
	workers := make([]WorkerStatus, 0, len(c.workers))
	for _, worker := range c.workers {
		workers = append(workers, worker)
	}
	sort.Slice(workers, func(i, j int) bool { return workers[i].WorkerID < workers[j].WorkerID })
	counts := map[ResourceKind]uint64{ResourceCPU: 0, ResourceGPU: 0, ResourceRAM: 0}
	for _, receipt := range c.receipts {
		if normalizeReceiptMotherID(receipt.MotherID) == motherID {
			counts[receipt.Resource]++
		}
	}
	activeLeases := 0
	for _, job := range c.jobs {
		if normalizeReceiptMotherID(job.MotherID) == motherID {
			activeLeases++
		}
	}
	for index := range motherHives {
		motherHives[index].LastSeenAt = formatOptionalTime(c.motherSeenAt[motherHives[index].MotherID])
	}
	settlement := settlementTenant(c.settlement, motherID)
	return PoolStatus{
		Version:       ProtocolVersion,
		CoordinatorID: c.config.ID,
		RelayID:       c.config.ID,
		Role:          "relay",
		MotherID:      motherID,
		MotherName:    motherName,
		MotherHives:   motherHives,
		Policy: RelayPolicy{
			CPUPercent: c.config.CPUPercent,
			GPUPercent: c.config.GPUPercent,
			RAMPercent: c.config.RAMPercent,
			CPUUnits:   scaledRelayUnits(c.config.CPUUnits, c.config.CPUPercent),
			GPUUnits:   scaledRelayUnits(c.config.GPUUnits, c.config.GPUPercent),
			RAMMiB:     scaledRelayUnits(c.config.RAMMiB, c.config.RAMPercent),
		},
		MotherSeenAt:            formatOptionalTime(c.motherSeenAt[motherID]),
		StartedAt:               c.startedAt.Format(time.RFC3339Nano),
		Workers:                 workers,
		ActiveLeases:            activeLeases,
		ReceiptCounts:           counts,
		SettlementReady:         settlement.Binding != nil,
		SettlementRelayID:       c.settlementRelayID,
		SettlementPublicKey:     c.settlementPublicKey,
		SettlementBinding:       settlement.Binding,
		PendingSettlementProofs: settlementPendingProofIDs(settlement),
		ComputeQueue:            c.computeQueueStatusForMotherLocked(motherID),
	}
}

func (c *Coordinator) LatestProof(resource ResourceKind, now time.Time) PoolProof {
	c.mu.Lock()
	defer c.mu.Unlock()
	cutoff := now.Add(-c.config.ProofWindow)
	selected := make([]Receipt, 0, c.config.MaxProofReceipts)
	for index := len(c.receipts) - 1; index >= 0 && len(selected) < c.config.MaxProofReceipts; index-- {
		receipt := c.receipts[index]
		if receipt.Resource != resource || normalizeReceiptMotherID(receipt.MotherID) != LegacyMotherID {
			continue
		}
		accepted, err := time.Parse(time.RFC3339Nano, receipt.AcceptedAt)
		if err != nil || accepted.Before(cutoff) {
			continue
		}
		selected = append(selected, receipt)
	}
	proof := AggregateReceipts(c.config.ID, resource, selected, now)
	proof.Signature = PoolProofSignature(c.config.MotherToken, proof)
	return proof
}

func (c *Coordinator) newJobLocked(worker WorkerStatus, request LeaseRequest, now time.Time) (Job, error) {
	seed := make([]byte, 32)
	if _, err := rand.Read(seed); err != nil {
		return Job{}, fmt.Errorf("generate job seed: %w", err)
	}
	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		return Job{}, fmt.Errorf("generate job id: %w", err)
	}
	job := Job{
		Version:   ProtocolVersion,
		ID:        hex.EncodeToString(idBytes),
		MotherID:  LegacyMotherID,
		WorkerID:  worker.WorkerID,
		Resource:  request.Resource,
		Seed:      hex.EncodeToString(seed),
		IssuedAt:  now.Format(time.RFC3339Nano),
		ExpiresAt: now.Add(c.config.JobTTL).Format(time.RFC3339Nano),
	}
	switch request.Resource {
	case ResourceCPU:
		job.Algorithm = AlgorithmCPU
		job.Units = boundedUnits(
			scaledRelayUnits(c.config.CPUUnits, c.config.CPUPercent),
			request.MaxUnits,
		)
	case ResourceGPU:
		if len(worker.Capabilities.GPUs) == 0 {
			return Job{}, errors.New("worker has no verified GPU helper capability")
		}
		job.Algorithm = AlgorithmGPU
		job.Units = boundedUnits(
			scaledRelayUnits(c.config.GPUUnits, c.config.GPUPercent),
			request.MaxUnits,
		)
	case ResourceRAM:
		job.Algorithm = AlgorithmRAM
		job.MemoryMiB = scaledRelayUnits(c.config.RAMMiB, c.config.RAMPercent)
		if request.MaxMemoryMiB > 0 && request.MaxMemoryMiB < job.MemoryMiB {
			job.MemoryMiB = request.MaxMemoryMiB
		}
		if worker.Capabilities.RAMMiB > 0 && worker.Capabilities.RAMMiB < job.MemoryMiB {
			job.MemoryMiB = worker.Capabilities.RAMMiB
		}
		if job.MemoryMiB == 0 {
			return Job{}, errors.New("worker has no RAM capacity available")
		}
		job.Units = job.MemoryMiB * 1024 * 1024
	}
	signature, err := SignJob(c.config.AgentToken, job)
	if err != nil {
		return Job{}, err
	}
	job.Signature = signature
	return job, nil
}

func (c *Coordinator) authenticate(w http.ResponseWriter, r *http.Request, token []byte, domain string) ([]byte, string, bool) {
	workerID := strings.TrimSpace(r.Header.Get(HeaderWorkerID))
	timestamp := strings.TrimSpace(r.Header.Get(HeaderTimestamp))
	nonce := strings.TrimSpace(r.Header.Get(HeaderNonce))
	signature := strings.TrimSpace(r.Header.Get(HeaderSignature))
	if err := ValidateWorkerID(workerID); err != nil || len(nonce) < 16 || len(nonce) > 128 {
		writePoolError(w, http.StatusUnauthorized, "invalid authentication headers")
		return nil, "", false
	}
	unixSeconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		writePoolError(w, http.StatusUnauthorized, "invalid authentication timestamp")
		return nil, "", false
	}
	now := time.Now().UTC()
	requestTime := time.Unix(unixSeconds, 0)
	if requestTime.Before(now.Add(-c.config.MaxClockSkew)) || requestTime.After(now.Add(c.config.MaxClockSkew)) {
		writePoolError(w, http.StatusUnauthorized, "authentication timestamp is outside the allowed window")
		return nil, "", false
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxCoordinatorRequestBody+1))
	if err != nil || len(body) > maxCoordinatorRequestBody {
		writePoolError(w, http.StatusRequestEntityTooLarge, "request body is too large")
		return nil, "", false
	}
	if !VerifyRequestSignature(token, signature, r.Method, r.URL.Path, timestamp, nonce, workerID, body) {
		writePoolError(w, http.StatusUnauthorized, "request signature did not verify")
		return nil, "", false
	}

	c.mu.Lock()
	c.pruneNoncesLocked(now)
	nonceKey := domain + ":" + workerID + ":" + nonce
	if _, exists := c.nonces[nonceKey]; exists {
		c.mu.Unlock()
		writePoolError(w, http.StatusConflict, "request nonce was already used")
		return nil, "", false
	}
	c.nonces[nonceKey] = now.Add(c.config.MaxClockSkew)
	c.mu.Unlock()
	return body, workerID, true
}

func (c *Coordinator) authenticateMother(w http.ResponseWriter, r *http.Request) ([]byte, string, motherAuthentication, bool) {
	encodedFederation := strings.TrimSpace(r.Header.Get(HeaderFederationContext))
	encodedMother := strings.TrimSpace(r.Header.Get(HeaderMotherContext))
	if encodedFederation != "" && encodedMother != "" {
		writePoolError(w, http.StatusUnauthorized, "request cannot combine local Mother Hive and federation contexts")
		return nil, "", motherAuthentication{}, false
	}
	if encodedMother != "" {
		token, contextValue, err := DeriveMotherToken(c.config.MotherToken, encodedMother)
		if err != nil {
			writePoolError(w, http.StatusUnauthorized, err.Error())
			return nil, "", motherAuthentication{}, false
		}
		registered, err := authorizeMotherTenant(c.config.StateDir, encodedMother)
		if err != nil || registered.MotherID != contextValue.MotherID {
			if err == nil {
				err = errors.New("Mother Hive registration does not match its credential")
			}
			writePoolError(w, http.StatusUnauthorized, err.Error())
			return nil, "", motherAuthentication{}, false
		}
		body, workerID, ok := c.authenticate(w, r, token, "mother-local:"+contextValue.MotherID)
		if !ok {
			return nil, "", motherAuthentication{}, false
		}
		return body, workerID, motherAuthentication{
			Token:      token,
			Mother:     &contextValue,
			MotherID:   contextValue.MotherID,
			MotherName: contextValue.MotherName,
		}, true
	}
	if encodedFederation == "" {
		body, workerID, ok := c.authenticate(w, r, c.config.MotherToken, "mother")
		return body, workerID, motherAuthentication{
			Token:      c.config.MotherToken,
			MotherID:   LegacyMotherID,
			MotherName: "Legacy Mother Hive",
		}, ok
	}
	token, contextValue, err := DeriveFederationToken(c.config.MotherToken, encodedFederation, time.Now().UTC())
	if err != nil {
		writePoolError(w, http.StatusUnauthorized, err.Error())
		return nil, "", motherAuthentication{}, false
	}
	body, workerID, ok := c.authenticate(w, r, token, "mother-federation:"+contextValue.OfferID)
	if !ok {
		return nil, "", motherAuthentication{}, false
	}
	return body, workerID, motherAuthentication{
		Token:      token,
		Federation: &contextValue,
		MotherID:   federationMotherID(encodedFederation),
		MotherName: contextValue.ProviderName,
	}, true
}

func (c *Coordinator) markMotherSeen(motherID string) {
	c.mu.Lock()
	c.motherSeenAt[normalizeReceiptMotherID(motherID)] = time.Now().UTC()
	c.mu.Unlock()
}

func normalizeRelayPercent(resource string, value int) (int, error) {
	if value == 0 {
		return 100, nil
	}
	if value < 1 || value > 100 {
		return 0, fmt.Errorf("relay %s percent must be between 1 and 100", resource)
	}
	return value, nil
}

func scaledRelayUnits(value uint64, percent int) uint64 {
	scaled := value * uint64(percent) / 100
	if scaled == 0 && value > 0 {
		return 1
	}
	return scaled
}

func formatOptionalTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func (c *Coordinator) rejectJob(workerID, jobID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.jobs, jobID)
	delete(c.completing, jobID)
	c.releaseComputeJobLocked(jobID, time.Now().UTC(), "Agent result failed verification; job returned to queue")
	worker := c.workers[workerID]
	worker.RejectedJobs++
	worker.LastSeenAt = time.Now().UTC().Format(time.RFC3339Nano)
	c.workers[workerID] = worker
}

func (c *Coordinator) clearCompleting(jobID string) {
	c.mu.Lock()
	delete(c.completing, jobID)
	c.mu.Unlock()
}

func receiptMatchesResult(receipt Receipt, result JobResult) bool {
	return receipt.JobID == result.JobID &&
		receipt.WorkerID == result.WorkerID &&
		receipt.Resource == result.Resource &&
		receipt.Algorithm == result.Algorithm &&
		strings.EqualFold(receipt.Digest, result.Digest) &&
		receipt.Units == result.Units &&
		receipt.MemoryMiB == result.MemoryMiB
}

func (c *Coordinator) makeReceipt(result JobResult) Receipt {
	return c.makeReceiptForJob(Job{MotherID: LegacyMotherID}, result)
}

func (c *Coordinator) makeReceiptForJob(job Job, result JobResult) Receipt {
	accepted := time.Now().UTC().Format(time.RFC3339Nano)
	motherID := normalizeReceiptMotherID(job.MotherID)
	identity := receiptIdentity(
		c.config.ID,
		motherID,
		result.JobID,
		result.WorkerID,
		result.Resource,
		result.Digest,
		result.Units,
	)
	digest := sha256.Sum256([]byte(identity))
	return Receipt{
		Version:       ProtocolVersion,
		ReceiptID:     hex.EncodeToString(digest[:]),
		JobID:         result.JobID,
		MotherID:      motherID,
		WorkerID:      result.WorkerID,
		Resource:      result.Resource,
		Algorithm:     result.Algorithm,
		Digest:        strings.ToLower(result.Digest),
		Units:         result.Units,
		MemoryMiB:     result.MemoryMiB,
		DurationMS:    result.DurationMS,
		CompletedAt:   result.Completed,
		AcceptedAt:    accepted,
		CoordinatorID: c.config.ID,
		Metadata:      result.Metadata,
	}
}

func (c *Coordinator) pruneLocked(now time.Time) {
	for id, job := range c.jobs {
		expires, err := time.Parse(time.RFC3339Nano, job.ExpiresAt)
		if err != nil || now.After(expires) {
			delete(c.jobs, id)
		}
	}
	if c.pruneComputeJobsLocked(now) {
		_ = c.persistComputeJobsLocked()
	}
	c.pruneNoncesLocked(now)
}

func (c *Coordinator) pruneNoncesLocked(now time.Time) {
	for nonce, expires := range c.nonces {
		if now.After(expires) {
			delete(c.nonces, nonce)
		}
	}
}

func (c *Coordinator) receiptsPath() string {
	return filepath.Join(c.config.StateDir, "receipts.jsonl")
}

func (c *Coordinator) appendReceipt(receipt Receipt) error {
	c.receiptPersistMu.Lock()
	defer c.receiptPersistMu.Unlock()

	raw, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(c.receiptsPath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err = file.Write(append(raw, '\n')); err != nil {
		return err
	}
	return file.Sync()
}

func (c *Coordinator) loadReceipts() error {
	file, err := os.Open(c.receiptsPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open edge receipt log: %w", err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		var receipt Receipt
		if err := json.Unmarshal(line, &receipt); err != nil {
			return fmt.Errorf("edge receipt log contains invalid JSON: %w", err)
		}
		receipt.MotherID = normalizeReceiptMotherID(receipt.MotherID)
		if err := c.validateStoredReceipt(receipt); err != nil {
			return fmt.Errorf("edge receipt log contains an invalid receipt: %w", err)
		}
		if existing, duplicate := c.receiptsByJob[receipt.JobID]; duplicate {
			if existing.ReceiptID != receipt.ReceiptID {
				return fmt.Errorf("edge receipt log has conflicting receipts for job %s", receipt.JobID)
			}
			continue
		}
		c.receipts = append(c.receipts, receipt)
		c.receiptsByJob[receipt.JobID] = receipt
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read edge receipt log: %w", err)
	}
	return nil
}

func (c *Coordinator) validateStoredReceipt(receipt Receipt) error {
	if receipt.Version != ProtocolVersion || receipt.CoordinatorID != c.config.ID {
		return errors.New("receipt protocol or coordinator identity does not match")
	}
	if receipt.JobID == "" || !validMotherTenantID(receipt.MotherID) || ValidateWorkerID(receipt.WorkerID) != nil || !receipt.Resource.Valid() {
		return errors.New("receipt job, worker, or resource is invalid")
	}
	if receipt.Units == 0 || receipt.DurationMS <= 0 {
		return errors.New("receipt accounting is invalid")
	}
	switch receipt.Resource {
	case ResourceCPU:
		if receipt.Algorithm != AlgorithmCPU || receipt.MemoryMiB != 0 || receipt.Units > MaxCPUUnits {
			return errors.New("CPU receipt accounting is invalid")
		}
	case ResourceGPU:
		if receipt.Algorithm != AlgorithmGPU || receipt.MemoryMiB != 0 || receipt.Units > MaxGPUUnits {
			return errors.New("GPU receipt accounting is invalid")
		}
	case ResourceRAM:
		if receipt.Algorithm != AlgorithmRAM || receipt.MemoryMiB == 0 || receipt.MemoryMiB > MaxRAMMiB || receipt.Units != receipt.MemoryMiB*1024*1024 {
			return errors.New("RAM receipt accounting is invalid")
		}
	}
	if _, err := time.Parse(time.RFC3339Nano, receipt.CompletedAt); err != nil {
		return errors.New("receipt completion time is invalid")
	}
	if _, err := time.Parse(time.RFC3339Nano, receipt.AcceptedAt); err != nil {
		return errors.New("receipt acceptance time is invalid")
	}
	digest, err := hex.DecodeString(receipt.Digest)
	if err != nil || len(digest) != sha256.Size {
		return errors.New("receipt digest is invalid")
	}
	identity := receiptIdentity(
		receipt.CoordinatorID,
		receipt.MotherID,
		receipt.JobID,
		receipt.WorkerID,
		receipt.Resource,
		receipt.Digest,
		receipt.Units,
	)
	expected := sha256.Sum256([]byte(identity))
	if !strings.EqualFold(receipt.ReceiptID, hex.EncodeToString(expected[:])) {
		return errors.New("receipt identity does not verify")
	}
	return validateResultMetadata(receipt.Metadata)
}

func receiptIdentity(coordinatorID, motherID, jobID, workerID string, resource ResourceKind, digest string, units uint64) string {
	parts := []string{
		ProtocolVersion,
		coordinatorID,
		jobID,
		workerID,
		string(resource),
		strings.ToLower(digest),
		strconv.FormatUint(units, 10),
	}
	if normalized := normalizeReceiptMotherID(motherID); normalized != LegacyMotherID {
		parts = append(parts, normalized)
	}
	return strings.Join(parts, ":")
}

func validateCapabilities(capabilities Capabilities) error {
	if capabilities.CPUThreads < 0 || capabilities.CPUThreads > 4096 {
		return errors.New("cpu_threads is outside the supported range")
	}
	if capabilities.RAMMiB > MaxAgentRAMMiB {
		return errors.New("ram_mib is outside the supported range")
	}
	if len(capabilities.Resources) == 0 || len(capabilities.Resources) > 3 {
		return errors.New("at least one and at most three resources must be registered")
	}
	seen := map[ResourceKind]bool{}
	for _, resource := range capabilities.Resources {
		if !resource.Valid() || seen[resource] {
			return errors.New("registered resources must be unique cpu, gpu, or ram values")
		}
		seen[resource] = true
	}
	if seen[ResourceCPU] && capabilities.CPUThreads == 0 {
		return errors.New("CPU workers must report at least one thread")
	}
	if seen[ResourceRAM] && capabilities.RAMMiB == 0 {
		return errors.New("RAM workers must report a positive memory limit")
	}
	if seen[ResourceGPU] && len(capabilities.GPUs) == 0 {
		return errors.New("GPU workers must report a trusted helper capability")
	}
	return nil
}

func validateResultMetadata(metadata map[string]string) error {
	if len(metadata) > 16 {
		return errors.New("result metadata has too many fields")
	}
	for key, value := range metadata {
		if len(key) == 0 || len(key) > 64 || len(value) > 256 {
			return errors.New("result metadata field exceeds its size limit")
		}
	}
	return nil
}

func boundedUnits(configured, requested uint64) uint64 {
	if requested > 0 && requested < configured {
		return requested
	}
	return configured
}

func truncateText(value string, max int) string {
	value = strings.TrimSpace(value)
	if len(value) > max {
		return value[:max]
	}
	return value
}

func decodePoolJSON(body []byte, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid JSON request: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("invalid JSON request: exactly one object is required")
	}
	return nil
}

func writePoolJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writePoolError(w http.ResponseWriter, status int, message string) {
	writePoolJSON(w, status, map[string]any{"ok": false, "error": message})
}
