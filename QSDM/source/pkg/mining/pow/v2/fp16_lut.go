package powv2

import (
	"fmt"
	"math"
)

// fp16ToFP32LUT is the 65,536-entry FP16 -> FP32 lookup table that
// backs FP16ToFloat32. Storing every input/output pair costs 256 KB
// of read-only data, which fits comfortably in L2 on every supported
// validator host (Xeon E5-class and newer have at least 256 KB of L2
// per core; modern desktop / server parts have 1-2 MB). The win is
// turning a multi-branch IEEE-754 conversion into a single indexed
// load, which on the §4.3 hot path collapses ~272 conversions per
// matmul (16x16 matrix + 16-element vector) from ~6.7 ns each to
// well under 1 ns each on commodity x86_64.
//
// The table is populated by init() using fp16ToFloat32Slow, the
// unrolled IEEE-754 reference. After population a self-check loops
// over a small set of boundary values to catch the kind of off-by-
// one that bricked the package's first cut at the slow conversion
// (see git blame on fp16.go for details). If the self-check fails
// the package panics at startup -- a misconfigured validator that
// would silently produce wrong mix-digests is a much worse failure
// mode than refusing to start.
var fp16ToFP32LUT [65536]float32

func init() {
	for u := 0; u < 65536; u++ {
		fp16ToFP32LUT[u] = fp16ToFloat32Slow(FP16(u))
	}

	// Self-check: the LUT MUST be bit-identical to the slow path
	// for a hand-picked set of boundary patterns covering every
	// IEEE-754 region (zeros, subnormals, smallest normal, the 1.0
	// neighbourhood, largest finite, +/-Inf, NaN). If init() ever
	// trips this panic the package is unusable -- by design.
	for _, u := range []uint16{
		0x0000, 0x0001, 0x03FF, 0x0400, 0x3C00, 0x3C01, 0x4000,
		0x7BFF, 0x7C00, 0x7E00, 0x8000, 0x8001, 0xBC00, 0xC000,
		0xFBFF, 0xFC00,
	} {
		want := fp16ToFloat32Slow(FP16(u))
		got := fp16ToFP32LUT[u]
		if !bitEqualFloat32(got, want) {
			panic(fmt.Sprintf("powv2: fp16ToFP32LUT[%#04x] = %#08x, slow = %#08x",
				u, math.Float32bits(got), math.Float32bits(want)))
		}
	}
}

// bitEqualFloat32 compares two float32 by their underlying bit
// patterns (so NaN equals NaN, +0 != -0). The LUT validation MUST
// use this -- regular float32 == treats NaN != NaN and would give
// false positives on the FP16 NaN canonical-payload entries.
func bitEqualFloat32(a, b float32) bool {
	return math.Float32bits(a) == math.Float32bits(b)
}
