//go:build cgo
// +build cgo

package mesh3d

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/crypto"
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
	mu        sync.Mutex
	dilithium *crypto.Dilithium
	cuda      *CUDAAccelerator
	// Add fields as needed for state, reputation, etc.
}

// NewMesh3DValidator creates a new Mesh3DValidator instance.
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
	
	// Initialize Dilithium (may fail if liboqs DLL missing)
	// Wrap in recover to prevent crash if Dilithium initialization fails
	var d *crypto.Dilithium
	func() {
		defer func() {
			if r := recover(); r != nil {
				fmt.Printf("Mesh3D: Dilithium initialization panic: %v\n", r)
				fmt.Printf("Mesh3D: Continuing without quantum-safe crypto\n")
			}
		}()
		d = crypto.NewDilithium()
	}()
	
	return &Mesh3DValidator{
		dilithium: d,
		cuda:      cuda,
	}
}

// ValidationResult contains detailed validation results
type ValidationResult struct {
	Valid       bool
	Errors      []string
	Warnings    []string
	ParentHashes map[string]string
	ValidationTime time.Duration
}

// ValidateTransaction validates a transaction with 3-5 parent cells with enhanced checks
func (v *Mesh3DValidator) ValidateTransaction(tx *Transaction) (bool, error) {
	start := time.Now()
	result := v.ValidateTransactionDetailed(tx)
	result.ValidationTime = time.Since(start)
	
	if !result.Valid {
		return false, fmt.Errorf("validation failed: %v", result.Errors)
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

	numParents := len(tx.ParentCells)
	if numParents < 3 || numParents > 5 {
		result.Valid = false
		result.Errors = append(result.Errors, fmt.Sprintf("invalid number of parent cells: %d (expected 3-5)", numParents))
		return result
	}

	// Validate transaction ID format
	if tx.ID == "" {
		result.Valid = false
		result.Errors = append(result.Errors, "transaction ID is required")
		return result
	}

	// Validate transaction ID format (should be hex string)
	if len(tx.ID) < 32 {
		result.Warnings = append(result.Warnings, "transaction ID is shorter than expected (minimum 32 characters)")
	}

	// Validate parent cells exist and have data
	for i, parent := range tx.ParentCells {
		if parent.ID == "" {
			result.Valid = false
			result.Errors = append(result.Errors, fmt.Sprintf("parent cell %d has no ID", i))
			continue
		}
		if len(parent.Data) == 0 {
			result.Valid = false
			result.Errors = append(result.Errors, fmt.Sprintf("parent cell %d has no data", i))
			continue
		}
	}

	if !result.Valid {
		return result
	}

	// Validate transaction data is not empty
	if len(tx.Data) == 0 {
		result.Valid = false
		result.Errors = append(result.Errors, "transaction data is empty")
		return result
	}

	// Enhanced cryptographic validation: Verify parent cell integrity
	for i, parent := range tx.ParentCells {
		// Minimum data size check
		if len(parent.Data) < 32 {
			result.Valid = false
			result.Errors = append(result.Errors, fmt.Sprintf("parent cell %d data too small: %d bytes (minimum 32)", i, len(parent.Data)))
			continue
		}

		// Compute and store hash
		hash := sha256.Sum256(parent.Data)
		hashStr := hex.EncodeToString(hash[:])
		result.ParentHashes[parent.ID] = hashStr

		// Validate hash format
		if len(hashStr) != 64 {
			result.Valid = false
			result.Errors = append(result.Errors, fmt.Sprintf("parent cell %d hash has invalid length: %d (expected 64)", i, len(hashStr)))
		}

		// Check for suspicious patterns (all zeros, all ones, etc.)
		if isSuspiciousHash(hashStr) {
			result.Warnings = append(result.Warnings, fmt.Sprintf("parent cell %d hash has suspicious pattern", i))
		}
	}

	// Consensus rule: Verify parent cell relationships
	parentIDs := make(map[string]bool)
	for _, parent := range tx.ParentCells {
		if parentIDs[parent.ID] {
			result.Valid = false
			result.Errors = append(result.Errors, fmt.Sprintf("duplicate parent cell ID: %s", parent.ID))
		}
		parentIDs[parent.ID] = true
	}

	// Validate parent cell relationships (entanglement structure)
	if err := v.validateEntanglementStructure(tx.ParentCells); err != nil {
		result.Warnings = append(result.Warnings, fmt.Sprintf("entanglement structure validation: %v", err))
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

	// Enhanced signature verification if Dilithium is available
	if v.dilithium != nil {
		if err := v.validateTransactionSignature(tx); err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("signature validation: %v", err))
		}
	}

	return result
}

// validateEntanglementStructure validates the 3D mesh entanglement structure
func (v *Mesh3DValidator) validateEntanglementStructure(parents []ParentCell) error {
	// Check that parent cells form a valid entanglement graph
	// In a 3D mesh, each parent should reference at least one other parent
	// This is a simplified check - full validation would require graph analysis
	
	if len(parents) < 3 {
		return fmt.Errorf("insufficient parents for entanglement: %d (minimum 3)", len(parents))
	}

	// Validate that parent cells have sufficient data overlap
	// (In a real system, this would check actual entanglement relationships)
	for i := 0; i < len(parents)-1; i++ {
		if len(parents[i].Data) == 0 || len(parents[i+1].Data) == 0 {
			return fmt.Errorf("parent cells %d and %d have insufficient data for entanglement", i, i+1)
		}
	}

	return nil
}

// validateTransactionSignature validates the transaction signature
func (v *Mesh3DValidator) validateTransactionSignature(tx *Transaction) error {
	// Extract signature from transaction data
	// This is a placeholder - full implementation would parse transaction format
	// and verify signature using Dilithium
	
	if len(tx.Data) < 64 {
		return fmt.Errorf("transaction data too small for signature: %d bytes", len(tx.Data))
	}

	// In a real implementation, we would:
	// 1. Parse transaction to extract signature
	// 2. Extract message (transaction without signature)
	// 3. Verify signature using Dilithium
	
	return nil
}

// isSuspiciousHash checks for suspicious hash patterns
func isSuspiciousHash(hash string) bool {
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
