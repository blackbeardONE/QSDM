package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mining"
)

const defaultCUDABatchSize uint64 = 1 << 16

type cudaSolver struct {
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	stdout     *bufio.Reader
	mu         sync.Mutex
	deviceName string
	computeCap string
	closed     bool
}

func resolveCUDASolverPath(explicit string) (string, error) {
	name := "qsdm-miner-cuda-solver"
	if os.PathSeparator == '\\' {
		name += ".exe"
	}

	var candidates []string
	if value := strings.TrimSpace(explicit); value != "" {
		candidates = append(candidates, value)
	}
	if value := strings.TrimSpace(os.Getenv("QSDM_MINER_CUDA_SOLVER")); value != "" {
		candidates = append(candidates, value)
	}
	if executable, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(executable), name))
	}
	candidates = append(candidates, filepath.Join(".", name))

	for _, candidate := range candidates {
		absolute, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		info, err := os.Stat(absolute)
		if err == nil && !info.IsDir() {
			return absolute, nil
		}
	}
	return "", fmt.Errorf("CUDA proof solver %q was not found beside qsdmminer-console; reinstall the latest QSDM Hive", name)
}

func startCUDASolver(ctx context.Context, path string) (*cudaSolver, error) {
	cmd := exec.CommandContext(ctx, path, "--server")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("cuda solver stdin: %w", err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("cuda solver stdout: %w", err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("start CUDA proof solver: %w", err)
	}

	solver := &cudaSolver{
		cmd:    cmd,
		stdin:  stdin,
		stdout: bufio.NewReader(stdoutPipe),
	}
	startupCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	for {
		line, err := solver.readLine(startupCtx)
		if err != nil {
			_ = solver.Close()
			return nil, fmt.Errorf("CUDA proof solver startup: %w", err)
		}
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[0] == "READY" {
			nameBytes, decodeErr := hex.DecodeString(fields[1])
			if decodeErr != nil {
				_ = solver.Close()
				return nil, fmt.Errorf("CUDA proof solver returned an invalid device name: %w", decodeErr)
			}
			solver.deviceName = string(nameBytes)
			solver.computeCap = fields[2]
			return solver, nil
		}
		if len(fields) > 0 && fields[0] == "ERR" {
			_ = solver.Close()
			return nil, errors.New(strings.TrimSpace(strings.TrimPrefix(line, "ERR")))
		}
		// The helper performs a mini-DAG conformance test before READY and
		// reports its internal INIT result. Ignore that one startup line.
	}
}

func (s *cudaSolver) DeviceName() string { return s.deviceName }

func (s *cudaSolver) ComputeCapability() string { return s.computeCap }

func (s *cudaSolver) InitDAG(ctx context.Context, epoch uint64, root [32]byte, entries uint32) error {
	line, err := s.command(ctx, fmt.Sprintf("INIT %d %x %d", epoch, root, entries))
	if err != nil {
		return err
	}
	fields := strings.Fields(line)
	if len(fields) < 6 || fields[0] != "OK" || fields[1] != "INIT" {
		return fmt.Errorf("unexpected CUDA INIT response: %s", line)
	}
	return nil
}

func (s *cudaSolver) Solve(
	ctx context.Context,
	p mining.SolverParams,
	startNonce *[16]byte,
	batchSize uint64,
	attemptsSink *uint64,
) (*mining.SolveResult, error) {
	if p.MinerAddr == "" || p.BatchCount == 0 || p.Target == nil || p.Target.Sign() <= 0 {
		return nil, errors.New("mining: CUDA solver received invalid work parameters")
	}
	if mining.IsV2TC(p.Height) {
		return nil, errors.New("mining: CUDA SHA3 solver cannot mine after the Tensor-Core fork")
	}
	if batchSize == 0 {
		batchSize = defaultCUDABatchSize
	}
	if batchSize > 4*1024*1024 {
		return nil, fmt.Errorf("mining: CUDA batch size %d exceeds 4194304", batchSize)
	}

	var nonce [16]byte
	if startNonce != nil {
		nonce = *startNonce
	} else if _, err := rand.Read(nonce[:]); err != nil {
		return nil, fmt.Errorf("mining: draw CUDA nonce: %w", err)
	}

	targetBytes := make([]byte, 32)
	p.Target.FillBytes(targetBytes)
	result := &mining.SolveResult{StartedAt: time.Now().UnixMilli()}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		line, err := s.command(ctx, fmt.Sprintf(
			"SOLVE %x %x %x %x %d",
			p.HeaderHash,
			p.BatchRoot,
			targetBytes,
			nonce,
			batchSize,
		))
		if err != nil {
			return nil, err
		}
		fields := strings.Fields(line)
		if len(fields) != 8 || fields[0] != "OK" || fields[1] != "SOLVE" {
			return nil, fmt.Errorf("unexpected CUDA SOLVE response: %s", line)
		}
		attempted, err := strconv.ParseUint(fields[6], 10, 64)
		if err != nil || attempted == 0 {
			return nil, fmt.Errorf("invalid CUDA attempt count in response: %s", line)
		}
		result.Attempts += attempted
		if attemptsSink != nil {
			atomic.AddUint64(attemptsSink, attempted)
		}
		if fields[2] == "0" {
			incrementNonceBy(&nonce, attempted)
			continue
		}
		if fields[2] != "1" {
			return nil, fmt.Errorf("invalid CUDA found flag in response: %s", line)
		}

		var foundNonce [16]byte
		err = decodeFixedHex(fields[3], foundNonce[:])
		if err != nil {
			return nil, fmt.Errorf("decode CUDA nonce: %w", err)
		}
		var mix [32]byte
		err = decodeFixedHex(fields[4], mix[:])
		if err != nil {
			return nil, fmt.Errorf("decode CUDA mix digest: %w", err)
		}
		var reportedHash [32]byte
		err = decodeFixedHex(fields[5], reportedHash[:])
		if err != nil {
			return nil, fmt.Errorf("decode CUDA PoW hash: %w", err)
		}
		powHash := mining.ProofPoWHash(p.HeaderHash, foundNonce, p.BatchRoot, mix)
		if powHash != reportedHash || !mining.MeetsTarget(powHash, p.Target) {
			return nil, errors.New("mining: CUDA solver returned a proof that failed host target verification")
		}
		result.Proof = &mining.Proof{
			Version:    mining.ProtocolVersion,
			Epoch:      p.Epoch,
			Height:     p.Height,
			HeaderHash: p.HeaderHash,
			MinerAddr:  p.MinerAddr,
			BatchRoot:  p.BatchRoot,
			BatchCount: p.BatchCount,
			Nonce:      foundNonce,
			MixDigest:  mix,
		}
		result.FoundAt = time.Now().UnixMilli()
		return result, nil
	}
}

