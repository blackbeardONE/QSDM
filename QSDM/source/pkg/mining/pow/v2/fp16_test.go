package powv2

import (
	"math"
	"testing"
)

// TestFP16_DecodeEncodeRoundTrip covers every 16-bit value and asserts
// that DecodeFP16BE -> EncodeFP16BE is the identity for non-NaN inputs
// and folds every NaN payload to FP16Qnan.
func TestFP16_DecodeEncodeRoundTrip(t *testing.T) {
	for u := uint32(0); u < 1<<16; u++ {
		var b [2]byte
		b[0] = byte(u >> 8)
		b[1] = byte(u)
		got := DecodeFP16BE(b[:])

		isNaN := (uint16(u)&0x7C00) == 0x7C00 && (uint16(u)&0x03FF) != 0
		if isNaN {
			if got != FP16Qnan {
				t.Fatalf("NaN payload %#04x decoded to %#04x, want %#04x", u, uint16(got), uint16(FP16Qnan))
			}
		} else {
			if uint16(got) != uint16(u) {
				t.Fatalf("non-NaN %#04x decoded to %#04x", u, uint16(got))
			}
		}

		var enc [2]byte
		EncodeFP16BE(enc[:], got)
		if isNaN {
			if enc[0] != 0x7E || enc[1] != 0x00 {
				t.Fatalf("re-encoded NaN: %x %x", enc[0], enc[1])
			}
		} else {
			if enc[0] != b[0] || enc[1] != b[1] {
				t.Fatalf("re-encoded %#04x as %x %x", u, enc[0], enc[1])
			}
		}
	}
}

// TestFP16ToFloat32_Specials covers the IEEE-754 special values that
// matter: signed zero, smallest subnormal, smallest normal, largest
// finite, +-Inf, NaN, and a handful of "ordinary" values.
func TestFP16ToFloat32_Specials(t *testing.T) {
	type tc struct {
		name string
		fp16 uint16
		want float32
	}
	cases := []tc{
		{"+0", 0x0000, 0},
		{"-0", 0x8000, float32(math.Copysign(0, -1))},
		{"smallest subnormal", 0x0001, float32(math.Ldexp(1, -24))},
		{"largest subnormal", 0x03FF, float32(math.Ldexp(1023, -24))},
		{"smallest normal", 0x0400, float32(math.Ldexp(1, -14))},
		{"1.0", 0x3C00, 1.0},
		{"-1.0", 0xBC00, -1.0},
		{"2.0", 0x4000, 2.0},
		{"largest finite (65504)", 0x7BFF, 65504.0},
		{"+Inf", 0x7C00, float32(math.Inf(+1))},
		{"-Inf", 0xFC00, float32(math.Inf(-1))},
	}
	for _, c := range cases {
		got := FP16ToFloat32(FP16(c.fp16))
		if math.Float32bits(got) != math.Float32bits(float32(c.want)) {
			t.Errorf("%s (%#04x) -> %v (bits %#08x), want %v (bits %#08x)",
				c.name, c.fp16, got, math.Float32bits(got),
				c.want, math.Float32bits(float32(c.want)))
		}
	}
	// NaN is handled separately.
	got := FP16ToFloat32(FP16(0x7E00))
	if !math.IsNaN(float64(got)) {
		t.Errorf("FP16Qnan -> %v, want NaN", got)
	}
	if math.Float32bits(got) != FP32Qnan {
		t.Errorf("FP16Qnan -> non-canonical FP32 NaN bits %#08x", math.Float32bits(got))
	}
}

