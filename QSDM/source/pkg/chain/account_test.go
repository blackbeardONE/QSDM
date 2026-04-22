package chain

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

func TestAccountStore_Debit(t *testing.T) {
	as := NewAccountStore()
	as.Credit("alice", 200)
	if err := as.Debit("alice", 50); err != nil {
		t.Fatal(err)
	}
	acc, _ := as.Get("alice")
	if acc.Balance != 150 {
		t.Fatalf("balance %v", acc.Balance)
	}
	if err := as.Debit("alice", 999); err == nil {
		t.Fatal("expected insufficient balance")
	}
}

func TestAccountStore_CreditAndGet(t *testing.T) {
	as := NewAccountStore()
	as.Credit("alice", 1000)

	acc, ok := as.Get("alice")
	if !ok {
		t.Fatal("expected alice")
	}
	if acc.Balance != 1000 {
		t.Fatalf("expected 1000, got %f", acc.Balance)
	}
	if acc.Nonce != 0 {
		t.Fatalf("expected nonce 0, got %d", acc.Nonce)
	}
}

func TestAccountStore_ApplyTx(t *testing.T) {
	as := NewAccountStore()
	as.Credit("alice", 100)

	tx := &mempool.Tx{ID: "tx1", Sender: "alice", Recipient: "bob", Amount: 30, Fee: 1, Nonce: 0}
	if err := as.ApplyTx(tx); err != nil {
		t.Fatalf("ApplyTx: %v", err)
	}

	alice, _ := as.Get("alice")
	if alice.Balance != 69 {
		t.Fatalf("expected 69, got %f", alice.Balance)
	}
	if alice.Nonce != 1 {
		t.Fatalf("expected nonce 1, got %d", alice.Nonce)
	}

	bob, _ := as.Get("bob")
	if bob.Balance != 30 {
		t.Fatalf("expected 30, got %f", bob.Balance)
	}
}

func TestAccountStore_NonceMismatch(t *testing.T) {
	as := NewAccountStore()
	as.Credit("alice", 100)

	tx := &mempool.Tx{ID: "tx1", Sender: "alice", Recipient: "bob", Amount: 10, Fee: 1, Nonce: 5}
	err := as.ApplyTx(tx)
	if err == nil {
		t.Fatal("expected nonce mismatch error")
	}
}

func TestAccountStore_InsufficientBalance(t *testing.T) {
	as := NewAccountStore()
	as.Credit("alice", 10)

	tx := &mempool.Tx{ID: "tx1", Sender: "alice", Recipient: "bob", Amount: 50, Fee: 1, Nonce: 0}
	err := as.ApplyTx(tx)
	if err == nil {
		t.Fatal("expected insufficient balance error")
	}
}

func TestAccountStore_ReplayProtection(t *testing.T) {
	as := NewAccountStore()
	as.Credit("alice", 1000)

	tx1 := &mempool.Tx{ID: "tx1", Sender: "alice", Recipient: "bob", Amount: 10, Fee: 1, Nonce: 0}
	as.ApplyTx(tx1)

	// Replay same nonce
	tx2 := &mempool.Tx{ID: "tx2", Sender: "alice", Recipient: "bob", Amount: 10, Fee: 1, Nonce: 0}
	err := as.ApplyTx(tx2)
	if err == nil {
		t.Fatal("expected replay rejection (nonce already used)")
	}
}

func TestAccountStore_SequentialNonces(t *testing.T) {
	as := NewAccountStore()
	as.Credit("alice", 1000)

	for i := uint64(0); i < 5; i++ {
		tx := &mempool.Tx{ID: "tx", Sender: "alice", Recipient: "bob", Amount: 1, Fee: 0.1, Nonce: i}
		if err := as.ApplyTx(tx); err != nil {
			t.Fatalf("ApplyTx nonce %d: %v", i, err)
		}
	}

	alice, _ := as.Get("alice")
	if alice.Nonce != 5 {
		t.Fatalf("expected nonce 5, got %d", alice.Nonce)
	}
}

func TestAccountStore_RestoreFrom(t *testing.T) {
	as := NewAccountStore()
	as.Credit("alice", 100)
	snap := as.Clone()
	if err := as.Debit("alice", 30); err != nil {
		t.Fatal(err)
	}
	as.RestoreFrom(snap)
	a, _ := as.Get("alice")
	if a.Balance != 100 {
		t.Fatalf("expected balance 100 after restore, got %v", a.Balance)
	}
}

