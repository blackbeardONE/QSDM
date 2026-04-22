//go:build cgo
// +build cgo

package storage

import (
	"os"
	"testing"
)

func BenchmarkStoreTransaction(b *testing.B) {
	tmpDB, err := os.CreateTemp("", "bench_*.db")
	if err != nil {
		b.Fatalf("Failed to create temp DB: %v", err)
	}
	tmpDB.Close()
	defer os.Remove(tmpDB.Name())

	store, err := NewStorage(tmpDB.Name())
	if err != nil {
		b.Fatalf("Failed to initialize storage: %v", err)
	}
	defer store.Close()

	txData := []byte(`{"id":"tx1","sender":"sender123","recipient":"recipient456","amount":100,"fee":0.001,"geotag":"US","parent_cells":["p1","p2"],"signature":"sig123","timestamp":"2025-01-01T00:00:00Z"}`)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		err := store.StoreTransaction(txData)
		if err != nil {
			b.Fatalf("Failed to store transaction: %v", err)
		}
	}
}

func BenchmarkGetBalance(b *testing.B) {
	tmpDB, err := os.CreateTemp("", "bench_*.db")
	if err != nil {
		b.Fatalf("Failed to create temp DB: %v", err)
	}
	tmpDB.Close()
	defer os.Remove(tmpDB.Name())

	store, err := NewStorage(tmpDB.Name())
	if err != nil {
		b.Fatalf("Failed to initialize storage: %v", err)
	}
	defer store.Close()

	address := "test_address_123"
	store.SetBalance(address, 1000)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := store.GetBalance(address)
		if err != nil {
			b.Fatalf("Failed to get balance: %v", err)
		}
	}
}

func BenchmarkUpdateBalance(b *testing.B) {
	tmpDB, err := os.CreateTemp("", "bench_*.db")
	if err != nil {
		b.Fatalf("Failed to create temp DB: %v", err)
	}
	tmpDB.Close()
	defer os.Remove(tmpDB.Name())

	store, err := NewStorage(tmpDB.Name())
	if err != nil {
		b.Fatalf("Failed to initialize storage: %v", err)
	}
	defer store.Close()

	address := "test_address_123"
	store.SetBalance(address, 1000)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		err := store.UpdateBalance(address, 10)
		if err != nil {
			b.Fatalf("Failed to update balance: %v", err)
		}
	}
}

