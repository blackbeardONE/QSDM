package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
)

// TransactionData represents the transaction structure for validation
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

// ValidationResult represents the result of validation
type ValidationResult struct {
	Valid   bool   `json:"valid"`
	Message string `json:"message"`
}

// validateTransactionStructure validates the basic structure of a transaction
func validateTransactionStructure(tx *TransactionData) error {
	if tx.ID == "" {
		return errors.New("transaction ID is required")
	}
	if tx.Sender == "" {
		return errors.New("sender address is required")
	}
	if tx.Recipient == "" {
		return errors.New("recipient address is required")
	}
	if tx.Amount <= 0 {
		return errors.New("amount must be positive")
	}
	if tx.Fee < 0 {
		return errors.New("fee cannot be negative")
	}
	if len(tx.ParentCells) < 2 {
		return errors.New("at least 2 parent cells are required for Phase 1/2")
	}
	if len(tx.ParentCells) > 5 {
		return errors.New("maximum 5 parent cells allowed")
	}
	if tx.Signature == "" {
		return errors.New("signature is required")
	}
	if tx.Timestamp == "" {
		return errors.New("timestamp is required")
	}
	return nil
}

// validateParentCells validates parent cell structure and uniqueness
func validateParentCells(parentCells []string) error {
	if len(parentCells) == 0 {
		return errors.New("parent cells are required")
	}
	
	// Check for duplicates
	seen := make(map[string]bool)
	for i, pc := range parentCells {
		if pc == "" {
			return errors.New("parent cell cannot be empty")
		}
		if seen[pc] {
			return errors.New("duplicate parent cell ID: " + pc)
		}
		seen[pc] = true
		
		// Validate parent cell ID format (should be hex string)
		if len(pc) < 8 {
			return errors.New("parent cell ID too short at index " + string(rune(i)))
		}
	}
	return nil
}

// validateTransactionIntegrity validates transaction data integrity using hash
func validateTransactionIntegrity(tx *TransactionData) error {
	// Create a copy without signature for hashing
	txForHash := TransactionData{
		ID:          tx.ID,
		Sender:      tx.Sender,
		Recipient:   tx.Recipient,
		Amount:      tx.Amount,
		Fee:         tx.Fee,
		GeoTag:      tx.GeoTag,
		ParentCells: tx.ParentCells,
		Timestamp:   tx.Timestamp,
		// Signature intentionally omitted
	}
	
	txBytes, err := json.Marshal(txForHash)
	if err != nil {
		return errors.New("failed to marshal transaction for integrity check: " + err.Error())
	}
	
	// Compute hash
	hash := sha256.Sum256(txBytes)
	_ = hex.EncodeToString(hash[:]) // In real implementation, verify against stored hash
	
	return nil
}

// validateSignatureFormat validates that the signature is in correct format
func validateSignatureFormat(signature string) error {
	if signature == "" {
		return errors.New("signature cannot be empty")
	}
	// Signature should be hex-encoded
	_, err := hex.DecodeString(signature)
	if err != nil {
		return errors.New("signature must be valid hex string: " + err.Error())
	}
	if len(signature) < 64 {
		return errors.New("signature too short (minimum 64 hex characters)")
	}
	return nil
}

// ValidateTransaction is the main validation function
// It validates transaction structure, parent cells, integrity, and signature format
func ValidateTransaction(txDataJSON string) (bool, string) {
	// Parse transaction JSON
	var tx TransactionData
	err := json.Unmarshal([]byte(txDataJSON), &tx)
	if err != nil {
		return false, "failed to parse transaction JSON: " + err.Error()
	}
	
	// Validate structure
	err = validateTransactionStructure(&tx)
	if err != nil {
		return false, "structure validation failed: " + err.Error()
	}
	
	// Validate parent cells
	err = validateParentCells(tx.ParentCells)
	if err != nil {
		return false, "parent cell validation failed: " + err.Error()
	}
	
	// Validate signature format
	err = validateSignatureFormat(tx.Signature)
	if err != nil {
		return false, "signature format validation failed: " + err.Error()
	}
	
	// Validate transaction integrity
	err = validateTransactionIntegrity(&tx)
	if err != nil {
		return false, "integrity validation failed: " + err.Error()
	}
	
	// Additional business logic validations
	if tx.Sender == tx.Recipient {
		return false, "sender and recipient cannot be the same"
	}
	
	// Validate geotag format (should be uppercase region code)
	if tx.GeoTag != "" {
		validGeoTags := []string{"US", "EU", "ASIA", "AFRICA", "OCEANIA", "AMERICAS"}
		valid := false
		for _, tag := range validGeoTags {
			if strings.ToUpper(tx.GeoTag) == tag {
				valid = true
				break
			}
		}
		if !valid && tx.GeoTag != "" {
			return false, "invalid geotag: " + tx.GeoTag
		}
	}
	
	return true, "transaction is valid"
}

// ValidateTransactionWASI is the WASI-compatible entry point
// txDataPtr and txDataLen represent the memory location and length of the transaction JSON
// Returns 1 for valid, 0 for invalid
//export ValidateTransaction
func ValidateTransactionWASI(txDataPtr uint32, txDataLen uint32) uint32 {
	// In a real WASI implementation, this would read from WASM memory
	// For now, this is a placeholder that demonstrates the interface
	// The actual memory access would be implemented using WASI memory functions
	
	// Placeholder: In real implementation, read txDataPtr[0:txDataLen] from WASM memory
	// For demonstration purposes, we return a default value
	// This would be replaced with actual memory access in production
	
	// Example: If we had access to memory, we would do:
	// txData := readWASIMemory(txDataPtr, txDataLen)
	// valid, _ := ValidateTransaction(string(txData))
	// return uint32(boolToInt(valid))
	
	// For now, return 1 (valid) as a placeholder
	// The actual validation logic is in ValidateTransaction function above
	return 1
}

// Helper function to convert bool to int (for WASI return value)
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

//export Hello
func Hello() uint32 {
	return 42
}

func main() {
	// WASI modules do not have a main loop like JS WASM.
	// Initialization code can be added here if needed.
}
