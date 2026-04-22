//go:build cgo
// +build cgo

package wallet

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/crypto"
)

// WalletService provides wallet functionality for the QSDM node
type WalletService struct {
	address   string
	dilithium *crypto.Dilithium
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
	Signature   string   `json:"signature"`
	// PublicKey is hex-encoded ML-DSA-87 public key (for P2P preflight / verifiers); not part of the signed payload.
	PublicKey string `json:"public_key,omitempty"`
	Timestamp string   `json:"timestamp"`
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
		balance:   1000, // Initial balance for demonstration
	}, nil
}

// GetAddress returns the wallet address
func (ws *WalletService) GetAddress() string {
	return ws.address
}

// GetBalance returns the current wallet balance
func (ws *WalletService) GetBalance() int {
	return ws.balance
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
