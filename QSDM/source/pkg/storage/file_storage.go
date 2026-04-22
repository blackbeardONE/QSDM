package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// FileStorage implements storage by saving transactions as individual files.
type FileStorage struct {
	dir string
	mu  sync.Mutex
}

// NewFileStorage creates a new FileStorage instance.
func NewFileStorage(dir string) (*FileStorage, error) {
	err := os.MkdirAll(dir, 0755)
	if err != nil {
		return nil, fmt.Errorf("failed to create storage directory: %w", err)
	}
	return &FileStorage{dir: dir}, nil
}

func sanitizeWalletTxIDForPath(id string) string {
	var b strings.Builder
	b.Grow(len(id))
	for _, r := range id {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	s := b.String()
	if len(s) > 200 {
		s = s[:200]
	}
	if s == "" {
		s = "empty"
	}
	return s
}

// StoreTransaction stores a transaction as a file. When JSON contains a non-empty `id`,
// the file name is derived from that id so duplicate ingests are skipped (parity with SQLite dedupe).
func (fs *FileStorage) StoreTransaction(data []byte) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	var filename string
	var txMap map[string]interface{}
	if err := json.Unmarshal(data, &txMap); err == nil {
		txID, _ := txMap["id"].(string)
		txID = strings.TrimSpace(txID)
		if txID != "" {
			base := "wallet_tx_" + sanitizeWalletTxIDForPath(txID) + ".dat"
			filename = filepath.Join(fs.dir, base)
			if _, err := os.Stat(filename); err == nil {
				return nil
			}
		}
	}
	if filename == "" {
		filename = filepath.Join(fs.dir, fmt.Sprintf("tx_%d_%d.dat", os.Getpid(), time.Now().UnixNano()))
	}

	err := os.WriteFile(filename, data, 0644)
	if err != nil {
		return fmt.Errorf("failed to write transaction file: %w", err)
	}
	return nil
}

// Close performs any cleanup (no-op for file storage).
func (fs *FileStorage) Close() error {
	return nil
}

// Ready checks that the storage directory is accessible.
func (fs *FileStorage) Ready() error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	_, err := os.Stat(fs.dir)
	if err != nil {
		return fmt.Errorf("file storage directory: %w", err)
	}
	return nil
}

// GetBalance returns 0 (file storage doesn't track balances)
func (fs *FileStorage) GetBalance(address string) (float64, error) {
	return 0, nil
}

// UpdateBalance is a no-op for file storage
func (fs *FileStorage) UpdateBalance(address string, amount float64) error {
	return nil
}

// SetBalance is a no-op for file storage
func (fs *FileStorage) SetBalance(address string, balance float64) error {
	return nil
}

// GetRecentTransactions returns empty list (file storage doesn't track transactions by address)
func (fs *FileStorage) GetRecentTransactions(address string, limit int) ([]map[string]interface{}, error) {
	return []map[string]interface{}{}, nil
}

// GetTransaction returns error (file storage doesn't support transaction lookup by ID)
func (fs *FileStorage) GetTransaction(txID string) (map[string]interface{}, error) {
	return nil, fmt.Errorf("transaction lookup not supported by file storage")
}
