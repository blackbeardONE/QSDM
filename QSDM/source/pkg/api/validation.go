package api

import (
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// Validation errors
var (
	ErrInvalidAddress    = errors.New("invalid address format")
	ErrInvalidAmount      = errors.New("invalid amount")
	ErrInvalidTransactionID = errors.New("invalid transaction ID format")
	ErrInvalidString      = errors.New("string validation failed")
	ErrStringTooLong      = errors.New("string exceeds maximum length")
	ErrStringTooShort     = errors.New("string below minimum length")
)

// Validation constants
const (
	// Address validation
	MinAddressLength = 32  // Minimum hex address length (16 bytes = 32 hex chars)
	MaxAddressLength = 128 // Maximum address length
	
	// Transaction ID validation
	MinTxIDLength = 16  // Minimum transaction ID length
	MaxTxIDLength = 128 // Maximum transaction ID length
	
	// Amount validation
	MinAmount     = 0.00000001 // Minimum transaction amount (1 satoshi equivalent)
	MaxAmount     = 1000000000 // Maximum transaction amount (1 billion)
	
	// String length limits
	MaxStringLength = 10000 // Maximum string length for general inputs
	MaxGeoTagLength = 100   // Maximum geotag length
	MaxParentCells  = 10    // Maximum number of parent cells
	
	// Password validation
	MinPasswordLength = 12  // Minimum password length (increased from 8)
	MaxPasswordLength = 256 // Maximum password length
)

// ValidateAddress validates a wallet address format
func ValidateAddress(address string) error {
	if address == "" {
		return fmt.Errorf("%w: address cannot be empty", ErrInvalidAddress)
	}
	
	if len(address) < MinAddressLength {
		return fmt.Errorf("%w: address too short (minimum %d characters)", ErrInvalidAddress, MinAddressLength)
	}
	
	if len(address) > MaxAddressLength {
		return fmt.Errorf("%w: address too long (maximum %d characters)", ErrInvalidAddress, MaxAddressLength)
	}
	
	// Address should be hex-encoded (case-insensitive)
	hexPattern := regexp.MustCompile(`^[0-9a-fA-F]+$`)
	if !hexPattern.MatchString(address) {
		return fmt.Errorf("%w: address must be hex-encoded", ErrInvalidAddress)
	}
	
	return nil
}

// ValidateTransactionID validates a transaction ID format
func ValidateTransactionID(txID string) error {
	if txID == "" {
		return fmt.Errorf("%w: transaction ID cannot be empty", ErrInvalidTransactionID)
	}
	
	if len(txID) < MinTxIDLength {
		return fmt.Errorf("%w: transaction ID too short (minimum %d characters)", ErrInvalidTransactionID, MinTxIDLength)
	}
	
	if len(txID) > MaxTxIDLength {
		return fmt.Errorf("%w: transaction ID too long (maximum %d characters)", ErrInvalidTransactionID, MaxTxIDLength)
	}
	
	// Transaction ID should be alphanumeric (hex or base64-like)
	// Allow hex, base64, and alphanumeric characters
	idPattern := regexp.MustCompile(`^[0-9a-zA-Z_-]+$`)
	if !idPattern.MatchString(txID) {
		return fmt.Errorf("%w: transaction ID contains invalid characters", ErrInvalidTransactionID)
	}
	
	return nil
}

// ValidateAmount validates a transaction amount
func ValidateAmount(amount float64) error {
	if amount < MinAmount {
		return fmt.Errorf("%w: amount %.8f is below minimum %.8f", ErrInvalidAmount, amount, MinAmount)
	}
	
	if amount > float64(MaxAmount) {
		return fmt.Errorf("%w: amount %.2f exceeds maximum %d", ErrInvalidAmount, amount, MaxAmount)
	}
	
	// Check for NaN or Infinity
	if amount != amount { // NaN check
		return fmt.Errorf("%w: amount is NaN", ErrInvalidAmount)
	}
	
	return nil
}

// ValidateString validates a general string input
func ValidateString(s, fieldName string, minLen, maxLen int) error {
	if s == "" && minLen > 0 {
		return fmt.Errorf("%w: %s cannot be empty", ErrStringTooShort, fieldName)
	}
	
	if len(s) < minLen {
		return fmt.Errorf("%w: %s too short (minimum %d characters)", ErrStringTooShort, fieldName, minLen)
	}
	
	if len(s) > maxLen {
		return fmt.Errorf("%w: %s too long (maximum %d characters)", ErrStringTooLong, fieldName, maxLen)
	}
	
	return nil
}

// ValidateGeoTag validates a geographic tag
func ValidateGeoTag(geotag string) error {
	// GeoTag is optional, but if provided, validate it
	if geotag == "" {
		return nil // Optional field
	}
	
	return ValidateString(geotag, "geotag", 0, MaxGeoTagLength)
}

// ValidateParentCells validates parent cell IDs
func ValidateParentCells(parentCells []string) error {
	if len(parentCells) > MaxParentCells {
		return fmt.Errorf("too many parent cells (maximum %d)", MaxParentCells)
	}
	
	// Validate each parent cell ID
	for i, cellID := range parentCells {
		if err := ValidateTransactionID(cellID); err != nil {
			return fmt.Errorf("parent cell %d: %w", i, err)
		}
	}
	
	return nil
}

// ValidatePassword validates a password according to security policy
func ValidatePassword(password string) error {
	if len(password) < MinPasswordLength {
		return fmt.Errorf("password too short (minimum %d characters)", MinPasswordLength)
	}
	
	if len(password) > MaxPasswordLength {
		return fmt.Errorf("password too long (maximum %d characters)", MaxPasswordLength)
	}
	
	// Check for required character types
	var (
		hasUpper   = false
		hasLower   = false
		hasNumber  = false
		hasSpecial = false
	)
	
	for _, char := range password {
		switch {
		case unicode.IsUpper(char):
			hasUpper = true
		case unicode.IsLower(char):
			hasLower = true
		case unicode.IsNumber(char):
			hasNumber = true
		case unicode.IsPunct(char) || unicode.IsSymbol(char):
			hasSpecial = true
		}
	}
	
	var missing []string
	if !hasUpper {
		missing = append(missing, "uppercase letter")
	}
	if !hasLower {
		missing = append(missing, "lowercase letter")
	}
	if !hasNumber {
		missing = append(missing, "number")
	}
	if !hasSpecial {
		missing = append(missing, "special character")
	}
	
	if len(missing) > 0 {
		return fmt.Errorf("password must contain at least one: %s", strings.Join(missing, ", "))
	}
	
	// Check for common weak passwords
	weakPasswords := []string{
		"password", "123456", "qwerty", "abc123", "password123",
		"admin", "letmein", "welcome", "monkey", "12345678",
	}
	passwordLower := strings.ToLower(password)
	for _, weak := range weakPasswords {
		if strings.Contains(passwordLower, weak) {
			return fmt.Errorf("password is too weak (contains common pattern)")
		}
	}
	
	return nil
}

// SanitizeString sanitizes a string for safe logging
func SanitizeString(s string, maxLen int) string {
	// Truncate if too long
	if len(s) > maxLen {
		s = s[:maxLen] + "..."
	}
	
	// Remove control characters
	var sanitized strings.Builder
	for _, r := range s {
		if unicode.IsPrint(r) || unicode.IsSpace(r) {
			sanitized.WriteRune(r)
		}
	}
	
	return sanitized.String()
}

// ValidateHexString validates a hex-encoded string
func ValidateHexString(s string, expectedLength int) error {
	if s == "" {
		return errors.New("hex string cannot be empty")
	}
	
	// Check if it's valid hex
	_, err := hex.DecodeString(s)
	if err != nil {
		return fmt.Errorf("invalid hex string: %w", err)
	}
	
	// Check length (hex string is 2x byte length)
	if expectedLength > 0 && len(s) != expectedLength*2 {
		return fmt.Errorf("hex string length mismatch: expected %d characters, got %d", expectedLength*2, len(s))
	}
	
	return nil
}

// ValidateOptionalMLDSAPublicKeyHex validates a hex-encoded ML-DSA-87 public key when present on P2P wallet JSON.
func ValidateOptionalMLDSAPublicKeyHex(s string) error {
	if s == "" {
		return nil
	}
	const mldsa87PubBytes = 2592 // ML-DSA-87 / liboqs OQS_SIG length_public_key
	if len(s) != mldsa87PubBytes*2 {
		return fmt.Errorf("public_key hex length must be %d for ML-DSA-87, got %d", mldsa87PubBytes*2, len(s))
	}
	return ValidateHexString(s, mldsa87PubBytes)
}

// ValidateSignature validates a signature format
func ValidateSignature(signature string) error {
	// Signature should be hex-encoded
	// ML-DSA-87 signature is ~4.6KB uncompressed, ~2.3KB compressed
	// Hex encoding doubles the size
	minSigLength := 100  // Minimum reasonable signature length
	maxSigLength := 10000 // Maximum signature length (allows for future algorithms)
	
	if len(signature) < minSigLength {
		return fmt.Errorf("signature too short (minimum %d characters)", minSigLength)
	}
	
	if len(signature) > maxSigLength {
		return fmt.Errorf("signature too long (maximum %d characters)", maxSigLength)
	}
	
	// Validate hex format
	return ValidateHexString(signature, 0) // Don't enforce exact length
}

