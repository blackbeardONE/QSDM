package errors

import (
	"fmt"
	"runtime"
)

// ErrorCode represents a specific error code
type ErrorCode string

const (
	ErrCodeStorage        ErrorCode = "STORAGE_ERROR"
	ErrCodeValidation     ErrorCode = "VALIDATION_ERROR"
	ErrCodeConsensus      ErrorCode = "CONSENSUS_ERROR"
	ErrCodeNetwork        ErrorCode = "NETWORK_ERROR"
	ErrCodeGovernance     ErrorCode = "GOVERNANCE_ERROR"
	ErrCodeWallet         ErrorCode = "WALLET_ERROR"
	ErrCodeQuarantine     ErrorCode = "QUARANTINE_ERROR"
	ErrCodeReputation     ErrorCode = "REPUTATION_ERROR"
	ErrCodeUnknown        ErrorCode = "UNKNOWN_ERROR"
)

// QSDMError represents a structured error with context
type QSDMError struct {
	Code      ErrorCode
	Message   string
	Operation string
	Err       error
	File      string
	Line      int
}

// Error implements the error interface
func (e *QSDMError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("[%s] %s (operation: %s, file: %s:%d): %v",
			e.Code, e.Message, e.Operation, e.File, e.Line, e.Err)
	}
	return fmt.Sprintf("[%s] %s (operation: %s, file: %s:%d)",
		e.Code, e.Message, e.Operation, e.File, e.Line)
}

// Unwrap returns the underlying error
func (e *QSDMError) Unwrap() error {
	return e.Err
}

// NewError creates a new QSDM error with automatic file/line detection
func NewError(code ErrorCode, message, operation string, err error) *QSDMError {
	_, file, line, _ := runtime.Caller(1)
	return &QSDMError{
		Code:      code,
		Message:   message,
		Operation: operation,
		Err:       err,
		File:      file,
		Line:      line,
	}
}

// WrapError wraps an existing error with QSDM error context
func WrapError(code ErrorCode, message, operation string, err error) *QSDMError {
	if err == nil {
		return nil
	}
	_, file, line, _ := runtime.Caller(1)
	return &QSDMError{
		Code:      code,
		Message:   message,
		Operation: operation,
		Err:       err,
		File:      file,
		Line:      line,
	}
}

// IsCode checks if an error has a specific error code
func IsCode(err error, code ErrorCode) bool {
	if qerr, ok := err.(*QSDMError); ok {
		return qerr.Code == code
	}
	return false
}

// Helper functions for common error types

// NewStorageError creates a storage-related error
func NewStorageError(operation string, err error) *QSDMError {
	return NewError(ErrCodeStorage, "Storage operation failed", operation, err)
}

// NewValidationError creates a validation-related error
func NewValidationError(operation string, err error) *QSDMError {
	return NewError(ErrCodeValidation, "Validation failed", operation, err)
}

// NewConsensusError creates a consensus-related error
func NewConsensusError(operation string, err error) *QSDMError {
	return NewError(ErrCodeConsensus, "Consensus operation failed", operation, err)
}

// NewNetworkError creates a network-related error
func NewNetworkError(operation string, err error) *QSDMError {
	return NewError(ErrCodeNetwork, "Network operation failed", operation, err)
}

// NewGovernanceError creates a governance-related error
func NewGovernanceError(operation string, err error) *QSDMError {
	return NewError(ErrCodeGovernance, "Governance operation failed", operation, err)
}

// NewWalletError creates a wallet-related error
func NewWalletError(operation string, err error) *QSDMError {
	return NewError(ErrCodeWallet, "Wallet operation failed", operation, err)
}

