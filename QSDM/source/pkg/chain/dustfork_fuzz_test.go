package chain

import (
	"errors"
	"math"
	"testing"
)

// FuzzCellToDust checks the CELL-to-dust conversion used for ledger credits
// once the integer-dust fork is active, on three inputs.
//
//   - Any float64: an amount is either refused with ErrDustConversionRange or
//     converted to the dust value nearest to it, and then it is finite, not
//     negative, below 2^64 dust and within float noise of a whole number of
//     dust (max(1e-6, 4 ULP) of the scaled value).
//   - Any whole number of dust below 2^50 (about 11.26 M CELL), written as
//     CELL with DustToCell: it converts back to exactly the same dust. Below
//     2^50 the two float64 roundings move the scaled value by at most a
//     quarter of a dust. Higher up the guarantee ends: from 2^51 dust some
//     amounts come back one dust off, and above about 67.1 M CELL two
//     neighbouring dust amounts can share one float64.
//   - A whole number of dust below 2^40 plus a fraction between 0.01 and 0.99
//     of a dust: always refused, because float64 can still show that fraction.
func FuzzCellToDust(f *testing.F) {
	f.Add(0.0, uint64(0), uint16(0))
	f.Add(3.56490988, uint64(356_490_988), uint16(32767))
	f.Add(0.000000005, uint64(1), uint16(0))
	f.Add(89_999_999.99999999, CellSupplyCapDust, uint16(65535))
	f.Add(math.Ldexp(1, 64)/float64(DustPerCellInt), uint64(1)<<53, uint16(1))
	f.Add(math.Inf(1), uint64(math.MaxUint64), uint16(100))
	f.Add(math.NaN(), uint64(1)<<40, uint16(200))
	f.Add(-1e-8, uint64(123_456_789), uint16(400))

	f.Fuzz(func(t *testing.T, cell float64, dust uint64, frac uint16) {
		d, err := CellToDust(cell)
		if err != nil {
			if !errors.Is(err, ErrDustConversionRange) {
				t.Fatalf("CellToDust(%v): unexpected error type %v", cell, err)
			}
		} else {
			if math.IsNaN(cell) || math.IsInf(cell, 0) || cell < 0 {
				t.Fatalf("CellToDust(%v) = %d, want a refusal", cell, d)
			}
			scaled := cell * float64(DustPerCellInt)
			rounded := math.Round(scaled)
			if rounded >= math.Ldexp(1, 64) || float64(d) != rounded {
				t.Fatalf("CellToDust(%v) = %d, but the nearest dust is %v", cell, d, rounded)
			}
			ulp := math.Nextafter(scaled, math.Inf(1)) - scaled
			if math.Abs(scaled-rounded) > math.Max(1e-6, 4*ulp) {
				t.Fatalf("CellToDust(%v) = %d accepted a fraction of a dust (%v)", cell, d, scaled-rounded)
			}
		}

		exact := dust % (1 << 50)
		if got, err := CellToDust(DustToCell(exact)); err != nil || got != exact {
			t.Fatalf("CellToDust(DustToCell(%d)) = %d, %v", exact, got, err)
		}

		whole := dust % (1 << 40)
		fraction := 0.01 + 0.98*float64(frac)/math.MaxUint16
		off := (float64(whole) + fraction) / float64(DustPerCellInt)
		if got, err := CellToDust(off); !errors.Is(err, ErrDustConversionRange) {
			t.Fatalf("CellToDust(%v) = %d, %v; %d dust plus %.4f must be refused", off, got, err, whole, fraction)
		}
	})
}
