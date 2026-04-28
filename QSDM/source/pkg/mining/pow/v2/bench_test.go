package powv2

import "testing"

// Benchmarks for the powv2 hot path. The numbers we care about for
// the §4.3 validator-cost estimate:
//
//   - BenchmarkComputeMixDigestV2: total per-attempt cost (includes
//     64 SHAKE256 expansions, 64 16x16 matmuls, 64 SHA3-256
//     finalizations, 64 DAG lookups). Validator SLO: < 100 ms,
//     §4.3 budget: ~700 µs.
//   - BenchmarkTensorMul: inner-loop matmul only. Cell-by-cell
//     FP16->FP32 conversion is currently the suspected hot spot.
//   - BenchmarkMatrixFromMix: SHAKE256 expansion + 256 FP16
//     decodes. Run once per outer iteration.
//   - BenchmarkFP16ToFloat32: per-element conversion micro-bench;
//     answers "is the per-cell branch tree the bottleneck or is
//     it FP32 arithmetic?"
//   - BenchmarkFloat32ToFP16RNE: down-convert micro-bench, run 16
//     times per matmul.

func BenchmarkComputeMixDigestV2(b *testing.B) {
	dag := newBenchDAG(b, 0, [32]byte{}, 64)
	var hh [32]byte
	for i := range hh {
		hh[i] = byte(i)
	}
	var nonce [16]byte
	for i := range nonce {
		nonce[i] = byte(0xA0 + i)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := ComputeMixDigestV2(hh, nonce, dag)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMatrixFromMix(b *testing.B) {
	var mix [32]byte
	for i := range mix {
		mix[i] = byte(i)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = MatrixFromMix(mix)
	}
}

func BenchmarkTensorMul(b *testing.B) {
	var mix [32]byte
	for i := range mix {
		mix[i] = byte(i)
	}
	M := MatrixFromMix(mix)
	var entry [32]byte
	for i := range entry {
		entry[i] = byte(0xC0 - i)
	}
	v := VectorFromEntry(entry)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = TensorMul(M, v)
	}
}

func BenchmarkFP16ToFloat32(b *testing.B) {
	// Mix of normals, subnormals, zeros, inf, NaN to defeat any
	// branch-prediction luck the benchmark might otherwise enjoy.
	patterns := []FP16{
		0x0000, 0x0001, 0x03FF, 0x0400, 0x3C00, 0x4000, 0x7BFF,
		0x7C00, 0x7E00, 0xBC00, 0xC000, 0xFBFF, 0xFC00, 0x8000,
	}
	b.ReportAllocs()
	b.ResetTimer()
	var sink float32
	for i := 0; i < b.N; i++ {
		sink += FP16ToFloat32(patterns[i&0xF&0xFF%len(patterns)])
	}
	_ = sink
}

func BenchmarkFloat32ToFP16RNE(b *testing.B) {
	patterns := []float32{
		0, 1, -1, 2, -2, 0.5, -0.5, 65504, -65504,
		1e-7, 1e-5, 1e10, -1e10, 3.14159, -3.14159,
	}
	b.ReportAllocs()
	b.ResetTimer()
	var sink FP16
	for i := 0; i < b.N; i++ {
		sink ^= Float32ToFP16RNE(patterns[i%len(patterns)])
	}
	_ = sink
}

// newBenchDAG mirrors newTestDAG but uses *testing.B. Kept separate
// so the benchmark file does not depend on the test file's helpers
// surviving any future refactor.
func newBenchDAG(b *testing.B, epoch uint64, workSetRoot [32]byte, n uint32) *testInMemoryDAG {
	b.Helper()
	// Reuse newTestDAG by adapting *testing.B into *testing.T's
	// Helper/Fatalf surface. testInMemoryDAG only calls Fatalf in
	// the n<2 branch, which we precondition out.
	if n < 2 {
		b.Fatalf("bench DAG: N must be >= 2, got %d", n)
	}
	return buildBenchDAG(epoch, workSetRoot, n)
}
