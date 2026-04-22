//go:build cgo && !(windows || (linux && cuda))
// +build cgo
// +build !windows
// +build !linux !cuda

package mesh3d

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

