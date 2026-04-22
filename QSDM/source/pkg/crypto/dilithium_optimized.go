package crypto

import (
	"sync"
)

// SigningOptimizer provides optimizations for ML-DSA-87 signing
// without changing the algorithm (keeps 256-bit security)
type SigningOptimizer struct {
	// Memory pool for signature buffers to reduce allocations
	sigBufPool sync.Pool
	// Pre-allocated buffer for common signature sizes
	preallocBuf []byte
	mu          sync.RWMutex
}

var globalOptimizer *SigningOptimizer
var optimizerOnce sync.Once

// GetSigningOptimizer returns the global signing optimizer instance
func GetSigningOptimizer() *SigningOptimizer {
	optimizerOnce.Do(func() {
		globalOptimizer = &SigningOptimizer{
			sigBufPool: sync.Pool{
				New: func() interface{} {
					// Pre-allocate buffer for ML-DSA-87 signature size (4627 bytes)
					return make([]byte, 5000) // Slightly larger for safety
				},
			},
			preallocBuf: make([]byte, 5000),
		}
	})
	return globalOptimizer
}

// Note: SignOptimized and SignBatchOptimized are now in dilithium.go
// This file contains the optimizer infrastructure

