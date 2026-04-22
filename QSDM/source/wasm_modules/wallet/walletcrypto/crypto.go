//go:build cgo
// +build cgo

package walletcrypto

import (
	"errors"
	"fmt"
)

// Stub implementation - wallet crypto uses pkg/crypto/dilithium.go instead
// This file exists for compatibility but delegates to the main crypto package

type KeyPair struct {
	PrivateKey []byte
	PublicKey  []byte
}

// GenerateKeyPair generates a stub key pair
// Note: Actual wallet crypto is handled by pkg/crypto/dilithium.go
func GenerateKeyPair() (*KeyPair, error) {
	return nil, fmt.Errorf("wallet crypto: use pkg/crypto/dilithium.go instead (liboqs via CGO)")
}

// Sign signs the given message (stub)
func (kp *KeyPair) Sign(message []byte) ([]byte, error) {
	return nil, errors.New("wallet crypto: use pkg/crypto/dilithium.go instead")
}

// Verify verifies the signature (stub)
func (kp *KeyPair) Verify(message []byte, signature []byte) (bool, error) {
	return false, errors.New("wallet crypto: use pkg/crypto/dilithium.go instead")
}
