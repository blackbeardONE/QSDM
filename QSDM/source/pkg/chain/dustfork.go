package chain

import (
	"errors"
	"fmt"
	"math"
	"sync/atomic"
)

// Integer-dust accounting fork.
//
// The ledger's account layer stores balances as float64 CELL while the
// emission layer computes in uint64 dust (1 CELL = 1e8 dust). That mismatch
// is not cosmetic — it destroys money:
//
//	funder balance          ~1.0e15 CELL
//	float64 ULP there        0.125 CELL
//	epoch-0 reward/block     3.564909879 CELL
//	debit actually applied   3.625 CELL      (quantised up to the ULP grid)
//	credit actually applied  3.564909879 CELL (exact, small balance)
//	net destroyed per block   0.0601 CELL  ->  ~190,000 CELL/year
//
// AccountStore.StateRoot compounds it by formatting balances with %f — six
// decimal places — so anything below 1e-6 CELL is invisible to the state
// root even though the protocol's unit is 1e-8.
//
// Fixing this changes every state root, so it is gated on an activation
// height exactly like the Tensor-Core fork in pkg/mining/fork.go: nodes
// agree on the legacy encoding below the fork and the integer encoding at or
// above it, letting a live network roll forward without splitting.
//
// Default is math.MaxUint64 — never active — so a node that has not been
// explicitly configured behaves exactly as before.

// DustPerCellInt is the protocol's smallest unit, mirroring
// emission.DustPerCell as an integer constant for account arithmetic.
const DustPerCellInt uint64 = 100_000_000

// forkDustHeight is the first height governed by integer-dust accounting.
var forkDustHeight atomic.Uint64

func init() { forkDustHeight.Store(math.MaxUint64) }

// ForkDustHeight returns the activation height for integer-dust accounting.
func ForkDustHeight() uint64 { return forkDustHeight.Load() }

// SetForkDustHeight pins the activation height. Setting it to
// math.MaxUint64 disables the fork.
func SetForkDustHeight(h uint64) { forkDustHeight.Store(h) }

// IsDustAccounting reports whether a block at the given height is governed
// by integer-dust accounting. Boundary-inclusive, matching mining.IsV2TC.
func IsDustAccounting(height uint64) bool { return height >= forkDustHeight.Load() }

// ErrSupplyCapExceeded is returned when an operation would push total
// supply past the protocol maximum.
var ErrSupplyCapExceeded = errors.New("chain: operation would exceed the CELL supply cap")

// ErrDustConversionRange is returned when a CELL amount cannot be
// represented exactly in dust.
var ErrDustConversionRange = errors.New("chain: CELL amount is not exactly representable in dust")

// CellToDust converts whole-CELL float to integer dust, refusing any value
// that cannot be represented exactly.
//
// Refusing rather than rounding is deliberate: silent rounding at the
// conversion boundary is how the float64 layer lost money in the first
// place. A caller that genuinely wants truncation must ask for it.
//
// NOT to be confused with balanceToDust in enrollment_apply.go, which floors
// and clamps to MaxUint64 on overflow. That leniency is fine for enrollment
// stakes (bounded near 10 CELL, far inside float64's exact range) but is
// exactly the wrong behaviour on the general money path, where a silently
// floored conversion is a silently destroyed balance. Use CellToDust for
// ledger balances; balanceToDust remains for the enrollment helper's
// existing, bounded use.
func CellToDust(cell float64) (uint64, error) {
	if math.IsNaN(cell) || math.IsInf(cell, 0) {
		return 0, fmt.Errorf("%w: %v is not a finite amount", ErrDustConversionRange, cell)
	}
	if cell < 0 {
		return 0, fmt.Errorf("%w: %v is negative", ErrDustConversionRange, cell)
	}
	scaled := cell * float64(DustPerCellInt)
	if scaled > float64(math.MaxUint64) {
		return 0, fmt.Errorf("%w: %v overflows uint64 dust", ErrDustConversionRange, cell)
	}
	rounded := math.Round(scaled)
	// Accept only values that land on (or within half a ULP of) an exact
	// dust boundary. Beyond ~9e7 CELL, float64's ULP exceeds one dust and
	// exactness is no longer decidable from the float alone.
	if math.Abs(scaled-rounded) > 0.5 {
		return 0, fmt.Errorf("%w: %v is not a whole number of dust", ErrDustConversionRange, cell)
	}
	return uint64(rounded), nil
}

// DustToCell converts integer dust to whole-CELL float for legacy surfaces.
// Exact for every value below 2^53 dust (~90.07 M CELL), which covers the
// entire 100 M supply.
func DustToCell(dust uint64) float64 {
	return float64(dust) / float64(DustPerCellInt)
}

// CellSupplyCapDust is the protocol maximum total supply in dust:
// 90 M mining emission + 10 M genesis treasury = 100 M CELL.
const CellSupplyCapDust uint64 = 100_000_000 * 100_000_000

// SupplyLedger tracks total issued dust so no path — mining reward, faucet,
// genesis seeding or a future contract — can mint past the cap.
//
// The emission schedule already caps what MINING can pay out
// (emission.go), but that is a property of one code path. Nothing enforced a
// ledger-wide invariant, so any other minting path was unbounded.
type SupplyLedger struct {
	issued atomic.Uint64
	cap    atomic.Uint64
}

// NewSupplyLedger returns a ledger enforcing the protocol cap.
func NewSupplyLedger() *SupplyLedger {
	s := &SupplyLedger{}
	s.cap.Store(CellSupplyCapDust)
	return s
}

// NewSupplyLedgerWithCap returns a ledger with a custom cap (tests,
// devnets). A zero cap is rejected as a configuration error.
func NewSupplyLedgerWithCap(capDust uint64) (*SupplyLedger, error) {
	if capDust == 0 {
		return nil, errors.New("chain: supply cap must be greater than zero")
	}
	s := &SupplyLedger{}
	s.cap.Store(capDust)
	return s, nil
}

// Cap returns the configured maximum supply in dust.
func (s *SupplyLedger) Cap() uint64 { return s.cap.Load() }

// Issued returns total dust issued so far.
func (s *SupplyLedger) Issued() uint64 { return s.issued.Load() }

// Remaining returns how much dust may still be minted.
func (s *SupplyLedger) Remaining() uint64 {
	issued, capDust := s.issued.Load(), s.cap.Load()
	if issued >= capDust {
		return 0
	}
	return capDust - issued
}

// Mint records newly issued dust, refusing anything that would exceed the
// cap. The check and the increment are one atomic compare-and-swap loop, so
// concurrent minters cannot race past the cap between check and commit.
func (s *SupplyLedger) Mint(dust uint64) error {
	if dust == 0 {
		return nil
	}
	capDust := s.cap.Load()
	for {
		cur := s.issued.Load()
		if dust > capDust-cur {
			return fmt.Errorf("%w: issued %d + %d > cap %d", ErrSupplyCapExceeded, cur, dust, capDust)
		}
		if s.issued.CompareAndSwap(cur, cur+dust) {
			return nil
		}
	}
}

// Burn reduces issued supply (e.g. burned fees), never below zero.
func (s *SupplyLedger) Burn(dust uint64) {
	for {
		cur := s.issued.Load()
		next := uint64(0)
		if cur > dust {
			next = cur - dust
		}
		if s.issued.CompareAndSwap(cur, next) {
			return
		}
	}
}
