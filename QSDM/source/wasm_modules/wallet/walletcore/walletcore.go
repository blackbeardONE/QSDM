package walletcore

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/blackbeardONE/QSDM/wasm_modules/wallet/walletcrypto"
	"sync"
	"time"
)

// Wallet represents a simple wallet with balance and key pair
type Wallet struct {
	balance int
	keyPair *walletcrypto.KeyPair
	address string
	mu      sync.Mutex
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
	Timestamp   string   `json:"timestamp"`
}

var wallet *Wallet

func init() {
	var err error
	wallet = &Wallet{}
	wallet.keyPair, err = walletcrypto.GenerateKeyPair()
	if err != nil {
		// Don't panic - wallet functionality may be handled by pkg/wallet instead
		// This init() is for backward compatibility
		fmt.Printf("WARNING: walletcore init: failed to generate key pair: %v\n", err)
		fmt.Printf("WARNING: walletcore will not be available. Use pkg/wallet instead.\n")
		// Set wallet to nil to indicate initialization failed
		wallet = nil
		return
	}
	wallet.balance = 1000 // Initial balance for demonstration
	// Generate address from public key (hash of public key)
	hash := sha256.Sum256(wallet.keyPair.PublicKey)
	wallet.address = hex.EncodeToString(hash[:])
}

// GetBalance returns the current wallet balance
func GetBalance() int {
	if wallet == nil {
		return 0
	}
	wallet.mu.Lock()
	defer wallet.mu.Unlock()
	return wallet.balance
}

// GetAddress returns the wallet address (derived from public key)
func GetAddress() string {
	if wallet == nil {
		return ""
	}
	wallet.mu.Lock()
	defer wallet.mu.Unlock()
	return wallet.address
}

// GetKeyPair returns the wallet's key pair (for external use)
func GetKeyPair() *walletcrypto.KeyPair {
	if wallet == nil {
		return nil
	}
	wallet.mu.Lock()
	defer wallet.mu.Unlock()
	return wallet.keyPair
}

// SendTransaction creates a signed transaction and returns it as JSON bytes.
// Returns transaction JSON bytes on success, nil on failure.
func SendTransaction(recipient string, amount int, fee float64, geotag string, parentCells []string) ([]byte, error) {
	if wallet == nil {
		return nil, errors.New("wallet not initialized - walletcore init failed")
	}
	wallet.mu.Lock()
	defer wallet.mu.Unlock()

	if amount <= 0 {
		return nil, errors.New("amount must be positive")
	}
	if wallet.balance < amount {
		return nil, fmt.Errorf("insufficient balance: have %d, need %d", wallet.balance, amount)
	}
	if recipient == "" {
		return nil, errors.New("recipient address is required")
	}
	if wallet.keyPair == nil {
		return nil, errors.New("key pair not initialized")
	}

	// Generate transaction ID from timestamp and sender/recipient
	timestamp := time.Now()
	txIDData := fmt.Sprintf("%s-%s-%d", wallet.address, recipient, timestamp.UnixNano())
	txIDHash := sha256.Sum256([]byte(txIDData))
	txID := hex.EncodeToString(txIDHash[:16]) // Use first 16 bytes as ID

	// Ensure we have at least 2 parent cells for PoE consensus
	if len(parentCells) < 2 {
		// Generate dummy parent cells if not provided (in real system, these would be actual parent transaction IDs)
		parentCells = []string{"parent1", "parent2"}
	}

	// Create transaction data (without signature first)
	txData := TransactionData{
		ID:          txID,
		Sender:      wallet.address,
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

	// Sign the transaction
	signature, err := wallet.keyPair.Sign(txBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to sign transaction: %w", err)
	}

	// Add signature to transaction
	txData.Signature = hex.EncodeToString(signature)

	// Final transaction JSON
	finalTxBytes, err := json.Marshal(txData)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal final transaction: %w", err)
	}

	// Deduct amount from balance (will be updated when transaction is confirmed)
	wallet.balance -= amount

	return finalTxBytes, nil
}

// SignTransaction signs arbitrary transaction data using wallet's key pair
func SignTransaction(data []byte) ([]byte, error) {
	if wallet == nil {
		return nil, errors.New("wallet not initialized - walletcore init failed")
	}
	wallet.mu.Lock()
	defer wallet.mu.Unlock()
	if wallet.keyPair == nil {
		return nil, errors.New("key pair not initialized")
	}
	return wallet.keyPair.Sign(data)
}
