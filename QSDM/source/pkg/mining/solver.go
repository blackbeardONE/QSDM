package mining

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"sync/atomic"

	powv2 "github.com/blackbeardONE/QSDM/pkg/mining/pow/v2"
)

// SolverParams bundles everything a miner needs to search for a nonce.
// A SolverParams is single-use: one target block height, one header,
// one work-set prefix.
type SolverParams struct {
	Epoch      uint64
	Height     uint64
	HeaderHash [32]byte
	MinerAddr  string
	BatchRoot  [32]byte
	BatchCount uint32
	Target     *big.Int
	DAG        DAG
}

// Validate rejects obviously-wrong solver inputs before the miner burns
// cycles on a search that can never succeed.
func (p SolverParams) Validate() error {
	if p.MinerAddr == "" {
		return errors.New("mining: solver requires MinerAddr")
	}
	if p.BatchCount == 0 {
		return errors.New("mining: solver requires BatchCount >= 1")
	}
	if p.Target == nil || p.Target.Sign() <= 0 {
		return errors.New("mining: solver requires positive Target")
	}
	if p.DAG == nil || p.DAG.N() < 2 {
		return errors.New("mining: solver requires a DAG with N >= 2")
	}
	return nil
}

// SolveResult is what Solve returns on a hit.
type SolveResult struct {
	Proof     *Proof
	Attempts  uint64
	StartedAt int64
	FoundAt   int64
}

// Solve searches for a nonce that satisfies the PoW inequality. It is
// deliberately single-threaded so the reference miner (cmd/qsdmminer) is
// trivially correct; production-grade miners fan out their own workers
// and call Solve with pre-partitioned nonce spaces via NonceRange.
//
// Respects ctx cancellation between attempts. Returns (nil, ctx.Err())
// if ctx is cancelled before a nonce is found.
//
// `startNonce` lets callers checkpoint progress across restarts. If
// startNonce is nil, a fresh random 16-byte nonce is drawn from
// crypto/rand.
//
// `attemptsSink`, when non-nil, is atomically incremented once per hash
// attempt so an external dashboard can read live hashrate without
// interrupting the solver loop.
func Solve(ctx context.Context, p SolverParams, startNonce *[16]byte, attemptsSink *uint64) (*SolveResult, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}

	var nonce [16]byte
	if startNonce != nil {
		nonce = *startNonce
	} else {
		if _, err := rand.Read(nonce[:]); err != nil {
			return nil, fmt.Errorf("mining: draw nonce: %w", err)
		}
	}

	res := &SolveResult{}
	for {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// Mix-digest algorithm switches on FORK_V2_TC_HEIGHT
		// (MINING_PROTOCOL_V2 §4). Pre-TC: the v1 SHA3 walk; at-or-
		// above the TC height: the byte-exact Tensor-Core mixin in
		// pkg/mining/pow/v2. Mining the wrong algorithm for the
		// target height produces a digest the verifier will reject
		// at step 10, so we dispatch here to keep the reference
		// solver useful on both sides of the fork boundary.
		var mix [32]byte
		var err error
		if IsV2TC(p.Height) {
			mix, err = powv2.ComputeMixDigestV2(p.HeaderHash, nonce, p.DAG)
		} else {
			mix, err = ComputeMixDigest(p.HeaderHash, nonce, p.DAG)
		}
		if err != nil {
			return nil, err
		}
		h := ProofPoWHash(p.HeaderHash, nonce, p.BatchRoot, mix)
		res.Attempts++
		if attemptsSink != nil {
			atomic.AddUint64(attemptsSink, 1)
		}
		if MeetsTarget(h, p.Target) {
			res.Proof = &Proof{
				Version:    ProtocolVersion,
				Epoch:      p.Epoch,
				Height:     p.Height,
				HeaderHash: p.HeaderHash,
				MinerAddr:  p.MinerAddr,
				BatchRoot:  p.BatchRoot,
				BatchCount: p.BatchCount,
				Nonce:      nonce,
				MixDigest:  mix,
			}
			return res, nil
		}
		incrementNonce(&nonce)
	}
}

// incrementNonce treats the 16-byte nonce as a little-endian uint128 and
// adds 1 (wrapping on overflow, but 2^128 attempts will not occur inside
// a human lifetime so the wrap is benign).
func incrementNonce(n *[16]byte) {
	for i := 0; i < len(n); i++ {
		n[i]++
		if n[i] != 0 {
			return
		}
	}
}
