//go:build cgo
// +build cgo

package main

import (
	"path/filepath"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/storage"
)

func TestCountStoredTransactions_andBalances(t *testing.T) {
	db := filepath.Join(t.TempDir(), "stats.db")
	s, err := storage.NewStorage(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.StoreTransaction([]byte(`{"id":"s1","sender":"a","recipient":"b","amount":1}`)); err != nil {
		t.Fatal(err)
	}
	if err := s.SetBalance("addr1", 42); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s2, err := storage.NewStorage(db)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()

	txN, err := countStoredTransactions(s2)
	if err != nil {
		t.Fatal(err)
	}
	if txN != 1 {
		t.Fatalf("tx count = %d want 1", txN)
	}
	balN, err := countBalanceRows(s2)
	if err != nil {
		t.Fatal(err)
	}
	if balN != 1 {
		t.Fatalf("balance count = %d want 1", balN)
	}
}
