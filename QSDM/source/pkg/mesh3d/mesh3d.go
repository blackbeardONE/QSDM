//go:build cgo
// +build cgo

package mesh3d

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// ParentCell represents a parent cell in the 3D mesh.
type ParentCell struct {
	ID   string
	Data []byte
}

// Transaction represents a transaction with multiple parent cells.
type Transaction struct {
	ID          string
	ParentCells []ParentCell
	Data        []byte
}

// Mesh3DValidator validates transactions in a 3D mesh with 3-5 parent cells.
type Mesh3DValidator struct {
	mu   sync.Mutex
	cuda *CUDAAccelerator
}

// NewMesh3DValidator creates a new Mesh3DValidator instance.
//
// The validator holds no key: the payload signature is verified under the
// payload's own public key (structure.go), so there is nothing to generate.
func NewMesh3DValidator() *Mesh3DValidator {
	// Initialize CUDA accelerator first (may fail if CUDA DLLs missing)
	// Wrap in recover to prevent crash if CUDA initialization fails
	var cuda *CUDAAccelerator
	func() {
		defer func() {
			if r := recover(); r != nil {
				fmt.Printf("Mesh3D: CUDA initialization panic: %v\n", r)
				fmt.Printf("Mesh3D: Continuing without CUDA acceleration\n")
			}
		}()
		cuda = NewCUDAAccelerator()
	}()

	return &Mesh3DValidator{
		cuda: cuda,
	}
}

// ValidationResult contains detailed validation results
type ValidationResult struct {
	Valid          bool
	Errors         []string
	Warnings       []string
	ParentHashes   map[string]string
	ValidationTime time.Duration

	// err is the first fatal error, kept whole so callers can use errors.Is
	// on it (ErrStructure, and pkg/poe errors for the payload's parents).
	err error
}

// ValidateTransaction validates a mesh transaction. Every structural or
// signature failure is fatal (see structure.go); only environmental notes
// (such as CUDA being unavailable) are warnings.
func (v *Mesh3DValidator) ValidateTransaction(tx *Transaction) (bool, error) {
	start := time.Now()
	result := v.ValidateTransactionDetailed(tx)
	result.ValidationTime = time.Since(start)

	if !result.Valid {
		if result.err != nil {
			return false, result.err
		}
		return false, fmt.Errorf("%w: %v", ErrStructure, result.Errors)
	}

	return true, nil
}

// ValidateTransactionDetailed performs comprehensive validation with detailed results
func (v *Mesh3DValidator) ValidateTransactionDetailed(tx *Transaction) *ValidationResult {
	v.mu.Lock()
	defer v.mu.Unlock()

	result := &ValidationResult{
		Valid:        true,
		Errors:       []string{},
		Warnings:     []string{},
		ParentHashes: make(map[string]string),
	}

	// The entanglement structure and the payload signature (structure.go).
	if _, err := ValidateStructure(tx); err != nil {
		result.Valid = false
		result.err = err
		result.Errors = append(result.Errors, err.Error())
		return result
	}

	for i, parent := range tx.ParentCells {
		hash := sha256.Sum256(parent.Data)
		hashStr := hex.EncodeToString(hash[:])
		result.ParentHashes[parent.ID] = hashStr
		if isSuspiciousHash(hashStr) {
			result.Valid = false
			result.Errors = append(result.Errors, fmt.Sprintf("parent cell %d hash has a degenerate pattern", i))
		}
	}
	if !result.Valid {
		return result
	}

	// CUDA mesh kernels are not shipped yet — only use GPU path when KernelsReady().
	if v.cuda != nil && v.cuda.IsAvailable() && v.cuda.KernelsReady() {
		parentData := make([][]byte, len(tx.ParentCells))
		for i, parent := range tx.ParentCells {
			parentData[i] = parent.Data
		}

		results, err := v.cuda.ValidateParentCellsParallel(parentData)
		if err == nil && results != nil {
			for i, valid := range results {
				if !valid {
					result.Valid = false
					result.Errors = append(result.Errors, fmt.Sprintf("parent cell %d failed CUDA validation", i))
				}
			}
		} else if err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("CUDA validation failed, using CPU fallback: %v", err))
		}
	} else if v.cuda != nil && v.cuda.IsAvailable() && !v.cuda.KernelsReady() {
		result.Warnings = append(result.Warnings, "CUDA device present but mesh3d GPU kernels not enabled; using CPU validation only")
	}

	return result
}

// isSuspiciousHash checks for suspicious hash patterns
func isSuspiciousHash(hash string) bool {
	if hash == "" {
		return true
	}
	// Check for all zeros
	allZeros := true
	for _, c := range hash {
		if c != '0' {
			allZeros = false
			break
		}
	}
	if allZeros {
		return true
	}

	// Check for all same character
	firstChar := hash[0]
	allSame := true
	for _, c := range []byte(hash) {
		if c != firstChar {
			allSame = false
			break
		}
	}
	return allSame
}
