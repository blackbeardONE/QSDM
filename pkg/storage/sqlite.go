package storage

import (
    "bytes"
    "database/sql"
    "fmt"
    "log"

    _ "github.com/mattn/go-sqlite3"
    "github.com/klauspost/compress/zstd"
)

type Storage struct {
    db *sql.DB
}

func NewStorage(dbPath string) (*Storage, error) {
    db, err := sql.Open("sqlite3", dbPath)
    if err != nil {
        return nil, err
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
        data BLOB NOT NULL
    );`
    _, err = db.Exec(createTableSQL)
    if err != nil {
        db.Close()
        return nil, err
    }
    log.Println("SQLite storage initialized with WAL mode and performance pragmas")
    return &Storage{db: db}, nil
}

func (s *Storage) StoreTransaction(data []byte) error {
    // Compress data using zstd for efficient storage
    var b bytes.Buffer
    encoder, err := zstd.NewWriter(&b)
    if err != nil {
        return err
    }
    _, err = encoder.Write(data)
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
    log.Println("Stored compressed transaction data")
    return nil
}

func (s *Storage) Close() error {
    return s.db.Close()
}
