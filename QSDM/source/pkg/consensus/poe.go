//go:build cgo
// +build cgo

package consensus

import (
	"encoding/hex"
	"errors"

	"github.com/blackbeardONE/QSDM/internal/logging"
	"github.com/blackbeardONE/QSDM/pkg/crypto"
)

// ProofOfEntanglement represents the PoE consensus mechanism
type ProofOfEntanglement struct {
	dilithium *crypto.Dilithium
}

// NewProofOfEntanglement creates a new PoE instance with Dilithium crypto
func NewProofOfEntanglement() *ProofOfEntanglement {
	d := crypto.NewDilithium()
	if d == nil {
		return nil
	}
	return &ProofOfEntanglement{
		dilithium: d,
	}
}

// MLDSAPublicKeyHex returns the hex-encoded ML-DSA-87 public key used by this PoE instance for signing.
func (poe *ProofOfEntanglement) MLDSAPublicKeyHex() string {
	if poe == nil || poe.dilithium == nil {
		return ""
	}
	return hex.EncodeToString(poe.dilithium.GetPublicKey())
}

// Sign signs a message using Dilithium
func (poe *ProofOfEntanglement) Sign(message []byte) ([]byte, error) {
	return poe.dilithium.Sign(message)
}

// SignOptimized signs a message using optimized memory management (5-10% faster)
func (poe *ProofOfEntanglement) SignOptimized(message []byte) ([]byte, error) {
	if poe == nil || poe.dilithium == nil {
		return nil, errors.New("ProofOfEntanglement not initialized")
	}
	return poe.dilithium.SignOptimized(message)
}

// SignBatchOptimized signs multiple messages in parallel (10-100x faster for batches)
func (poe *ProofOfEntanglement) SignBatchOptimized(messages [][]byte) ([][]byte, error) {
	if poe == nil || poe.dilithium == nil {
		return nil, errors.New("ProofOfEntanglement not initialized")
	}
	return poe.dilithium.SignBatchOptimized(messages)
}

// SignCompressed signs a message and returns a compressed signature.
// This reduces signature size by approximately 50% (4.6 KB → 2.3 KB for ML-DSA-87).
func (poe *ProofOfEntanglement) SignCompressed(message []byte) ([]byte, error) {
	if poe == nil || poe.dilithium == nil {
		return nil, errors.New("ProofOfEntanglement not initialized")
	}
	return poe.dilithium.SignCompressed(message)
}

// ValidateTransaction validates a transaction by checking that the sender
// supplied at least two parent cells (the minimum entanglement degree) and
// that every attached signature verifies over `tx`.
//
// Historically this helper required *exactly* two parent cells, which was
// correct for the pre-mesh3D wallet path but silently rejected every valid
// Phase-3 / mesh3D payload (those carry three parent cells by protocol, see
// `pkg/mesh3d`). The field is not actually consumed during cryptographic
// verification -- only the signatures over `tx` are -- so the count check
// is only a plausibility guard. Relax it to "at least two" so both the
// legacy wallet path and the mesh3D Phase-3 path flow through the same
// consensus entry point.
func (poe *ProofOfEntanglement) ValidateTransaction(tx []byte, parentCells [][]byte, signatures [][]byte, logger *logging.Logger) (bool, error) {
	if poe == nil || poe.dilithium == nil {
		logger.Error("ProofOfEntanglement or Dilithium not initialized", "file", "poe.go")
		return false, errors.New("ProofOfEntanglement or Dilithium not initialized")
	}
	if len(parentCells) < 2 {
		logger.Error("Invalid number of parent cells, expected at least 2", "file", "poe.go", "got", len(parentCells))
		return false, errors.New("invalid number of parent cells, expected at least 2")
	}
	if len(signatures) == 0 {
		logger.Error("No signatures provided", "file", "poe.go")
		return false, errors.New("no signatures provided")
	}
	// Verify signatures using Dilithium
	for _, sig := range signatures {
		valid, err := poe.dilithium.Verify(tx, sig)
		if err != nil || !valid {
			logger.Error("Signature verification failed", "file", "poe.go")
			return false, errors.New("signature verification failed")
		}
	}
	logger.Info("Transaction validated with Proof-of-Entanglement consensus", "file", "poe.go")
	return true, nil
}

// ValidateTransactionCompressed validates a transaction with compressed signatures.
// Signatures are automatically decompressed before verification.
func (poe *ProofOfEntanglement) ValidateTransactionCompressed(tx []byte, parentCells [][]byte, compressedSignatures [][]byte, logger *logging.Logger) (bool, error) {
	if poe == nil || poe.dilithium == nil {
		logger.Error("ProofOfEntanglement or Dilithium not initialized", "file", "poe.go")
		return false, errors.New("ProofOfEntanglement or Dilithium not initialized")
	}
	if len(parentCells) < 2 {
		logger.Error("Invalid number of parent cells, expected at least 2", "file", "poe.go", "got", len(parentCells))
		return false, errors.New("invalid number of parent cells, expected at least 2")
	}
	if len(compressedSignatures) == 0 {
		logger.Error("No signatures provided", "file", "poe.go")
		return false, errors.New("no signatures provided")
	}
	// Verify compressed signatures using Dilithium
	for _, compressedSig := range compressedSignatures {
		valid, err := poe.dilithium.VerifyCompressed(tx, compressedSig)
		if err != nil || !valid {
			logger.Error("Compressed signature verification failed", "file", "poe.go", "error", err)
			return false, errors.New("compressed signature verification failed")
		}
	}
	logger.Info("Transaction validated with Proof-of-Entanglement consensus (compressed signatures)", "file", "poe.go")
	return true, nil
}
