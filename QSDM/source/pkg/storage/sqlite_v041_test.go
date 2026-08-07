//go:build cgo
// +build cgo

package storage

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
)

// The v0.4.1 replay-protection + atomic-debit SQL in sqlite_v041.go had no
// test file at all, despite being the load-bearing path for
// POST /api/v1/wallet/submit-signed. These tests run against a real SQLite
// database (no mocks) so the schema CHECK constraints, the nonce CAS, and
// the transaction rollback semantics are all genuinely exercised.

func newTestStorage(t *testing.T) *Storage {
	t.Helper()
	db := filepath.Join(t.TempDir(), "v041.db")
	s, err := NewStorage(db)
	if err != nil {
		t.Fatalf("NewStorage: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// fund gives an address a starting balance by transferring into it from a
// pre-seeded source, using the legacy nonce-0 path so no nonce is consumed.
func fund(t *testing.T, s *Storage, addr string, amount float64) {
	t.Helper()
	if err := s.UpdateBalance(addr, amount); err != nil {
		t.Fatalf("seed balance for %s: %v", addr, err)
	}
}

func rawEnvelope(t *testing.T, txID string) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]string{"id": txID})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestApplyTransferAtomic_happyPathDebitsCreditsAndBumpsNonce(t *testing.T) {
	s := newTestStorage(t)
	fund(t, s, "alice", 100)

	err := s.ApplyTransferAtomic(context.Background(),
		"alice", "bob", 10, 1, 1, "tx-1", rawEnvelope(t, "tx-1"))
	if err != nil {
		t.Fatalf("ApplyTransferAtomic: %v", err)
	}

	aliceBal, err := s.GetBalance("alice")
	if err != nil {
		t.Fatal(err)
	}
	if aliceBal != 89 { // 100 - (10 amount + 1 fee)
		t.Fatalf("sender balance: want 89, got %v", aliceBal)
	}

	bobBal, err := s.GetBalance("bob")
	if err != nil {
		t.Fatal(err)
	}
	if bobBal != 10 { // recipient receives amount only; fee is not credited
		t.Fatalf("recipient balance: want 10, got %v", bobBal)
	}

	nonce, err := s.GetNonce("alice")
	if err != nil {
		t.Fatal(err)
	}
	if nonce != 1 {
		t.Fatalf("sender nonce: want 1, got %d", nonce)
	}
}

// The core replay-protection property: re-submitting the same envelope must
// not move funds twice.
func TestApplyTransferAtomic_replayIsRejected(t *testing.T) {
	s := newTestStorage(t)
	fund(t, s, "alice", 100)
	ctx := context.Background()

	if err := s.ApplyTransferAtomic(ctx, "alice", "bob", 10, 0, 1, "tx-1", rawEnvelope(t, "tx-1")); err != nil {
		t.Fatalf("first transfer: %v", err)
	}

	// Exact replay — same tx_id.
	err := s.ApplyTransferAtomic(ctx, "alice", "bob", 10, 0, 1, "tx-1", rawEnvelope(t, "tx-1"))
	if !errors.Is(err, ErrTxAlreadyExists) {
		t.Fatalf("replayed tx_id must return ErrTxAlreadyExists, got %v", err)
	}

	// Fresh tx_id but a stale nonce — the same spend re-signed.
	err = s.ApplyTransferAtomic(ctx, "alice", "bob", 10, 0, 1, "tx-2", rawEnvelope(t, "tx-2"))
	if !errors.Is(err, ErrNonceConflict) {
		t.Fatalf("stale nonce must return ErrNonceConflict, got %v", err)
	}

	bal, err := s.GetBalance("alice")
	if err != nil {
		t.Fatal(err)
	}
	if bal != 90 {
		t.Fatalf("replays must not move funds: want 90, got %v", bal)
	}
}

// v0.4.1 requires strict serialisation: nonce must be exactly last+1.
// Skipping ahead is refused even though it is monotonically increasing.
func TestApplyTransferAtomic_nonceMustBeExactlyNextNotMerelyGreater(t *testing.T) {
	s := newTestStorage(t)
	fund(t, s, "alice", 100)

	err := s.ApplyTransferAtomic(context.Background(),
		"alice", "bob", 5, 0, 7, "tx-skip", rawEnvelope(t, "tx-skip"))
	if !errors.Is(err, ErrNonceConflict) {
		t.Fatalf("a gapped nonce must be refused (strict mode), got %v", err)
	}
}

func TestApplyTransferAtomic_insufficientBalanceIsAtomic(t *testing.T) {
	s := newTestStorage(t)
	fund(t, s, "alice", 5)

	err := s.ApplyTransferAtomic(context.Background(),
		"alice", "bob", 10, 0, 1, "tx-broke", rawEnvelope(t, "tx-broke"))
	if !errors.Is(err, ErrInsufficientBalance) {
		t.Fatalf("want ErrInsufficientBalance, got %v", err)
	}

	// Nothing may have changed: no debit, no credit, no nonce bump.
	if bal, _ := s.GetBalance("alice"); bal != 5 {
		t.Fatalf("failed transfer must not debit sender: got %v", bal)
	}
	if bal, _ := s.GetBalance("bob"); bal != 0 {
		t.Fatalf("failed transfer must not credit recipient: got %v", bal)
	}
	if n, _ := s.GetNonce("alice"); n != 0 {
		t.Fatalf("failed transfer must not bump nonce: got %d", n)
	}
}

// The fee is debited from the sender in addition to the amount, so a balance
// that covers only the amount is insufficient.
func TestApplyTransferAtomic_feeCountsAgainstBalance(t *testing.T) {
	s := newTestStorage(t)
	fund(t, s, "alice", 10)

	err := s.ApplyTransferAtomic(context.Background(),
		"alice", "bob", 10, 1, 1, "tx-fee", rawEnvelope(t, "tx-fee"))
	if !errors.Is(err, ErrInsufficientBalance) {
		t.Fatalf("amount+fee must exceed balance, got %v", err)
	}
}

func TestApplyTransferAtomic_rejectsNegativeAmountOrFee(t *testing.T) {
	s := newTestStorage(t)
	fund(t, s, "alice", 100)
	ctx := context.Background()

	if err := s.ApplyTransferAtomic(ctx, "alice", "bob", -1, 0, 1, "neg-a", rawEnvelope(t, "neg-a")); err == nil {
		t.Fatal("negative amount must be refused")
	}
	if err := s.ApplyTransferAtomic(ctx, "alice", "bob", 1, -1, 1, "neg-f", rawEnvelope(t, "neg-f")); err == nil {
		t.Fatal("negative fee must be refused")
	}
}

// Nonce 0 is the legacy v0.4.0 path: no nonce check and no nonce bump.
func TestApplyTransferAtomic_legacyZeroNonceDoesNotBump(t *testing.T) {
	s := newTestStorage(t)
	fund(t, s, "alice", 100)
	ctx := context.Background()

	if err := s.ApplyTransferAtomic(ctx, "alice", "bob", 1, 0, 0, "legacy-1", rawEnvelope(t, "legacy-1")); err != nil {
		t.Fatalf("legacy path: %v", err)
	}
	if n, _ := s.GetNonce("alice"); n != 0 {
		t.Fatalf("legacy nonce-0 path must not bump the nonce, got %d", n)
	}
	// A second legacy transfer with a distinct tx_id still works.
	if err := s.ApplyTransferAtomic(ctx, "alice", "bob", 1, 0, 0, "legacy-2", rawEnvelope(t, "legacy-2")); err != nil {
		t.Fatalf("second legacy transfer: %v", err)
	}
}

func TestGetNonce_unknownAddressIsZero(t *testing.T) {
	s := newTestStorage(t)
	n, err := s.GetNonce("nobody")
	if err != nil {
		t.Fatalf("GetNonce for unknown address should not error: %v", err)
	}
	if n != 0 {
		t.Fatalf("unknown address nonce: want 0, got %d", n)
	}
}

// Concurrent spends from one account must not double-spend: exactly one of N
// racing transfers at the same nonce may win. This is what the in-transaction
// CAS on (balance, nonce) exists to guarantee.
func TestApplyTransferAtomic_concurrentSpendsDoNotDoubleSpend(t *testing.T) {
	s := newTestStorage(t)
	fund(t, s, "alice", 100)

	const racers = 8
	var wg sync.WaitGroup
	results := make([]error, racers)
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			txID := "race-" + string(rune('a'+i))
			results[i] = s.ApplyTransferAtomic(context.Background(),
				"alice", "bob", 10, 0, 1, txID, rawEnvelope(t, txID))
		}(i)
	}
	wg.Wait()

	wins := 0
	for _, err := range results {
		if err == nil {
			wins++
		}
	}
	if wins != 1 {
		t.Fatalf("exactly one racer may spend nonce 1, got %d winners (results=%v)", wins, results)
	}

	if bal, _ := s.GetBalance("alice"); bal != 90 {
		t.Fatalf("only one debit may land: want 90, got %v", bal)
	}
	if n, _ := s.GetNonce("alice"); n != 1 {
		t.Fatalf("nonce must advance exactly once: want 1, got %d", n)
	}
}
