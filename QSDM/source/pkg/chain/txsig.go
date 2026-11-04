package chain

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/blackbeardONE/QSDM/pkg/crypto"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

// SigAlgorithm identifies which signature scheme is in use.
type SigAlgorithm string

const (
	// SigEd25519 is retired: Verify refuses it with ErrEd25519Retired. The
	// constant stays so the refusal can name it.
	SigEd25519 SigAlgorithm = "ed25519"
	SigMLDSA   SigAlgorithm = "ml-dsa"
)

// ErrEd25519Retired refuses a SignedTx that claims the Ed25519 algorithm.
// ML-DSA-87 is the only accepted transaction signature. This is mempool
// admission policy only: blocks do not record a gossip tx's signature
// algorithm and replay does not re-verify it, so history is unaffected.
var ErrEd25519Retired = errors.New("chain: ed25519 transaction signatures are retired; sign with ML-DSA-87 (algorithm \"ml-dsa\")")

// txSigEd25519Rejected counts SignedTx refused with ErrEd25519Retired.
var txSigEd25519Rejected atomic.Uint64

// TxSigEd25519Rejected reports how many Ed25519 SignedTx Verify refused
// since the process started.
func TxSigEd25519Rejected() uint64 { return txSigEd25519Rejected.Load() }

// SignedTx wraps a mempool.Tx with a cryptographic signature.
type SignedTx struct {
	Tx        *mempool.Tx `json:"tx"`
	Signature []byte      `json:"signature"`
	PublicKey []byte      `json:"public_key"`
	Algorithm SigAlgorithm `json:"algorithm"`
}

// TxSigningHash produces the canonical hash of a transaction for signing.
// Fields are concatenated in a deterministic order.
func TxSigningHash(tx *mempool.Tx) []byte {
	h := sha256.New()
	fmt.Fprintf(h, "%s:%s:%s:%.8f:%.8f:%d:%d",
		tx.ID, tx.Sender, tx.Recipient,
		tx.Amount, tx.Fee, tx.GasLimit, tx.Nonce)
	if tx.ContractID != "" {
		fmt.Fprintf(h, ":cid:%s", tx.ContractID)
	}
	if len(tx.Payload) > 0 {
		h.Write(tx.Payload)
	}
	return h.Sum(nil)
}

// SigVerifier validates transaction signatures before mempool admission.
type SigVerifier struct {
	mu        sync.RWMutex
	mldsaRing map[string][]byte // normalized sender address -> registered ML-DSA-87 public key (optional pin)
}

// NewSigVerifier creates a verifier with an empty keyring.
func NewSigVerifier() *SigVerifier {
	return &SigVerifier{
		mldsaRing: make(map[string][]byte),
	}
}

func normalizeSigAddress(address string) string {
	return strings.ToLower(strings.TrimSpace(address))
}

// RegisterMLDSAKey optionally pins the ML-DSA-87 public key for a sender address (hex wallet address).
// When set, verifyMLDSA requires the transaction public key to match exactly (in addition to signature checks).
func (sv *SigVerifier) RegisterMLDSAKey(address string, publicKey []byte) error {
	if strings.TrimSpace(address) == "" {
		return fmt.Errorf("empty address")
	}
	if len(publicKey) != mldsa87PublicKeyLen {
		return fmt.Errorf("ml-dsa-87 public key must be %d bytes, got %d", mldsa87PublicKeyLen, len(publicKey))
	}
	pk := append([]byte(nil), publicKey...)
	sv.mu.Lock()
	defer sv.mu.Unlock()
	sv.mldsaRing[normalizeSigAddress(address)] = pk
	return nil
}

// RegisterMLDSAKeyHex is like RegisterMLDSAKey with a hex-encoded ML-DSA-87 public key.
func (sv *SigVerifier) RegisterMLDSAKeyHex(address string, publicKeyHex string) error {
	raw, err := hex.DecodeString(strings.TrimSpace(publicKeyHex))
	if err != nil {
		return fmt.Errorf("decode ml-dsa public key hex: %w", err)
	}
	return sv.RegisterMLDSAKey(address, raw)
}

