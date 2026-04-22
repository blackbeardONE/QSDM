//go:build !cgo
// +build !cgo

package crypto

import (
	"errors"
)

// Dilithium represents the Dilithium signature scheme (stub for non-CGO builds)
type Dilithium struct{}

// NewDilithium returns nil for non-CGO builds
func NewDilithium() *Dilithium {
	return nil
}

// NewDilithiumVerifyOnly returns nil for non-CGO builds
func NewDilithiumVerifyOnly() *Dilithium {
	return nil
}

// Sign returns an error for non-CGO builds
func (d *Dilithium) Sign(message []byte) ([]byte, error) {
	if d == nil {
		return nil, errors.New("Dilithium not available: CGO and liboqs required")
	}
	return nil, errors.New("Dilithium not available: CGO and liboqs required")
}

// Verify returns false and an error for non-CGO builds
func (d *Dilithium) Verify(message []byte, signature []byte) (bool, error) {
	if d == nil {
		return false, errors.New("Dilithium not available: CGO and liboqs required")
	}
	return false, errors.New("Dilithium not available: CGO and liboqs required")
}

// VerifyWithPublicKey is a stub for non-CGO builds
func (d *Dilithium) VerifyWithPublicKey(message []byte, signature []byte, publicKey []byte) (bool, error) {
	if d == nil {
		return false, errors.New("Dilithium not available: CGO and liboqs required")
	}
	return false, errors.New("Dilithium not available: CGO and liboqs required")
}

// Free is a no-op for stub
func (d *Dilithium) Free() {
	// No-op
}

