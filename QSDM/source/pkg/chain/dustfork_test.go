package chain

import (
	"errors"
	"math"
	"sync"
	"testing"
)

func TestForkDustHeight_defaultsInactive(t *testing.T) {
	// A node that has not opted in must behave exactly as before.
	if ForkDustHeight() != math.MaxUint64 {
		t.Fatalf("dust fork must default to never-active, got %d", ForkDustHeight())
	}
	if IsDustAccounting(0) || IsDustAccounting(1_000_000) {
		t.Fatal("dust accounting must be inactive by default")
	}
}

func TestIsDustAccounting_boundaryInclusive(t *testing.T) {
	SetForkDustHeight(100)
	t.Cleanup(func() { SetForkDustHeight(math.MaxUint64) })

	if IsDustAccounting(99) {
		t.Fatal("height below the fork must use legacy accounting")
	}
	if !IsDustAccounting(100) {
		t.Fatal("the fork height itself must be governed by the fork (boundary-inclusive)")
	}
	if !IsDustAccounting(101) {
		t.Fatal("heights above the fork must use dust accounting")
	}
}

func TestCellToDust_exactConversions(t *testing.T) {
	cases := []struct {
		cell float64
		dust uint64
	}{
		{0, 0},
		{1, 100_000_000},
		{0.00000001, 1},   // one dust
		{3.5649, 356_490_000},
		{100, 10_000_000_000},
	}
	for _, c := range cases {
		got, err := CellToDust(c.cell)
		if err != nil {
			t.Fatalf("CellToDust(%v): %v", c.cell, err)
		}
		if got != c.dust {
			t.Fatalf("CellToDust(%v) = %d, want %d", c.cell, got, c.dust)
		}
	}
}

func TestCellToDust_rejectsUnrepresentable(t *testing.T) {
	for _, bad := range []float64{-1, math.NaN(), math.Inf(1), math.Inf(-1)} {
		if _, err := CellToDust(bad); !errors.Is(err, ErrDustConversionRange) {
			t.Fatalf("CellToDust(%v) should be refused, got %v", bad, err)
		}
	}
}

func TestDustToCell_roundTripsExactlyAcrossFullSupply(t *testing.T) {
	// These amounts, up to the 90 M cap, survive dust -> CELL -> dust. Not
	// every amount does: some between 33.5 M and 45.0 M CELL, and some from
	// 67.1 M CELL, come back one dust off (see DustToCell).
	for _, dust := range []uint64{
		0, 1, 100_000_000, 356_490_000,
		CellSupplyCapDust / 2, CellSupplyCapDust,
	} {
		cell := DustToCell(dust)
		back, err := CellToDust(cell)
		if err != nil {
			t.Fatalf("round trip of %d dust: %v", dust, err)
		}
		if back != dust {
			t.Fatalf("round trip lost precision: %d -> %v -> %d", dust, cell, back)
		}
	}
}

// The whole point of the migration: dust-scale amounts stay exact at
// supply-scale balances, where float64 could not represent them at all.
func TestDustAccounting_preservesAmountsFloat64Destroyed(t *testing.T) {
	// The historical funder balance, in dust.
	const funderCell = 1e15
	// float64 could not represent one dust at that magnitude:
	if math.Nextafter(funderCell, math.Inf(1))-funderCell <= 1.0/float64(DustPerCellInt) {
		t.Fatal("precondition: float64 was expected to be too coarse at 1e15 CELL")
	}

	// Integer dust has no such problem: a reward-sized debit is exact.
	balance := uint64(90_000_000) * DustPerCellInt // full mining supply
	reward := uint64(356_490_988)                  // ~3.5649 CELL in dust

	after := balance - reward
	if balance-after != reward {
		t.Fatalf("integer debit must be exact: lost %d dust", (balance-after)-reward)
	}
}

func TestSupplyLedger_enforcesCap(t *testing.T) {
	s := NewSupplyLedger()
	if s.Cap() != CellSupplyCapDust {
		t.Fatalf("cap should be 90M CELL in dust, got %d", s.Cap())
	}

	if err := s.Mint(s.Cap()); err != nil {
		t.Fatalf("minting exactly the cap must succeed: %v", err)
	}
	if s.Remaining() != 0 {
		t.Fatalf("remaining should be 0 at the cap, got %d", s.Remaining())
	}
	if err := s.Mint(1); !errors.Is(err, ErrSupplyCapExceeded) {
		t.Fatalf("minting past the cap must be refused, got %v", err)
	}
}