func (s *cudaSolver) command(ctx context.Context, command string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return "", errors.New("CUDA proof solver is closed")
	}
	if _, err := io.WriteString(s.stdin, command+"\n"); err != nil {
		return "", fmt.Errorf("write CUDA command: %w", err)
	}
	line, err := s.readLine(ctx)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(line, "ERR ") {
		return "", errors.New(strings.TrimSpace(strings.TrimPrefix(line, "ERR ")))
	}
	return line, nil
}

func (s *cudaSolver) readLine(ctx context.Context) (string, error) {
	type response struct {
		line string
		err  error
	}
	result := make(chan response, 1)
	go func() {
		line, err := s.stdout.ReadString('\n')
		result <- response{line: strings.TrimSpace(line), err: err}
	}()
	select {
	case <-ctx.Done():
		if s.cmd.Process != nil {
			_ = s.cmd.Process.Kill()
		}
		return "", ctx.Err()
	case value := <-result:
		if value.err != nil {
			return "", value.err
		}
		return value.line, nil
	}
}

func (s *cudaSolver) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	_, _ = io.WriteString(s.stdin, "QUIT\n")
	_ = s.stdin.Close()
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	return s.cmd.Wait()
}

func decodeFixedHex(value string, output []byte) error {
	raw, err := hex.DecodeString(value)
	if err != nil {
		return err
	}
	if len(raw) != len(output) {
		return fmt.Errorf("got %d bytes, want %d", len(raw), len(output))
	}
	copy(output, raw)
	return nil
}

func incrementNonceBy(nonce *[16]byte, increment uint64) {
	carry := increment
	for i := 0; i < len(nonce) && carry != 0; i++ {
		sum := uint64(nonce[i]) + (carry & 0xff)
		nonce[i] = byte(sum)
		carry = (carry >> 8) + (sum >> 8)
	}
}
