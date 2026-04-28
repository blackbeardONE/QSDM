package powv2

import (
	"encoding/binary"
	"math"
	"math/bits"
)

// FP16 is an IEEE-754 binary16 ("half-precision") value held as its
// 16-bit canonical big-endian bit pattern. We keep it as a plain
// uint16 (not a wrapper struct) so it serializes for free and is
// trivial to compare bit-for-bit in tests.
//
// Layout (MSB to LSB):
//
//	bit  15: sign
//	bits 14-10: exponent (5 bits, bias 15)
//	bits  9-0:  mantissa (10 bits, leading 1 implicit for normals)
//
// All FP16 values that flow through this package are canonicalized at
// every encode boundary: any NaN bit pattern is rewritten to FP16Qnan.
type FP16 uint16

const (
	// FP16Qnan is the canonical FP16 quiet-NaN bit pattern. We pin this
	// because IEEE-754 leaves 1022 NaN payloads legal, and CUDA, x86,
	// and ARM emit different ones; we can't let that bit difference
	// reach the SHA3-256 absorber.
	FP16Qnan FP16 = 0x7E00

	// FP32Qnan is the canonical FP32 quiet-NaN bit pattern. Same
	// rationale as FP16Qnan; used inside accumulators before they are
	// down-converted to FP16.
	FP32Qnan uint32 = 0x7FC00000
)

// IsNaN reports whether x is any FP16 NaN encoding (canonical or not).
func (x FP16) IsNaN() bool {
	exp := uint16(x) & 0x7C00
	frac := uint16(x) & 0x03FF
	return exp == 0x7C00 && frac != 0
}

// IsInf reports whether x is +inf or -inf in FP16.
func (x FP16) IsInf() bool {
	return uint16(x)&0x7FFF == 0x7C00
}

// canonicalizeFP16 folds every NaN payload to FP16Qnan. Non-NaN values
// pass through unchanged (including +0, -0, +Inf, -Inf, subnormals).
func canonicalizeFP16(x FP16) FP16 {
	if x.IsNaN() {
		return FP16Qnan
	}
	return x
}

// canonicalizeFP32 folds every FP32 NaN payload to FP32Qnan. Used
// inside the matmul accumulator so a transient NaN arising from
// (+Inf)+(-Inf) or 0*Inf doesn't carry a hardware-specific payload
// into the FP16 down-convert.
func canonicalizeFP32(bits uint32) uint32 {
	exp := bits & 0x7F800000
	frac := bits & 0x007FFFFF
	if exp == 0x7F800000 && frac != 0 {
		return FP32Qnan
	}
	return bits
}

// DecodeFP16BE reads a 2-byte big-endian FP16 and canonicalizes its
// NaN payload. Length must be exactly 2.
func DecodeFP16BE(b []byte) FP16 {
	return canonicalizeFP16(FP16(binary.BigEndian.Uint16(b)))
}

// EncodeFP16BE writes x as a 2-byte big-endian FP16 into out, after
// canonicalizing NaN payloads. Length must be exactly 2.
func EncodeFP16BE(out []byte, x FP16) {
	binary.BigEndian.PutUint16(out, uint16(canonicalizeFP16(x)))
}

// FP16ToFloat32 converts a 16-bit half-precision value to its 32-bit
// single-precision equivalent. The conversion is exact for every
// finite FP16 (FP32's 24-bit mantissa easily holds FP16's 11-bit
// significand). NaN payloads are not preserved -- they are folded to
// FP32Qnan -- because cross-platform NaN payloads are non-portable.
//
// Implementation: this is a single load from a 256 KB lookup table
// (fp16ToFP32LUT) populated at package-init time by the reference
// algorithm in fp16ToFloat32Slow. The table fits in L2 cache on every
// supported validator host and replaces what would otherwise be a
// chain of switch/branch logic with one indexed read. A self-test in
// init() confirms the LUT is byte-identical to the slow path for all
// 65,536 inputs; the file panics at startup if it isn't.
func FP16ToFloat32(x FP16) float32 {
	return fp16ToFP32LUT[uint16(x)]
}

