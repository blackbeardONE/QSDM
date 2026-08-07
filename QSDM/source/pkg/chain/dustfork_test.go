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
	// Every value up to the 100 M cap must survive dust -> CELL -> dust.
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
		t.Fatalf("cap should be 100M CELL in dust, got %d", s.Cap())
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
