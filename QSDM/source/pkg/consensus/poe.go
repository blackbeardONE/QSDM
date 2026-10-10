// Package consensus — Proof-of-Entanglement signature path.
//
// As of 2026-05-06 (Stage B) this file is the canonical PoE
// implementation under both CGO+liboqs and non-CGO builds. It
// uses pkg/crypto.NewDilithium for the actual ML-DSA-87 work,
// which selects the correct backend at compile time
// (dilithium.go on cgo, dilithium_circl.go on !cgo). The
// previous always-accept stub at poe_stub.go has been deleted:
// every supported build path now has a real verifier, so the
// "accept transactions without signature verification" failure
// mode is no longer reachable.
//
// Key binding (2026-10): validation verifies a transaction's signatures
// under the transaction's OWN public key and requires the sender address to
// be hex(sha256(public_key)), the wallet address derivation. The earlier
// ValidateTransaction verified under this node's PoE key instead, so it
// could only ever accept data this node had signed itself; no caller can
// reach that path any more. The parent-cell rules come from pkg/poe, the
// same definition block production and replay enforce (pkg/chain/poe.go).

package consensus

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/blackbeardONE/QSDM/internal/logging"
	"github.com/blackbeardONE/QSDM/pkg/crypto"
	"github.com/blackbeardONE/QSDM/pkg/poe"
)

// ProofOfEntanglement holds this node's ML-DSA-87 key for signing data the
// node itself originates. Validating other parties' transactions never uses
// it: see ValidateSignedTransaction.
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

// MLDSAPublicKey returns this PoE instance's raw ML-DSA-87 public key.
func (poe *ProofOfEntanglement) MLDSAPublicKey() []byte {
	if poe == nil || poe.dilithium == nil {
		return nil
	}
	return poe.dilithium.GetPublicKey()
}

// Sign signs a message using Dilithium
func (poe *ProofOfEntanglement) Sign(message []byte) ([]byte, error) {
	if poe == nil || poe.dilithium == nil {
		return nil, errors.New("ProofOfEntanglement not initialized")
	}
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

// SignedTransaction is a transaction as the PoE helper validates it.
type SignedTransaction struct {
	// ID is the transaction's own ID (checked against its parents).
	ID string
	// Sender must equal hex(sha256(PublicKey)).
	Sender string
	// SigningBytes are the exact bytes the signatures cover (for a wallet
	// envelope, wallet.TransactionData.CanonicalBytes).
	SigningBytes []byte
	// ParentCells are the parent IDs the signer committed to.
	ParentCells []string
	// Signatures must each verify under PublicKey. At least one is required.
	Signatures [][]byte
	// PublicKey is the signer's ML-DSA-87 public key, taken from the
	// transaction itself -- never this node's key.
	PublicKey []byte
	// Compressed selects compressed signature encoding.
	Compressed bool
}

// Errors returned by ValidateSignedTransaction besides pkg/poe's.
var (
	ErrNoSignature     = errors.New("consensus: no signatures provided")
	ErrNoPublicKey     = errors.New("consensus: transaction carries no public key")
	ErrSenderMismatch  = errors.New("consensus: sender is not hex(sha256(public_key))")
	ErrBadSignature    = errors.New("consensus: signature does not verify under the transaction public key")
	ErrVerifierMissing = errors.New("consensus: ML-DSA-87 verifier unavailable")
)

// ValidateSignedTransaction checks a transaction without any node key:
//
//  1. the context-free PoE parent rules (pkg/poe.CheckShape: MinParents to
//     MaxParents well-formed, distinct parents, none equal to the ID);
//  2. the sender is the wallet address of PublicKey, hex(sha256(PublicKey));
//  3. every signature verifies over SigningBytes under PublicKey.
//
// Whether the parents are committed transactions needs the chain; callers
// with a block producer also run
// chain.(*BlockProducer).CheckWalletTransferParents.
func ValidateSignedTransaction(tx SignedTransaction) error {
	if err := poe.CheckShape(tx.ID, tx.ParentCells); err != nil {
		return err
	}
	if len(tx.Signatures) == 0 {
		return ErrNoSignature
	}
	if len(tx.PublicKey) == 0 {
		return ErrNoPublicKey
	}
	sum := sha256.Sum256(tx.PublicKey)
	if !strings.EqualFold(hex.EncodeToString(sum[:]), strings.TrimSpace(tx.Sender)) {
		return ErrSenderMismatch
	}
	d := crypto.NewDilithiumVerifyOnly()
	if d == nil {
		return ErrVerifierMissing
	}
	defer d.Free()
	for i, sig := range tx.Signatures {
		var ok bool
		var err error
		if tx.Compressed {
			ok, err = d.VerifyWithPublicKeyCompressed(tx.SigningBytes, sig, tx.PublicKey)
		} else {
			ok, err = d.VerifyWithPublicKey(tx.SigningBytes, sig, tx.PublicKey)
		}
		if err != nil {
			return fmt.Errorf("%w: signature %d: %v", ErrBadSignature, i, err)
		}
		if !ok {
			return fmt.Errorf("%w: signature %d", ErrBadSignature, i)
		}
	}
	return nil
}

// ValidateTransaction validates tx with ValidateSignedTransaction and logs
// the outcome. The receiver's own key plays no part, so a nil receiver works.
func (poe *ProofOfEntanglement) ValidateTransaction(tx SignedTransaction, logger *logging.Logger) (bool, error) {
	if err := ValidateSignedTransaction(tx); err != nil {
		if logger != nil {
			logger.Warn("Proof-of-Entanglement validation failed", "file", "poe.go", "tx_id", tx.ID, "error", err)
		}
		return false, err
	}
	if logger != nil {
		logger.Info("Transaction validated with Proof-of-Entanglement consensus", "file", "poe.go", "tx_id", tx.ID)
	}
	return true, nil
}
