package edgepool

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	computeStateVersion      = "qsdm-compute-state/v1"
	defaultComputeDeadline   = 15 * time.Minute
	minimumComputeDeadline   = 30 * time.Second
	maximumComputeDeadline   = time.Hour
	maximumQueuedComputeJobs = 256
	maximumComputeJobHistory = 1024
	defaultComputeListLimit  = 50
	maximumComputeListLimit  = 100
)

var (
	errComputeJobNotFound        = errors.New("compute job was not found")
	errComputeJobConflict        = errors.New("client_request_id is already bound to different work")
	errComputeQueueFull          = errors.New("compute queue is full")
	errComputeJobAlreadyTerminal = errors.New("compute job is already terminal")
	errComputeJobCompleting      = errors.New("compute job completion is being verified")
)

type computeStateSnapshot struct {
	Version string             `json:"version"`
	Jobs    []ComputeJobRecord `json:"jobs"`
}

func (c *Coordinator) handleComputeJobs(w http.ResponseWriter, r *http.Request) {
	body, _, ok := c.authenticate(w, r, c.config.MotherToken, "mother")
	if !ok {
		return
	}
	c.markMotherSeen()

	switch r.Method {
	case http.MethodPost:
		var request ComputeJobSubmitRequest
		if err := decodePoolJSON(body, &request); err != nil {
			writePoolError(w, http.StatusBadRequest, err.Error())
			return
		}
		record, err := c.SubmitComputeJob(request, time.Now().UTC())
		if err != nil {
			status := http.StatusBadRequest
			switch {
			case errors.Is(err, errComputeJobConflict):
				status = http.StatusConflict
			case errors.Is(err, errComputeQueueFull):
				status = http.StatusTooManyRequests
			}
			writePoolError(w, status, err.Error())
			return
		}
		writePoolJSON(w, http.StatusAccepted, record)
	case http.MethodGet:
		limit := defaultComputeListLimit
		if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed < 1 || parsed > maximumComputeListLimit {
				writePoolError(w, http.StatusBadRequest, "limit must be between 1 and 100")
				return
			}
			limit = parsed
		}
		writePoolJSON(w, http.StatusOK, ComputeJobList{
			Version: ComputeProtocolVersion,
			Jobs:    c.ListComputeJobs(limit, time.Now().UTC()),
		})
	default:
		writePoolError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (c *Coordinator) handleComputeJob(w http.ResponseWriter, r *http.Request) {
	_, _, ok := c.authenticate(w, r, c.config.MotherToken, "mother")
	if !ok {
		return
	}
	c.markMotherSeen()

	jobID := strings.TrimPrefix(r.URL.Path, "/v1/compute/jobs/")
	if len(jobID) != 32 {
		writePoolError(w, http.StatusBadRequest, "compute job id must be 16 bytes of hexadecimal data")
		return
	}
	if _, err := hex.DecodeString(jobID); err != nil {
		writePoolError(w, http.StatusBadRequest, "compute job id must be 16 bytes of hexadecimal data")
		return
	}

	var (
		record ComputeJobRecord
		err    error
	)
	switch r.Method {
	case http.MethodGet:
		record, err = c.ComputeJob(jobID, time.Now().UTC())
	case http.MethodDelete:
		record, err = c.CancelComputeJob(jobID, time.Now().UTC())
	default:
		writePoolError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if err != nil {
		status := http.StatusInternalServerError
		switch {
		case errors.Is(err, errComputeJobNotFound):
			status = http.StatusNotFound
		case errors.Is(err, errComputeJobAlreadyTerminal), errors.Is(err, errComputeJobCompleting):
			status = http.StatusConflict
		}
		writePoolError(w, status, err.Error())
		return
	}
	writePoolJSON(w, http.StatusOK, record)
}

func (c *Coordinator) SubmitComputeJob(request ComputeJobSubmitRequest, now time.Time) (ComputeJobRecord, error) {
	if request.Version != ComputeProtocolVersion {
		return ComputeJobRecord{}, fmt.Errorf("version must be %q", ComputeProtocolVersion)
	}
	if err := ValidateComputeRequestID(request.ClientRequestID); err != nil {
		return ComputeJobRecord{}, err
	}
	if !request.Resource.Valid() {
		return ComputeJobRecord{}, errors.New("resource must be cpu, gpu, or ram")
	}
	algorithm, units, memoryMiB, err := c.computeRequestBudget(request)
	if err != nil {
		return ComputeJobRecord{}, err
	}
	deadline := time.Duration(request.DeadlineSeconds) * time.Second
	if deadline == 0 {
		deadline = defaultComputeDeadline
	}
	if deadline < minimumComputeDeadline || deadline > maximumComputeDeadline {
		return ComputeJobRecord{}, errors.New("deadline_seconds must be between 30 and 3600")
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.pruneLocked(now)
	if existingID := c.computeByRequest[request.ClientRequestID]; existingID != "" {
		existing := c.computeJobs[existingID]
		if existing != nil && computeRequestMatches(*existing, request.Resource, units, memoryMiB, deadline) {
			return cloneComputeJob(*existing), nil
		}
		return ComputeJobRecord{}, errComputeJobConflict
	}
	if c.activeComputeJobsLocked() >= maximumQueuedComputeJobs {
		return ComputeJobRecord{}, errComputeQueueFull
	}

	id, err := randomHex(16)
	if err != nil {
		return ComputeJobRecord{}, fmt.Errorf("generate compute job id: %w", err)
	}
	seed, err := randomHex(32)
	if err != nil {
		return ComputeJobRecord{}, fmt.Errorf("generate compute seed: %w", err)
	}
	created := now.UTC().Format(time.RFC3339Nano)
	record := &ComputeJobRecord{
		Version:         ComputeProtocolVersion,
		ID:              id,
		ClientRequestID: request.ClientRequestID,
		Resource:        request.Resource,
		Algorithm:       algorithm,
		Seed:            seed,
		Units:           units,
		MemoryMiB:       memoryMiB,
		State:           ComputeJobQueued,
		CreatedAt:       created,
		UpdatedAt:       created,
		DeadlineAt:      now.Add(deadline).UTC().Format(time.RFC3339Nano),
	}
	c.computeJobs[id] = record
	c.computeByRequest[request.ClientRequestID] = id
	c.computeOrder = append(c.computeOrder, id)
	if err := c.persistComputeJobsLocked(); err != nil {
		delete(c.computeJobs, id)
		delete(c.computeByRequest, request.ClientRequestID)
		c.computeOrder = c.computeOrder[:len(c.computeOrder)-1]
		return ComputeJobRecord{}, fmt.Errorf("persist compute job: %w", err)
	}
	return cloneComputeJob(*record), nil
}

func (c *Coordinator) ComputeJob(jobID string, now time.Time) (ComputeJobRecord, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pruneLocked(now)
	record := c.computeJobs[strings.ToLower(jobID)]
	if record == nil {
		return ComputeJobRecord{}, errComputeJobNotFound
	}
	return cloneComputeJob(*record), nil
}

func (c *Coordinator) ListComputeJobs(limit int, now time.Time) []ComputeJobRecord {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pruneLocked(now)
	if limit < 1 || limit > maximumComputeListLimit {
		limit = defaultComputeListLimit
	}
	out := make([]ComputeJobRecord, 0, limit)
	for index := len(c.computeOrder) - 1; index >= 0 && len(out) < limit; index-- {
		if record := c.computeJobs[c.computeOrder[index]]; record != nil {
			out = append(out, cloneComputeJob(*record))
		}
	}
	return out
}

func (c *Coordinator) CancelComputeJob(jobID string, now time.Time) (ComputeJobRecord, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	record := c.computeJobs[strings.ToLower(jobID)]
	if record == nil {
		return ComputeJobRecord{}, errComputeJobNotFound
	}
	if record.State.terminal() {
		return ComputeJobRecord{}, errComputeJobAlreadyTerminal
	}
	if _, completing := c.completing[record.ID]; completing {
		return ComputeJobRecord{}, errComputeJobCompleting
	}
	previous := cloneComputeJob(*record)
	activeJob, hadActiveJob := c.jobs[record.ID]
	delete(c.jobs, record.ID)
	delete(c.completing, record.ID)
	record.State = ComputeJobCancelled
	record.UpdatedAt = now.UTC().Format(time.RFC3339Nano)
	record.LeaseExpiresAt = ""
	record.LastError = "cancelled by Mother Hive application"
	if err := c.persistComputeJobsLocked(); err != nil {
		*record = previous
		if hadActiveJob {
			c.jobs[record.ID] = activeJob
		}
		return ComputeJobRecord{}, fmt.Errorf("persist compute cancellation: %w", err)
	}
	return cloneComputeJob(*record), nil
}

func computeRequestMatches(record ComputeJobRecord, resource ResourceKind, units, memoryMiB uint64, deadline time.Duration) bool {
	if record.Resource != resource || record.Units != units || record.MemoryMiB != memoryMiB {
		return false
	}
	createdAt, createdErr := time.Parse(time.RFC3339Nano, record.CreatedAt)
	deadlineAt, deadlineErr := time.Parse(time.RFC3339Nano, record.DeadlineAt)
	return createdErr == nil && deadlineErr == nil && deadlineAt.Sub(createdAt) == deadline
}

func (c *Coordinator) computeRequestBudget(request ComputeJobSubmitRequest) (string, uint64, uint64, error) {
	switch request.Resource {
	case ResourceCPU:
		maximum := scaledRelayUnits(c.config.CPUUnits, c.config.CPUPercent)
		units := request.Units
		if units == 0 {
			units = maximum
		}
		if request.MemoryMiB != 0 || units > maximum {
			return "", 0, 0, fmt.Errorf("CPU units must be between 1 and %d", maximum)
		}
		return AlgorithmCPU, units, 0, nil
	case ResourceGPU:
		maximum := scaledRelayUnits(c.config.GPUUnits, c.config.GPUPercent)
		units := request.Units
		if units == 0 {
			units = maximum
		}
		if request.MemoryMiB != 0 || units > maximum {
			return "", 0, 0, fmt.Errorf("GPU units must be between 1 and %d", maximum)
		}
		return AlgorithmGPU, units, 0, nil
	case ResourceRAM:
		maximum := scaledRelayUnits(c.config.RAMMiB, c.config.RAMPercent)
		memoryMiB := request.MemoryMiB
		if memoryMiB == 0 {
			memoryMiB = maximum
		}
		if request.Units != 0 || memoryMiB > maximum {
			return "", 0, 0, fmt.Errorf("RAM memory_mib must be between 1 and %d", maximum)
		}
		return AlgorithmRAM, memoryMiB * 1024 * 1024, memoryMiB, nil
	default:
		return "", 0, 0, errors.New("unsupported compute resource")
	}
}

func (c *Coordinator) leaseComputeJobLocked(worker WorkerStatus, request LeaseRequest, now time.Time) (Job, bool, error) {
	for _, id := range c.computeOrder {
		record := c.computeJobs[id]
		if record == nil || record.State != ComputeJobQueued || record.Resource != request.Resource {
			continue
		}
		deadline, err := time.Parse(time.RFC3339Nano, record.DeadlineAt)
		if err != nil || !now.Before(deadline) {
			record.State = ComputeJobExpired
			record.UpdatedAt = now.UTC().Format(time.RFC3339Nano)
			record.LastError = "job deadline expired before an Agent accepted it"
			continue
		}
		if !workerCanRunComputeJob(worker, request, *record) {
			continue
		}
		expires := now.Add(c.config.JobTTL)
		if expires.After(deadline) {
			expires = deadline
		}
		job := Job{
			Version:   ProtocolVersion,
			ID:        record.ID,
			WorkerID:  worker.WorkerID,
			Resource:  record.Resource,
			Algorithm: record.Algorithm,
			Seed:      record.Seed,
			Units:     record.Units,
			MemoryMiB: record.MemoryMiB,
			IssuedAt:  now.UTC().Format(time.RFC3339Nano),
			ExpiresAt: expires.UTC().Format(time.RFC3339Nano),
		}
		signature, err := SignJob(c.config.AgentToken, job)
		if err != nil {
			return Job{}, false, err
		}
		job.Signature = signature
		previous := cloneComputeJob(*record)
		record.State = ComputeJobLeased
		record.WorkerID = worker.WorkerID
		record.UpdatedAt = job.IssuedAt
		record.LeaseExpiresAt = job.ExpiresAt
		record.LastError = ""
		c.jobs[job.ID] = job
		if err := c.persistComputeJobsLocked(); err != nil {
			delete(c.jobs, job.ID)
			*record = previous
			return Job{}, false, fmt.Errorf("persist compute lease: %w", err)
		}
		return job, true, nil
	}
	return Job{}, false, nil
}

func workerCanRunComputeJob(worker WorkerStatus, request LeaseRequest, record ComputeJobRecord) bool {
	if !worker.Capabilities.Supports(record.Resource) {
		return false
	}
	switch record.Resource {
	case ResourceCPU, ResourceGPU:
		if request.MaxUnits > 0 && record.Units > request.MaxUnits {
			return false
		}
		return record.Resource != ResourceGPU || len(worker.Capabilities.GPUs) > 0
	case ResourceRAM:
		limit := request.MaxMemoryMiB
		if limit == 0 || (worker.Capabilities.RAMMiB > 0 && worker.Capabilities.RAMMiB < limit) {
			limit = worker.Capabilities.RAMMiB
		}
		return limit > 0 && record.MemoryMiB <= limit
	default:
		return false
	}
}

func (c *Coordinator) completeComputeJobLocked(receipt Receipt, result JobResult) error {
	record := c.computeJobs[receipt.JobID]
	if record == nil {
		return nil
	}
	resultCopy := result
	resultCopy.Metadata = cloneStringMap(result.Metadata)
	record.State = ComputeJobCompleted
	record.WorkerID = receipt.WorkerID
	record.UpdatedAt = receipt.AcceptedAt
	record.LeaseExpiresAt = ""
	record.ReceiptID = receipt.ReceiptID
	record.Result = &resultCopy
	record.LastError = ""
	return c.persistComputeJobsLocked()
}

func (c *Coordinator) releaseComputeJobLocked(jobID string, now time.Time, detail string) {
	record := c.computeJobs[jobID]
	if record == nil || record.State != ComputeJobLeased {
		return
	}
	deadline, err := time.Parse(time.RFC3339Nano, record.DeadlineAt)
	if err != nil || !now.Before(deadline) {
		record.State = ComputeJobExpired
	} else {
		record.State = ComputeJobQueued
	}
	record.WorkerID = ""
	record.LeaseExpiresAt = ""
	record.UpdatedAt = now.UTC().Format(time.RFC3339Nano)
	record.LastError = detail
	_ = c.persistComputeJobsLocked()
}

func (c *Coordinator) pruneComputeJobsLocked(now time.Time) bool {
	changed := false
	for _, id := range c.computeOrder {
		record := c.computeJobs[id]
		if record == nil || record.State.terminal() {
			continue
		}
		deadline, err := time.Parse(time.RFC3339Nano, record.DeadlineAt)
		if err != nil || !now.Before(deadline) {
			delete(c.jobs, id)
			delete(c.completing, id)
			record.State = ComputeJobExpired
			record.WorkerID = ""
			record.LeaseExpiresAt = ""
			record.UpdatedAt = now.UTC().Format(time.RFC3339Nano)
			record.LastError = "job deadline expired"
			changed = true
			continue
		}
		if record.State == ComputeJobLeased {
			leaseExpires, leaseErr := time.Parse(time.RFC3339Nano, record.LeaseExpiresAt)
			if leaseErr != nil || !now.Before(leaseExpires) {
				record.State = ComputeJobQueued
				record.WorkerID = ""
				record.LeaseExpiresAt = ""
				record.UpdatedAt = now.UTC().Format(time.RFC3339Nano)
				record.LastError = "Agent lease expired; job returned to queue"
				changed = true
			}
		}
	}
	return changed
}

func (c *Coordinator) computeQueueStatusLocked() ComputeQueueStatus {
	var status ComputeQueueStatus
	for _, record := range c.computeJobs {
		switch record.State {
		case ComputeJobQueued:
			status.Queued++
		case ComputeJobLeased:
			status.Leased++
		case ComputeJobCompleted:
			status.Completed++
		case ComputeJobCancelled:
			status.Cancelled++
		case ComputeJobExpired:
			status.Expired++
		}
	}
	return status
}

func (c *Coordinator) activeComputeJobsLocked() int {
	status := c.computeQueueStatusLocked()
	return status.Queued + status.Leased
}

func (c *Coordinator) computeJobsPath() string {
	return filepath.Join(c.config.StateDir, "compute-jobs.json")
}

func (c *Coordinator) loadComputeJobs() error {
	raw, err := os.ReadFile(c.computeJobsPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read compute job state: %w", err)
	}
	var snapshot computeStateSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return fmt.Errorf("decode compute job state: %w", err)
	}
	if snapshot.Version != computeStateVersion || len(snapshot.Jobs) > maximumComputeJobHistory {
		return errors.New("compute job state has an unsupported version or size")
	}
	now := time.Now().UTC()
	for _, stored := range snapshot.Jobs {
		if err := validateStoredComputeJob(stored); err != nil {
			return fmt.Errorf("compute job state contains invalid job %q: %w", stored.ID, err)
		}
		if _, exists := c.computeJobs[stored.ID]; exists || c.computeByRequest[stored.ClientRequestID] != "" {
			return errors.New("compute job state contains duplicate identities")
		}
		record := cloneComputeJob(stored)
		if receipt, completed := c.receiptsByJob[record.ID]; completed {
			result := jobResultFromReceipt(receipt)
			record.State = ComputeJobCompleted
			record.WorkerID = receipt.WorkerID
			record.UpdatedAt = receipt.AcceptedAt
			record.LeaseExpiresAt = ""
			record.ReceiptID = receipt.ReceiptID
			record.Result = &result
			record.LastError = ""
		} else if record.State == ComputeJobLeased {
			record.State = ComputeJobQueued
			record.WorkerID = ""
			record.LeaseExpiresAt = ""
			record.UpdatedAt = now.Format(time.RFC3339Nano)
			record.LastError = "Relay restarted; job returned to queue"
		}
		c.computeJobs[record.ID] = &record
		c.computeByRequest[record.ClientRequestID] = record.ID
		c.computeOrder = append(c.computeOrder, record.ID)
	}
	c.pruneComputeJobsLocked(now)
	return c.persistComputeJobsLocked()
}

func validateStoredComputeJob(record ComputeJobRecord) error {
	if record.Version != ComputeProtocolVersion {
		return errors.New("invalid protocol version")
	}
	if decoded, err := hex.DecodeString(record.ID); err != nil || len(decoded) != 16 {
		return errors.New("invalid job id")
	}
	if err := ValidateComputeRequestID(record.ClientRequestID); err != nil {
		return err
	}
	if !record.Resource.Valid() || record.Units == 0 {
		return errors.New("invalid resource accounting")
	}
	if decoded, err := hex.DecodeString(record.Seed); err != nil || len(decoded) != 32 {
		return errors.New("invalid seed")
	}
	if _, err := time.Parse(time.RFC3339Nano, record.CreatedAt); err != nil {
		return errors.New("invalid creation time")
	}
	if _, err := time.Parse(time.RFC3339Nano, record.UpdatedAt); err != nil {
		return errors.New("invalid update time")
	}
	if _, err := time.Parse(time.RFC3339Nano, record.DeadlineAt); err != nil {
		return errors.New("invalid deadline")
	}
	switch record.State {
	case ComputeJobQueued, ComputeJobLeased, ComputeJobCompleted, ComputeJobCancelled, ComputeJobExpired:
	default:
		return errors.New("invalid state")
	}
	switch record.Resource {
	case ResourceCPU:
		if record.Algorithm != AlgorithmCPU || record.Units > MaxCPUUnits || record.MemoryMiB != 0 {
			return errors.New("invalid CPU job")
		}
	case ResourceGPU:
		if record.Algorithm != AlgorithmGPU || record.Units > MaxGPUUnits || record.MemoryMiB != 0 {
			return errors.New("invalid GPU job")
		}
	case ResourceRAM:
		if record.Algorithm != AlgorithmRAM || record.MemoryMiB == 0 || record.MemoryMiB > MaxRAMMiB || record.Units != record.MemoryMiB*1024*1024 {
			return errors.New("invalid RAM job")
		}
	}
	return nil
}

func (c *Coordinator) persistComputeJobsLocked() error {
	c.trimComputeHistoryLocked()
	jobs := make([]ComputeJobRecord, 0, len(c.computeOrder))
	for _, id := range c.computeOrder {
		if record := c.computeJobs[id]; record != nil {
			jobs = append(jobs, cloneComputeJob(*record))
		}
	}
	raw, err := json.MarshalIndent(computeStateSnapshot{Version: computeStateVersion, Jobs: jobs}, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	path := c.computeJobsPath()
	temporary := path + ".tmp"
	c.persistMu.Lock()
	defer c.persistMu.Unlock()
	if err := os.WriteFile(temporary, raw, 0o600); err != nil {
		return err
	}
	if err := os.Chmod(temporary, 0o600); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}

func (c *Coordinator) trimComputeHistoryLocked() {
	for len(c.computeOrder) > maximumComputeJobHistory {
		removeAt := -1
		for index, id := range c.computeOrder {
			if record := c.computeJobs[id]; record != nil && record.State.terminal() {
				removeAt = index
				break
			}
		}
		if removeAt < 0 {
			return
		}
		id := c.computeOrder[removeAt]
		if record := c.computeJobs[id]; record != nil {
			delete(c.computeByRequest, record.ClientRequestID)
		}
		delete(c.computeJobs, id)
		c.computeOrder = append(c.computeOrder[:removeAt], c.computeOrder[removeAt+1:]...)
	}
}

func randomHex(size int) (string, error) {
	raw := make([]byte, size)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func cloneComputeJob(record ComputeJobRecord) ComputeJobRecord {
	out := record
	out.Metadata = cloneStringMap(record.Metadata)
	if record.Result != nil {
		result := *record.Result
		result.Metadata = cloneStringMap(record.Result.Metadata)
		out.Result = &result
	}
	return out
}

func cloneStringMap(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	out := make(map[string]string, len(source))
	for key, value := range source {
		out[key] = value
	}
	return out
}

func jobResultFromReceipt(receipt Receipt) JobResult {
	return JobResult{
		Version:    receipt.Version,
		JobID:      receipt.JobID,
		WorkerID:   receipt.WorkerID,
		Resource:   receipt.Resource,
		Algorithm:  receipt.Algorithm,
		Digest:     receipt.Digest,
		Units:      receipt.Units,
		MemoryMiB:  receipt.MemoryMiB,
		DurationMS: receipt.DurationMS,
		Completed:  receipt.CompletedAt,
		Metadata:   cloneStringMap(receipt.Metadata),
	}
}
