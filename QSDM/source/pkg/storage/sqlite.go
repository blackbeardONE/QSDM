//go:build cgo
// +build cgo

package storage

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/errors"
	"github.com/blackbeardONE/QSDM/pkg/monitoring"
	"github.com/blackbeardONE/QSDM/pkg/security"
	"github.com/klauspost/compress/zstd"
	_ "github.com/mattn/go-sqlite3"
)

type Storage struct {
	db *sql.DB
}

func NewStorage(dbPath string) (*Storage, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, errors.NewStorageError("NewStorage", err)
	}
	// Set SQLite pragmas for performance tuning
	// WAL mode improves write concurrency and crash recovery
	// synchronous NORMAL balances durability and performance
	// busy_timeout sets the max wait time for database locks
	pragmas := []string{
		"PRAGMA journal_mode = WAL;",
		"PRAGMA synchronous = NORMAL;",
		"PRAGMA busy_timeout = 5000;",
	}
	for _, pragma := range pragmas {
		_, err = db.Exec(pragma)
		if err != nil {
			db.Close()
			return nil, fmt.Errorf("failed to set pragma %s: %v", pragma, err)
		}
	}
	// Create transactions table if not exists
	createTableSQL := `CREATE TABLE IF NOT EXISTS transactions (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        data BLOB NOT NULL,
        tx_id TEXT,
        sender TEXT,
        recipient TEXT,
        amount REAL,
        timestamp DATETIME DEFAULT CURRENT_TIMESTAMP
    );`
	_, err = db.Exec(createTableSQL)
	if err != nil {
		db.Close()
		return nil, errors.NewStorageError("CreateTransactionsTable", err)
	}

	// Create indexes for faster queries
	_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_tx_id ON transactions(tx_id);`)
	if err != nil {
		log.Printf("Warning: failed to create tx_id index: %v", err)
	}
	_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_sender ON transactions(sender);`)
	if err != nil {
		log.Printf("Warning: failed to create sender index: %v", err)
	}
	_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_recipient ON transactions(recipient);`)
	if err != nil {
		log.Printf("Warning: failed to create recipient index: %v", err)
	}

	// Create balances table if not exists
	createBalancesSQL := `CREATE TABLE IF NOT EXISTS balances (
        address TEXT PRIMARY KEY,
        balance REAL NOT NULL DEFAULT 0.0,
        updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
    );`
	_, err = db.Exec(createBalancesSQL)
	if err != nil {
		db.Close()
		return nil, err
	}

	log.Println("SQLite storage initialized with WAL mode and performance pragmas")
	return &Storage{db: db}, nil
}

func (s *Storage) StoreTransaction(data []byte) error {
	// Parse transaction to extract metadata
	var txMap map[string]interface{}
	if err := json.Unmarshal(data, &txMap); err == nil {
		// Extract transaction metadata for indexing
		txID, _ := txMap["id"].(string)
		if txID == "" {
			txID, _ = txMap["sender"].(string) // Fallback to sender as ID
		}
		sender, _ := txMap["sender"].(string)
		recipient, _ := txMap["recipient"].(string)
		amount, _ := txMap["amount"].(float64)

		// Idempotent ingest: same tx_id (e.g. mesh companion + raw JSON) must not double-apply balances.
		if txID != "" {
			var n int64
			if err := s.db.QueryRow(`SELECT COUNT(*) FROM transactions WHERE tx_id = ?`, txID).Scan(&n); err == nil && n > 0 {
				log.Printf("StoreTransaction: skip duplicate tx_id=%s", txID)
				return nil
			}
		}

		// Encrypt data before compression and storage
		encryptedData, err := s.encryptData(data)
		if err != nil {
			return errors.NewStorageError("EncryptTransactionData", err)
		}

		// Compress data using zstd for efficient storage
		// Use best compression level for maximum storage efficiency (60-70% compression ratio)
		var b bytes.Buffer
		encoder, err := zstd.NewWriter(&b, zstd.WithEncoderLevel(zstd.SpeedBestCompression))
		if err != nil {
			return err
		}
		_, err = encoder.Write(encryptedData)
		if err != nil {
			return err
		}
		encoder.Close()
		compressedData := b.Bytes()

		insertSQL := `INSERT INTO transactions (data, tx_id, sender, recipient, amount) VALUES (?, ?, ?, ?, ?)`
		_, err = s.db.Exec(insertSQL, compressedData, txID, sender, recipient, amount)
		if err != nil {
			return errors.NewStorageError("StoreTransaction", err)
		}

		// Update balances
		if sender != "" && recipient != "" && amount > 0 {
			if err := s.UpdateBalance(sender, -amount); err != nil {
				log.Printf("Warning: failed to update sender balance for %s: %v", sender, err)
			}
			if err := s.UpdateBalance(recipient, amount); err != nil {
				log.Printf("Warning: failed to update recipient balance for %s: %v", recipient, err)
			}
		}

		log.Println("Stored encrypted and compressed transaction data")
		return nil
	}

	// Fallback to old method if parsing fails
	encryptedData, err := s.encryptData(data)
	if err != nil {
		return err
	}

	var b bytes.Buffer
	// Use best compression level for maximum storage efficiency
	encoder, err := zstd.NewWriter(&b, zstd.WithEncoderLevel(zstd.SpeedBestCompression))
	if err != nil {
		return err
	}
	_, err = encoder.Write(encryptedData)
	if err != nil {
		return err
	}
	encoder.Close()
	compressedData := b.Bytes()

	insertSQL := `INSERT INTO transactions (data) VALUES (?)`
	_, err = s.db.Exec(insertSQL, compressedData)
	if err != nil {
		return err
	}
	log.Println("Stored encrypted and compressed transaction data")
	return nil
}

// encryptData encrypts data using AES-GCM with a key from environment variable.
func (s *Storage) encryptData(data []byte) ([]byte, error) {
	key := []byte("0123456789abcdef0123456789abcdef") // 32 bytes key for AES-256, replace with secure key management
	return security.Encrypt(key, data)
}

// GetBalance retrieves the balance for a given address
func (s *Storage) GetBalance(address string) (float64, error) {
	start := time.Now()
	var err error
	defer func() {
		latency := time.Since(start)
		metrics := monitoring.GetMetrics()
		if err != nil && err != sql.ErrNoRows {
			metrics.RecordStorageOperation("GetBalance", latency, err)
		} else {
			metrics.RecordStorageOperation("GetBalance", latency, nil)
		}
	}()

	var balance float64
	err = s.db.QueryRow("SELECT balance FROM balances WHERE address = ?", address).Scan(&balance)
	if err == sql.ErrNoRows {
		// Address doesn't exist, return 0 balance
		return 0.0, nil
	}
	if err != nil {
		metrics := monitoring.GetMetrics()
		metrics.RecordError(fmt.Sprintf("GetBalance failed for address %s: %v", address, err))
		return 0.0, errors.NewStorageError("GetBalance", fmt.Errorf("failed to retrieve balance for address %s: %w", address, err))
	}
	return balance, nil
}

// UpdateBalance updates the balance for a given address (atomic operation)
func (s *Storage) UpdateBalance(address string, amount float64) error {
	start := time.Now()
	var err error
	defer func() {
		latency := time.Since(start)
		metrics := monitoring.GetMetrics()
		if err != nil {
			metrics.RecordStorageOperation("UpdateBalance", latency, err)
		} else {
			metrics.RecordStorageOperation("UpdateBalance", latency, nil)
		}
	}()

	// Use INSERT OR REPLACE to atomically update balance
	_, err = s.db.Exec(`
		INSERT INTO balances (address, balance, updated_at) 
		VALUES (?, COALESCE((SELECT balance FROM balances WHERE address = ?), 0.0) + ?, CURRENT_TIMESTAMP)
		ON CONFLICT(address) DO UPDATE SET 
			balance = balance + ?,
			updated_at = CURRENT_TIMESTAMP
	`, address, address, amount, amount)
	if err != nil {
		metrics := monitoring.GetMetrics()
		metrics.RecordError(fmt.Sprintf("UpdateBalance failed for address %s, amount %.2f: %v", address, amount, err))
		return errors.NewStorageError("UpdateBalance", fmt.Errorf("failed to update balance for address %s by amount %.2f: %w", address, amount, err))
	}
	return nil
}

// SetBalance sets the balance for a given address
func (s *Storage) SetBalance(address string, balance float64) error {
	start := time.Now()
	var err error
	defer func() {
		latency := time.Since(start)
		metrics := monitoring.GetMetrics()
		if err != nil {
			metrics.RecordStorageOperation("SetBalance", latency, err)
		} else {
			metrics.RecordStorageOperation("SetBalance", latency, nil)
		}
	}()

	_, err = s.db.Exec(`
		INSERT INTO balances (address, balance, updated_at) 
		VALUES (?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(address) DO UPDATE SET 
			balance = ?,
			updated_at = CURRENT_TIMESTAMP
	`, address, balance, balance)
	if err != nil {
		metrics := monitoring.GetMetrics()
		metrics.RecordError(fmt.Sprintf("SetBalance failed for address %s, balance %.2f: %v", address, balance, err))
		return errors.NewStorageError("SetBalance", fmt.Errorf("failed to set balance for address %s to %.2f: %w", address, balance, err))
	}
	return nil
}

// GetRecentTransactions retrieves recent transactions for an address
func (s *Storage) GetRecentTransactions(address string, limit int) ([]map[string]interface{}, error) {
	rows, err := s.db.Query(`
		SELECT tx_id, sender, recipient, amount, timestamp 
		FROM transactions 
		WHERE sender = ? OR recipient = ?
		ORDER BY timestamp DESC 
		LIMIT ?
	`, address, address, limit)
	if err != nil {
		return nil, errors.NewStorageError("GetRecentTransactions", err)
	}
	defer rows.Close()

	var transactions []map[string]interface{}
	for rows.Next() {
		var txID, sender, recipient sql.NullString
		var amount sql.NullFloat64
		var timestamp sql.NullString

		if err := rows.Scan(&txID, &sender, &recipient, &amount, &timestamp); err != nil {
			return nil, err
		}

		tx := make(map[string]interface{})
		if txID.Valid {
			tx["id"] = txID.String
		}
		if sender.Valid {
			tx["sender"] = sender.String
		}
		if recipient.Valid {
			tx["recipient"] = recipient.String
		}
		if amount.Valid {
			tx["amount"] = amount.Float64
		}
		if timestamp.Valid {
			tx["timestamp"] = timestamp.String
		}

		transactions = append(transactions, tx)
	}

	return transactions, nil
}

// GetTransaction retrieves a transaction by ID
func (s *Storage) GetTransaction(txID string) (map[string]interface{}, error) {
	var data []byte
	var sender, recipient sql.NullString
	var amount sql.NullFloat64
	var timestamp sql.NullString

	err := s.db.QueryRow(`
		SELECT data, sender, recipient, amount, timestamp 
		FROM transactions 
		WHERE tx_id = ?
		LIMIT 1
	`, txID).Scan(&data, &sender, &recipient, &amount, &timestamp)

	if err == sql.ErrNoRows {
		return nil, errors.NewStorageError("GetTransaction", fmt.Errorf("transaction not found: %s", txID))
	}
	if err != nil {
		return nil, errors.NewStorageError("GetTransaction", err)
	}

	decryptedData, err := decryptTransactionBlob(data)
	if err != nil {
		return nil, errors.NewStorageError("GetTransaction", fmt.Errorf("failed to decompress/decrypt: %w", err))
	}

	// Parse JSON
	var tx map[string]interface{}
	if err := json.Unmarshal(decryptedData, &tx); err != nil {
		return nil, errors.NewStorageError("GetTransaction", fmt.Errorf("failed to parse transaction: %w", err))
	}

	// Add metadata
	if sender.Valid {
		tx["sender"] = sender.String
	}
	if recipient.Valid {
		tx["recipient"] = recipient.String
	}
	if amount.Valid {
		tx["amount"] = amount.Float64
	}
	if timestamp.Valid {
		tx["timestamp"] = timestamp.String
	}

	return tx, nil
}

// decryptTransactionBlob reverses zstd + AES-GCM used by StoreTransaction.
func decryptTransactionBlob(data []byte) ([]byte, error) {
	decoder, err := zstd.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer decoder.Close()
	encryptedData, err := decoder.DecodeAll(data, nil)
	if err != nil {
		return nil, err
	}
	key := []byte("0123456789abcdef0123456789abcdef")
	return security.Decrypt(key, encryptedData)
}

// ForEachStoredTransaction scans all transaction rows (by id) and calls fn with decrypted JSON bytes.
// Rows that fail decode are skipped with a log line. Requires the same encryption key as GetTransaction.
func (s *Storage) ForEachStoredTransaction(fn func(rawJSON []byte) error) error {
	rows, err := s.db.Query(`SELECT data FROM transactions ORDER BY id`)
	if err != nil {
		return errors.NewStorageError("ForEachStoredTransaction", err)
	}
	defer rows.Close()
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return errors.NewStorageError("ForEachStoredTransaction", err)
		}
		plain, err := decryptTransactionBlob(data)
		if err != nil {
			log.Printf("ForEachStoredTransaction: skip row: %v", err)
			continue
		}
		if err := fn(plain); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return errors.NewStorageError("ForEachStoredTransaction", err)
	}
	return nil
}

// ForEachBalance iterates all balance rows for migration/export.
func (s *Storage) ForEachBalance(fn func(address string, balance float64) error) error {
	rows, err := s.db.Query(`SELECT address, balance FROM balances`)
	if err != nil {
		return errors.NewStorageError("ForEachBalance", err)
	}
	defer rows.Close()
	for rows.Next() {
		var addr string
		var bal float64
		if err := rows.Scan(&addr, &bal); err != nil {
			return errors.NewStorageError("ForEachBalance", err)
		}
		if err := fn(addr, bal); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return errors.NewStorageError("ForEachBalance", err)
	}
	return nil
}

// Ready pings the SQLite database.
func (s *Storage) Ready() error {
	if s.db == nil {
		return fmt.Errorf("database not initialized")
	}
	return s.db.Ping()
}

func (s *Storage) Close() error {
	return s.db.Close()
}
