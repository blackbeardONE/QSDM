package legacymining

// HL2 WP-E: the config difficulty and the planning arithmetic, plus a CPU
// benchmark of one PoW attempt on the production work shape.

import (
	"math"
	"math/big"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mining"
)

func TestConfigDifficulty(t *testing.T) {
	// v1 keeps the HL1 difficulty exactly.
	if d := ConfigDifficulty(gtConfig()); d == nil || d.Cmp(mining.DefaultMinDifficulty) != 0 {
		t.Fatalf("v1 difficulty %v, want %v", d, mining.DefaultMinDifficulty)
	}
	// A fresh copy: mutating it does not touch mining.DefaultMinDifficulty.
	d := ConfigDifficulty(gtConfig())
	d.SetInt64(3)
	if mining.DefaultMinDifficulty.Cmp(big.NewInt(1<<16)) != 0 {
		t.Fatal("ConfigDifficulty aliased mining.DefaultMinDifficulty")
	}
	for _, bits := range []int{MinDifficultyBits, 20, 26, MaxDifficultyBits} {
		c := v2Config()
		c.DifficultyBits = bits
		d := ConfigDifficulty(c)
		if want := new(big.Int).Lsh(big.NewInt(1), uint(bits)); d == nil || d.Cmp(want) != 0 {
			t.Errorf("bits %d: %v, want %v", bits, d, want)
		}
		if d.Cmp(mining.DefaultMinDifficulty) < 0 {
			t.Errorf("bits %d below the HL1 floor", bits)
		}
	}
	for _, c := range []Config{{Version: 3}, {Version: 2, DifficultyBits: -1}, {Version: 2, DifficultyBits: 256}} {
		if d := ConfigDifficulty(c); d != nil {
			t.Errorf("%+v: %v, want nil", c, d)
		}
	}
}

// The target 2^256/2^bits is met with probability 2^-bits: a proof solved
// against the v2 target meets it, and Verify's MeetsTarget agrees with the
// served difficulty (mining.TargetFromDifficulty is what both use).
func TestConfigDifficultyTarget(t *testing.T) {
	c := v2Config()
	c.DifficultyBits = 20
	tgt, err := mining.TargetFromDifficulty(ConfigDifficulty(c))
	if err != nil {
		t.Fatal(err)
	}
	// floor((2^256-1) / 2^20) - 1 = 2^236 - 2 (mining.TargetFromDifficulty).
	want := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 236), big.NewInt(2))
	if tgt.Cmp(want) != 0 {
		t.Fatalf("target %x, want %x", tgt, want)
	}
}

func TestDifficultyPlanning(t *testing.T) {
	if BlockPeriod != 10*time.Second || GraceDuration != time.Minute {
		t.Fatalf("BlockPeriod %v, GraceDuration %v", BlockPeriod, GraceDuration)
	}
	near := func(name string, got, want float64) {
		t.Helper()
		if math.Abs(got-want) > 1e-9*math.Max(1, math.Abs(want)) {
			t.Errorf("%s = %v, want %v", name, got, want)
		}
	}
	// 2^16 attempts/s at 16 bits: one proof per second.
	near("ppm 16", ExpectedProofsPerMinute(1<<16, 16), 60)
	near("ppm 24", ExpectedProofsPerMinute(1<<24, 24), 60)
	near("ppm 26", ExpectedProofsPerMinute(10e6, 26), 10e6*60/(1<<26))
	near("ppm 0 hashrate", ExpectedProofsPerMinute(0, 20), 0)
	if got := ExpectedSolveTime(1<<20, 24); got != 16*time.Second {
		t.Errorf("solve time = %v, want 16s", got)
	}
	if got := ExpectedSolveTime(0, 24); got != time.Duration(math.MaxInt64) {
		t.Errorf("solve time at 0 H/s = %v", got)
	}
	// Fast miner: no grace loss to speak of.
	fast := LandedProofsPerMinute(1<<20, 20, GraceDuration) // tau = 1 s
	near("landed fast", fast, 60*-math.Expm1(-60))
	// tau = W: (1 - 1/e) of the nominal rate.
	near("landed tau=W", LandedProofsPerMinute(math.Ldexp(1, 22)/60, 22, GraceDuration), 1-math.Exp(-1))
	// Slow miner: far below its nominal share (~W/tau^2).
	slow := LandedProofsPerMinute(1<<16, 26, GraceDuration) // tau = 1024 s
	if nominal := ExpectedProofsPerMinute(1<<16, 26); slow >= nominal*0.1 {
		t.Errorf("slow miner landed %v of nominal %v; expected a strong grace-window loss", slow, nominal)
	}
	near("landed no window", LandedProofsPerMinute(1<<16, 26, 0), ExpectedProofsPerMinute(1<<16, 26))

	for _, c := range []struct {
		h, ppm float64
		want   int
	}{
		{1 << 16, 60, 16}, // the HL1 canary point
		{20e6, 6, 27},     // 20 MH/s, 6/min: 2e8 attempts per proof -> 2^27.6
		{1e3, 6, 16},      // clamped up to the floor
		{1e18, 1, 48},     // clamped down to the ceiling
		{0, 6, 16}, {1e6, 0, 16},
	} {
		if got := SuggestDifficultyBits(c.h, c.ppm); got != c.want {
			t.Errorf("SuggestDifficultyBits(%g, %g) = %d, want %d", c.h, c.ppm, got, c.want)
		}
	}
}

// BenchmarkPoWAttemptCPU measures one CPU PoW attempt (the v1 SHA3 DAG walk
// plus the final hash, i.e. what mining.Solve and the CUDA solver compute
// per nonce) on the production work shape (DAG 1024 entries). ns/op is the
// inverse of the single-core CPU hashrate; feed that hashrate to
// hl1/tools/difficulty-estimate.py:
//
//	go test -run '^$' -bench PoWAttemptCPU -benchtime 3s ./internal/legacymining
func BenchmarkPoWAttemptCPU(b *testing.B) {
	dag, err := mining.NewInMemoryDAG(0, [32]byte{1}, 1024)
	if err != nil {
		b.Fatal(err)
	}
	var hdr, root [32]byte
	hdr[0], root[0] = 0xaa, 0xbb
	var nonce [16]byte
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		nonce[0], nonce[1], nonce[2] = byte(i), byte(i>>8), byte(i>>16)
		mix, err := mining.ComputeMixDigest(hdr, nonce, dag)
		if err != nil {
			b.Fatal(err)
		}
		_ = mining.ProofPoWHash(hdr, nonce, root, mix)
	}
	b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "H/s")
}
