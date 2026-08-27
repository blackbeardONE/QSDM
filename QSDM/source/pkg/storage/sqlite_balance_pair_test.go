//go:build cgo
// +build cgo

package storage

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

// A debit that fails must not leave the credit applied.
//
// The v0.4.1 migration puts `CHECK(balance >= 0)` on balances (sqlite_v041.go),
// so debiting an unfunded sender fails at the database. StoreTransaction used to
// call UpdateBalance twice as independent statements, log
// `Warning: failed to update sender balance` and carry on -- so the recipient
// was credited with no matching debit. That mints CELL in the balances table
// out of nothing, and the API reports success.
//
// Recording the transaction is still allowed: this path also ingests gossip and
// replay for senders this node never funded, and refusing to store those would
// be a worse bug. The invariant is only that the two balance sides move
// together.
func TestStoreTransaction_debitFailureDoesNotLeaveCreditApplied(t *testing.T) {
	s, err := NewStorage(filepath.Join(t.TempDir(), "pair.db"))
	if err != nil {
		t.Fatalf("NewStorage: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	// Sender has no balance row, so the debit violates CHECK(balance >= 0).
	payload, err := json.Marshal(map[string]interface{}{
		"id": "tx-unfunded-1", "sender": "unfunded-sender",
		"recipient": "lucky-recipient", "amount": 500.0,
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := s.StoreTransaction(payload); err != nil {
		t.Fatalf("the transaction row must still be stored for ingest paths: %v", err)
	}

	got, err := s.GetBalance("lucky-recipient")
	if err != nil {
		t.Fatalf("GetBalance: %v", err)
	}
	if got != 0 {
		t.Fatalf("recipient credited %.2f with no matching debit -- the balance pair was "+
			"not atomic, so this mints CELL from nothing", got)
	}

	sender, err := s.GetBalance("unfunded-sender")
	if err != nil {
		t.Fatalf("GetBalance sender: %v", err)
	}
	if sender != 0 {
		t.Fatalf("sender balance moved to %.2f despite the debit failing", sender)
	}
}

// The honest path must still move both sides, or the guard above is satisfied by
// an implementation that never applies balances at all.
func TestStoreTransaction_fundedSenderMovesBothSides(t *testing.T) {
	s, err := NewStorage(filepath.Join(t.TempDir(), "pair2.db"))
	if err != nil {
		t.Fatalf("NewStorage: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	if err := s.UpdateBalance("funded-sender", 1000); err != nil {
		t.Fatalf("seed sender: %v", err)
	}

	payload, err := json.Marshal(map[string]interface{}{
		"id": "tx-funded-1", "sender": "funded-sender",
		"recipient": "payee", "amount": 400.0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.StoreTransaction(payload); err != nil {
		t.Fatalf("StoreTransaction: %v", err)
	}

	if got, _ := s.GetBalance("funded-sender"); got != 600 {
		t.Errorf("sender should be debited to 600, got %.2f", got)
	}
	if got, _ := s.GetBalance("payee"); got != 400 {
		t.Errorf("recipient should be credited 400, got %.2f", got)
	}
}
