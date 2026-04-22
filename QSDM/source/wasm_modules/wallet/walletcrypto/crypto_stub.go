//go:build !cgo
// +build !cgo

package walletcrypto

import (
	"errors"
	"fmt"
)

// Stub implementation when CGO is disabled or liboqs-go is not available
type KeyPair struct {
	PrivateKey []byte
	PublicKey  []byte
}

// GenerateKeyPair generates a stub key pair (for testing only)
func GenerateKeyPair() (*KeyPair, error) {
	return nil, fmt.Errorf("wallet crypto not available: requires CGO and liboqs-go")
}

// Sign signs the given message (stub)
func (kp *KeyPair) Sign(message []byte) ([]byte, error) {
	return nil, errors.New("wallet crypto not available")
}

// Verify verifies the signature (stub)
func (kp *KeyPair) Verify(message []byte, signature []byte) (bool, error) {
	return false, errors.New("wallet crypto not available")
}

