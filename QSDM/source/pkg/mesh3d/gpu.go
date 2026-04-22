package mesh3d

import (
	"crypto/sha256"
	"runtime"
	"sync"
)

// GPUInfo reports capabilities of the underlying GPU accelerator.
type GPUInfo struct {
	Available    bool   `json:"available"`
	KernelsReady bool   `json:"kernels_ready"`
	DeviceName   string `json:"device_name,omitempty"`
	Backend      string `json:"backend"` // "cuda", "cpu_parallel", "none"
}

// GPUAccelerator is the interface satisfied by both the real CUDA accelerator
// and the CPU-parallel fallback.
type GPUAccelerator interface {
	IsAvailable() bool
	KernelsReady() bool
	ValidateParentCellsParallel(parentCells [][]byte) ([]bool, error)
	HashParentCellsParallel(parentCells [][]byte) ([][]byte, error)
	Info() GPUInfo
}

// CPUParallelAccelerator provides a goroutine-based fallback that mirrors the
// CUDA accelerator API. It uses GOMAXPROCS workers to validate and hash
// parent cells concurrently.
type CPUParallelAccelerator struct {
	workers int
}

// NewCPUParallelAccelerator creates an accelerator that distributes work across
// available CPU cores.
func NewCPUParallelAccelerator() *CPUParallelAccelerator {
	w := runtime.GOMAXPROCS(0)
	if w < 1 {
		w = 1
	}
	return &CPUParallelAccelerator{workers: w}
}

func (c *CPUParallelAccelerator) IsAvailable() bool  { return true }
func (c *CPUParallelAccelerator) KernelsReady() bool  { return true }

func (c *CPUParallelAccelerator) Info() GPUInfo {
	return GPUInfo{
		Available:    true,
		KernelsReady: true,
		Backend:      "cpu_parallel",
	}
}

// ValidateParentCellsParallel validates parent cells in parallel using goroutines.
// A cell is valid if it is at least 32 bytes and doesn't hash to all zeros.
func (c *CPUParallelAccelerator) ValidateParentCellsParallel(parentCells [][]byte) ([]bool, error) {
	results := make([]bool, len(parentCells))
	var wg sync.WaitGroup

	sem := make(chan struct{}, c.workers)

	for i, cell := range parentCells {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int, data []byte) {
			defer wg.Done()
			defer func() { <-sem }()
			results[idx] = validateCell(data)
		}(i, cell)
	}

	wg.Wait()
	return results, nil
}

// HashParentCellsParallel computes SHA-256 hashes of parent cells in parallel.
func (c *CPUParallelAccelerator) HashParentCellsParallel(parentCells [][]byte) ([][]byte, error) {
	results := make([][]byte, len(parentCells))
	var wg sync.WaitGroup

	sem := make(chan struct{}, c.workers)

	for i, cell := range parentCells {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int, data []byte) {
			defer wg.Done()
			defer func() { <-sem }()
			h := sha256.Sum256(data)
			results[idx] = h[:]
		}(i, cell)
	}

	wg.Wait()
	return results, nil
}

func validateCell(data []byte) bool {
	if len(data) < 32 {
		return false
	}
	h := sha256.Sum256(data)
	allZero := true
	for _, b := range h {
		if b != 0 {
			allZero = false
			break
		}
	}
	return !allZero
}