// TestFP16ToFP32_LUTMatchesSlow asserts that the 65,536-entry LUT
// populated by init() is byte-identical to the unrolled IEEE-754
// reference (fp16ToFloat32Slow) for every possible FP16 input. If
// this ever fails, FP16ToFloat32 is silently producing wrong values
// on whatever subset of inputs slipped through, and every downstream
// mix-digest is wrong by a corresponding amount.
func TestFP16ToFP32_LUTMatchesSlow(t *testing.T) {
	for u := 0; u < 65536; u++ {
		want := fp16ToFloat32Slow(FP16(u))
		got := FP16ToFloat32(FP16(u))
		if !bitEqualFloat32(got, want) {
			t.Fatalf("FP16ToFloat32(%#04x): LUT %#08x, slow %#08x",
				u, math.Float32bits(got), math.Float32bits(want))
		}
	}
}

// TestFP16_RoundTrip16Bit asserts that for every finite 16-bit value v,
// the FP16 -> FP32 -> FP16 round-trip is the identity. This is the
// strongest determinism check: if it fails, no upper-layer code can
// trust this package for byte-exactness.
func TestFP16_RoundTrip16Bit(t *testing.T) {
	for u := uint32(0); u < 1<<16; u++ {
		fp := FP16(u)
		// Skip NaN payloads (they're canonicalized, not preserved).
		if fp.IsNaN() {
			continue
		}
		f32 := FP16ToFloat32(fp)
		got := Float32ToFP16RNE(f32)
		if uint16(got) != uint16(fp) {
			t.Fatalf("round-trip %#04x -> %v -> %#04x", u, f32, uint16(got))
		}
	}
}

// TestFloat32ToFP16RNE_Specials covers FP32 -> FP16 conversion at the
// boundaries that historically trip up hand-written conversions.
func TestFloat32ToFP16RNE_Specials(t *testing.T) {
	type tc struct {
		name string
		f32  float32
		want uint16
	}
	cases := []tc{
		{"+0", 0, 0x0000},
		{"-0", float32(math.Copysign(0, -1)), 0x8000},
		{"1.0", 1.0, 0x3C00},
		{"-1.0", -1.0, 0xBC00},
		// Exactly halfway between FP16 1.0 (0x3C00) and 1.0 + 2^-10
		// (0x3C01). Bit-13 (the round bit) is set, lower bits are
		// zero, current LSB is 0 -> tie-to-even rounds DOWN, stays
		// 0x3C00.
		{"halfway-down (tie to 0x3C00)",
			math.Float32frombits(0x3F801000), 0x3C00},
		// One ulp above the halfway point: round bit set, sticky set
		// -> rounds up to 0x3C01.
		{"halfway+1 (round up)",
			math.Float32frombits(0x3F801001), 0x3C01},
		// 2^-25 - eps: rounds to +0.
		{"below subnormal threshold rounds to 0",
			math.Float32frombits(0x32FFFFFF), 0x0000},
		// 2^-24: smallest FP16 subnormal (0x0001).
		{"smallest FP16 subnormal", float32(math.Ldexp(1, -24)), 0x0001},
		// 2^-14: smallest FP16 normal.
		{"smallest FP16 normal", float32(math.Ldexp(1, -14)), 0x0400},
		// 65504: largest finite FP16.
		{"largest finite FP16", 65504.0, 0x7BFF},
		// 65520 = halfway between 65504 and 2^16 -> overflow to Inf.
		{"overflow to Inf", 65520.0, 0x7C00},
		{"+Inf", float32(math.Inf(+1)), 0x7C00},
		{"-Inf", float32(math.Inf(-1)), 0xFC00},
	}
	for _, c := range cases {
		got := Float32ToFP16RNE(c.f32)
		if uint16(got) != c.want {
			t.Errorf("%s: got %#04x, want %#04x", c.name, uint16(got), c.want)
		}
	}
	// NaN: any NaN input must produce FP16Qnan exactly.
	for _, nanBits := range []uint32{0x7FC00000, 0x7F800001, 0xFFFFFFFF, 0xFFC00000} {
		got := Float32ToFP16RNE(math.Float32frombits(nanBits))
		if got != FP16Qnan {
			t.Errorf("NaN bits %#08x -> %#04x, want FP16Qnan %#04x",
				nanBits, uint16(got), uint16(FP16Qnan))
		}
	}
}
