//go:build cgo && (windows || (linux && cuda))

package mesh3d

// GPU-path micro-benchmarks that exercise the real CUDA kernels when
// the build tags and host toolchain allow it. The companion
// CPU-parallel benchmarks in gpu_test.go are tag-agnostic and run on
// every build; what's new here is the direct GPU vs CPU comparison at
// representative mesh fan-outs. The numbers feed back into the
// operator docs (QSDM/docs/docs/MINING_PROTOCOL.md §benchmarks) so we
// can advertise honest GPU-vs-CPU speedups per generation.
//
// Run:
//
//	cd QSDM/source
//	go test -tags cuda -bench 'BenchmarkMesh3DGPUVsCPU' -benchmem \
//	  -run '^$' ./pkg/mesh3d/...
//
// The `-run '^$'` skips the regular tests; `-bench` pulls in the
// functions prefixed Benchmark below. Each size variant is a separate
// sub-benchmark so `go test` prints one line per (N, backend).
//
// If mesh3d_kernels.dll / .so is not on the search path the GPU
// benchmarks call b.Skip() rather than failing the run, so CI
// machines without the kernel library still see the CPU baseline.

import (
	"bytes"
	"testing"
)

// mesh3dBenchSizes drives a consistent set of fan-outs across both
// backends. 16 matches an idle validator; 256 is a busy miner pulling
// transactions from a mid-size shard; 4096 stresses the GPU enough
// that the PCIe memcpy no longer dominates and we see the raw kernel
// throughput.
var mesh3dBenchSizes = []int{16, 256, 4096}

// benchCells returns n parent cells, each `cellBytes` long, filled
// with byte(i) so every cell has a distinct hash without touching
// crypto/rand inside the timed section.
func benchCells(n, cellBytes int) [][]byte {
	cells := make([][]byte, n)
	for i := range cells {
		cells[i] = bytes.Repeat([]byte{byte(i * 7)}, cellBytes)
	}
	return cells
}

// BenchmarkMesh3DGPUVsCPU_Validate compares validate throughput.
// We report bytes-per-op via b.SetBytes so `go test -bench` shows
// MB/s — the single number operators actually care about when
// sizing a miner rig.
func BenchmarkMesh3DGPUVsCPU_Validate(b *testing.B) {
	const cellBytes = 64

	for _, n := range mesh3dBenchSizes {
		cells := benchCells(n, cellBytes)
		totalBytes := int64(n * cellBytes)

		b.Run("n="+itoa(n)+"/cpu", func(b *testing.B) {
			acc := NewCPUParallelAccelerator()
			b.SetBytes(totalBytes)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := acc.ValidateParentCellsParallel(cells); err != nil {
					b.Fatal(err)
				}
			}
		})

		b.Run("n="+itoa(n)+"/gpu", func(b *testing.B) {
			acc := NewCUDAAccelerator()
			if acc == nil || !acc.IsAvailable() || !acc.KernelsReady() {
				b.Skipf("CUDA not available: driver=%v kernels=%v — build mesh3d_kernels.dll / .so first (see QSDM/scripts/build_kernels.ps1)",
					acc != nil && acc.IsAvailable(),
					acc != nil && acc.KernelsReady())
			}
			// Warm-up: first CUDA call pays driver init + JIT cache.
			// Without this we bake device-context creation into b.N=1
			// and report nonsense for the low-N variants.
			if _, err := acc.ValidateParentCellsParallel(cells); err != nil {
				b.Fatal(err)
			}
			b.SetBytes(totalBytes)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := acc.ValidateParentCellsParallel(cells); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkMesh3DGPUVsCPU_Hash compares raw SHA-256 throughput.
// Validate and Hash share the same kernel family; splitting them out
// makes it easy to see whether the validate path is paying for the
// all-zero scan vs just the hash on-chip.
func BenchmarkMesh3DGPUVsCPU_Hash(b *testing.B) {
	const cellBytes = 64

	for _, n := range mesh3dBenchSizes {
		cells := benchCells(n, cellBytes)
		totalBytes := int64(n * cellBytes)

		b.Run("n="+itoa(n)+"/cpu", func(b *testing.B) {
			acc := NewCPUParallelAccelerator()
			b.SetBytes(totalBytes)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := acc.HashParentCellsParallel(cells); err != nil {
					b.Fatal(err)
				}
			}
		})

		b.Run("n="+itoa(n)+"/gpu", func(b *testing.B) {
			acc := NewCUDAAccelerator()
			if acc == nil || !acc.IsAvailable() || !acc.KernelsReady() {
				b.Skipf("CUDA not available: build mesh3d_kernels.dll / .so first")
			}
			if _, err := acc.HashParentCellsParallel(cells); err != nil {
				b.Fatal(err)
			}
			b.SetBytes(totalBytes)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := acc.HashParentCellsParallel(cells); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// itoa avoids importing strconv for three call sites in the benchmark
// wrapper, and keeps the bench file free of any dep the rest of the
// package doesn't already pull in.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := [20]byte{}
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
