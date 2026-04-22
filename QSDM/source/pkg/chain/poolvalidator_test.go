package chain

import (
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

func TestTxValidator_ValidTx(t *testing.T) {
	as := NewAccountStore()
	as.Credit("alice", 1000)
	tv := NewTxValidator(as)

	tx := &mempool.Tx{ID: "tx1", Sender: "alice", Recipient: "bob", Amount: 10, Fee: 1, Nonce: 0}
	if err := tv.Validate(tx); err != nil {
		t.Fatalf("expected valid: %v", err)
	}
}

func TestTxValidator_SenderNotFound(t *testing.T) {
	as := NewAccountStore()
	tv := NewTxValidator(as)

	tx := &mempool.Tx{ID: "tx1", Sender: "ghost", Recipient: "bob", Amount: 10, Fee: 1, Nonce: 0}
	if err := tv.Validate(tx); err == nil {
		t.Fatal("expected error for non-existent sender")
	}
}

func TestTxValidator_NonceMismatch(t *testing.T) {
	as := NewAccountStore()
	as.Credit("alice", 1000)
	tv := NewTxValidator(as)

	tx := &mempool.Tx{ID: "tx1", Sender: "alice", Recipient: "bob", Amount: 10, Fee: 1, Nonce: 5}
	if err := tv.Validate(tx); err == nil {
		t.Fatal("expected nonce mismatch error")
	}
}

func TestTxValidator_InsufficientBalance(t *testing.T) {
	as := NewAccountStore()
	as.Credit("alice", 5)
	tv := NewTxValidator(as)

	tx := &mempool.Tx{ID: "tx1", Sender: "alice", Recipient: "bob", Amount: 10, Fee: 1, Nonce: 0}
	if err := tv.Validate(tx); err == nil {
		t.Fatal("expected insufficient balance error")
	}
}

func TestTxValidator_PendingNonceTracking(t *testing.T) {
	as := NewAccountStore()
	as.Credit("alice", 1000)
	tv := NewTxValidator(as)

	tx1 := &mempool.Tx{ID: "tx1", Sender: "alice", Recipient: "bob", Amount: 10, Fee: 1, Nonce: 0}
	tv.Validate(tx1)
	tv.Accept(tx1)

	// Next tx should need nonce 1
	tx2 := &mempool.Tx{ID: "tx2", Sender: "alice", Recipient: "bob", Amount: 10, Fee: 1, Nonce: 1}
	if err := tv.Validate(tx2); err != nil {
		t.Fatalf("expected valid with nonce 1: %v", err)
	}

	// Nonce 0 should be rejected (already used)
	tx3 := &mempool.Tx{ID: "tx3", Sender: "alice", Recipient: "bob", Amount: 10, Fee: 1, Nonce: 0}
	if err := tv.Validate(tx3); err == nil {
		t.Fatal("expected nonce rejection for already-used nonce")
	}
}

func TestTxValidator_ReservedBalance(t *testing.T) {
	as := NewAccountStore()
	as.Credit("alice", 100)
	tv := NewTxValidator(as)

	tx1 := &mempool.Tx{ID: "tx1", Sender: "alice", Recipient: "bob", Amount: 40, Fee: 5, Nonce: 0}
	tv.Validate(tx1)
	tv.Accept(tx1)

	// Now 45 is reserved; balance is 100; available is 55
	tx2 := &mempool.Tx{ID: "tx2", Sender: "alice", Recipient: "bob", Amount: 50, Fee: 5, Nonce: 1}
	if err := tv.Validate(tx2); err != nil {
		t.Fatalf("expected valid (55 available): %v", err)
	}

	// But 60+fee would exceed available
	tx3 := &mempool.Tx{ID: "tx3", Sender: "alice", Recipient: "bob", Amount: 60, Fee: 1, Nonce: 1}
	if err := tv.Validate(tx3); err == nil {
		t.Fatal("expected insufficient available balance")
	}
}

func TestTxValidator_ValidateAndAdd(t *testing.T) {
	as := NewAccountStore()
	as.Credit("alice", 1000)
	tv := NewTxValidator(as)
	pool := mempool.New(mempool.DefaultConfig())

	tx := &mempool.Tx{ID: "tx1", Sender: "alice", Recipient: "bob", Amount: 10, Fee: 1, Nonce: 0}
	if err := tv.ValidateAndAdd(pool, tx); err != nil {
		t.Fatalf("ValidateAndAdd: %v", err)
	}

	if pool.Size() != 1 {
		t.Fatal("expected 1 tx in pool")
	}
	if tv.PendingNonce("alice") != 1 {
		t.Fatal("expected pending nonce 1")
	}
}

func TestTxValidator_Reset(t *testing.T) {
	as := NewAccountStore()
	as.Credit("alice", 1000)
	tv := NewTxValidator(as)

	tx := &mempool.Tx{ID: "tx1", Sender: "alice", Recipient: "bob", Amount: 10, Fee: 1, Nonce: 0}
	tv.Accept(tx)

	tv.Reset()
	if tv.PendingNonce("alice") != 0 {
		t.Fatal("expected nonce 0 after reset")
	}
	if tv.ReservedBalance("alice") != 0 {
		t.Fatal("expected 0 reserved after reset")
	}
}

func TestTxValidator_EmptySender(t *testing.T) {
	tv := NewTxValidator(NewAccountStore())
	tx := &mempool.Tx{ID: "tx1", Sender: "", Recipient: "bob", Amount: 10}
	if err := tv.Validate(tx); err == nil {
		t.Fatal("expected error for empty sender")
	}
}

func TestTxValidator_NegativeAmount(t *testing.T) {
	as := NewAccountStore()
	as.Credit("alice", 1000)
	tv := NewTxValidator(as)

	tx := &mempool.Tx{ID: "tx1", Sender: "alice", Recipient: "bob", Amount: -5, Fee: 1, Nonce: 0}
	if err := tv.Validate(tx); err == nil {
		t.Fatal("expected error for negative amount")
	}
}

func TestTxValidator_Remove(t *testing.T) {
	as := NewAccountStore()
	as.Credit("alice", 1000)
	tv := NewTxValidator(as)

	tx := &mempool.Tx{ID: "tx1", Sender: "alice", Recipient: "bob", Amount: 100, Fee: 5, Nonce: 0}
	tv.Accept(tx)

	if tv.ReservedBalance("alice") != 105 {
		t.Fatal("expected 105 reserved")
	}

	tv.Remove(tx)
	if tv.ReservedBalance("alice") != 0 {
		t.Fatalf("expected 0 reserved after remove, got %f", tv.ReservedBalance("alice"))
	}
}
