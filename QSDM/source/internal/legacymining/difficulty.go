package legacymining

// difficulty.go (HL2 WP-E, design §2 M4): the admission difficulty of a
// config, and the arithmetic to choose difficulty_bits for a hashrate.
//
// The difficulty is producer-local. miningsvc serves it in /work and checks
// it in mining.Verifier step 10 on the producer only; reward txs carry only
// LMP1 proof IDs, and no follower, block-validation, sync or replay path
// re-verifies a proof or a difficulty (doc.go, "Contract revision HL2
// (WP-E)"). Changing difficulty_bits is therefore a config change (a new
// config hash), not a fork.

import (
	"math"
	"math/big"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mining"
)

// ConfigDifficulty is the difficulty a config makes miningsvc advertise in
// /work and enforce in Verify:
//   - version 1: mining.DefaultMinDifficulty (2^16), exactly as in HL1;
//   - version 2: 2^DifficultyBits (ValidateConfig bounds the bits to
//     MinDifficultyBits..MaxDifficultyBits, so it is never below 2^16).
//
// It returns a fresh value, or nil for another version or a negative or
// absurd bit count (a Config that ValidateConfig would refuse).
func ConfigDifficulty(c Config) *big.Int {
	switch c.Version {
	case ConfigVersion1:
		return new(big.Int).Set(mining.DefaultMinDifficulty)
	case ConfigVersion2:
		if c.DifficultyBits < 0 || c.DifficultyBits > 255 {
			return nil
		}
		return new(big.Int).Lsh(big.NewInt(1), uint(c.DifficultyBits))
	}
	return nil
}

// Difficulty planning. A proof meets the target with probability
// target/2^256 = 1/D for D = 2^bits (mining.TargetFromDifficulty), so the
// attempts per proof are geometric with mean D, and a miner hashing at H
// attempts per second finds proofs at H*60/D per minute.
//
// The reference miners (cmd/qsdmminer-console) solve one fetched work
// height until a proof is found and do not abandon it when the chain moves
// on, and Verify rejects a proof more than mining.GraceWindow blocks behind
// the tip (ReasonTooLate, not counted against the owner). A solve that takes
// longer than about GraceWindow*BlockPeriod is wasted. With mean solve time
// tau = D/H and window W, the landed rate is
//
//	(1 - exp(-W/tau)) / tau
//
// which is H/D when tau << W and falls like W/tau^2 once tau >> W: a miner
// much slower than one proof per window earns less than its hashrate share.

// BlockPeriod is the producer's seal period (blockdriver.DefaultPeriod;
// OwnerEpoch / OwnerEpochBlocks).
const BlockPeriod = OwnerEpoch / OwnerEpochBlocks

// GraceDuration is the nominal time a proof stays acceptable after the
// height it was mined on: mining.GraceWindow blocks of BlockPeriod (60 s).
const GraceDuration = time.Duration(mining.GraceWindow) * BlockPeriod

// ExpectedProofsPerMinute returns H*60/2^bits for hashrate H in attempts
// per second: the mean proof rate with no grace-window loss and no cap.
func ExpectedProofsPerMinute(hashrate float64, bits int) float64 {
	if hashrate <= 0 || bits < 0 {
		return 0
	}
	return hashrate * 60 / math.Ldexp(1, bits)
}

// ExpectedSolveTime returns the mean time to one proof, 2^bits/H. It is
// math.MaxInt64 for a non-positive hashrate.
func ExpectedSolveTime(hashrate float64, bits int) time.Duration {
	if hashrate <= 0 || bits < 0 {
		return time.Duration(math.MaxInt64)
	}
	s := math.Ldexp(1, bits) / hashrate
	if s*float64(time.Second) >= math.MaxInt64 {
		return time.Duration(math.MaxInt64)
	}
	return time.Duration(s * float64(time.Second))
}

// LandedProofsPerMinute returns the rate of proofs that arrive inside window
// for a miner that solves each work height to completion (see above):
// 60*(1-exp(-W/tau))/tau with tau = 2^bits/H seconds. window <= 0 means no
// loss (ExpectedProofsPerMinute). Admission caps are not applied.
func LandedProofsPerMinute(hashrate float64, bits int, window time.Duration) float64 {
	if hashrate <= 0 || bits < 0 {
		return 0
	}
	if window <= 0 {
		return ExpectedProofsPerMinute(hashrate, bits)
	}
	tau := math.Ldexp(1, bits) / hashrate
	return 60 * -math.Expm1(-window.Seconds()/tau) / tau
}

// SuggestDifficultyBits returns the largest bits in
// MinDifficultyBits..MaxDifficultyBits at which a miner hashing at hashrate
// still finds at least proofsPerMin proofs per minute on average, or
// MinDifficultyBits if none does.
func SuggestDifficultyBits(hashrate, proofsPerMin float64) int {
	if hashrate <= 0 || proofsPerMin <= 0 {
		return MinDifficultyBits
	}
	b := int(math.Floor(math.Log2(hashrate * 60 / proofsPerMin)))
	switch {
	case b < MinDifficultyBits:
		return MinDifficultyBits
	case b > MaxDifficultyBits:
		return MaxDifficultyBits
	}
	return b
}
