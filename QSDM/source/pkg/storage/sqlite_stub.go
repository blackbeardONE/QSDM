//go:build !cgo
// +build !cgo

package storage

import (
	"context"
	stderrors "errors"
	"fmt"
)

// v0.4.1 sentinel errors. Re-declared here so that test code and
// other packages can errors.Is(..., storage.ErrInsufficientBalance)
// uniformly across CGO / !CGO builds. The CGO-side sqlite_v041.go
// declares the load-bearing copies; here they're just type-stable
// names that the !CGO build's stubs return.
var (
	ErrInsufficientBalance = stderrors.New("storage: insufficient balance")
	ErrNonceConflict       = stderrors.New("storage: nonce conflict")
	ErrTxAlreadyExists     = stderrors.New("storage: tx_id already exists")
)

// V041MigrationClampedRows is the !CGO stub of the CGO sqlite_v041.go
// gauge accessor. Returns -1 in this build because there is no
// sqlite balances table to migrate (file-storage / scylla backends
// expose their own metrics; the gauge name
// `qsdm_storage_v041_migration_clamped` is sqlite-specific).
func V041MigrationClampedRows() int64 { return -1 }

// Storage is a stub when CGO is disabled
type Storage struct{}

// NewStorage returns an error when CGO is disabled (SQLite requires CGO)
func NewStorage(dbPath string) (*Storage, error) {
	return nil, fmt.Errorf("SQLite storage requires CGO to be enabled. Build with CGO_ENABLED=1 or use file storage fallback")
}

func (s *Storage) StoreTransaction(data []byte) error {
	return fmt.Errorf("SQLite storage not available (CGO disabled)")
}

func (s *Storage) Close() error {
	return nil
}

func (s *Storage) Ready() error {
	return fmt.Errorf("SQLite storage not available (CGO disabled)")
}

func (s *Storage) GetBalance(address string) (float64, error) {
	return 0, fmt.Errorf("SQLite storage not available (CGO disabled)")
}

func (s *Storage) UpdateBalance(address string, amount float64) error {
	return fmt.Errorf("SQLite storage not available (CGO disabled)")
}

func (s *Storage) SetBalance(address string, balance float64) error {
	return fmt.Errorf("SQLite storage not available (CGO disabled)")
}

func (s *Storage) GetRecentTransactions(address string, limit int) ([]map[string]interface{}, error) {
	return nil, fmt.Errorf("SQLite storage not available (CGO disabled)")
}

func (s *Storage) GetTransaction(txID string) (map[string]interface{}, error) {
	return nil, fmt.Errorf("SQLite storage not available (CGO disabled)")
}

func (s *Storage) ForEachStoredTransaction(fn func(rawJSON []byte) error) error {
	return fmt.Errorf("SQLite storage not available (CGO disabled)")
}

func (s *Storage) ForEachBalance(fn func(address string, balance float64) error) error {
	return fmt.Errorf("SQLite storage not available (CGO disabled)")
}

// v0.4.1 stubs (Session 99). The handler integration in Session 100
// will route around backends that don't implement these — the
// interface check in pkg/api/server.go will surface the missing
// method at compile time rather than at runtime.
func (s *Storage) GetNonce(address string) (uint64, error) {
	return 0, fmt.Errorf("SQLite storage not available (CGO disabled)")
}

func (s *Storage) ApplyTransferAtomic(
	ctx context.Context,
	sender, recipient string,
	amount, fee float64,
	envelopeNonce uint64,
	txID string,
	rawEnvelope []byte,
) error {
	return fmt.Errorf("SQLite storage not available (CGO disabled)")
}
