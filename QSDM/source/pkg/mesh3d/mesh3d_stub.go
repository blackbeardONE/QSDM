//go:build !cgo
// +build !cgo

package mesh3d

import (
	"sync"
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
// Without CGO there is no CUDA path; the structural and signature rules are
// the same as the cgo build (structure.go), and every failure is fatal.
type Mesh3DValidator struct {
	mu sync.Mutex
}

// NewMesh3DValidator creates a new Mesh3DValidator instance.
func NewMesh3DValidator() *Mesh3DValidator {
	return &Mesh3DValidator{}
}

// ValidateTransaction validates a mesh transaction (structure.go).
func (v *Mesh3DValidator) ValidateTransaction(tx *Transaction) (bool, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if _, err := ValidateStructure(tx); err != nil {
		return false, err
	}
	return true, nil
}
