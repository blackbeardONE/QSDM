package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/monitoring"
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
func (fs *FileStorage) StoreTransaction(data []byte) (resErr error) {
	defer func() {
		if resErr != nil {
			monitoring.RecordStorageOp(monitoring.StorageOpStoreTransaction, monitoring.StorageOpResultError)
		} else {
			monitoring.RecordStorageOp(monitoring.StorageOpStoreTransaction, monitoring.StorageOpResultSuccess)
		}
	}()

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
func (fs *FileStorage) Ready() (resErr error) {
	defer func() {
		if resErr != nil {
			monitoring.RecordStorageOp(monitoring.StorageOpReady, monitoring.StorageOpResultError)
		} else {
			monitoring.RecordStorageOp(monitoring.StorageOpReady, monitoring.StorageOpResultSuccess)
		}
	}()

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

// v0.4.1 (Session 99 → Session 100 deploy fix): per-account nonce
// + atomic-debit stubs.
//
// FileStorage doesn't track per-account state at all — GetBalance
// returns 0 unconditionally, UpdateBalance is a no-op, and there
// is no persisted nonce store. The honest semantic for the
// READ-ONLY GetNonce path is therefore "this backend has no state,
// so every sender's last-applied nonce is 0", which is symmetric
// with GetBalance's silent-zero behavior. Returning that as
// (0, nil) keeps the public GET /api/v1/wallet/nonce endpoint
// functional on a FileStorage-backed validator (the production
// BLR1 node ran on FileStorage as of v0.4.0), so self-custody
// clients can probe the route to detect v0.4.1 presence and
// resolve `next: 1` for their first submission. Replays against
// such a node still cannot land because the WRITE path
// (ApplyTransferAtomic) honestly refuses below — which makes
// /wallet/submit-signed return 500 "file storage does not support
// atomic transfers", visible in qsdm_wallet_send_total{result=
// "store_failed"}.
//
// Net effect on a FileStorage-backed validator:
//   GET  /wallet/nonce          → 200 {nonce:0, next:1}   (was 500 in
//                                                          the pre-fix v0.4.1)
//   POST /wallet/submit-signed  → 500 store_failed         (same as before
//                                                          the fix — settlement
//                                                          path always required
//                                                          SQLite or Scylla)
//
// This keeps the READ surface consistent across backends and
// pushes the WRITE-side limitation onto a single, named result
// tag operators can monitor. The SQLite v0.4.1 (sqlite_v041.go)
// + Scylla (scylla.go) backends do the real CAS + atomic debit.
func (fs *FileStorage) GetNonce(address string) (uint64, error) {
	return 0, nil
}

func (fs *FileStorage) ApplyTransferAtomic(
	ctx context.Context,
	sender, recipient string,
	amount, fee float64,
	envelopeNonce uint64,
	txID string,
	rawEnvelope []byte,
) error {
	return fmt.Errorf("FileStorage.ApplyTransferAtomic: file storage does not support atomic transfers (use SQLite or Scylla for v0.4.1)")
}
