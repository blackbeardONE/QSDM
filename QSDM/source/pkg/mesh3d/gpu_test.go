package mesh3d

import (
	"bytes"
	"crypto/sha256"
	"testing"
)

func TestCPUParallelAccelerator_Info(t *testing.T) {
	acc := NewCPUParallelAccelerator()
	info := acc.Info()

	if !info.Available {
		t.Error("CPU accelerator should be available")
	}
	if !info.KernelsReady {
		t.Error("CPU accelerator kernels should be ready")
	}
	if info.Backend != "cpu_parallel" {
		t.Errorf("backend = %q, want cpu_parallel", info.Backend)
	}
}

func TestCPUParallelAccelerator_Validate(t *testing.T) {
	acc := NewCPUParallelAccelerator()

	cells := [][]byte{
		bytes.Repeat([]byte{0x01}, 32),
		bytes.Repeat([]byte{0x02}, 64),
		bytes.Repeat([]byte{0x03}, 128),
		make([]byte, 10), // too small
	}

	results, err := acc.ValidateParentCellsParallel(cells)
	if err != nil {
		t.Fatalf("ValidateParentCellsParallel: %v", err)
	}

	if len(results) != 4 {
		t.Fatalf("expected 4 results, got %d", len(results))
	}

	if !results[0] {
		t.Error("cell 0 (32 bytes) should be valid")
	}
	if !results[1] {
		t.Error("cell 1 (64 bytes) should be valid")
	}
	if !results[2] {
		t.Error("cell 2 (128 bytes) should be valid")
	}
	if results[3] {
		t.Error("cell 3 (10 bytes) should be invalid (too small)")
	}
}

func TestCPUParallelAccelerator_Hash(t *testing.T) {
	acc := NewCPUParallelAccelerator()

	cells := [][]byte{
		bytes.Repeat([]byte{0xAA}, 32),
		bytes.Repeat([]byte{0xBB}, 64),
	}

	hashes, err := acc.HashParentCellsParallel(cells)
	if err != nil {
		t.Fatalf("HashParentCellsParallel: %v", err)
	}

	if len(hashes) != 2 {
		t.Fatalf("expected 2 hashes, got %d", len(hashes))
	}

	for i, cell := range cells {
		expected := sha256.Sum256(cell)
		if !bytes.Equal(hashes[i], expected[:]) {
			t.Errorf("hash[%d] mismatch", i)
		}
	}
}

func TestCPUParallelAccelerator_EmptyInput(t *testing.T) {
	acc := NewCPUParallelAccelerator()

	results, err := acc.ValidateParentCellsParallel(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}

	hashes, err := acc.HashParentCellsParallel(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hashes) != 0 {
		t.Errorf("expected 0 hashes, got %d", len(hashes))
	}
}

func TestGPUAcceleratorInterface(t *testing.T) {
	var _ GPUAccelerator = (*CPUParallelAccelerator)(nil)
}

func BenchmarkCPUParallelValidate(b *testing.B) {
	acc := NewCPUParallelAccelerator()
	cells := make([][]byte, 100)
	for i := range cells {
		cells[i] = bytes.Repeat([]byte{byte(i)}, 64)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		acc.ValidateParentCellsParallel(cells)
	}
}

func BenchmarkCPUParallelHash(b *testing.B) {
	acc := NewCPUParallelAccelerator()
	cells := make([][]byte, 100)
	for i := range cells {
		cells[i] = bytes.Repeat([]byte{byte(i)}, 64)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		acc.HashParentCellsParallel(cells)
	}
}
