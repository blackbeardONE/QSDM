package mining

import (
	"math/big"
	"testing"
)

func TestRetargetHonorsTargetTime(t *testing.T) {
	p := NewDifficultyAdjusterParams()
	d0 := big.NewInt(1 << 20)
	// Block time exactly on target → difficulty unchanged.
	target := int64(p.RetargetInterval * p.TargetBlockTimeSeconds)
	next, err := p.Retarget(d0, 0, target)
	if err != nil {
		t.Fatalf("retarget: %v", err)
	}
	if next.Cmp(d0) != 0 {
		t.Fatalf("on-target retarget changed difficulty: %s -> %s", d0, next)
	}
}

func TestRetargetSlowBlocksLowersDifficulty(t *testing.T) {
	p := NewDifficultyAdjusterParams()
	d0 := big.NewInt(1 << 30)
	target := int64(p.RetargetInterval * p.TargetBlockTimeSeconds)
	// Blocks took 2× as long → difficulty should roughly halve.
	next, err := p.Retarget(d0, 0, target*2)
	if err != nil {
		t.Fatalf("retarget: %v", err)
	}
	want := new(big.Int).Quo(d0, big.NewInt(2))
	if next.Cmp(want) != 0 {
		t.Fatalf("slow retarget: got %s want %s", next, want)
	}
}

func TestRetargetClampsUpwards(t *testing.T) {
	p := NewDifficultyAdjusterParams()
	d0 := big.NewInt(1 << 30)
	// Blocks took 1/100 of target → raw factor 100, must clamp to ×4.
	target := int64(p.RetargetInterval * p.TargetBlockTimeSeconds)
	next, err := p.Retarget(d0, 0, target/100)
	if err != nil {
		t.Fatalf("retarget: %v", err)
	}
	want := new(big.Int).Mul(d0, big.NewInt(p.ClampFactor))
	if next.Cmp(want) != 0 {
		t.Fatalf("clamp: got %s want %s", next, want)
	}
}

func TestRetargetClampsDownwards(t *testing.T) {
	p := NewDifficultyAdjusterParams()
	d0 := big.NewInt(1 << 30)
	// Blocks took 100× target → raw factor 0.01, must clamp to ÷4.
	target := int64(p.RetargetInterval * p.TargetBlockTimeSeconds)
	next, err := p.Retarget(d0, 0, target*100)
	if err != nil {
		t.Fatalf("retarget: %v", err)
	}
	want := new(big.Int).Quo(d0, big.NewInt(p.ClampFactor))
	if next.Cmp(want) != 0 {
		t.Fatalf("clamp: got %s want %s", next, want)
	}
}

func TestRetargetFloorsAtMinimum(t *testing.T) {
	p := NewDifficultyAdjusterParams()
	p.MinDifficulty = big.NewInt(100)
	d0 := big.NewInt(101)
	// Pretend blocks took 1000× target; raw = 0.001, clamped to ÷4 = 25,
	// floored at MinDifficulty.
	target := int64(p.RetargetInterval * p.TargetBlockTimeSeconds)
	next, err := p.Retarget(d0, 0, target*1000)
	if err != nil {
		t.Fatalf("retarget: %v", err)
	}
	if next.Cmp(p.MinDifficulty) != 0 {
		t.Fatalf("floor: got %s want %s", next, p.MinDifficulty)
	}
}

func TestRetargetRejectsTimeWarp(t *testing.T) {
	p := NewDifficultyAdjusterParams()
	d0 := big.NewInt(1 << 20)
	if _, err := p.Retarget(d0, 100, 100); err == nil {
		t.Fatal("endTime == startTime must error")
	}
	if _, err := p.Retarget(d0, 100, 50); err == nil {
		t.Fatal("endTime < startTime must error")
	}
}

func TestIsRetargetHeight(t *testing.T) {
	p := NewDifficultyAdjusterParams()
	if p.IsRetargetHeight(0) {
		t.Fatal("height 0 is not a retarget")
	}
	if !p.IsRetargetHeight(p.RetargetInterval) {
		t.Fatalf("height %d should be a retarget", p.RetargetInterval)
	}
	if p.IsRetargetHeight(p.RetargetInterval + 1) {
		t.Fatal("off-by-one height should not retarget")
	}
}

func TestTargetFromDifficultyRoundTrip(t *testing.T) {
	for _, d := range []*big.Int{
		big.NewInt(2),
		big.NewInt(1_000_000),
		new(big.Int).Lsh(big.NewInt(1), 80),
	} {
		tgt, err := TargetFromDifficulty(d)
		if err != nil {
			t.Fatalf("target(%s): %v", d, err)
		}
		back, err := DifficultyFromTarget(tgt)
		if err != nil {
			t.Fatalf("roundtrip: %v", err)
		}
		if back.Cmp(d) != 0 {
			t.Fatalf("difficulty roundtrip lost precision: in=%s back=%s", d, back)
		}
	}
}
