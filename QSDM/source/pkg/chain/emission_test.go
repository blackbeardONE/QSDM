package chain

import (
	"testing"
)

func TestDefaultEmissionSchedule_Parameters(t *testing.T) {
	s := DefaultEmissionSchedule()
	if s.MiningCapDust != CellMiningCapDust {
		t.Errorf("MiningCapDust = %d, want %d", s.MiningCapDust, CellMiningCapDust)
	}
	if s.EpochLengthSeconds != EpochLengthSeconds {
		t.Errorf("EpochLengthSeconds = %d, want %d", s.EpochLengthSeconds, EpochLengthSeconds)
	}
	if s.TargetBlockTimeSeconds != DefaultTargetBlockTimeSeconds {
		t.Errorf("TargetBlockTimeSeconds = %d, want %d", s.TargetBlockTimeSeconds, DefaultTargetBlockTimeSeconds)
	}
	// 4y / 10s = 12,623,040 blocks per epoch (using Julian year).
	want := uint64(12_623_040)
	if s.BlocksPerEpoch != want {
		t.Errorf("BlocksPerEpoch = %d, want %d", s.BlocksPerEpoch, want)
	}
}

func TestNewEmissionSchedule_Validation(t *testing.T) {
	cases := []struct {
		name    string
		cap     uint64
		epoch   uint64
		block   uint64
		wantErr bool
	}{
		{"ok_defaults", CellMiningCapDust, EpochLengthSeconds, 10, false},
		{"ok_4s_blocks", CellMiningCapDust, EpochLengthSeconds, 4, false},
		{"zero_cap", 0, EpochLengthSeconds, 10, true},
		{"zero_epoch", CellMiningCapDust, 0, 10, true},
		{"zero_block", CellMiningCapDust, EpochLengthSeconds, 0, true},
		{"non_divisor_block_time", CellMiningCapDust, EpochLengthSeconds, 7, true}, // 126230400 % 7 != 0
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewEmissionSchedule(tc.cap, tc.epoch, tc.block)
			if tc.wantErr && err == nil {
				t.Fatalf("want error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestEpochForHeight(t *testing.T) {
	s := DefaultEmissionSchedule()
	bpe := s.BlocksPerEpoch
	cases := []struct {
		name   string
		height uint64
		want   uint32
	}{
		{"genesis", 0, 0},
		{"first_block", 1, 0},
		{"last_block_of_epoch_0", bpe, 0},
		{"first_block_of_epoch_1", bpe + 1, 1},
		{"mid_epoch_1", bpe * 3 / 2, 1},
		{"last_block_of_epoch_1", 2 * bpe, 1},
		{"first_block_of_epoch_2", 2*bpe + 1, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := s.EpochForHeight(tc.height)
			if got != tc.want {
				t.Errorf("EpochForHeight(%d) = %d, want %d", tc.height, got, tc.want)
			}
		})
	}
}

func TestEpochAllocationHalves(t *testing.T) {
	s := DefaultEmissionSchedule()
	prev := s.EpochAllocationDust(0)
	if prev != CellMiningCapDust/2 {
		t.Fatalf("epoch 0 allocation = %d, want %d", prev, CellMiningCapDust/2)
	}
	for e := uint32(1); e < 10; e++ {
		cur := s.EpochAllocationDust(e)
		// Each epoch allocation is exactly half of the previous (integer
		// division by 2 preserves exact halving because cap is a power of
		// 10^8 * 9*10^7, and cap/2, cap/4, ... are all even down to a
		// large number of halvings).
		if cur != prev/2 {
			t.Errorf("epoch %d allocation = %d, want %d (half of epoch %d = %d)",
				e, cur, prev/2, e-1, prev)
		}
		prev = cur
	}
}

func TestEpochAllocationZeroAfterMaxHalvings(t *testing.T) {
	s := DefaultEmissionSchedule()
	if got := s.EpochAllocationDust(MaxHalvings); got != 0 {
		t.Errorf("EpochAllocationDust(MaxHalvings)=%d, want 0", got)
	}
	if got := s.EpochAllocationDust(MaxHalvings + 5); got != 0 {
		t.Errorf("EpochAllocationDust(MaxHalvings+5)=%d, want 0", got)
	}
}

func TestBlockRewardIsEpochAllocationDividedByBlocks(t *testing.T) {
	s := DefaultEmissionSchedule()
	for e := uint32(0); e < 5; e++ {
		// First block in epoch e.
		height := uint64(e)*s.BlocksPerEpoch + 1
		got := s.BlockRewardDust(height)
		want := s.EpochAllocationDust(e) / s.BlocksPerEpoch
		if got != want {
			t.Errorf("epoch %d (height %d) reward = %d, want %d", e, height, got, want)
		}
	}
}

func TestBlockRewardZeroAtGenesis(t *testing.T) {
	s := DefaultEmissionSchedule()
	if got := s.BlockRewardDust(0); got != 0 {
		t.Errorf("genesis reward = %d, want 0", got)
	}
}

func TestBlockRewardHalvesEveryEpoch(t *testing.T) {
	s := DefaultEmissionSchedule()
	first := s.BlockRewardDust(1)
	second := s.BlockRewardDust(s.BlocksPerEpoch + 1)
	if second != first/2 {
		t.Errorf("epoch 1 reward = %d, want half of epoch 0 reward %d = %d",
			second, first, first/2)
	}
}

func TestCumulativeEmissionExactlyMatchesEpochTotals(t *testing.T) {
	s := DefaultEmissionSchedule()
	// End of epoch 0: cumulative should equal reward(epoch 0) * BlocksPerEpoch
	r0 := s.BlockRewardDust(1)
	endEpoch0 := r0 * s.BlocksPerEpoch
	got := s.CumulativeEmittedDust(s.BlocksPerEpoch)
	if got != endEpoch0 {
		t.Errorf("cumulative at end of epoch 0 = %d, want %d", got, endEpoch0)
	}
	// End of epoch 1.
	r1 := s.BlockRewardDust(s.BlocksPerEpoch + 1)
	endEpoch1 := endEpoch0 + r1*s.BlocksPerEpoch
	got = s.CumulativeEmittedDust(2 * s.BlocksPerEpoch)
	if got != endEpoch1 {
		t.Errorf("cumulative at end of epoch 1 = %d, want %d", got, endEpoch1)
	}
}

func TestConvergenceToCapMinusSmallRemainder(t *testing.T) {
	s := DefaultEmissionSchedule()
	remainder := s.ConvergenceCheck()
	// Each active epoch loses at most BlocksPerEpoch-1 dust via integer
	// division; late epochs (where allocation < BlocksPerEpoch) lose their
	// entire allocation but the sum of those allocations is a geometric
	// series bounded above by BlocksPerEpoch * 2. The conservative upper
	// bound on total remainder is therefore MaxHalvings * BlocksPerEpoch.
	upperBound := uint64(MaxHalvings) * s.BlocksPerEpoch
	if remainder > upperBound {
		t.Errorf("convergence remainder = %d dust, want <= %d dust (bpe=%d)",
			remainder, upperBound, s.BlocksPerEpoch)
	}
	// Stricter invariant for the default schedule: the remainder must be
	// less than 0.00001 % of the cap, i.e. the schedule does not leak
	// meaningful supply.
	if remainder > s.MiningCapDust/10_000_000 {
		t.Errorf("convergence remainder %d dust exceeds 0.00001%% of cap %d",
			remainder, s.MiningCapDust)
	}
	// And the ultimate emission must never exceed the cap.
	ult := s.UltimateCumulativeEmissionDust()
	if ult > s.MiningCapDust {
		t.Errorf("ultimate emission %d > cap %d", ult, s.MiningCapDust)
	}
}

func TestCumulativeNeverExceedsCap(t *testing.T) {
	s := DefaultEmissionSchedule()
	// Sample heights at every epoch boundary plus a few mid-points.
	for e := uint32(0); e < 40; e++ {
		for _, mid := range []uint64{1, s.BlocksPerEpoch / 2, s.BlocksPerEpoch} {
			h := uint64(e)*s.BlocksPerEpoch + mid
			emitted := s.CumulativeEmittedDust(h)
			if emitted > s.MiningCapDust {
				t.Errorf("cumulative at h=%d = %d > cap %d", h, emitted, s.MiningCapDust)
			}
		}
	}
}

func TestRemainingSupplyAtGenesisIsCap(t *testing.T) {
	s := DefaultEmissionSchedule()
	if got := s.RemainingSupplyDust(0); got != s.MiningCapDust {
		t.Errorf("remaining at genesis = %d, want %d", got, s.MiningCapDust)
	}
}

func TestNextHalvingHeightAndETA(t *testing.T) {
	s := DefaultEmissionSchedule()
	// Mid of epoch 0: next halving is at BlocksPerEpoch+1 boundary -> height BPE.
	mid := s.BlocksPerEpoch / 2
	next := s.NextHalvingHeight(mid)
	if next != s.BlocksPerEpoch {
		t.Errorf("next halving from mid epoch 0 = %d, want %d", next, s.BlocksPerEpoch)
	}
	eta := s.NextHalvingETA(mid)
	wantETA := (s.BlocksPerEpoch - mid) * s.TargetBlockTimeSeconds
	if eta != wantETA {
		t.Errorf("ETA from mid epoch 0 = %d, want %d", eta, wantETA)
	}
}

func TestBlockRewardCellFormatting(t *testing.T) {
	s := DefaultEmissionSchedule()
	got := s.BlockRewardCell(1)
	// At epoch 0 in the default schedule:
	//   reward = floor((9e15 / 2) / 12,623,040)
	//          = floor(4,500,000,000,000,000 / 12,623,040)
	//          = 356,490,987 dust
	//          = 3.56490987 CELL
	want := "3.56490987"
	if got != want {
		t.Errorf("BlockRewardCell(1) = %q, want %q", got, want)
	}
}

func TestAlternateBlockTime_PreservesCap(t *testing.T) {
	// 4-second blocks → 31,557,600 blocks per epoch.
	s, err := NewEmissionSchedule(CellMiningCapDust, EpochLengthSeconds, 4)
	if err != nil {
		t.Fatalf("construct: %v", err)
	}
	if s.BlocksPerEpoch != 31_557_600 {
		t.Errorf("BlocksPerEpoch = %d, want 31_557_600", s.BlocksPerEpoch)
	}
	// Ultimate emission must still converge to ≤ cap.
	ult := s.UltimateCumulativeEmissionDust()
	if ult > s.MiningCapDust {
		t.Errorf("alternate schedule ultimate %d > cap %d", ult, s.MiningCapDust)
	}
	if rem := s.ConvergenceCheck(); rem > uint64(MaxHalvings)*s.BlocksPerEpoch {
		t.Errorf("alternate remainder = %d > MaxHalvings*bpe=%d",
			rem, uint64(MaxHalvings)*s.BlocksPerEpoch)
	}
}

func TestApproxAnnualInflationRate_Monotone(t *testing.T) {
	s := DefaultEmissionSchedule()
	// Inflation at height 1 (before any prior emission) is computed against
	// the treasury denominator; it should be positive and finite.
	r := s.ApproxAnnualInflationRate(1)
	if r <= 0 {
		t.Errorf("inflation at height 1 = %g, want > 0", r)
	}
	// After a full epoch the denominator is much larger, so the rate must
	// drop.
	r2 := s.ApproxAnnualInflationRate(s.BlocksPerEpoch + 1)
	if !(r2 < r) {
		t.Errorf("inflation at epoch 1 (%g) not less than epoch 0 (%g)", r2, r)
	}
}
