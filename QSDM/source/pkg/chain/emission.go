package chain

import (
	"errors"
	"fmt"
	"math"
	"math/big"
)

// Emission schedule for the Cell coin.
//
// This file implements the normative per-block reward calculator referenced
// by QSDM/docs/docs/CELL_TOKENOMICS.md §3. It is pure Go, integer-only, and
// deterministic so two validators on different architectures never disagree
// about a block's reward.
//
// Ground truths (do not change without updating CELL_TOKENOMICS.md):
//
//   - Total mining cap:      90,000,000 CELL = 9,000,000,000,000,000 dust
//   - Coin decimals:         8 (1 CELL = 10^8 dust)
//   - Halving period:        every 4 years of wall-clock time
//   - Block time:            configurable (default 10 seconds)
//
// All arithmetic below is performed in `dust` (uint64), which comfortably
// fits the 9e15 cap inside a uint64 (max 1.8e19). No floating-point math
// appears on the reward path.

// Authoritative constants. Changing any of these is a hard fork.
const (
	// CellDecimals is the number of fractional digits in one CELL. Mirrors
	// pkg/branding.CoinDecimals; duplicated here so this file has no
	// cross-package import for a fundamental constant.
	CellDecimals = 8

	// DustPerCell is 10^CellDecimals, i.e. the number of smallest-unit dust
	// in one whole CELL.
	DustPerCell uint64 = 100_000_000

	// CellMiningCapWhole is the mining-emission cap in whole CELL (i.e.
	// excluding the 10 M treasury). The full 100 M total supply is
	// (mining cap + treasury) = 90 M + 10 M = 100 M.
	CellMiningCapWhole uint64 = 90_000_000

	// CellMiningCapDust is the mining cap expressed in dust. Computed at
	// package init as CellMiningCapWhole * DustPerCell.
	CellMiningCapDust uint64 = 9_000_000_000_000_000

	// EpochLengthSeconds is the wall-clock length of a single emission
	// epoch: 4 years, using the Julian year of 365.25 days so leap years
	// are absorbed uniformly. 4 * 365.25 * 86400 = 126,230,400 seconds.
	EpochLengthSeconds uint64 = 126_230_400

	// DefaultTargetBlockTimeSeconds is the reference target block time used
	// when no explicit value is provided. Matches the 10 s figure quoted in
	// Major Update §4.2.
	DefaultTargetBlockTimeSeconds uint64 = 10

	// MaxHalvings is the number of halvings after which the per-block
	// reward is guaranteed to be zero in integer arithmetic. With a 9e15
	// starting budget, 63 halvings drive the allocation below 1 dust.
	MaxHalvings uint32 = 64
)

// compile-time sanity check that CellMiningCapDust == CellMiningCapWhole *
// DustPerCell. We assert it at package init rather than at constant
// declaration because Go does not allow constant initializers that call
// helpers or that depend on arithmetic the compiler considers non-constant
// at the spec level in all contexts — an init-time check is trivial and
// catches accidental edits.
func init() {
	if CellMiningCapDust != CellMiningCapWhole*DustPerCell {
		panic(fmt.Sprintf(
			"emission: CellMiningCapDust (%d) != CellMiningCapWhole*DustPerCell (%d)",
			CellMiningCapDust, CellMiningCapWhole*DustPerCell))
	}
}

// EmissionSchedule describes the parameters needed to derive a per-block
// reward. All fields are public so operator tooling can introspect a
// schedule without running the full derivation.
//
// A zero value is NOT valid; use NewEmissionSchedule (preferred) or
// DefaultEmissionSchedule.
type EmissionSchedule struct {
	// MiningCapDust is the total supply that will ever be emitted through
	// the PoW layer, in dust. Must be positive.
	MiningCapDust uint64

	// EpochLengthSeconds is the wall-clock length of one halving epoch.
	// Must be positive.
	EpochLengthSeconds uint64

	// TargetBlockTimeSeconds is the target time between blocks. Must be
	// positive and must divide EpochLengthSeconds (otherwise the block
	// count per epoch is non-integer and the schedule is ill-defined).
	TargetBlockTimeSeconds uint64

	// BlocksPerEpoch is a derived value cached at construction:
	// EpochLengthSeconds / TargetBlockTimeSeconds. Must be positive.
	BlocksPerEpoch uint64
}

// DefaultEmissionSchedule returns the canonical Cell emission schedule:
// 90 M CELL cap, 4-year halvings, 10-second blocks. This is the schedule
// that ships with mainnet and matches CELL_TOKENOMICS.md §3.
func DefaultEmissionSchedule() EmissionSchedule {
	s, err := NewEmissionSchedule(CellMiningCapDust, EpochLengthSeconds, DefaultTargetBlockTimeSeconds)
	if err != nil {
		// Impossible: the defaults are constants that we verify with tests.
		panic(fmt.Sprintf("emission: default schedule invalid: %v", err))
	}
	return s
}