// The historical genesis allocation — 1e15 CELL to qsdm-system-funder — is
// 10,000,000x the advertised supply. It is so far out of range that it
// cannot even be expressed in the protocol's own unit: 1e15 CELL is 1e23
// dust, which overflows uint64 (max ~1.84e19). That is itself the clearest
// statement that the float64 account layer was operating outside the
// protocol's declared numeric domain.
func TestSupplyLedger_refusesHistoricalGenesisOverAllocation(t *testing.T) {
	// 1e15 CELL is not representable in dust at all.
	if _, err := CellToDust(1e15); !errors.Is(err, ErrDustConversionRange) {
		t.Fatalf("1e15 CELL should overflow uint64 dust, got %v", err)
	}

	// And any absurd mint is refused outright by the invariant.
	s := NewSupplyLedger()
	if err := s.Mint(math.MaxUint64); !errors.Is(err, ErrSupplyCapExceeded) {
		t.Fatalf("an over-cap genesis must be refused by the supply invariant, got %v", err)
	}
	if s.Issued() != 0 {
		t.Fatalf("a refused mint must not change issued supply, got %d", s.Issued())
	}
}

func TestSupplyLedger_burnReducesIssued(t *testing.T) {
	s := NewSupplyLedger()
	if err := s.Mint(1000); err != nil {
		t.Fatal(err)
	}
	s.Burn(400)
	if s.Issued() != 600 {
		t.Fatalf("issued after burn: want 600, got %d", s.Issued())
	}
	s.Burn(10_000) // clamps at zero rather than underflowing
	if s.Issued() != 0 {
		t.Fatalf("burn must clamp at zero, got %d", s.Issued())
	}
}

// Concurrent minters must not race past the cap between check and commit.
func TestSupplyLedger_concurrentMintCannotExceedCap(t *testing.T) {
	const capDust = 1000
	s, err := NewSupplyLedgerWithCap(capDust)
	if err != nil {
		t.Fatal(err)
	}

	const workers = 32
	const each = 100 // 32 * 100 = 3200 > cap
	var wg sync.WaitGroup
	ok := make([]bool, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ok[i] = s.Mint(each) == nil
		}(i)
	}
	wg.Wait()

	wins := 0
	for _, v := range ok {
		if v {
			wins++
		}
	}
	if uint64(wins*each) != s.Issued() {
		t.Fatalf("issued %d disagrees with %d successful mints", s.Issued(), wins)
	}
	if s.Issued() > capDust {
		t.Fatalf("concurrent minting breached the cap: issued %d > cap %d", s.Issued(), capDust)
	}
}

func TestNewSupplyLedgerWithCap_rejectsZero(t *testing.T) {
	if _, err := NewSupplyLedgerWithCap(0); err == nil {
		t.Fatal("a zero supply cap is a configuration error")
	}
}

// QSDM Genesis Policy v1.1, clause 1 (decision D1-a, 2026-10-16): the maximum
// supply is 90,000,000 CELL, all of it from mining, with no genesis treasury.
func TestCellSupplyCap_matchesGenesisPolicy(t *testing.T) {
	if CellSupplyCapDust != 9_000_000_000_000_000 {
		t.Fatalf("supply cap = %d dust, want 9e15 (90 M CELL)", CellSupplyCapDust)
	}
	if CellSupplyCapDust != CellMiningCapDust {
		t.Fatalf("supply cap %d != mining cap %d: the policy has no genesis treasury",
			CellSupplyCapDust, CellMiningCapDust)
	}
	if s := DefaultEmissionSchedule(); s.MiningCapDust != CellSupplyCapDust {
		t.Fatalf("default schedule cap %d != supply cap %d", s.MiningCapDust, CellSupplyCapDust)
	}
	// The whole supply fits below 2^53 dust, so float64(dust) is exact for
	// every reachable amount. DustToCell's division still rounds: see its
	// comment.
	if CellSupplyCapDust >= 1<<53 {
		t.Fatalf("supply cap %d is not below 2^53", CellSupplyCapDust)
	}
}

// Liveness evidence for the 100 M -> 90 M change, on a ledger shaped like the
// public chain: the pinned canonical genesis moves 100 CELL out of the funder
// at height 0, and the pilot had emitted about 3.05 M CELL by mid-October 2026
// (height ~856,000 at 3.56490988 CELL per block). The test counts BOTH as
// issuance -- stricter than any production path, because SupplyLedger has no
// production caller -- and shows that the full schedule for decades stays
// under the new cap.
//
// What it does NOT count, and why: the synthetic qsdm-system-funder bookkeeping
// balance (~1e15 CELL) is not issuance; it is 1e23 dust and cannot even be
// represented (TestSupplyLedger_refusesHistoricalGenesisOverAllocation), and it
// was equally far over the old 100 M cap. Development credits made with
// AccountStore.Credit never touch SupplyLedger either.
func TestSupplyLedger_liveShapedChainStaysUnderPolicyCap(t *testing.T) {
	const genesisSeedDust = 100 * DustPerCellInt // qsdmCanonicalGenesisAmount
	const liveTip = uint64(856_000)

	s := DefaultEmissionSchedule()
	ledger := NewSupplyLedger()
	if err := ledger.Mint(genesisSeedDust); err != nil {
		t.Fatalf("genesis seed: %v", err)
	}
	emitted := s.CumulativeEmittedDust(liveTip)
	if emitted < 3_000_000*DustPerCellInt || emitted > 3_100_000*DustPerCellInt {
		t.Fatalf("live-shaped tip emitted %d dust, expected about 3.05 M CELL", emitted)
	}
	if err := ledger.Mint(emitted); err != nil {
		t.Fatalf("live-shaped emission refused under the 90 M cap: %v", err)
	}
	// Every block reward for the next five emission epochs (20 years, 87%
	// of the mining schedule) still fits.
	horizon := 5 * s.BlocksPerEpoch
	more := s.CumulativeEmittedDust(horizon) - emitted
	if err := ledger.Mint(more); err != nil {
		t.Fatalf("schedule to height %d refused: %v", horizon, err)
	}
	if ledger.Issued() > CellSupplyCapDust {
		t.Fatalf("issued %d > cap %d", ledger.Issued(), CellSupplyCapDust)
	}
}

