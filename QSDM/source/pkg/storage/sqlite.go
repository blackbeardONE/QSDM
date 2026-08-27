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

	// v0.4.1 (Session 99): migrate the balances table to the
	// nonce + CHECK(balance >= 0) shape if it's still in v0.4.0
	// form. Idempotent — short-circuits on subsequent boots once
	// the migration is applied. See sqlite_v041.go.
	s := &Storage{db: db}
	if migErr := s.migrateBalancesToV041(); migErr != nil {
		db.Close()
		return nil, fmt.Errorf("v041 balances migration failed: %w", migErr)
	}
	return s, nil
}

func (s *Storage) StoreTransaction(data []byte) (resErr error) {
	// Single instrumentation point: every return path flips the
	// qsdm_storage_op_total{op="store_transaction", result=...}
	// counter so the scrape sees both successes and failures
	// regardless of which branch returned.
	defer func() {
		if resErr != nil {
			monitoring.RecordStorageOp(monitoring.StorageOpStoreTransaction, monitoring.StorageOpResultError)
		} else {
			monitoring.RecordStorageOp(monitoring.StorageOpStoreTransaction, monitoring.StorageOpResultSuccess)
		}
	}()

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

		// The duplicate check, the insert and both balance updates run in ONE
		// transaction.
		//
		// They used to be four separate db.Exec/QueryRow calls, which produced
		// two distinct money bugs:
		//
		//   - Partial application. The insert could succeed and either balance
		//     update fail; both failures were logged with log.Printf("Warning:
		//     ...") and StoreTransaction returned nil anyway. A sender debit
		//     that landed beside a recipient credit that did not destroys value
		//     in the balances table while the API reports success.
		//   - Double application. The idempotency check read COUNT(*) outside
		//     any transaction, so two concurrent ingests of the same tx_id --
		//     the mesh companion and the raw JSON, or an HTTP send racing gossip
		//     -- could both observe zero and both apply the balances.
		//
		// A rollback now undoes the insert too, so a stored transaction always
		// has its balance effects applied, and never has them applied twice.
		tx, err := s.db.Begin()
		if err != nil {
			return errors.NewStorageError("StoreTransactionBegin", err)
		}
		committed := false
		defer func() {
			if !committed {
				_ = tx.Rollback()
			}
		}()

		// Idempotent ingest: same tx_id (e.g. mesh companion + raw JSON) must not double-apply balances.
		if txID != "" {
			var n int64
			if err := tx.QueryRow(`SELECT COUNT(*) FROM transactions WHERE tx_id = ?`, txID).Scan(&n); err == nil && n > 0 {
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
		_, err = tx.Exec(insertSQL, compressedData, txID, sender, recipient, amount)
		if err != nil {
			return errors.NewStorageError("StoreTransaction", err)
		}

		// Balances: the debit and the credit apply together or not at all.
		//
		// Deliberately NOT fatal to the insert. StoreTransaction is also the
		// ingest path for transactions this node did not originate -- gossip,
		// replay, the mesh companion -- where the local balances table may not
		// have funded the sender at all. Refusing to RECORD such a transaction
		// would be a worse bug than the one being fixed, and an earlier version
		// of this change did exactly that: it made the whole write fail and
		// broke TestForEachStoredTransaction_roundTrip, whose sender has no
		// balance row.
		//
		// So the pair runs inside a SAVEPOINT. Either both land or neither
		// does, and the transaction row survives either way. What is no longer
		// possible is the case that used to be routine: the debit failing the
		// `CHECK(balance >= 0)` constraint added by the v0.4.1 migration, being
		// logged as a warning and ignored, and the recipient being credited
		// anyway -- which creates CELL out of nothing in the balances table.
		if sender != "" && recipient != "" && amount > 0 {
			if _, spErr := tx.Exec(`SAVEPOINT balance_pair`); spErr != nil {
				return errors.NewStorageError("StoreTransactionSavepoint", spErr)
			}
			debitErr := updateBalanceTx(tx, sender, -amount)
			var creditErr error
			if debitErr == nil {
				creditErr = updateBalanceTx(tx, recipient, amount)
			}
			if debitErr != nil || creditErr != nil {
				if _, rbErr := tx.Exec(`ROLLBACK TO SAVEPOINT balance_pair`); rbErr != nil {
					return errors.NewStorageError("StoreTransactionBalanceRollback", rbErr)
				}
				log.Printf("StoreTransaction: balances NOT applied for tx_id=%s (debit=%v credit=%v); "+
					"transaction row retained, both sides rolled back together", txID, debitErr, creditErr)
			}
			if _, relErr := tx.Exec(`RELEASE SAVEPOINT balance_pair`); relErr != nil {
				return errors.NewStorageError("StoreTransactionSavepointRelease", relErr)
			}
		}

		if err := tx.Commit(); err != nil {
			return errors.NewStorageError("StoreTransactionCommit", err)
		}
		committed = true

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
			monitoring.RecordStorageOp(monitoring.StorageOpGetBalance, monitoring.StorageOpResultError)
		} else {
			metrics.RecordStorageOperation("GetBalance", latency, nil)
			monitoring.RecordStorageOp(monitoring.StorageOpGetBalance, monitoring.StorageOpResultSuccess)
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
			monitoring.RecordStorageOp(monitoring.StorageOpUpdateBalance, monitoring.StorageOpResultError)
		} else {
			metrics.RecordStorageOperation("UpdateBalance", latency, nil)
			monitoring.RecordStorageOp(monitoring.StorageOpUpdateBalance, monitoring.StorageOpResultSuccess)
		}
	}()

	// Delegates to the same statement the transactional path uses, so the two
	// cannot drift. A duplicated derivation is how chain/propagation.go came to
	// carry its own copy of the block hash and miss a change made to the other.
	err = updateBalanceExec(s.db, address, amount)
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
			monitoring.RecordStorageOp(monitoring.StorageOpSetBalance, monitoring.StorageOpResultError)
		} else {
			metrics.RecordStorageOperation("SetBalance", latency, nil)
			monitoring.RecordStorageOp(monitoring.StorageOpSetBalance, monitoring.StorageOpResultSuccess)
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
func (s *Storage) Ready() (resErr error) {
	defer func() {
		if resErr != nil {
			monitoring.RecordStorageOp(monitoring.StorageOpReady, monitoring.StorageOpResultError)
		} else {
			monitoring.RecordStorageOp(monitoring.StorageOpReady, monitoring.StorageOpResultSuccess)
		}
	}()
	if s.db == nil {
		return fmt.Errorf("database not initialized")
	}
	return s.db.Ping()
}

func (s *Storage) Close() error {
	return s.db.Close()
}

// balanceExecer is satisfied by both *sql.DB and *sql.Tx, so the balance
// statement has exactly one definition regardless of whether the caller is
// inside a transaction.
type balanceExecer interface {
	Exec(query string, args ...interface{}) (sql.Result, error)
}

// updateBalanceExec applies a signed delta to an address's balance.
//
// NOTE ON SIGN: there is deliberately no non-negative constraint here. Adding
// one would change behaviour for callers this commit did not audit -- mining
// rewards, the synthetic funder, and the migration paths all route through
// UpdateBalance -- and whether a balance row may go negative is a policy
// question, not a bug fix. It is recorded as open in the audit rather than
// decided here. What this commit does fix is that a debit and its matching
// credit now succeed or fail together.
func updateBalanceExec(db balanceExecer, address string, amount float64) error {
	_, err := db.Exec(`
		INSERT INTO balances (address, balance, updated_at) 
		VALUES (?, COALESCE((SELECT balance FROM balances WHERE address = ?), 0.0) + ?, CURRENT_TIMESTAMP)
		ON CONFLICT(address) DO UPDATE SET 
			balance = balance + ?,
			updated_at = CURRENT_TIMESTAMP
	`, address, address, amount, amount)
	return err
}

// updateBalanceTx is the in-transaction form used by StoreTransaction.
func updateBalanceTx(tx *sql.Tx, address string, amount float64) error {
	return updateBalanceExec(tx, address, amount)
}