func TestAccountStore_CloneIndependent(t *testing.T) {
	as := NewAccountStore()
	as.Credit("alice", 50)
	as.Credit("bob", 10)
	cl := as.Clone()
	cl.Credit("alice", 5)
	a, _ := as.Get("alice")
	if a.Balance != 50 {
		t.Fatalf("original mutated")
	}
	c, _ := cl.Get("alice")
	if c.Balance != 55 {
		t.Fatalf("clone wrong balance")
	}
	if as.StateRoot() == cl.StateRoot() {
		t.Fatal("state roots should differ after clone-only mutation")
	}
}

func TestAccountStore_ChainReplayApplier(t *testing.T) {
	as := NewAccountStore()
	as.Credit("alice", 40)
	var ra ChainReplayApplier = as
	snap := ra.ChainReplayClone().(*AccountStore)
	snap.Credit("alice", 10)
	if a, _ := as.Get("alice"); a.Balance != 40 {
		t.Fatalf("live should stay 40, got %v", a.Balance)
	}
	if err := as.RestoreFromChainReplay(snap); err != nil {
		t.Fatal(err)
	}
	if a, _ := as.Get("alice"); a.Balance != 50 {
		t.Fatalf("after restore expected 50, got %v", a.Balance)
	}
}

func TestAccountStore_StateRoot(t *testing.T) {
	as := NewAccountStore()
	as.Credit("alice", 100)
	as.Credit("bob", 50)

	root1 := as.StateRoot()
	if root1 == "" {
		t.Fatal("expected non-empty root")
	}

	// Same state = same root
	as2 := NewAccountStore()
	as2.Credit("alice", 100)
	as2.Credit("bob", 50)
	root2 := as2.StateRoot()
	if root1 != root2 {
		t.Fatal("same state should produce same root")
	}

	// Different state = different root
	as2.Credit("bob", 1)
	root3 := as2.StateRoot()
	if root1 == root3 {
		t.Fatal("different state should produce different root")
	}
}

func TestAccountStore_GetOrCreate(t *testing.T) {
	as := NewAccountStore()
	acc := as.GetOrCreate("new_addr")
	if acc.Address != "new_addr" || acc.Balance != 0 || acc.Nonce != 0 {
		t.Fatal("GetOrCreate should return zero-value account")
	}
	if as.Count() != 1 {
		t.Fatal("expected 1 account after GetOrCreate")
	}
}

func TestAccountStore_AllAccounts(t *testing.T) {
	as := NewAccountStore()
	as.Credit("charlie", 300)
	as.Credit("alice", 100)
	as.Credit("bob", 200)

	all := as.AllAccounts()
	if len(all) != 3 {
		t.Fatalf("expected 3 accounts, got %d", len(all))
	}
	if all[0].Address != "alice" {
		t.Fatal("expected sorted by address, alice first")
	}
}

func TestAccountStore_SaveAndLoad(t *testing.T) {
	dir := filepath.Join(os.TempDir(), "qsdm_account_test")
	os.MkdirAll(dir, 0755)
	defer os.RemoveAll(dir)

	as := NewAccountStore()
	as.Credit("alice", 100)
	as.Credit("bob", 200)
	tx := &mempool.Tx{ID: "tx1", Sender: "alice", Recipient: "bob", Amount: 10, Fee: 1, Nonce: 0}
	as.ApplyTx(tx)

	path := filepath.Join(dir, "accounts.json")
	as.Save(path)

	as2 := NewAccountStore()
	loaded, err := as2.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded != 2 {
		t.Fatalf("expected 2 loaded, got %d", loaded)
	}

	alice, _ := as2.Get("alice")
	if alice.Nonce != 1 {
		t.Fatalf("expected nonce 1 after load, got %d", alice.Nonce)
	}
	if alice.Balance != 89 {
		t.Fatalf("expected balance 89, got %f", alice.Balance)
	}
}

func TestAccountStore_SenderNotFound(t *testing.T) {
	as := NewAccountStore()
	tx := &mempool.Tx{ID: "tx1", Sender: "ghost", Recipient: "bob", Amount: 10, Fee: 1, Nonce: 0}
	err := as.ApplyTx(tx)
	if err == nil {
		t.Fatal("expected error for non-existent sender")
	}
}

func TestAccountStore_ImplementsStateApplier(t *testing.T) {
	var _ StateApplier = NewAccountStore()
}
