package edgepool

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	ProtocolVersion           = "qsdm-edge-pool/v1"
	ComputeProtocolVersion    = "qsdm-compute-gateway/v1"
	SettlementProtocolVersion = "qsdm-edge-settlement/v1"
	SettlementProofSource     = "qsdm-edge-relay-v2"
	// ProductionEcosystemWallet is the consensus-bound reserve that receives
	// the ecosystem share of every pooled resource settlement.
	ProductionEcosystemWallet = "651a79b2b1790820dd73bda81be24057e1bc27377c1f1117c6db2ab79dc038ea"

	HeaderWorkerID  = "X-QSDM-Worker-ID"
	HeaderTimestamp = "X-QSDM-Timestamp"
	HeaderNonce     = "X-QSDM-Nonce"
	HeaderSignature = "X-QSDM-Signature"

	ResourceCPU ResourceKind = "cpu"
	ResourceGPU ResourceKind = "gpu"
	ResourceRAM ResourceKind = "ram"
)

var workerIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
var computeRequestIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{7,127}$`)

type ResourceKind string

func (r ResourceKind) Valid() bool {
	switch r {
	case ResourceCPU, ResourceGPU, ResourceRAM:
		return true
	default:
		return false
	}
}

type GPUCapability struct {
	Name      string `json:"name"`
	UUID      string `json:"uuid,omitempty"`
	MemoryMiB uint64 `json:"memory_mib,omitempty"`
	Helper    string `json:"helper,omitempty"`
}

type Capabilities struct {
	CPUThreads int             `json:"cpu_threads"`
	RAMMiB     uint64          `json:"ram_mib"`
	GPUs       []GPUCapability `json:"gpus,omitempty"`
	Resources  []ResourceKind  `json:"resources"`
}

func (c Capabilities) Supports(resource ResourceKind) bool {
	for _, candidate := range c.Resources {
		if candidate == resource {
			return true
		}
	}
	return false
}

type RegisterRequest struct {
	Version      string       `json:"version"`
	WorkerID     string       `json:"worker_id"`
	Hostname     string       `json:"hostname"`
	AgentVersion string       `json:"agent_version"`
	Capabilities Capabilities `json:"capabilities"`
}

type RegisterResponse struct {
	OK          bool   `json:"ok"`
	Coordinator string `json:"coordinator"`
	Relay       string `json:"relay,omitempty"`
	WorkerID    string `json:"worker_id"`
	Registered  string `json:"registered_at"`
}

type LeaseRequest struct {
	Version      string       `json:"version"`
	WorkerID     string       `json:"worker_id"`
	Resource     ResourceKind `json:"resource"`
	MaxUnits     uint64       `json:"max_units,omitempty"`
	MaxMemoryMiB uint64       `json:"max_memory_mib,omitempty"`
}

type Job struct {
	Version   string       `json:"version"`
	ID        string       `json:"id"`
	WorkerID  string       `json:"worker_id"`
	Resource  ResourceKind `json:"resource"`
	Algorithm string       `json:"algorithm"`
	Seed      string       `json:"seed"`
	Units     uint64       `json:"units"`
	MemoryMiB uint64       `json:"memory_mib,omitempty"`
	IssuedAt  string       `json:"issued_at"`
	ExpiresAt string       `json:"expires_at"`
	Signature string       `json:"signature"`
}

type JobResult struct {
	Version    string            `json:"version"`
	JobID      string            `json:"job_id"`
	WorkerID   string            `json:"worker_id"`
	Resource   ResourceKind      `json:"resource"`
	Algorithm  string            `json:"algorithm"`
	Digest     string            `json:"digest"`
	Units      uint64            `json:"units"`
	MemoryMiB  uint64            `json:"memory_mib,omitempty"`
	DurationMS int64             `json:"duration_ms"`
	Completed  string            `json:"completed_at"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

type ComputeJobState string

const (
	ComputeJobQueued    ComputeJobState = "queued"
	ComputeJobLeased    ComputeJobState = "leased"
	ComputeJobCompleted ComputeJobState = "completed"
	ComputeJobCancelled ComputeJobState = "cancelled"
	ComputeJobExpired   ComputeJobState = "expired"
)

func (s ComputeJobState) terminal() bool {
	return s == ComputeJobCompleted || s == ComputeJobCancelled || s == ComputeJobExpired
}