// NewEmissionSchedule constructs an EmissionSchedule with validation.
// Returns an error if any parameter is zero or the block time does not
// evenly divide the epoch length.
func NewEmissionSchedule(miningCapDust, epochLengthSec, targetBlockTimeSec uint64) (EmissionSchedule, error) {
	if miningCapDust == 0 {
		return EmissionSchedule{}, errors.New("emission: miningCapDust must be > 0")
	}
	if epochLengthSec == 0 {
		return EmissionSchedule{}, errors.New("emission: epochLengthSec must be > 0")
	}
	if targetBlockTimeSec == 0 {
		return EmissionSchedule{}, errors.New("emission: targetBlockTimeSec must be > 0")
	}
	if epochLengthSec%targetBlockTimeSec != 0 {
		return EmissionSchedule{}, fmt.Errorf(
			"emission: targetBlockTimeSec (%d) must divide epochLengthSec (%d)",
			targetBlockTimeSec, epochLengthSec)
	}
	return EmissionSchedule{
		MiningCapDust:          miningCapDust,
		EpochLengthSeconds:     epochLengthSec,
		TargetBlockTimeSeconds: targetBlockTimeSec,
		BlocksPerEpoch:         epochLengthSec / targetBlockTimeSec,
	}, nil
}

// EpochForHeight returns the zero-indexed epoch number for a given block
// height. Height 1 .. BlocksPerEpoch are in epoch 0; heights
// BlocksPerEpoch+1 .. 2*BlocksPerEpoch are in epoch 1; and so on.
//
// A height of 0 is treated as the genesis block and belongs to epoch 0
// (genesis carries no mining reward).
func (s EmissionSchedule) EpochForHeight(height uint64) uint32 {
	if height == 0 {
		return 0
	}
	e := (height - 1) / s.BlocksPerEpoch
	if e >= uint64(MaxHalvings) {
		return MaxHalvings
	}
	return uint32(e)
}

// EpochAllocationDust returns the total dust allocated to the given epoch.
//
// The allocation schedule is:
//
//	epoch 0 gets floor(cap / 2)
//	epoch 1 gets floor(cap / 4)
//	epoch k gets floor(cap / 2^(k+1))
//
// After MaxHalvings the allocation is 0 — emission has asymptotically
// reached cap but cannot exceed it in integer arithmetic.
func (s EmissionSchedule) EpochAllocationDust(epoch uint32) uint64 {
	// For epoch >= 63, 2^(epoch+1) overflows uint64 (shift by >=64 is 0 in
	// Go, which would cause a divide-by-zero). The allocation is
	// guaranteed 0 well before then — cap=9e15 is smaller than 2^53, so
	// cap / 2^53 = 0 and every later epoch is also 0.
	if epoch >= 63 {
		return 0
	}
	divisor := uint64(1) << (uint64(epoch) + 1)
	return s.MiningCapDust / divisor
}

// BlockRewardDust returns the per-block reward, in dust, at the given block
// height. Height 0 (genesis) returns 0.
//
// Integer math note: the per-block reward is
//
//	floor(EpochAllocationDust(epoch) / BlocksPerEpoch)
//
// so each epoch emits EXACTLY reward * BlocksPerEpoch dust. The tiny
// remainder (< BlocksPerEpoch dust per epoch) is intentionally forfeited to
// keep the calculation deterministic and cheap — the schedule gives up at
// most BlocksPerEpoch * MaxHalvings dust (~10^9 dust = 10 CELL across the
// entire 20-year emission window, negligible versus the 90 M cap).
func (s EmissionSchedule) BlockRewardDust(height uint64) uint64 {
	if height == 0 {
		return 0
	}
	epoch := s.EpochForHeight(height)
	if epoch >= MaxHalvings {
		return 0
	}
	alloc := s.EpochAllocationDust(epoch)
	return alloc / s.BlocksPerEpoch
}

// CumulativeEmittedDust returns the total dust emitted through mining up to
// and including the given block height.
//
// This walks at most MaxHalvings (= 64) iterations and is O(1) for any
// realistic block height. It is safe to call in hot paths.
func (s EmissionSchedule) CumulativeEmittedDust(height uint64) uint64 {
	if height == 0 {
		return 0
	}
	var total uint64
	currentEpoch := s.EpochForHeight(height)
	// Full epochs 0 .. currentEpoch-1 emitted their per-block reward across
	// BlocksPerEpoch blocks.
	for e := uint32(0); e < currentEpoch && e < MaxHalvings; e++ {
		reward := s.EpochAllocationDust(e) / s.BlocksPerEpoch
		total += reward * s.BlocksPerEpoch
	}
	// Partial current epoch.
	if currentEpoch < MaxHalvings {
		blocksIntoEpoch := height - uint64(currentEpoch)*s.BlocksPerEpoch
		reward := s.EpochAllocationDust(currentEpoch) / s.BlocksPerEpoch
		total += reward * blocksIntoEpoch
	}
	return total
}

