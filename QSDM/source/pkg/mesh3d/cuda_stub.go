//go:build cgo && !cuda
// +build cgo,!cuda

package mesh3d

import "github.com/blackbeardONE/QSDM/pkg/monitoring/stubactive"

// init flips qsdm_stub_active{kind="mesh3d_cuda"} to 1 in builds
// where CUDA mesh validation falls back to CPU. Operationally
// usually fine (CPU path is correct, just slower) — but operators
// running large meshes who expected CUDA acceleration will see
// throughput far below capacity, and this signal helps surface
// misconfiguration.
func init() {
	stubactive.MarkActive(stubactive.KindMesh3DCUDA)
}

// CUDAAccelerator stub when CUDA is not available
type CUDAAccelerator struct {
	initialized bool
}

// NewCUDAAccelerator returns nil when CUDA is not available
func NewCUDAAccelerator() *CUDAAccelerator {
	return nil
}

// IsAvailable always returns false for stub
func (c *CUDAAccelerator) IsAvailable() bool {
	return false
}

// KernelsReady is false when this build has no CUDA mesh kernels.
func (c *CUDAAccelerator) KernelsReady() bool {
	return false
}

// ValidateParentCellsParallel stub implementation
func (c *CUDAAccelerator) ValidateParentCellsParallel(parentCells [][]byte) ([]bool, error) {
	return nil, nil
}

// HashParentCellsParallel stub implementation
func (c *CUDAAccelerator) HashParentCellsParallel(parentCells [][]byte) ([][]byte, error) {
	return nil, nil
}

// Info returns GPU capability information (stub: no CUDA).
func (c *CUDAAccelerator) Info() GPUInfo {
	return GPUInfo{
		Available:    false,
		KernelsReady: false,
		Backend:      "none",
	}
}

// RuntimeVersion is always 0 in the stub build.
func (c *CUDAAccelerator) RuntimeVersion() int {
	return 0
}
