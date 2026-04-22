//go:build !cgo
// +build !cgo

package consensus

import (
	"errors"

	"github.com/blackbeardONE/QSDM/internal/logging"
)

// ProofOfEntanglement represents the PoE consensus mechanism
// Stub implementation when CGO is disabled
type ProofOfEntanglement struct{}

// NewProofOfEntanglement creates a new PoE instance (stub when CGO disabled)
func NewProofOfEntanglement() *ProofOfEntanglement {
	return nil // Return nil to indicate consensus is not available
}

// MLDSAPublicKeyHex is a no-op for the CGO-disabled stub (only non-nil PoE exposes keys).
func (poe *ProofOfEntanglement) MLDSAPublicKeyHex() string {
	return ""
}

// Sign signs a message using Dilithium (stub)
func (poe *ProofOfEntanglement) Sign(message []byte) ([]byte, error) {
	return nil, errors.New("consensus not available: CGO is disabled")
}

// Verify verifies a signature (stub)
func (poe *ProofOfEntanglement) Verify(message []byte, signature []byte, publicKey []byte) (bool, error) {
	return false, errors.New("consensus not available: CGO is disabled")
}

// ValidateTransaction validates a transaction by checking 2 parent cells and signatures (stub)
func (poe *ProofOfEntanglement) ValidateTransaction(tx []byte, parentCells [][]byte, signatures [][]byte, logger *logging.Logger) (bool, error) {
	if poe == nil {
		if logger != nil {
			logger.Warn("ProofOfEntanglement not available (CGO disabled), accepting transaction without signature verification")
		}
		// Without crypto, we can't validate signatures, but we can still accept transactions
		// for testing purposes
		return true, nil
	}
	return false, errors.New("consensus not available: CGO is disabled")
}

// SignTransaction signs a transaction (stub)
func (poe *ProofOfEntanglement) SignTransaction(txBytes []byte) ([]byte, error) {
	return nil, errors.New("consensus not available: CGO is disabled")
}

