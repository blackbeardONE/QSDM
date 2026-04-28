package powv2

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"testing"

	"golang.org/x/crypto/sha3"
)

// -----------------------------------------------------------------------------
// Local helpers (avoid importing pkg/mining, which would create a cycle once
// pkg/mining itself imports powv2 via the verifier dispatch).
// -----------------------------------------------------------------------------

// testInMemoryDAG mirrors pkg/mining.NewInMemoryDAG byte-for-byte:
//
//	D[0] = SHA3-256("QSDM/mesh3d-pow/v1" || LE64(epoch) || workSetRoot)
//	D[i] = SHA3-256(D[i-1] || LE32(i))
//
// The recurrence and domain separator are normative (MINING_PROTOCOL §3.3),
// so any change here is a hard fork shared with pkg/mining.
type testInMemoryDAG struct {
	n    uint32
	data []byte
}

func newTestDAG(t *testing.T, epoch uint64, workSetRoot [32]byte, n uint32) *testInMemoryDAG {
	t.Helper()
	if n < 2 {
		t.Fatalf("test DAG: N must be >= 2, got %d", n)
	}
	d := &testInMemoryDAG{n: n, data: make([]byte, uint64(n)*32)}

	h := sha3.New256()
	_, _ = h.Write([]byte("QSDM/mesh3d-pow/v1"))
	var le64 [8]byte
	binary.LittleEndian.PutUint64(le64[:], epoch)
	_, _ = h.Write(le64[:])
	_, _ = h.Write(workSetRoot[:])
	copy(d.data[:32], h.Sum(nil))

	var le32 [4]byte
	for i := uint32(1); i < n; i++ {
		h.Reset()
		_, _ = h.Write(d.data[uint64(i-1)*32 : uint64(i)*32])
		binary.LittleEndian.PutUint32(le32[:], i)
		_, _ = h.Write(le32[:])
		copy(d.data[uint64(i)*32:uint64(i+1)*32], h.Sum(nil))
	}
	return d
}

func (d *testInMemoryDAG) N() uint32 { return d.n }
func (d *testInMemoryDAG) Get(idx uint32) ([32]byte, error) {
	if idx >= d.n {
		return [32]byte{}, fmt.Errorf("test DAG: index %d >= N %d", idx, d.n)
	}
	var out [32]byte
	copy(out[:], d.data[uint64(idx)*32:uint64(idx+1)*32])
	return out, nil
}

// testComputeMixDigestV1 mirrors pkg/mining.ComputeMixDigest exactly. We
// inline it here so this package's tests never import pkg/mining.
func testComputeMixDigestV1(headerHash [32]byte, nonce [16]byte, dag DAG) ([32]byte, error) {
	h := sha3.New256()
	_, _ = h.Write(headerHash[:])
	_, _ = h.Write(nonce[:])
	var mix [32]byte
	copy(mix[:], h.Sum(nil))

	n := dag.N()
	if n == 0 {
		return [32]byte{}, errors.New("test v1: empty DAG")
	}
	var digest [32]byte
	for s := 0; s < 64; s++ {
		idx := binary.BigEndian.Uint32(mix[0:4]) % n
		entry, err := dag.Get(idx)
		if err != nil {
			return [32]byte{}, err
		}
		h.Reset()
		_, _ = h.Write(mix[:])
		_, _ = h.Write(entry[:])
		copy(mix[:], h.Sum(digest[:0]))
	}
	return mix, nil
}

// -----------------------------------------------------------------------------
// Tests
// -----------------------------------------------------------------------------

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
		entry[2*k] = byte(0x3C)
		entry[2*k+1] = 0x00
	}
	entry[10] = 0x7F
	entry[11] = 0x55

	v := VectorFromEntry(entry)
	for k, want := range []FP16{
		0x3C00, 0x3C00, 0x3C00, 0x3C00, 0x3C00,
		FP16Qnan,
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
		I[i][i] = 0x3C00
	}
	v := [16]FP16{
		0x0000, 0x3C00, 0xBC00, 0x4000, 0xC000, 0x4900, 0x0001, 0x0400,
		0x7BFF, 0x3555, 0x3266, 0x4248, 0xC248, 0x0000, 0x3C00, 0x3C00,
	}
	r := TensorMul(I, v)
	for k := range v {
		if r[k] != v[k] {
			t.Errorf("identity * v[%d]: got %#04x, want %#04x", k, uint16(r[k]), uint16(v[k]))
		}
	}
}

// TestTensorMul_KnownRow hand-computes one row of a small matmul and
// asserts byte-equality.
func TestTensorMul_KnownRow(t *testing.T) {
	var M [16][16]FP16
	for j := 0; j < 16; j++ {
		if j%2 == 0 {
			M[0][j] = 0x3C00
		} else {
			M[0][j] = 0x3800
		}
	}
	v := [16]FP16{
		0x3C00, 0x3C00, 0x3C00, 0x3C00, 0x3C00, 0x3C00, 0x3C00, 0x3C00,
		0x3C00, 0x3C00, 0x3C00, 0x3C00, 0x3C00, 0x3C00, 0x3C00, 0x3C00,
	}
	r := TensorMul(M, v)
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
	dag := newTestDAG(t, 0, [32]byte{}, 64)
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
	dag := newTestDAG(t, 0, [32]byte{}, 64)
	var hh [32]byte
	var nonce [16]byte

	v1, err := testComputeMixDigestV1(hh, nonce, dag)
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
func TestComputeMixDigestV2_SmallChangeDiffuses(t *testing.T) {
	dag := newTestDAG(t, 0, [32]byte{}, 64)
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
//	dag         = MINING_PROTOCOL §3.3 in-memory DAG, epoch=0,
//	              workSetRoot=zero, N=64
func TestComputeMixDigestV2_GoldenVector(t *testing.T) {
	dag := newTestDAG(t, 0, [32]byte{}, 64)
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

	want, _ := hex.DecodeString(goldenMixDigestV2Hex)
	if len(want) != 32 || !bytes.Equal(got[:], want) {
		t.Fatalf("golden mix-digest mismatch:\n  got  %x\n  want %x", got, want)
	}
}

// goldenMixDigestV2Hex is the byte-exact reference output for the
// inputs in TestComputeMixDigestV2_GoldenVector. Update only as part
// of a versioned protocol change; never as a "test fix".
const goldenMixDigestV2Hex = "ef9319a6134aeb9b77f315427ec81cdbc40a03c60414284864a3e9bbd68153f4"
