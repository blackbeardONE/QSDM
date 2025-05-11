package storage

import (
    "bytes"
    "database/sql"
    "fmt"

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
    // Create transactions table if not exists
    createTableSQL := `CREATE TABLE IF NOT EXISTS transactions (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        data BLOB NOT NULL
    );`
    _, err = db.Exec(createTableSQL)
    if err != nil {
        return nil, err
    }
    return &Storage{db: db}, nil
}

func (s *Storage) StoreTransaction(data []byte) error {
    // Compress data using zstd
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
    fmt.Println("Stored compressed transaction data")
    return nil
}

func (s *Storage) Close() error {
    return s.db.Close()
}
