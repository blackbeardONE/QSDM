//go:build cgo && (windows || (linux && cuda))
// +build cgo
// +build windows linux,cuda

package mesh3d

/*
#cgo CFLAGS: -IC:/CUDA/include -Wno-builtin-macro-redefined -Wno-error=builtin-macro-redefined
#cgo LDFLAGS: -LC:/CUDA/lib/x64 -lcudart -lmesh3d_kernels
#include <cuda_runtime.h>
#include <stdlib.h>
#include <stdint.h>

// Implemented in kernels/sha256_validate.cu (compiled to mesh3d_kernels.dll/.so)
extern int mesh3d_hash_cells(
    const uint8_t *h_data,
    const uint32_t *h_offsets,
    const uint32_t *h_lengths,
    uint8_t *h_hashes,
    int n,
    uint32_t total_bytes
);

extern int mesh3d_validate_cells(
    const uint8_t *h_data,
    const uint32_t *h_offsets,
    const uint32_t *h_lengths,
    int *h_results,
    int n,
    uint32_t total_bytes
);
*/
import "C"
import (
	"fmt"
	"unsafe"
)

// CUDAAccelerator provides GPU-accelerated validation for 3D mesh
type CUDAAccelerator struct {
	initialized  bool
	deviceName   string
	kernelsLinked bool
}

// NewCUDAAccelerator creates a new CUDA accelerator instance
func NewCUDAAccelerator() *CUDAAccelerator {
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("CUDA: Panic during initialization: %v\n", r)
			fmt.Printf("CUDA: This may indicate missing CUDA DLLs (cudart64_*.dll)\n")
			fmt.Printf("CUDA: Continuing without CUDA acceleration\n")
		}
	}()

	var deviceCount C.int
	result := C.cudaGetDeviceCount(&deviceCount)
	if result != C.cudaSuccess || deviceCount == 0 {
		return nil
	}

	var devName string
	var prop C.struct_cudaDeviceProp
	if C.cudaGetDeviceProperties(&prop, 0) == C.cudaSuccess {
		devName = C.GoString(&prop.name[0])
	}

	return &CUDAAccelerator{
		initialized:  true,
		deviceName:   devName,
		kernelsLinked: true,
	}
}

func (c *CUDAAccelerator) flattenCells(parentCells [][]byte) ([]byte, []uint32, []uint32) {
	n := len(parentCells)
	offsets := make([]uint32, n)
	lengths := make([]uint32, n)
	total := 0
	for i, cell := range parentCells {
		offsets[i] = uint32(total)
		lengths[i] = uint32(len(cell))
		total += len(cell)
	}
	flat := make([]byte, total)
	off := 0
	for _, cell := range parentCells {
		copy(flat[off:], cell)
		off += len(cell)
	}
	return flat, offsets, lengths
}

// ValidateParentCellsParallel validates multiple parent cells in parallel on GPU
func (c *CUDAAccelerator) ValidateParentCellsParallel(parentCells [][]byte) ([]bool, error) {
	if !c.initialized || !c.kernelsLinked {
		return nil, nil
	}
	n := len(parentCells)
	if n == 0 {
		return nil, nil
	}

	flat, offsets, lengths := c.flattenCells(parentCells)
	results := make([]C.int, n)

	rc := C.mesh3d_validate_cells(
		(*C.uint8_t)(unsafe.Pointer(&flat[0])),
		(*C.uint32_t)(unsafe.Pointer(&offsets[0])),
		(*C.uint32_t)(unsafe.Pointer(&lengths[0])),
		(*C.int)(unsafe.Pointer(&results[0])),
		C.int(n),
		C.uint32_t(len(flat)),
	)
	if rc != 0 {
		return nil, fmt.Errorf("CUDA validate_cells error code %d", rc)
	}

	bools := make([]bool, n)
	for i, v := range results {
		bools[i] = v != 0
	}
	return bools, nil
}

// HashParentCellsParallel computes SHA-256 hashes of parent cells in parallel on GPU
func (c *CUDAAccelerator) HashParentCellsParallel(parentCells [][]byte) ([][]byte, error) {
	if !c.initialized || !c.kernelsLinked {
		return nil, nil
	}
	n := len(parentCells)
	if n == 0 {
		return nil, nil
	}

	flat, offsets, lengths := c.flattenCells(parentCells)
	hashBuf := make([]byte, n*32)

	rc := C.mesh3d_hash_cells(
		(*C.uint8_t)(unsafe.Pointer(&flat[0])),
		(*C.uint32_t)(unsafe.Pointer(&offsets[0])),
		(*C.uint32_t)(unsafe.Pointer(&lengths[0])),
		(*C.uint8_t)(unsafe.Pointer(&hashBuf[0])),
		C.int(n),
		C.uint32_t(len(flat)),
	)
	if rc != 0 {
		return nil, fmt.Errorf("CUDA hash_cells error code %d", rc)
	}

	hashes := make([][]byte, n)
	for i := 0; i < n; i++ {
		h := make([]byte, 32)
		copy(h, hashBuf[i*32:(i+1)*32])
		hashes[i] = h
	}
	return hashes, nil
}

// IsAvailable checks if CUDA runtime and at least one device are usable.
func (c *CUDAAccelerator) IsAvailable() bool {
	return c != nil && c.initialized
}

// KernelsReady returns true when the CUDA kernel library is linked and available.
func (c *CUDAAccelerator) KernelsReady() bool {
	return c != nil && c.initialized && c.kernelsLinked
}

// Info returns GPU capability information.
func (c *CUDAAccelerator) Info() GPUInfo {
	return GPUInfo{
		Available:    c.IsAvailable(),
		KernelsReady: c.KernelsReady(),
		DeviceName:   c.deviceName,
		Backend:      "cuda",
	}
}
