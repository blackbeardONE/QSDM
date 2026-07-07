package main

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mining"
)

func TestCUDASolverMatchesCanonicalGoDigest(t *testing.T) {
	helper := os.Getenv("QSDM_CUDA_SOLVER_TEST_EXE")
	if helper == "" {
		t.Skip("QSDM_CUDA_SOLVER_TEST_EXE is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	solver, err := startCUDASolver(ctx, helper)
	if err != nil {
		t.Fatalf("start CUDA solver: %v", err)
	}
	defer solver.Close()

	ws := syntheticWorkSet(4)
	const dagSize = 128
	const epoch = 7
	if err := solver.InitDAG(ctx, epoch, ws.Root(), dagSize); err != nil {
		t.Fatalf("initialize CUDA DAG: %v", err)
	}
	dag, err := mining.NewInMemoryDAG(epoch, ws.Root(), dagSize)
	if err != nil {
		t.Fatalf("build canonical DAG: %v", err)
	}
	batchRoot, err := ws.PrefixRoot(1)
	if err != nil {
		t.Fatalf("batch root: %v", err)
	}
	target, err := mining.TargetFromDifficulty(mining.DefaultMinDifficulty)
	if err != nil {
		t.Fatalf("target: %v", err)
	}
	params := mining.SolverParams{
		Epoch:      epoch,
		Height:     1,
		HeaderHash: [32]byte{0x5e, 0x1f, 0x7e, 0x57},
		MinerAddr:  "qsdm1cuda-conformance",
		BatchRoot:  batchRoot,
		BatchCount: 1,
		Target:     target,
	}
	// One protocol-floor launch is large enough to exercise sustained CUDA
	// execution while remaining bounded on the minimum supported Turing GPU.
	result, err := solver.Solve(ctx, params, nil, defaultCUDABatchSize, nil)
	if err != nil {
		t.Fatalf("CUDA solve: %v", err)
	}
	canonicalMix, err := mining.ComputeMixDigest(
		params.HeaderHash,
		result.Proof.Nonce,
		dag,
	)
	if err != nil {
		t.Fatalf("canonical digest: %v", err)
	}
	if canonicalMix != result.Proof.MixDigest {
		t.Fatalf("CUDA mix digest differs from canonical Go digest: got %x want %x", result.Proof.MixDigest, canonicalMix)
	}
	canonicalHash := mining.ProofPoWHash(
		params.HeaderHash,
		result.Proof.Nonce,
		params.BatchRoot,
		canonicalMix,
	)
	if !mining.MeetsTarget(canonicalHash, target) {
		t.Fatalf("CUDA proof does not meet canonical target: %x", canonicalHash)
	}
}
