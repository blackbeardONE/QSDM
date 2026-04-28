package powv2

import (
	"bytes"
	"encoding/hex"
	"testing"

	mining "github.com/blackbeardONE/QSDM/pkg/mining"
)

// TestMatrixFromMix_Determinism asserts that the same mix always
// expands to the same matrix, and that one-byte changes in the mix
// produce a different matrix in every cell with overwhelming
// probability.
func TestMatrixFromMix_Determinism(t *testing.T) {
	var mix1, mix2 [32]byte
	for i := range mix1 {
		mix1[i] = byte(i)
	}
	mix2 = mix1
	mix2[7] ^= 0x80

	a := MatrixFromMix(mix1)
	b := MatrixFromMix(mix1)
	if a != b {
		t.Fatalf("MatrixFromMix not deterministic for identical input")
	}
	c := MatrixFromMix(mix2)
	// SHAKE256 over a 1-byte XOR gives an essentially uniform new
	// 512-byte stream, so collisions in any cell should be
	// astronomically unlikely (P ~ 256 * 2^-16 < 1%).
	matches := 0
	for i := 0; i < 16; i++ {
		for j := 0; j < 16; j++ {
			if a[i][j] == c[i][j] {
				matches++
			}
		}
	}
	if matches > 8 {
		t.Fatalf("MatrixFromMix has too many collisions after 1-byte input change: %d/256", matches)
	}
}

// TestVectorFromEntry covers the simple entry -> 16x FP16 unpack and
// confirms NaN canonicalization at the decode boundary.
func TestVectorFromEntry(t *testing.T) {
	var entry [32]byte
	for k := 0; k < 16; k++ {
		entry[2*k] = byte(0x3C) // sign 0, exp 01111, frac 0000000000 -> 1.0
		entry[2*k+1] = 0x00
	}
	// Override slot 5 with a NaN payload that should be canonicalized.
	entry[10] = 0x7F
	entry[11] = 0x55

	v := VectorFromEntry(entry)
	for k, want := range []FP16{
		0x3C00, 0x3C00, 0x3C00, 0x3C00, 0x3C00,
		FP16Qnan, // canonicalized
		0x3C00, 0x3C00, 0x3C00, 0x3C00,
		0x3C00, 0x3C00, 0x3C00, 0x3C00, 0x3C00, 0x3C00,
	} {
		if v[k] != want {
			t.Errorf("v[%d] = %#04x, want %#04x", k, uint16(v[k]), uint16(want))
		}
	}
}

// TestTensorMul_Identity verifies that multiplying the identity matrix
// by an arbitrary vector returns the vector unchanged.
func TestTensorMul_Identity(t *testing.T) {
	var I [16][16]FP16
	for i := 0; i < 16; i++ {
		I[i][i] = 0x3C00 // 1.0
	}
	v := [16]FP16{
		0x0000, // 0
		0x3C00, // 1
		0xBC00, // -1
		0x4000, // 2
		0xC000, // -2
		0x4900, // 10
		0x0001, // smallest subnormal
		0x0400, // smallest normal
		0x7BFF, // largest finite
		0x3555, // ~1/3
		0x3266, // ~0.2
		0x4248, // 3.14
		0xC248, // -3.14
		0x0000,
		0x3C00,
		0x3C00,
	}
	r := TensorMul(I, v)
	for k := range v {
		if r[k] != v[k] {
			t.Errorf("identity * v[%d]: got %#04x, want %#04x", k, uint16(r[k]), uint16(v[k]))
		}
	}
}

// TestTensorMul_KnownRow hand-computes one row of a small matmul and
// asserts byte-equality. This is the closest thing to a Tensor-Core
// reference vector we have at the spec layer.
func TestTensorMul_KnownRow(t *testing.T) {
	var M [16][16]FP16
	// Row 0: alternating 1, 0.5
	for j := 0; j < 16; j++ {
		if j%2 == 0 {
			M[0][j] = 0x3C00 // 1.0
		} else {
			M[0][j] = 0x3800 // 0.5
		}
	}
	// All other rows zero.
	v := [16]FP16{
		0x3C00, 0x3C00, 0x3C00, 0x3C00, 0x3C00, 0x3C00, 0x3C00, 0x3C00,
		0x3C00, 0x3C00, 0x3C00, 0x3C00, 0x3C00, 0x3C00, 0x3C00, 0x3C00,
	}
	r := TensorMul(M, v)
	// Sum: 8 * 1 + 8 * 0.5 = 12 -> FP16 0x4A00.
	if r[0] != 0x4A00 {
		t.Fatalf("row-0 dot: got %#04x, want %#04x", uint16(r[0]), 0x4A00)
	}
	for k := 1; k < 16; k++ {
		if r[k] != 0x0000 {
			t.Errorf("row-%d (zero matrix): got %#04x, want 0", k, uint16(r[k]))
		}
	}
}

