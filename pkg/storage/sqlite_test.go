package storage

import (
    "os"
    "testing"
)

func TestStorage_InitAndStoreTransaction(t *testing.T) {
    dbFile := "test_transactions.db"
    defer os.Remove(dbFile)

    storage, err := NewStorage(dbFile)
    if err != nil {
        t.Fatalf("Failed to initialize storage: %v", err)
    }
    defer storage.Close()

    tx := []byte("test transaction data")
    err = storage.StoreTransaction(tx)
    if err != nil {
        t.Fatalf("Failed to store transaction: %v", err)
    }
}
