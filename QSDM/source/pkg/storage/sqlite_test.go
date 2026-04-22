//go:build cgo
// +build cgo

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

func TestForEachStoredTransaction_roundTrip(t *testing.T) {
	dbFile := "test_foreach_tx.db"
	defer os.Remove(dbFile)

	s, err := NewStorage(dbFile)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	payload := []byte(`{"id":"fe1","sender":"alice","recipient":"bob","amount":3}`)
	if err := s.StoreTransaction(payload); err != nil {
		t.Fatal(err)
	}
	var got int
	err = s.ForEachStoredTransaction(func(raw []byte) error {
		got++
		if string(raw) != string(payload) {
			t.Errorf("plain mismatch: %q vs %q", raw, payload)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != 1 {
		t.Fatalf("ForEach count %d", got)
	}
}
