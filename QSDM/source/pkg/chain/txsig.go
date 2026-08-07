package chain

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"

	"github.com/blackbeardONE/QSDM/pkg/crypto"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

// SigAlgorithm identifies which signature scheme is in use.
type SigAlgorithm string

const (
	SigEd25519 SigAlgorithm = "ed25519"
	SigMLDSA   SigAlgorithm = "ml-dsa"
)

// SignedTx wraps a mempool.Tx with a cryptographic signature.
type SignedTx struct {
	Tx        *mempool.Tx `json:"tx"`
	Signature []byte      `json:"signature"`
	PublicKey []byte      `json:"public_key"`
	Algorithm SigAlgorithm `json:"algorithm"`
}

// TxSigner produces signatures for transactions.
type TxSigner struct {
	privateKey ed25519.PrivateKey
	publicKey  ed25519.PublicKey
	algorithm  SigAlgorithm
}

// NewTxSigner creates a signer from an Ed25519 private key.
func NewTxSigner(priv ed25519.PrivateKey) *TxSigner {
	return &TxSigner{
		privateKey: priv,
		publicKey:  priv.Public().(ed25519.PublicKey),
		algorithm:  SigEd25519,
	}
}

// Sign produces a SignedTx with the transaction's canonical hash signed.
func (s *TxSigner) Sign(tx *mempool.Tx) *SignedTx {
	hash := TxSigningHash(tx)
	sig := ed25519.Sign(s.privateKey, hash)
	return &SignedTx{
		Tx:        tx,
		Signature: sig,
		PublicKey: s.publicKey,
		Algorithm: s.algorithm,
	}
}

// PublicKeyHex returns the hex-encoded public key.
func (s *TxSigner) PublicKeyHex() string {
	return hex.EncodeToString(s.publicKey)
}

// Address returns the canonical wallet address this signer can spend from.
// Transactions whose Sender is anything else are rejected by verifyEd25519
// unless an operator has pinned a different key via RegisterKey.
func (s *TxSigner) Address() string {
	return Ed25519WalletAddress(s.publicKey)
}

// Ed25519WalletAddress derives the canonical wallet address for an Ed25519
// public key: SHA256(public_key) as lower-case hex. This is the same
// derivation verifyMLDSA applies to ML-DSA-87 keys, so both algorithms share
// one address space and one identity rule.
func Ed25519WalletAddress(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:])
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
	mu         sync.RWMutex
	keyring    map[string]ed25519.PublicKey // address -> registered public key
	mldsaRing  map[string][]byte            // normalized sender address -> registered ML-DSA-87 public key (optional hybrid policy)
}

// NewSigVerifier creates a verifier with an empty keyring.
func NewSigVerifier() *SigVerifier {
	return &SigVerifier{
		keyring:   make(map[string]ed25519.PublicKey),
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

// RegisterKey associates a public key with an address. Addresses are
// normalized so lookups in verifyEd25519 are case- and whitespace-insensitive,
// matching the ML-DSA ring.
func (sv *SigVerifier) RegisterKey(address string, pubKey ed25519.PublicKey) {
	sv.mu.Lock()
	defer sv.mu.Unlock()
	sv.keyring[normalizeSigAddress(address)] = pubKey
}

// RemoveKey removes a registered key.
func (sv *SigVerifier) RemoveKey(address string) {
	sv.mu.Lock()
	defer sv.mu.Unlock()
	delete(sv.keyring, normalizeSigAddress(address))
}

// GetKey returns the registered public key for an address.
func (sv *SigVerifier) GetKey(address string) (ed25519.PublicKey, bool) {
	sv.mu.RLock()
	defer sv.mu.RUnlock()
	key, ok := sv.keyring[normalizeSigAddress(address)]
	return key, ok
}

// KeyCount returns the number of registered keys.
func (sv *SigVerifier) KeyCount() int {
	sv.mu.RLock()
	defer sv.mu.RUnlock()
	return len(sv.keyring)
}

// Verify checks that a SignedTx has a valid signature.
// It verifies:
//  1. The signature algorithm is supported
//  2. The public key length is correct
//  3. The signature verifies against the tx's canonical hash
//  4. If the sender has a registered key, the tx's public key must match
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
		return sv.verifyEd25519(stx)
	case SigMLDSA:
		return sv.verifyMLDSA(stx)
	default:
		return fmt.Errorf("unsupported signature algorithm: %s", stx.Algorithm)
	}
}

func (sv *SigVerifier) verifyEd25519(stx *SignedTx) error {
	if len(stx.PublicKey) != ed25519.PublicKeySize {
		return fmt.Errorf("invalid ed25519 public key size: %d", len(stx.PublicKey))
	}
	if len(stx.Signature) != ed25519.SignatureSize {
		return fmt.Errorf("invalid ed25519 signature size: %d", len(stx.Signature))
	}

	hash := TxSigningHash(stx.Tx)
	if !ed25519.Verify(stx.PublicKey, hash, stx.Signature) {
		return fmt.Errorf("ed25519 signature verification failed for tx %s", stx.Tx.ID)
	}

	// A valid signature only proves the holder of stx.PublicKey signed the tx —
	// it says nothing about whether that key is allowed to spend from
	// stx.Tx.Sender. Without the binding below, any gossip peer can present
	// (victim-sender, attacker-key, attacker-signature) and pass verification.
	// Sender identity is established one of two ways, mirroring verifyMLDSA:
	//
	//  1. An operator explicitly pinned a key for this sender via RegisterKey.
	//     The presented key must equal it exactly.
	//  2. Otherwise the sender must be the key's derived wallet address,
	//     SHA256(public_key) as lower-case hex — the same derivation
	//     verifyMLDSA enforces.
	//
	// Case 2 is what makes an empty keyring safe: the default is a hard
	// binding, not an unchecked pass.
	sv.mu.RLock()
	registeredKey, hasKey := sv.keyring[normalizeSigAddress(stx.Tx.Sender)]
	sv.mu.RUnlock()

	if hasKey {
		if !registeredKey.Equal(ed25519.PublicKey(stx.PublicKey)) {
			return fmt.Errorf("public key mismatch for sender %s", stx.Tx.Sender)
		}
		return nil
	}

	addr := sha256.Sum256(stx.PublicKey)
	if !strings.EqualFold(hex.EncodeToString(addr[:]), stx.Tx.Sender) {
		return fmt.Errorf("ed25519 public key does not match sender address %s", stx.Tx.Sender)
	}

	return nil
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