// RemainingSupplyDust returns CellMiningCapDust - CumulativeEmittedDust(height).
// Never returns a negative value; if height exceeds the asymptote, returns
// the unmineable remainder (total integer-truncation residue).
func (s EmissionSchedule) RemainingSupplyDust(height uint64) uint64 {
	emitted := s.CumulativeEmittedDust(height)
	if emitted >= s.MiningCapDust {
		return 0
	}
	return s.MiningCapDust - emitted
}

// NextHalvingHeight returns the block height at which the NEXT halving
// will occur (i.e. the first block of the epoch following the one that
// contains `height`). Returns 0 if height is already past the final halving
// boundary (MaxHalvings).
func (s EmissionSchedule) NextHalvingHeight(height uint64) uint64 {
	currentEpoch := s.EpochForHeight(height)
	if currentEpoch >= MaxHalvings {
		return 0
	}
	return uint64(currentEpoch+1) * s.BlocksPerEpoch
}

// NextHalvingETA returns the number of seconds until the next halving, at
// the schedule's target block time. A return value of 0 means the schedule
// is past the final halving. The returned value is an estimate: real block
// production drifts slightly around the target.
func (s EmissionSchedule) NextHalvingETA(height uint64) uint64 {
	next := s.NextHalvingHeight(height)
	if next == 0 || next <= height {
		return 0
	}
	return (next - height) * s.TargetBlockTimeSeconds
}

// UltimateCumulativeEmissionDust returns the exact dust emitted if
// emission runs to MaxHalvings (i.e. the theoretical asymptote reachable
// in integer math). This is useful for validating the schedule's
// dust-level truncation budget — it should be within
// MaxHalvings*BlocksPerEpoch of MiningCapDust.
func (s EmissionSchedule) UltimateCumulativeEmissionDust() uint64 {
	var total uint64
	for e := uint32(0); e < MaxHalvings; e++ {
		reward := s.EpochAllocationDust(e) / s.BlocksPerEpoch
		total += reward * s.BlocksPerEpoch
	}
	return total
}

// ConvergenceCheck asserts that the schedule's cumulative emission, as
// MaxHalvings → ∞, converges to MiningCapDust minus a bounded remainder.
// It returns the remainder in dust. A remainder of 0 means the schedule is
// lossless; a small remainder is expected because of integer-division
// truncation per epoch.
//
// This method is tested directly and exposed so operators can verify at
// genesis time that the concrete schedule they shipped has the expected
// truncation budget.
func (s EmissionSchedule) ConvergenceCheck() uint64 {
	ultimate := s.UltimateCumulativeEmissionDust()
	if ultimate > s.MiningCapDust {
		// Would indicate a coding error; panic is appropriate.
		panic(fmt.Sprintf("emission: ultimate %d > cap %d", ultimate, s.MiningCapDust))
	}
	return s.MiningCapDust - ultimate
}

// BlockRewardCell returns the block reward as a decimal CELL string with
// CellDecimals fractional digits. For display only; never use for
// equality comparisons.
func (s EmissionSchedule) BlockRewardCell(height uint64) string {
	dust := s.BlockRewardDust(height)
	whole := dust / DustPerCell
	frac := dust % DustPerCell
	return fmt.Sprintf("%d.%0*d", whole, CellDecimals, frac)
}

// bigCap is a convenience for tests that want to reason about the cap with
// arbitrary precision. Not used in hot paths.
var bigCap = new(big.Int).SetUint64(CellMiningCapDust)

// approxAnnualInflationRate returns a best-effort floating-point estimate
// of the current annualised inflation rate, as a fraction (0.07 = 7 %/yr),
// at the given height and assuming the schedule's target block time. This
// is DISPLAY ONLY — never use the returned float for consensus.
func (s EmissionSchedule) ApproxAnnualInflationRate(height uint64) float64 {
	reward := s.BlockRewardDust(height)
	if reward == 0 {
		return 0
	}
	blocksPerYear := float64(31_557_600) / float64(s.TargetBlockTimeSeconds) // Julian year
	annualEmission := float64(reward) * blocksPerYear
	emittedSoFar := float64(s.CumulativeEmittedDust(height))
	if emittedSoFar <= 0 {
		// Before any emission, denominator is the fixed 10 M treasury in dust.
		const treasuryDust = 10_000_000 * float64(100_000_000)
		return annualEmission / treasuryDust
	}
	return annualEmission / emittedSoFar
}

// ensure math import is used even when the inflation helper is not.
var _ = math.Pi
