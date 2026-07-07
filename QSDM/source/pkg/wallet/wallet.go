// Package wallet — production wallet service backed by the real
// ML-DSA-87 implementation in pkg/crypto.
//
// As of 2026-05-06 (Stage B) this file is the canonical wallet
// implementation for both CGO+liboqs builds (where pkg/crypto
// uses dilithium.go) and non-CGO builds (where pkg/crypto uses
// dilithium_circl.go). The previous build-tag-gated SHA-256
// fallback (wallet_stub.go) has been deleted: a real ML-DSA-87
// signer is available on every supported build now, so the
// fallback was strictly a downgrade. Both backends produce
// FIPS 204 wire-compatible signatures, so a wallet built under
// either backend interoperates with any QSDM validator.

package wallet

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/crypto"
	"github.com/cloudflare/circl/sign/mldsa/mldsa87"
)

// WalletService provides wallet functionality for the QSDM node
type WalletService struct {
	address   string
	dilithium *crypto.Dilithium
	mu        sync.RWMutex
	balance   int
}

// TransactionData represents transaction data for creation
type TransactionData struct {
	ID          string   `json:"id"`
	Sender      string   `json:"sender"`
	Recipient   string   `json:"recipient"`
	Amount      float64  `json:"amount"`
	Fee         float64  `json:"fee"`
	GeoTag      string   `json:"geotag"`
	ParentCells []string `json:"parent_cells"`
	// Nonce is the per-sender monotonically-increasing replay
	// counter added in v0.4.1 (Session 99, see
	// QSDM/docs/docs/V041_REPLAY_PROTECTION_DESIGN.md).
	//
	// `omitempty` is intentional: a nonce of 0 is the
	// backward-compat legacy path (v0.4.0 envelopes, which never
	// carried this field) and we want json.Marshal to emit
	// byte-for-byte the same canonical signing payload v0.4.0
	// clients produced — so existing signatures continue to
	// verify on a v0.4.1 server. A nonce of 1 or greater opts
	// into the v0.4.1 replay-protection path: the handler
	// checks env.Nonce > stored_last_nonce[sender] before
	// applying the debit, and atomic-commits the bump alongside
	// the balance change.
	Nonce     uint64 `json:"nonce,omitempty"`
	Signature string `json:"signature"`
	// PublicKey is hex-encoded ML-DSA-87 public key (for P2P preflight / verifiers); not part of the signed payload.
	PublicKey string `json:"public_key,omitempty"`
	Timestamp string `json:"timestamp"`
}

// CanonicalBytes returns the exact payload covered by a wallet transaction's
// ML-DSA signature. PublicKey identifies the signer but is deliberately not
// signed; sender is bound to sha256(PublicKey) during verification.
func (tx TransactionData) CanonicalBytes() ([]byte, error) {
	tx.Signature = ""
	tx.PublicKey = ""
	return json.Marshal(tx)
}

// VerifyTransactionData performs the consensus-safe verification required
// before a signed wallet transfer may mutate AccountStore. API admission uses
// the same envelope, but every peer must repeat this check while replaying the
// containing block rather than trusting the producing validator.
func VerifyTransactionData(tx TransactionData) error {
	if tx.ID == "" || tx.Sender == "" || tx.Recipient == "" {
		return errors.New("wallet: id, sender, and recipient are required")
	}
	if tx.Nonce == 0 {
		return errors.New("wallet: consensus transfer requires nonce >= 1")
	}
	if tx.Amount <= 0 || tx.Fee < 0 {
		return errors.New("wallet: amount must be positive and fee non-negative")
	}
	pub, err := hex.DecodeString(tx.PublicKey)
	if err != nil || len(pub) != mldsa87.PublicKeySize {
		return fmt.Errorf("wallet: public_key must be %d-byte hex", mldsa87.PublicKeySize)
	}
	derived := sha256.Sum256(pub)
	if tx.Sender != hex.EncodeToString(derived[:]) {
		return errors.New("wallet: sender does not match sha256(public_key)")
	}
	sig, err := hex.DecodeString(tx.Signature)
	if err != nil || len(sig) != mldsa87.SignatureSize {
		return fmt.Errorf("wallet: signature must be %d-byte hex", mldsa87.SignatureSize)
	}
	canonical, err := tx.CanonicalBytes()
	if err != nil {
		return fmt.Errorf("wallet: canonicalize transaction: %w", err)
	}
	var pk mldsa87.PublicKey
	if err := pk.UnmarshalBinary(pub); err != nil {
		return errors.New("wallet: malformed public_key")
	}
	if !mldsa87.Verify(&pk, canonical, nil, sig) {
		return errors.New("wallet: ML-DSA signature is invalid")
	}
	return nil
}

// NewWalletService creates a new wallet service using Dilithium directly
func NewWalletService() (*WalletService, error) {
	// Create Dilithium instance directly (this uses liboqs via CGO)
	dilithium := crypto.NewDilithium()
	if dilithium == nil {
		return nil, fmt.Errorf("failed to initialize Dilithium: liboqs/OpenSSL may not be available")
	}

	// Generate address from public key (hash of public key)
	publicKey := dilithium.GetPublicKey()
	hash := sha256.Sum256(publicKey)
	address := hex.EncodeToString(hash[:])

	return &WalletService{
		address:   address,
		dilithium: dilithium,
		balance:   0, // Balances come only from canonical ledger state.
	}, nil
}