// TestComputeMixDigestV2_Determinism verifies that ComputeMixDigestV2
// is pure: identical inputs always return the identical 32-byte mix.
func TestComputeMixDigestV2_Determinism(t *testing.T) {
	dag, err := mining.NewInMemoryDAG(0, [32]byte{}, 64)
	if err != nil {
		t.Fatalf("NewInMemoryDAG: %v", err)
	}
	var hh [32]byte
	for i := range hh {
		hh[i] = byte(i)
	}
	var nonce [16]byte
	for i := range nonce {
		nonce[i] = byte(0xA0 + i)
	}
	d1, err := ComputeMixDigestV2(hh, nonce, dag)
	if err != nil {
		t.Fatalf("ComputeMixDigestV2 (1): %v", err)
	}
	d2, err := ComputeMixDigestV2(hh, nonce, dag)
	if err != nil {
		t.Fatalf("ComputeMixDigestV2 (2): %v", err)
	}
	if d1 != d2 {
		t.Fatalf("non-deterministic: %x vs %x", d1, d2)
	}
}

// TestComputeMixDigestV2_DiffersFromV1 asserts that for the same
// inputs, the v2 mixin produces a different digest than the v1 walk.
// This is the bare minimum sanity check that the post-fork validator
// is doing something different from pre-fork.
func TestComputeMixDigestV2_DiffersFromV1(t *testing.T) {
	dag, err := mining.NewInMemoryDAG(0, [32]byte{}, 64)
	if err != nil {
		t.Fatalf("NewInMemoryDAG: %v", err)
	}
	var hh [32]byte
	var nonce [16]byte

	v1, err := mining.ComputeMixDigest(hh, nonce, dag)
	if err != nil {
		t.Fatalf("v1: %v", err)
	}
	v2, err := ComputeMixDigestV2(hh, nonce, dag)
	if err != nil {
		t.Fatalf("v2: %v", err)
	}
	if v1 == v2 {
		t.Fatalf("v1 and v2 produced identical digest %x", v1)
	}
}

// TestComputeMixDigestV2_SmallChangeDiffuses asserts that a 1-bit
// change in the nonce flips ~half the bits of the resulting digest.
// This is a fundamental hash-soundness property and would catch
// catastrophic bugs in the loop body that fail to fold one of the
// inputs into the running mix.
func TestComputeMixDigestV2_SmallChangeDiffuses(t *testing.T) {
	dag, err := mining.NewInMemoryDAG(0, [32]byte{}, 64)
	if err != nil {
		t.Fatalf("NewInMemoryDAG: %v", err)
	}
	var hh [32]byte
	var n1, n2 [16]byte
	n2[0] = 0x01

	d1, err := ComputeMixDigestV2(hh, n1, dag)
	if err != nil {
		t.Fatalf("d1: %v", err)
	}
	d2, err := ComputeMixDigestV2(hh, n2, dag)
	if err != nil {
		t.Fatalf("d2: %v", err)
	}
	diffBits := 0
	for i := range d1 {
		x := d1[i] ^ d2[i]
		for ; x != 0; x &= x - 1 {
			diffBits++
		}
	}
	// 256 bits, expected ~128. Allow a generous +/- 64 envelope.
	if diffBits < 64 || diffBits > 192 {
		t.Errorf("nonce diffuses to only %d bit-differences (expected ~128)", diffBits)
	}
}

// TestComputeMixDigestV2_GoldenVector freezes a single byte-exact
// expected output for a fixed (header_hash, nonce, DAG) input. If
// this ever flips, either the byte-exact spec changed (intentional
// hard fork) or someone broke determinism (regression). Either way,
// CI must scream.
//
// Inputs (chosen to be reproducible by a third-party miner from the
// spec text, with no hidden state):
//
//	header_hash = 0x00..1F
//	nonce       = 0xA0..AF
//	dag         = NewInMemoryDAG(epoch=0, workSetRoot=zero, N=64)
func TestComputeMixDigestV2_GoldenVector(t *testing.T) {
	dag, err := mining.NewInMemoryDAG(0, [32]byte{}, 64)
	if err != nil {
		t.Fatalf("NewInMemoryDAG: %v", err)
	}
	var hh [32]byte
	for i := range hh {
		hh[i] = byte(i)
	}
	var nonce [16]byte
	for i := range nonce {
		nonce[i] = byte(0xA0 + i)
	}
	got, err := ComputeMixDigestV2(hh, nonce, dag)
	if err != nil {
		t.Fatalf("ComputeMixDigestV2: %v", err)
	}
	t.Logf("golden mix-digest: %s", hex.EncodeToString(got[:]))

	// The golden value is generated by this very test's first run;
	// embed it once stable to lock the wire format.
	want, _ := hex.DecodeString(goldenMixDigestV2Hex)
	if len(want) != 32 || !bytes.Equal(got[:], want) {
		t.Fatalf("golden mix-digest mismatch:\n  got  %x\n  want %x", got, want)
	}
}

// goldenMixDigestV2Hex is the byte-exact reference output for the
// inputs in TestComputeMixDigestV2_GoldenVector. Update only as part
// of a versioned protocol change; never as a "test fix".
const goldenMixDigestV2Hex = "ef9319a6134aeb9b77f315427ec81cdbc40a03c60414284864a3e9bbd68153f4"
