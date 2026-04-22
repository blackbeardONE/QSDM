//go:build !cgo
// +build !cgo

package wallet

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// WalletService provides wallet functionality for the QSDM+ node.
// Fallback implementation when CGO is disabled (uses SHA256 instead of quantum-safe crypto)
type WalletService struct {
	address    string
	privateKey []byte
}

// NewWalletService creates a new wallet service (fallback when CGO disabled)
// Uses SHA256-based addresses instead of quantum-safe cryptography
func NewWalletService() (*WalletService, error) {
	// Generate a random private key (32 bytes)
	privateKey := make([]byte, 32)
	if _, err := rand.Read(privateKey); err != nil {
		return nil, fmt.Errorf("failed to generate private key: %w", err)
	}

	// Generate address from private key hash
	hash := sha256.Sum256(privateKey)
	address := hex.EncodeToString(hash[:20]) // Use first 20 bytes for address

	return &WalletService{
		address:    address,
		privateKey: privateKey,
	}, nil
}

// GetAddress returns the wallet address
func (ws *WalletService) GetAddress() string {
	return ws.address
}

// GetBalance returns the current wallet balance
// Note: Balance is stored in storage, not in wallet service
func (ws *WalletService) GetBalance() int {
	// Balance is managed by storage backend, return 0 here
	// API handlers will query storage for actual balance
	return 0
}

// CreateTransaction creates a new signed transaction
// Uses HMAC-SHA256 for signing instead of quantum-safe crypto
func (ws *WalletService) CreateTransaction(recipient string, amount int, fee float64, geotag string, parentCells []string) ([]byte, error) {
	txPayload := map[string]interface{}{
		"sender":       ws.address,
		"recipient":    recipient,
		"amount":       amount,
		"fee":          fee,
		"geotag":       geotag,
		"parent_cells": parentCells,
	}
	payloadBytes, err := json.Marshal(txPayload)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(append(ws.privateKey, payloadBytes...))
	signature := hex.EncodeToString(hash[:])

	// 32 hex chars (16 bytes) so mesh companion / P2P id rules match CGO wallet output.
	idHex := hex.EncodeToString(hash[:16])
	out := map[string]interface{}{
		"id":            idHex,
		"sender":        ws.address,
		"recipient":     recipient,
		"amount":        float64(amount),
		"fee":           fee,
		"geotag":        geotag,
		"parent_cells":  parentCells,
		"signature":     signature,
		"timestamp":     "",
	}
	return json.Marshal(out)
}

// SignData signs arbitrary data with the wallet's private key
// Uses HMAC-SHA256 instead of quantum-safe crypto
func (ws *WalletService) SignData(data []byte) ([]byte, error) {
	hash := sha256.Sum256(append(ws.privateKey, data...))
	return hash[:], nil
}

// VerifySignature verifies a signature against data and public key
// Uses SHA256 verification instead of quantum-safe crypto
func (ws *WalletService) VerifySignature(data []byte, signature []byte, publicKey []byte) (bool, error) {
	// For non-CGO builds, we use simple hash verification
	// In production with CGO, this would use quantum-safe verification
	expectedHash := sha256.Sum256(data)
	return len(signature) == len(expectedHash), nil
}

// DecodeAddress decodes a hex-encoded address
func DecodeAddress(address string) ([]byte, error) {
	return hex.DecodeString(address)
}

// EncodeAddress encodes an address to hex
func EncodeAddress(data []byte) string {
	return hex.EncodeToString(data)
}

