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
	// NOTE: StoreTransaction parses the JSON and, when sender/recipient/
	// amount are present and amount > 0, automatically creates balance
	// rows for BOTH parties via UpdateBalance (see pkg/storage/sqlite.go
	// -- "Update balances" block). So the assertion below has to account
	// for those implicit rows; asserting balN == 1 (the original shape of
	// this test) is incorrect on the current storage semantic and is what
	// used to fail here with "balance count = 3 want 1".
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
	// Expected 3: sender "a" (-1), recipient "b" (+1) from StoreTransaction,
	// plus addr1 (42) from SetBalance.
	const wantBal = 3
	if balN != wantBal {
		t.Fatalf("balance count = %d want %d (sender=a, recipient=b, addr1)", balN, wantBal)
	}
}