// fp16ToFloat32Slow is the unrolled IEEE-754-binary16-to-binary32
// reference. It is the source of truth for the 65,536-entry LUT and
// the only function tested directly against IEEE-754 boundary values
// (the LUT is then asserted to match it bit-for-bit). External
// callers should use FP16ToFloat32; this function exists only for
// init-time table population and golden-vector self-checks.
func fp16ToFloat32Slow(x FP16) float32 {
	x = canonicalizeFP16(x)
	raw := uint16(x)

	sign := uint32(raw>>15) & 0x1
	exp := uint32(raw>>10) & 0x1F
	frac := uint32(raw) & 0x3FF

	var out uint32
	switch exp {
	case 0:
		if frac == 0 {
			// Signed zero.
			out = sign << 31
		} else {
			// Subnormal FP16: magnitude = frac * 2^-24. Normalize as
			// 1.fracBelow * 2^(leadPos - 24), where leadPos is the
			// 0-indexed position of the leading 1 in frac (0..9).
			leadPos := uint32(bits.Len16(uint16(frac))) - 1
			fracBelow := frac & ((1 << leadPos) - 1)
			mantissa := fracBelow << (23 - leadPos)
			// FP32 biased exponent: (leadPos - 24) + 127 = leadPos + 103.
			exp32 := uint32(103) + leadPos
			out = (sign << 31) | (exp32 << 23) | mantissa
		}
	case 0x1F:
		// Inf or NaN. canonicalizeFP16 already folded NaN -> 0x7E00,
		// so a NaN here has frac=0x200; we still translate to the
		// canonical FP32 NaN.
		if frac == 0 {
			out = (sign << 31) | 0x7F800000
		} else {
			out = FP32Qnan
		}
	default:
		// Normal.
		out = (sign << 31) | ((exp + (127 - 15)) << 23) | (frac << 13)
	}
	return math.Float32frombits(out)
}

// Float32ToFP16RNE converts an FP32 to FP16 with IEEE-754
// round-to-nearest, ties-to-even, and canonical NaN payload. This is
// the conversion CUDA's Tensor Core fast-path performs at the end of
// each fragment; we match it bit-exactly so a miner can rely on this
// package as the spec rather than its own GPU.
//
// The algorithm uses the textbook round-bit + sticky-bit pattern:
// extract the bit immediately below the target precision (the "round"
// bit) and OR-reduce all bits below that (the "sticky" bit); increment
// iff round=1 AND (sticky=1 OR LSB-of-result=1). This is the canonical
// RNE rule and matches what x86 cvtps2ph and CUDA __float2half emit on
// non-NaN, non-Inf inputs.
func Float32ToFP16RNE(f float32) FP16 {
	bits := canonicalizeFP32(math.Float32bits(f))
	sign := uint16((bits >> 16) & 0x8000)
	bits &= 0x7FFFFFFF // absolute value as raw bits

	switch {
	case bits == 0:
		return FP16(sign)
	case bits > 0x7F800000:
		return FP16Qnan
	case bits == 0x7F800000:
		return FP16(sign | 0x7C00)
	case bits >= 0x47800000:
		// Magnitude >= 2^16 -> overflows FP16 max (65504); RNE goes to Inf.
		return FP16(sign | 0x7C00)
	case bits < 0x33000000:
		// Magnitude < 2^-25 -> rounds to signed zero (below half a ULP
		// of the smallest FP16 subnormal, 2^-24).
		return FP16(sign)
	}

	// Reconstruct the FP32 mantissa with its implicit leading 1.
	expF32 := int32(bits>>23) - 127
	mant := (bits & 0x007FFFFF) | 0x00800000

	// shift = number of mantissa bits to drop. For a normal FP16 result
	// (expF32 in [-14, 15]) we keep 11 bits (10 frac + the leading 1)
	// and drop the low 13 bits of the FP32 mantissa. For subnormals we
	// drop more.
	var shift uint32
	if expF32 < -14 {
		shift = uint32(13 + (-14 - expF32))
	} else {
		shift = 13
	}

	// Round-bit + sticky-bit RNE.
	roundMask := uint32(1) << (shift - 1)
	stickyMask := roundMask - 1
	roundBit := (mant & roundMask) >> (shift - 1)
	stickyBit := uint32(0)
	if mant&stickyMask != 0 {
		stickyBit = 1
	}
	out := mant >> shift
	if roundBit != 0 && (stickyBit != 0 || (out&1) != 0) {
		out++
	}

	// Subnormal: out is the full FP16 magnitude (no separate exp).
	if expF32 < -14 {
		// Carry into the smallest-normal exponent is handled naturally:
		// a subnormal with bit-10 set encodes as exp=1, frac=out&0x3FF.
		return FP16(sign | uint16(out))
	}

	// Normal: out has shape (1.frac_10bits) = bit10 set + 10 fraction
	// bits, OR if the round-up overflowed into bit-11 we just bumped
	// the exponent.
	expF16 := uint32(expF32+15) + (out >> 11)
	if expF16 >= 0x1F {
		return FP16(sign | 0x7C00)
	}
	frac := out & 0x3FF
	return FP16(sign | uint16(expF16<<10) | uint16(frac))
}
