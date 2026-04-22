package mining

import (
	"errors"
	"fmt"
	"math/big"
)

// Difficulty retargeting (MINING_PROTOCOL.md §8.2).
//
// Retargeting occurs every R blocks. At a retarget height h:
//
//	actual_time  := block_timestamp(h) - block_timestamp(h - R)
//	target_time  := R * T                 # T = target block time seconds
//	D_{h+1}      := D_h * target_time / actual_time
//
// Then clamp:
//
//	D_{h+1} := max(D_h / 4, min(D_h * 4, D_{h+1}))
//	D_{h+1} := max(D_min, D_{h+1})
//
// Everything goes through math/big.Int because intermediate products
// overflow uint64 on modest difficulties.

// Retarget parameters are public so pkg/api and the dashboard can quote
// them without stamping out magic numbers in several places.
const (
	// DefaultRetargetInterval is `R` from the spec: 1008 blocks ≈ 2.8 h at
	// 10 s blocks. Short enough that hashrate spikes are absorbed within a
	// few hours; long enough that a single-block timestamp tick does not
	// send the difficulty into either clamp.
	DefaultRetargetInterval uint64 = 1008

	// DefaultTargetBlockTimeSeconds matches pkg/chain/emission's default.
	// Re-declared here to keep pkg/mining free of a cross-package import
	// that would pull pkg/chain into every miner.
	DefaultTargetBlockTimeSeconds uint64 = 10

	// DefaultClampFactor is the ±4× bound per retarget.
	DefaultClampFactor int64 = 4
)

// DefaultMinDifficulty is D_min = 2^16 from MINING_PROTOCOL.md §8.2.
// Chosen large enough to keep the first-ever retarget stable and small
// enough that a laptop can still mine a test block on a regtest chain.
var DefaultMinDifficulty = new(big.Int).Lsh(big.NewInt(1), 16)

// MaxTarget is 2^256 - 1, used by TargetFromDifficulty as the numerator.
var maxTarget256 = func() *big.Int {
	x := new(big.Int).Lsh(big.NewInt(1), 256)
	return new(big.Int).Sub(x, big.NewInt(1))
}()

// DifficultyAdjusterParams configures a retargeting engine. Zero value is
// not valid; always construct via NewDifficultyAdjusterParams.
type DifficultyAdjusterParams struct {
	// RetargetInterval is `R`. MUST be > 0.
	RetargetInterval uint64
	// TargetBlockTimeSeconds is `T`. MUST be > 0.
	TargetBlockTimeSeconds uint64
	// ClampFactor is the ±N× bound per retarget. MUST be >= 2. Canonical
	// value is 4.
	ClampFactor int64
	// MinDifficulty is D_min. Never returned below this floor. MUST be > 0.
	MinDifficulty *big.Int
}

// NewDifficultyAdjusterParams returns the reference parameters.
func NewDifficultyAdjusterParams() DifficultyAdjusterParams {
	return DifficultyAdjusterParams{
		RetargetInterval:       DefaultRetargetInterval,
		TargetBlockTimeSeconds: DefaultTargetBlockTimeSeconds,
		ClampFactor:            DefaultClampFactor,
		MinDifficulty:          new(big.Int).Set(DefaultMinDifficulty),
	}
}

// Validate rejects malformed parameter sets.
func (p DifficultyAdjusterParams) Validate() error {
	if p.RetargetInterval == 0 {
		return errors.New("mining: RetargetInterval must be > 0")
	}
	if p.TargetBlockTimeSeconds == 0 {
		return errors.New("mining: TargetBlockTimeSeconds must be > 0")
	}
	if p.ClampFactor < 2 {
		return fmt.Errorf("mining: ClampFactor %d must be >= 2", p.ClampFactor)
	}
	if p.MinDifficulty == nil || p.MinDifficulty.Sign() <= 0 {
		return errors.New("mining: MinDifficulty must be > 0")
	}
	return nil
}

// IsRetargetHeight reports whether a block at `height` is a retarget
// boundary. Height 0 is the genesis; retargets begin at height
// RetargetInterval and recur every RetargetInterval thereafter.
func (p DifficultyAdjusterParams) IsRetargetHeight(height uint64) bool {
	if height == 0 {
		return false
	}
	return height%p.RetargetInterval == 0
}

// Retarget computes D_{h+1} given D_h and the wall-clock spread between
// the block at the start of the current interval (height h - R) and the
// block at the retarget height h. Times are in seconds since Unix epoch,
// from the block headers' own timestamps.
//
// Contract: caller ensures h is a retarget height (i.e. IsRetargetHeight
// returned true) and that startTime < endTime. If endTime <= startTime
// the function returns an error (a malicious block would need this check
// to pass time-warp the chain back to an easy target).
func (p DifficultyAdjusterParams) Retarget(currentDifficulty *big.Int, startTime, endTime int64) (*big.Int, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	if currentDifficulty == nil || currentDifficulty.Sign() <= 0 {
		return nil, errors.New("mining: current difficulty must be > 0")
	}
	if endTime <= startTime {
		return nil, fmt.Errorf("mining: retarget endTime %d must be > startTime %d", endTime, startTime)
	}
	actual := uint64(endTime - startTime)
	target := p.RetargetInterval * p.TargetBlockTimeSeconds

	// D_{h+1} = D_h * target / actual (all in big.Int)
	next := new(big.Int).Mul(currentDifficulty, new(big.Int).SetUint64(target))
	next.Quo(next, new(big.Int).SetUint64(actual))

	// Clamp [D_h / clamp, D_h * clamp]
	clamp := big.NewInt(p.ClampFactor)
	upper := new(big.Int).Mul(currentDifficulty, clamp)
	lower := new(big.Int).Quo(currentDifficulty, clamp)
	if next.Cmp(upper) > 0 {
		next.Set(upper)
	}
	if next.Cmp(lower) < 0 {
		next.Set(lower)
	}

	// Floor at MinDifficulty
	if next.Cmp(p.MinDifficulty) < 0 {
		next.Set(p.MinDifficulty)
	}
	return next, nil
}

// TargetFromDifficulty converts a difficulty scalar into the 256-bit
// target a proof hash must fall below (MINING_PROTOCOL.md §5.2):
//
//	target := floor( 2^256 / difficulty ) - 1
//
// Returns an error if difficulty <= 0.
func TargetFromDifficulty(difficulty *big.Int) (*big.Int, error) {
	if difficulty == nil || difficulty.Sign() <= 0 {
		return nil, errors.New("mining: difficulty must be > 0")
	}
	quo := new(big.Int).Quo(maxTarget256, difficulty)
	// The spec subtracts 1 to guarantee strict-less-than semantics.
	if quo.Sign() == 0 {
		return new(big.Int), nil
	}
	return new(big.Int).Sub(quo, big.NewInt(1)), nil
}

// DifficultyFromTarget is the inverse, used by the reference miner to
// print a human-readable difficulty when it only has a target in hand.
// It is lossy by exactly one: DifficultyFromTarget(TargetFromDifficulty(d))
// returns d for d >= 2 and 2 for d == 1.
func DifficultyFromTarget(target *big.Int) (*big.Int, error) {
	if target == nil || target.Sign() <= 0 {
		return nil, errors.New("mining: target must be > 0")
	}
	incremented := new(big.Int).Add(target, big.NewInt(1))
	return new(big.Int).Quo(maxTarget256, incremented), nil
}
