package edgepool

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const (
	AlgorithmCPU = "sha256-chain-v1"
	AlgorithmGPU = "cuda-splitmix64-v1"
	AlgorithmRAM = "ram-splitmix64-v1"

	MaxCPUUnits uint64 = 20_000_000
	MaxGPUUnits uint64 = 100_000_000
	// MaxRAMMiB is the memory ceiling for one verified RAM job. Relay-side
	// verification repeats the allocation, so this remains deliberately lower
	// than the total capacity an Agent may offer.
	MaxRAMMiB uint64 = 1024
	// MaxAgentRAMMiB bounds advertised aggregate capacity independently from
	// the per-job ceiling. It also matches the Relay capability validator.
	MaxAgentRAMMiB uint64 = 1024 * 1024
)

type GPUHelperOutput struct {
	Digest     string `json:"digest"`
	XORValue   string `json:"xor_value,omitempty"`
	SumValue   string `json:"sum_value,omitempty"`
	GPUName    string `json:"gpu_name,omitempty"`
	GPUUUID    string `json:"gpu_uuid,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
	Units      uint64 `json:"units"`
}

func ValidateJob(job Job) error {
	if job.Version != ProtocolVersion {
		return fmt.Errorf("unsupported job version %q", job.Version)
	}
	if job.ID == "" {
		return errors.New("job id is required")
	}
	if err := ValidateWorkerID(job.WorkerID); err != nil {
		return err
	}
	if !job.Resource.Valid() {
		return fmt.Errorf("unsupported resource %q", job.Resource)
	}
	seed, err := hex.DecodeString(job.Seed)
	if err != nil || len(seed) != 32 {
		return errors.New("job seed must be 32 bytes of hexadecimal data")
	}
	if job.Units == 0 {
		return errors.New("job units must be positive")
	}
	switch job.Resource {
	case ResourceCPU:
		if job.Algorithm != AlgorithmCPU || job.Units > MaxCPUUnits {
			return errors.New("CPU job exceeds the supported algorithm or unit limit")
		}
	case ResourceGPU:
		if job.Algorithm != AlgorithmGPU || job.Units > MaxGPUUnits {
			return errors.New("GPU job exceeds the supported algorithm or unit limit")
		}
	case ResourceRAM:
		if job.Algorithm != AlgorithmRAM || job.MemoryMiB == 0 || job.MemoryMiB > MaxRAMMiB {
			return errors.New("RAM job exceeds the supported algorithm or memory limit")
		}
		expectedUnits := job.MemoryMiB * 1024 * 1024
		if job.Units != expectedUnits {
			return errors.New("RAM job units must equal the requested memory size in bytes")
		}
	}
	return nil
}

func ComputeJobDigest(job Job) (string, error) {
	return ComputeJobDigestContext(context.Background(), job)
}

func ComputeJobDigestContext(ctx context.Context, job Job) (string, error) {
	if err := ValidateJob(job); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	seed, _ := hex.DecodeString(job.Seed)
	switch job.Resource {
	case ResourceCPU:
		return computeCPUDigest(ctx, seed, job.Units)
	case ResourceGPU:
		return computeGPUDigest(ctx, seed, job.Units)
	case ResourceRAM:
		return computeRAMDigest(ctx, seed, job.MemoryMiB)
	default:
		return "", fmt.Errorf("unsupported resource %q", job.Resource)
	}
}

func VerifyJobResult(job Job, result JobResult) error {
	return VerifyJobResultContext(context.Background(), job, result)
}

func VerifyJobResultContext(ctx context.Context, job Job, result JobResult) error {
	if result.Version != ProtocolVersion {
		return errors.New("result version mismatch")
	}
	if result.JobID != job.ID || result.WorkerID != job.WorkerID {
		return errors.New("result job or worker does not match the lease")
	}
	if result.Resource != job.Resource || result.Algorithm != job.Algorithm {
		return errors.New("result resource or algorithm does not match the lease")
	}
	if result.Units != job.Units || result.MemoryMiB != job.MemoryMiB {
		return errors.New("result resource accounting does not match the lease")
	}
	if result.DurationMS <= 0 {
		return errors.New("result duration must be positive")
	}
	completedAt, err := time.Parse(time.RFC3339Nano, result.Completed)
	if err != nil || completedAt.IsZero() {
		return errors.New("result completion time is invalid")
	}
	expected, err := ComputeJobDigestContext(ctx, job)
	if err != nil {
		return err
	}
	if !strings.EqualFold(expected, result.Digest) {
		return errors.New("result digest failed deterministic verification")
	}
	return nil
}

func ExecuteJob(ctx context.Context, job Job, gpuHelperPath string) (JobResult, error) {
	if err := ValidateJob(job); err != nil {
		return JobResult{}, err
	}
	started := time.Now()
	metadata := map[string]string{}
	var digest string
	var err error
	if job.Resource == ResourceGPU {
		var helper GPUHelperOutput
		helper, metadata, err = executeGPUHelper(ctx, gpuHelperPath, job)
		digest = helper.Digest
	} else {
		digest, err = ComputeJobDigestContext(ctx, job)
	}
	if err != nil {
		return JobResult{}, err
	}
	duration := time.Since(started).Milliseconds()
	if duration < 1 {
		duration = 1
	}
	return JobResult{
		Version:    ProtocolVersion,
		JobID:      job.ID,
		WorkerID:   job.WorkerID,
		Resource:   job.Resource,
		Algorithm:  job.Algorithm,
		Digest:     digest,
		Units:      job.Units,
		MemoryMiB:  job.MemoryMiB,
		DurationMS: duration,
		Completed:  time.Now().UTC().Format(time.RFC3339Nano),
		Metadata:   metadata,
	}, nil
}

func computeCPUDigest(ctx context.Context, seed []byte, units uint64) (string, error) {
	digest := sha256.Sum256(seed)
	var counter [8]byte
	for i := uint64(0); i < units; i++ {
		if i&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}
		binary.LittleEndian.PutUint64(counter[:], i)
		h := sha256.New()
		_, _ = h.Write(digest[:])
		_, _ = h.Write(counter[:])
		copy(digest[:], h.Sum(nil))
	}
	return hex.EncodeToString(digest[:]), nil
}

func splitmix64(value uint64) uint64 {
	value += 0x9e3779b97f4a7c15
	value = (value ^ (value >> 30)) * 0xbf58476d1ce4e5b9
	value = (value ^ (value >> 27)) * 0x94d049bb133111eb
	return value ^ (value >> 31)
}

func computeGPUDigest(ctx context.Context, seed []byte, units uint64) (string, error) {
	seed64 := binary.LittleEndian.Uint64(seed[:8])
	xorValue := uint64(0)
	sumValue := uint64(0)
	for i := uint64(0); i < units; i++ {
		if i&65535 == 0 {
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}
		value := splitmix64(seed64 + i)
		xorValue ^= value
		sumValue += value
	}
	return gpuDigest(seed, units, xorValue, sumValue), nil
}

func gpuDigest(seed []byte, units, xorValue, sumValue uint64) string {
	h := sha256.New()
	_, _ = h.Write([]byte("qsdm-edge-gpu-v1"))
	_, _ = h.Write(seed)
	var values [24]byte
	binary.LittleEndian.PutUint64(values[0:8], units)
	binary.LittleEndian.PutUint64(values[8:16], xorValue)
	binary.LittleEndian.PutUint64(values[16:24], sumValue)
	_, _ = h.Write(values[:])
	return hex.EncodeToString(h.Sum(nil))
}

func computeRAMDigest(ctx context.Context, seed []byte, memoryMiB uint64) (string, error) {
	size := memoryMiB * 1024 * 1024
	buffer := make([]byte, int(size))
	seed64 := binary.LittleEndian.Uint64(seed[:8])
	for offset := uint64(0); offset+8 <= size; offset += 8 {
		if offset%(1024*1024) == 0 {
			if err := ctx.Err(); err != nil {
				clear(buffer)
				return "", err
			}
		}
		binary.LittleEndian.PutUint64(buffer[offset:offset+8], splitmix64(seed64+(offset/8)))
	}
	digest := sha256.Sum256(buffer)
	clear(buffer)
	return hex.EncodeToString(digest[:]), nil
}

func executeGPUHelper(ctx context.Context, helperPath string, job Job) (GPUHelperOutput, map[string]string, error) {
	if strings.TrimSpace(helperPath) == "" {
		return GPUHelperOutput{}, nil, errors.New("GPU resource requested but no trusted GPU helper is configured")
	}
	info, err := os.Stat(helperPath)
	if err != nil || info.IsDir() {
		return GPUHelperOutput{}, nil, fmt.Errorf("GPU helper is unavailable at %q", helperPath)
	}
	helperHash, err := fileSHA256(helperPath)
	if err != nil {
		return GPUHelperOutput{}, nil, err
	}
	command := exec.CommandContext(ctx, helperPath,
		"--seed", job.Seed,
		"--units", strconv.FormatUint(job.Units, 10),
		"--json",
	)
	output, err := command.Output()
	if err != nil {
		return GPUHelperOutput{}, nil, fmt.Errorf("GPU helper failed: %w", err)
	}
	var result GPUHelperOutput
	if err := json.Unmarshal(output, &result); err != nil {
		return GPUHelperOutput{}, nil, fmt.Errorf("decode GPU helper output: %w", err)
	}
	if result.Units != job.Units {
		return GPUHelperOutput{}, nil, errors.New("GPU helper returned invalid resource accounting")
	}
	seed, _ := hex.DecodeString(job.Seed)
	xorValue, xorErr := strconv.ParseUint(strings.TrimSpace(result.XORValue), 16, 64)
	sumValue, sumErr := strconv.ParseUint(strings.TrimSpace(result.SumValue), 16, 64)
	if xorErr != nil || sumErr != nil {
		return GPUHelperOutput{}, nil, errors.New("GPU helper returned invalid aggregate values")
	}
	result.Digest = gpuDigest(seed, job.Units, xorValue, sumValue)
	metadata := map[string]string{
		"gpu_helper_sha256": helperHash,
	}
	if result.GPUName != "" {
		metadata["gpu_name"] = result.GPUName
	}
	if result.GPUUUID != "" {
		metadata["gpu_uuid"] = result.GPUUUID
	}
	return result, metadata, nil
}

func fileSHA256(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("hash helper: %w", err)
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}