// GetAddress returns the wallet address
func (ws *WalletService) GetAddress() string {
	return ws.address
}

// GetBalance returns the current wallet balance
func (ws *WalletService) GetBalance() int {
	ws.mu.RLock()
	defer ws.mu.RUnlock()
	return ws.balance
}

// SyncBalanceFromLedger refreshes the wallet's local preflight cache from an
// already-validated canonical ledger balance. It does not credit chain state
// or create CELL; transaction admission must still verify the canonical
// account balance.
func (ws *WalletService) SyncBalanceFromLedger(balance int) error {
	if balance < 0 {
		return errors.New("ledger balance cannot be negative")
	}
	ws.mu.Lock()
	defer ws.mu.Unlock()
	ws.balance = balance
	return nil
}

// CreateTransaction creates a new signed transaction
func (ws *WalletService) CreateTransaction(recipient string, amount int, fee float64, geotag string, parentCells []string) ([]byte, error) {
	if recipient == "" {
		return nil, errors.New("recipient address is required")
	}
	if amount <= 0 {
		return nil, errors.New("amount must be positive")
	}
	if fee < 0 {
		return nil, errors.New("fee cannot be negative")
	}
	ws.mu.Lock()
	defer ws.mu.Unlock()
	if ws.balance < amount {
		return nil, fmt.Errorf("insufficient balance: have %d, need %d", ws.balance, amount)
	}

	// Ensure we have at least 2 parent cells for PoE consensus
	if len(parentCells) < 2 {
		// Generate dummy parent cells if not provided (in real system, these would be actual parent transaction IDs)
		parentCells = []string{"parent1", "parent2"}
	}

	// Generate transaction ID from timestamp and sender/recipient
	timestamp := time.Now()
	txIDData := fmt.Sprintf("%s-%s-%d", ws.address, recipient, timestamp.UnixNano())
	txIDHash := sha256.Sum256([]byte(txIDData))
	txID := hex.EncodeToString(txIDHash[:16]) // Use first 16 bytes as ID

	// Create transaction data (without signature first)
	txData := TransactionData{
		ID:          txID,
		Sender:      ws.address,
		Recipient:   recipient,
		Amount:      float64(amount),
		Fee:         fee,
		GeoTag:      geotag,
		ParentCells: parentCells,
		Timestamp:   timestamp.Format(time.RFC3339),
	}

	// Serialize transaction data for signing
	txBytes, err := json.Marshal(txData)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal transaction: %w", err)
	}

	// Sign the transaction using Dilithium with optimized memory management
	// SignOptimized provides 5-10% performance improvement through memory pooling
	signature, err := ws.dilithium.SignOptimized(txBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to sign transaction: %w", err)
	}

	// Add signature to transaction
	txData.Signature = hex.EncodeToString(signature)
	txData.PublicKey = hex.EncodeToString(ws.dilithium.GetPublicKey())

	// Final transaction JSON
	finalTxBytes, err := json.Marshal(txData)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal final transaction: %w", err)
	}

	// Deduct amount from balance (will be updated when transaction is confirmed)
	ws.balance -= amount

	return finalTxBytes, nil
}

// SignData signs arbitrary data with the wallet's private key
func (ws *WalletService) SignData(data []byte) ([]byte, error) {
	if ws.dilithium == nil {
		return nil, errors.New("Dilithium not initialized")
	}
	return ws.dilithium.Sign(data)
}

// GetPublicKey returns the wallet's packed FIPS 204 ML-DSA-87
// public key (2592 bytes). Used by callers that want to verify
// a signature this wallet produced under
// VerifySignature(data, sig, ws.GetPublicKey()) — the
// pkg/crypto.*Dilithium.VerifyWithPublicKey path requires an
// externally-supplied key and does not consult the verifier
// handle's internal one. Returns nil if the underlying
// Dilithium handle has no key (verify-only construction, etc.).
func (ws *WalletService) GetPublicKey() []byte {
	if ws.dilithium == nil {
		return nil
	}
	return ws.dilithium.GetPublicKey()
}

// SignDataCompressed signs arbitrary data and returns a compressed signature.
// This reduces signature size by approximately 50% (4.6 KB → 2.3 KB for ML-DSA-87).
func (ws *WalletService) SignDataCompressed(data []byte) ([]byte, error) {
	if ws.dilithium == nil {
		return nil, errors.New("Dilithium not initialized")
	}
	return ws.dilithium.SignCompressed(data)
}

// VerifySignature verifies a signature against data and public key
func (ws *WalletService) VerifySignature(data []byte, signature []byte, publicKey []byte) (bool, error) {
	if ws.dilithium == nil {
		return false, errors.New("Dilithium not initialized")
	}
	return ws.dilithium.VerifyWithPublicKey(data, signature, publicKey)
}

// VerifySignatureCompressed verifies a compressed signature against data and public key.
// The signature is automatically decompressed before verification.
func (ws *WalletService) VerifySignatureCompressed(data []byte, compressedSig []byte, publicKey []byte) (bool, error) {
	if ws.dilithium == nil {
		return false, errors.New("Dilithium not initialized")
	}
	return ws.dilithium.VerifyWithPublicKeyCompressed(data, compressedSig, publicKey)
}

// DecodeAddress decodes a hex-encoded address
func DecodeAddress(address string) ([]byte, error) {
	return hex.DecodeString(address)
}

// EncodeAddress encodes an address to hex
func EncodeAddress(data []byte) string {
	return hex.EncodeToString(data)
}