// ComputeJobSubmitRequest is the deliberately narrow application-facing job
// contract. Applications select a resource budget; they never upload scripts,
// commands, binaries, or arbitrary executable payloads to Agent computers.
type ComputeJobSubmitRequest struct {
	Version         string       `json:"version"`
	ClientRequestID string       `json:"client_request_id"`
	Resource        ResourceKind `json:"resource"`
	Units           uint64       `json:"units,omitempty"`
	MemoryMiB       uint64       `json:"memory_mib,omitempty"`
	DeadlineSeconds uint64       `json:"deadline_seconds,omitempty"`
}

// ComputeJobRecord is returned to a local application through Mother Hive.
// Seed is retained so completed deterministic work can be independently
// reproduced; it is random job data, not a credential.
type ComputeJobRecord struct {
	Version         string            `json:"version"`
	ID              string            `json:"id"`
	ClientRequestID string            `json:"client_request_id"`
	Resource        ResourceKind      `json:"resource"`
	Algorithm       string            `json:"algorithm"`
	Seed            string            `json:"seed"`
	Units           uint64            `json:"units"`
	MemoryMiB       uint64            `json:"memory_mib,omitempty"`
	State           ComputeJobState   `json:"state"`
	WorkerID        string            `json:"worker_id,omitempty"`
	CreatedAt       string            `json:"created_at"`
	UpdatedAt       string            `json:"updated_at"`
	DeadlineAt      string            `json:"deadline_at"`
	LeaseExpiresAt  string            `json:"lease_expires_at,omitempty"`
	ReceiptID       string            `json:"receipt_id,omitempty"`
	Result          *JobResult        `json:"result,omitempty"`
	LastError       string            `json:"last_error,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
}

type ComputeJobList struct {
	Version string             `json:"version"`
	Jobs    []ComputeJobRecord `json:"jobs"`
}

type ComputeQueueStatus struct {
	Queued    int `json:"queued"`
	Leased    int `json:"leased"`
	Completed int `json:"completed"`
	Cancelled int `json:"cancelled"`
	Expired   int `json:"expired"`
}

type Receipt struct {
	Version       string            `json:"version"`
	ReceiptID     string            `json:"receipt_id"`
	JobID         string            `json:"job_id"`
	WorkerID      string            `json:"worker_id"`
	Resource      ResourceKind      `json:"resource"`
	Algorithm     string            `json:"algorithm"`
	Digest        string            `json:"digest"`
	Units         uint64            `json:"units"`
	MemoryMiB     uint64            `json:"memory_mib,omitempty"`
	DurationMS    int64             `json:"duration_ms"`
	CompletedAt   string            `json:"completed_at"`
	AcceptedAt    string            `json:"accepted_at"`
	CoordinatorID string            `json:"coordinator_id"`
	Metadata      map[string]string `json:"metadata,omitempty"`
}

type PoolProof struct {
	Version           string       `json:"version"`
	ProofID           string       `json:"proof_id"`
	CoordinatorID     string       `json:"coordinator_id"`
	Resource          ResourceKind `json:"resource"`
	WindowStart       string       `json:"window_start"`
	WindowEnd         string       `json:"window_end"`
	WorkerCount       int          `json:"worker_count"`
	JobCount          int          `json:"job_count"`
	TotalUnits        uint64       `json:"total_units"`
	TotalMemoryMiB    uint64       `json:"total_memory_mib,omitempty"`
	ReceiptRoot       string       `json:"receipt_root"`
	ReceiptIDs        []string     `json:"receipt_ids"`
	Signature         string       `json:"signature"`
	SettlementVersion string       `json:"settlement_version,omitempty"`
	ContributorWallet string       `json:"contributor_wallet,omitempty"`
	MotherHiveWallet  string       `json:"mother_hive_wallet,omitempty"`
	EcosystemWallet   string       `json:"ecosystem_wallet,omitempty"`
	RelayPublicKey    string       `json:"relay_public_key,omitempty"`
	RelaySignature    string       `json:"relay_signature,omitempty"`
}

// SettlementBinding fixes the three payout roles for a Relay. The binding is
// write-once through the authenticated Mother Hive endpoint; changing any
// wallet requires an explicit local Relay reset instead of a remote request.
type SettlementBinding struct {
	Version           string `json:"version"`
	ContributorWallet string `json:"contributor_wallet"`
	MotherHiveWallet  string `json:"mother_hive_wallet"`
	EcosystemWallet   string `json:"ecosystem_wallet"`
	BoundAt           string `json:"bound_at"`
}

type SettlementBindRequest struct {
	Version           string `json:"version"`
	ContributorWallet string `json:"contributor_wallet"`
	MotherHiveWallet  string `json:"mother_hive_wallet"`
	EcosystemWallet   string `json:"ecosystem_wallet"`
}

type SettlementAckRequest struct {
	Version string `json:"version"`
	ProofID string `json:"proof_id"`
}

type SettlementAckResponse struct {
	OK               bool         `json:"ok"`
	ProofID          string       `json:"proof_id"`
	Resource         ResourceKind `json:"resource"`
	ConsumedReceipts int          `json:"consumed_receipts"`
	AcknowledgedAt   string       `json:"acknowledged_at"`
}

type WorkerStatus struct {
	WorkerID      string       `json:"worker_id"`
	Hostname      string       `json:"hostname"`
	AgentVersion  string       `json:"agent_version"`
	Capabilities  Capabilities `json:"capabilities"`
	RegisteredAt  string       `json:"registered_at"`
	LastSeenAt    string       `json:"last_seen_at"`
	CompletedJobs uint64       `json:"completed_jobs"`
	RejectedJobs  uint64       `json:"rejected_jobs"`
}

// RelayPolicy is the resource ceiling enforced between agents and Mother Hive.
// Percentages scale the per-job limits after the agent's own lower limits are
// applied, so neither side can force an agent above what it offered.
type RelayPolicy struct {
	CPUPercent int    `json:"cpu_percent"`
	GPUPercent int    `json:"gpu_percent"`
	RAMPercent int    `json:"ram_percent"`
	CPUUnits   uint64 `json:"cpu_units_per_job"`
	GPUUnits   uint64 `json:"gpu_units_per_job"`
	RAMMiB     uint64 `json:"ram_mib_per_job"`
}

type PoolStatus struct {
	Version                 string                  `json:"version"`
	CoordinatorID           string                  `json:"coordinator_id"`
	RelayID                 string                  `json:"relay_id"`
	Role                    string                  `json:"role"`
	Policy                  RelayPolicy             `json:"policy"`
	MotherSeenAt            string                  `json:"mother_hive_last_seen_at,omitempty"`
	StartedAt               string                  `json:"started_at"`
	Workers                 []WorkerStatus          `json:"workers"`
	ActiveLeases            int                     `json:"active_leases"`
	ReceiptCounts           map[ResourceKind]uint64 `json:"receipt_counts"`
	SettlementReady         bool                    `json:"settlement_ready"`
	SettlementRelayID       string                  `json:"settlement_relay_id,omitempty"`
	SettlementPublicKey     string                  `json:"settlement_public_key,omitempty"`
	SettlementBinding       *SettlementBinding      `json:"settlement_binding,omitempty"`
	PendingSettlementProofs map[ResourceKind]string `json:"pending_settlement_proofs,omitempty"`
	ComputeQueue            ComputeQueueStatus      `json:"compute_queue"`
}

func ValidateWorkerID(workerID string) error {
	if !workerIDPattern.MatchString(workerID) {
		return errors.New("worker_id must contain 1-64 letters, numbers, dots, underscores, or hyphens")
	}
	return nil
}

func ValidateComputeRequestID(requestID string) error {
	if !computeRequestIDPattern.MatchString(requestID) {
		return errors.New("client_request_id must contain 8-128 letters, numbers, dots, underscores, or hyphens")
	}
	return nil
}

func GenerateToken() ([]byte, error) {
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		return nil, fmt.Errorf("generate token: %w", err)
	}
	return token, nil
}

func LoadTokenFile(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read token file: %w", err)
	}
	trimmed := strings.TrimSpace(string(raw))
	if decoded, decodeErr := hex.DecodeString(trimmed); decodeErr == nil && len(decoded) >= 32 {
		return decoded, nil
	}
	if len(trimmed) < 32 {
		return nil, errors.New("token file must contain at least 32 bytes or 64 hexadecimal characters")
	}
	return []byte(trimmed), nil
}

func WriteTokenFile(path string, token []byte) error {
	if len(token) < 32 {
		return errors.New("token must contain at least 32 bytes")
	}
	if err := os.WriteFile(path, []byte(hex.EncodeToString(token)+"\n"), 0o600); err != nil {
		return fmt.Errorf("write token file: %w", err)
	}
	return nil
}

func BodyHash(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func RequestSignature(token []byte, method, path, timestamp, nonce, workerID string, body []byte) string {
	canonical := strings.Join([]string{
		strings.ToUpper(method),
		path,
		timestamp,
		nonce,
		workerID,
		BodyHash(body),
	}, "\n")
	mac := hmac.New(sha256.New, token)
	_, _ = mac.Write([]byte(canonical))
	return hex.EncodeToString(mac.Sum(nil))
}

func VerifyRequestSignature(token []byte, signature, method, path, timestamp, nonce, workerID string, body []byte) bool {
	provided, err := hex.DecodeString(signature)
	if err != nil {
		return false
	}
	expected, err := hex.DecodeString(RequestSignature(token, method, path, timestamp, nonce, workerID, body))
	return err == nil && hmac.Equal(provided, expected)
}

func SignJob(token []byte, job Job) (string, error) {
	job.Signature = ""
	raw, err := json.Marshal(job)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, token)
	_, _ = mac.Write(raw)
	return hex.EncodeToString(mac.Sum(nil)), nil
}

func VerifyJob(token []byte, job Job) bool {
	provided, err := hex.DecodeString(job.Signature)
	if err != nil {
		return false
	}
	expectedHex, err := SignJob(token, job)
	if err != nil {
		return false
	}
	expected, err := hex.DecodeString(expectedHex)
	return err == nil && hmac.Equal(provided, expected)
}

func PoolProofSignature(token []byte, proof PoolProof) string {
	canonical := strings.Join([]string{
		proof.Version,
		proof.ProofID,
		proof.CoordinatorID,
		string(proof.Resource),
		proof.WindowStart,
		proof.WindowEnd,
		fmt.Sprint(proof.WorkerCount),
		fmt.Sprint(proof.JobCount),
		fmt.Sprint(proof.TotalUnits),
		fmt.Sprint(proof.TotalMemoryMiB),
		proof.ReceiptRoot,
		strings.Join(proof.ReceiptIDs, ","),
	}, "\n")
	mac := hmac.New(sha256.New, token)
	_, _ = mac.Write([]byte(canonical))
	return hex.EncodeToString(mac.Sum(nil))
}

func VerifyPoolProof(token []byte, proof PoolProof) bool {
	provided, err := hex.DecodeString(proof.Signature)
	if err != nil {
		return false
	}
	expected, err := hex.DecodeString(PoolProofSignature(token, proof))
	return err == nil && hmac.Equal(provided, expected)
}

func AggregateReceipts(coordinatorID string, resource ResourceKind, receipts []Receipt, now time.Time) PoolProof {
	sorted := append([]Receipt(nil), receipts...)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].ReceiptID < sorted[j].ReceiptID
	})

	workers := map[string]struct{}{}
	receiptIDs := make([]string, 0, len(sorted))
	totalUnits := uint64(0)
	totalMemory := uint64(0)
	windowStart := now.UTC()
	windowEnd := time.Time{}
	h := sha256.New()
	for _, receipt := range sorted {
		if receipt.Resource != resource {
			continue
		}
		workers[receipt.WorkerID] = struct{}{}
		receiptIDs = append(receiptIDs, receipt.ReceiptID)
		totalUnits += receipt.Units
		totalMemory += receipt.MemoryMiB
		_, _ = h.Write([]byte(receipt.ReceiptID))
		_, _ = h.Write([]byte(receipt.Digest))
		if accepted, err := time.Parse(time.RFC3339Nano, receipt.AcceptedAt); err == nil {
			if accepted.Before(windowStart) {
				windowStart = accepted
			}
			if accepted.After(windowEnd) {
				windowEnd = accepted
			}
		}
	}
	if windowEnd.IsZero() {
		windowEnd = now.UTC()
	}
	root := hex.EncodeToString(h.Sum(nil))
	proofHash := sha256.Sum256([]byte(strings.Join([]string{
		ProtocolVersion,
		coordinatorID,
		string(resource),
		root,
		fmt.Sprint(totalUnits),
	}, ":")))

	return PoolProof{
		Version:        ProtocolVersion,
		ProofID:        hex.EncodeToString(proofHash[:]),
		CoordinatorID:  coordinatorID,
		Resource:       resource,
		WindowStart:    windowStart.Format(time.RFC3339Nano),
		WindowEnd:      windowEnd.Format(time.RFC3339Nano),
		WorkerCount:    len(workers),
		JobCount:       len(receiptIDs),
		TotalUnits:     totalUnits,
		TotalMemoryMiB: totalMemory,
		ReceiptRoot:    root,
		ReceiptIDs:     receiptIDs,
	}
}
