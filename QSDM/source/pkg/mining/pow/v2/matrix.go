package powv2

import (
	"golang.org/x/crypto/sha3"
)

// MatrixDomainSeparator is the fixed ASCII tag mixed in to SHAKE256 so
// the FP16 matrix expansion is domain-separated from every other
// SHA3/SHAKE use in the protocol. Any change to this string is a hard
// fork.
const MatrixDomainSeparator = "qsdm/pow/v2/matrix\x00"

// matrixBytes is the number of bytes SHAKE256 must emit to populate a
// 16x16 FP16 matrix: 16 * 16 * 2 = 512.
const matrixBytes = 16 * 16 * 2

// MatrixFromMix expands the 32-byte running mix into a deterministic
// 16x16 FP16 matrix. The expansion is keyed solely on mix and
// MatrixDomainSeparator, so two validators starting from the same mix
// (and only from the same mix) produce bit-identical matrices.
//
// SHAKE256 is the variable-output FIPS-202 XOF; we read exactly 512
// output bytes and decode them in row-major big-endian FP16 order
// (M[0][0] takes the first 2 bytes, M[0][1] the next, ..., M[15][15]
// the last). Decoding canonicalizes any FP16 NaN payload to FP16Qnan.
func MatrixFromMix(mix [32]byte) [16][16]FP16 {
	h := sha3.NewShake256()
	_, _ = h.Write([]byte(MatrixDomainSeparator))
	_, _ = h.Write(mix[:])

	var raw [matrixBytes]byte
	_, _ = h.Read(raw[:])

	var m [16][16]FP16
	for i := 0; i < 16; i++ {
		for j := 0; j < 16; j++ {
			off := 2 * (16*i + j)
			m[i][j] = DecodeFP16BE(raw[off : off+2])
		}
	}
	return m
}

// VectorFromEntry decodes a 32-byte DAG entry as 16 big-endian FP16
// elements. NaN payloads are canonicalized at decode time.
func VectorFromEntry(entry [32]byte) [16]FP16 {
	var v [16]FP16
	for k := 0; k < 16; k++ {
		v[k] = DecodeFP16BE(entry[2*k : 2*k+2])
	}
	return v
}

// PackFP16VectorBE writes the 16-element FP16 vector r into a 32-byte
// big-endian byte string (the "tc" output of step body §4.2). NaN
// payloads are canonicalized at encode time.
func PackFP16VectorBE(r [16]FP16) [32]byte {
	var out [32]byte
	for k := 0; k < 16; k++ {
		EncodeFP16BE(out[2*k:2*k+2], r[k])
	}
	return out
}

// TensorMul computes r = M * v as a 16-element FP16 vector. For each
// output element r[i]:
//
//	acc := float32(0)
//	for j in 0..16: acc = acc + (float32(M[i][j]) * float32(v[j]))
//	r[i] := Float32ToFP16RNE(acc)
//
// Multiplication is performed in FP32, which is exact for any product
// of two finite FP16 values (FP32's 24-bit mantissa easily holds the
// 22-bit product of two 11-bit significands; FP32's exponent range
// covers FP16 * FP16 with room to spare). Accumulation is done in
// FP32 with strict left-to-right order; any transient NaN is folded
// to the canonical payload. The final FP32 -> FP16 down-convert uses
// IEEE-754 round-to-nearest, ties-to-even.
//
// Strict left-to-right order is what differs most often from CUDA
// WMMA's tree-reduction; miners using WMMA must emulate this loop's
// order in software (e.g. one mac per thread) to stay bit-compatible.
func TensorMul(M [16][16]FP16, v [16]FP16) [16]FP16 {
	var r [16]FP16
	var vf [16]float32
	for j := 0; j < 16; j++ {
		vf[j] = FP16ToFloat32(v[j])
	}
	for i := 0; i < 16; i++ {
		var acc float32
		for j := 0; j < 16; j++ {
			prod := FP16ToFloat32(M[i][j]) * vf[j]
			acc = acc + prod
		}
		r[i] = Float32ToFP16RNE(acc)
	}
	return r
}
