//go:build !cgo
// +build !cgo

package storage

import (
	"fmt"
)

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