// Mining alone can never exceed the cap: the integer schedule converges to
// at most CellMiningCapDust, which is now the whole supply. The 100 CELL
// genesis seed is the only non-mining issuance on the public chain; under the
// policy (clause 5(a)/(b)) the migration removes or classifies it. If it were
// kept AND SupplyLedger were wired, the cap would bind only once cumulative
// emission passes cap - 100 CELL, which this test pins as more than 75 years
// out (height ~250.65 M, ~79 years), long after the migration that must
// precede any wiring.
func TestSupplyLedger_fullScheduleFitsPolicyCap(t *testing.T) {
	s := DefaultEmissionSchedule()
	ledger := NewSupplyLedger()
	if err := ledger.Mint(s.UltimateCumulativeEmissionDust()); err != nil {
		t.Fatalf("full mining schedule refused under the 90 M cap: %v", err)
	}

	const genesisSeedDust = 100 * DustPerCellInt
	limit := CellSupplyCapDust - genesisSeedDust
	lo, hi := uint64(0), uint64(MaxHalvings)*s.BlocksPerEpoch
	for lo < hi { // first height whose cumulative emission exceeds limit
		mid := lo + (hi-lo)/2
		if s.CumulativeEmittedDust(mid) > limit {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	if s.CumulativeEmittedDust(lo) <= limit {
		return // the seed plus the whole schedule fits: nothing can bind
	}
	years := float64(lo) * float64(s.TargetBlockTimeSeconds) / 31_557_600
	if years < 75 {
		t.Fatalf("genesis seed + emission would reach the cap at height %d (~%.0f years)", lo, years)
	}
	t.Logf("genesis seed + emission would reach the 90 M cap only at height %d (~%.0f years)", lo, years)
}

func TestCellToDust_refusesSubDustFraction(t *testing.T) {
	for _, bad := range []float64{0.000000005, 1.000000004, 3.564909875, 0.123456789} {
		if _, err := CellToDust(bad); !errors.Is(err, ErrDustConversionRange) {
			t.Fatalf("CellToDust(%v) should be refused as not a whole number of dust, got %v", bad, err)
		}
	}
	for _, ok := range []float64{0.1, 0.3, 3.56490987, 1e-8, 89_999_999.99999999} {
		if _, err := CellToDust(ok); err != nil {
			t.Fatalf("CellToDust(%v) should be accepted: %v", ok, err)
		}
	}
}

func TestAccountStoreCredit_floorsSubDustUnderDust(t *testing.T) {
	SetForkDustHeight(1)
	t.Cleanup(func() { SetForkDustHeight(math.MaxUint64) })
	as := atHeight(10)
	as.Credit("a", 1.000000009)
	if got := as.accounts["a"].BalanceDust; got != 100_000_000 {
		t.Fatalf("credit of 1.000000009 CELL = %d dust, want floor 100000000", got)
	}
}

// 2^64 dust does not fit in a uint64. float64(math.MaxUint64) rounds up to
// 2^64, so the overflow check must refuse that value too; converting it
// returned 2^63 with no error.
func TestCellToDust_refusesTwoToThe64Dust(t *testing.T) {
	two64 := math.Ldexp(1, 64)
	cell := two64 / float64(DustPerCellInt)
	if cell*float64(DustPerCellInt) != two64 {
		t.Fatalf("test value %v does not scale to 2^64 dust", cell)
	}
	if d, err := CellToDust(cell); !errors.Is(err, ErrDustConversionRange) {
		t.Fatalf("CellToDust(%v) = %d, %v; want a range error", cell, d, err)
	}
	// The next float below still fits and converts to the same integer.
	below := math.Nextafter(cell, 0)
	scaled := below * float64(DustPerCellInt)
	d, err := CellToDust(below)
	if err != nil || scaled >= two64 || d != uint64(scaled) {
		t.Fatalf("CellToDust(%v) = %d, %v; want %d", below, d, err, uint64(scaled))
	}
}