// RemoveMLDSAKey removes an optional ML-DSA key binding for an address.
func (sv *SigVerifier) RemoveMLDSAKey(address string) {
	sv.mu.Lock()
	defer sv.mu.Unlock()
	delete(sv.mldsaRing, normalizeSigAddress(address))
}

// GetMLDSAKey returns the registered ML-DSA public key for an address, if any.
func (sv *SigVerifier) GetMLDSAKey(address string) ([]byte, bool) {
	sv.mu.RLock()
	defer sv.mu.RUnlock()
	k, ok := sv.mldsaRing[normalizeSigAddress(address)]
	if !ok {
		return nil, false
	}
	return append([]byte(nil), k...), true
}

// MLDSAKeyCount returns how many optional ML-DSA sender bindings are registered.
func (sv *SigVerifier) MLDSAKeyCount() int {
	sv.mu.RLock()
	defer sv.mu.RUnlock()
	return len(sv.mldsaRing)
}

// Verify checks that a SignedTx has a valid signature.
// It verifies:
//  1. The signature algorithm is ML-DSA-87 (Ed25519 is retired:
//     ErrEd25519Retired; anything else is unsupported)
//  2. The public key length is correct
//  3. The sender is SHA256(public key)
//  4. The signature verifies against the tx's canonical hash
//  5. If the sender has a pinned ML-DSA key, the tx's public key must match
func (sv *SigVerifier) Verify(stx *SignedTx) error {
	if stx.Tx == nil {
		return fmt.Errorf("nil transaction")
	}
	if len(stx.Signature) == 0 {
		return fmt.Errorf("missing signature")
	}
	if len(stx.PublicKey) == 0 {
		return fmt.Errorf("missing public key")
	}

	switch stx.Algorithm {
	case SigEd25519:
		txSigEd25519Rejected.Add(1)
		return fmt.Errorf("%w (tx %s)", ErrEd25519Retired, stx.Tx.ID)
	case SigMLDSA:
		return sv.verifyMLDSA(stx)
	default:
		return fmt.Errorf("unsupported signature algorithm: %s", stx.Algorithm)
	}
}

// ML-DSA-87 (liboqs "ML-DSA-87") encoded sizes.
const mldsa87PublicKeyLen = 2592
const mldsa87SignatureLen = 4627

func (sv *SigVerifier) verifyMLDSA(stx *SignedTx) error {
	if len(stx.PublicKey) != mldsa87PublicKeyLen {
		return fmt.Errorf("ml-dsa-87 public key must be %d bytes, got %d", mldsa87PublicKeyLen, len(stx.PublicKey))
	}
	if len(stx.Signature) != mldsa87SignatureLen {
		return fmt.Errorf("ml-dsa-87 signature must be %d bytes, got %d", mldsa87SignatureLen, len(stx.Signature))
	}
	d := crypto.NewDilithiumVerifyOnly()
	if d == nil {
		return fmt.Errorf("ML-DSA verifier unavailable (requires CGO and liboqs ML-DSA-87)")
	}
	defer d.Free()

	// Wallet-style addresses are SHA256(public_key) as lower-case hex.
	addr := sha256.Sum256(stx.PublicKey)
	if !strings.EqualFold(hex.EncodeToString(addr[:]), stx.Tx.Sender) {
		return fmt.Errorf("ml-dsa public key does not match sender address")
	}

	msg := TxSigningHash(stx.Tx)
	ok, err := d.VerifyWithPublicKey(msg, stx.Signature, stx.PublicKey)
	if err != nil {
		return fmt.Errorf("ml-dsa verify: %w", err)
	}
	if !ok {
		return fmt.Errorf("ml-dsa signature verification failed for tx %s", stx.Tx.ID)
	}

	sv.mu.RLock()
	regPK, hasReg := sv.mldsaRing[normalizeSigAddress(stx.Tx.Sender)]
	sv.mu.RUnlock()
	if hasReg && !bytes.Equal(regPK, stx.PublicKey) {
		return fmt.Errorf("ml-dsa public key mismatch for sender %s (optional keyring)", stx.Tx.Sender)
	}

	return nil
}
