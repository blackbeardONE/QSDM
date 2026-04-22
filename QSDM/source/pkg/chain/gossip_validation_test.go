package chain

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

func TestGossipValidator_AcceptsValidTx(t *testing.T) {
	as := NewAccountStore()
	as.Credit("alice", 100)
	txv := NewTxValidator(as)
	sv := NewSigVerifier()
	pool := mempool.New(mempool.DefaultConfig())
	gv := NewGossipValidator(sv, txv, DefaultGossipValidationConfig())

	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	sv.RegisterKey("alice", pub)
	stx := NewTxSigner(priv).Sign(&mempool.Tx{
		ID: "tx1", Sender: "alice", Recipient: "bob", Amount: 1, Fee: 0.1, Nonce: 0,
	})

	verdict, err := gv.HandleIncoming(pool, stx)
	if err != nil {
		t.Fatalf("expected accepted tx: %v", err)
	}
	if verdict != GossipAccepted {
		t.Fatalf("expected accepted verdict, got %s", verdict)
	}
	if pool.Size() != 1 {
		t.Fatalf("expected pool size 1, got %d", pool.Size())
	}
}

func TestGossipValidator_RejectsBadSignature(t *testing.T) {
	as := NewAccountStore()
	as.Credit("alice", 100)
	txv := NewTxValidator(as)
	sv := NewSigVerifier()
	pool := mempool.New(mempool.DefaultConfig())
	gv := NewGossipValidator(sv, txv, DefaultGossipValidationConfig())

	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	stx := NewTxSigner(priv).Sign(&mempool.Tx{
		ID: "tx1", Sender: "alice", Recipient: "bob", Amount: 1, Fee: 0.1, Nonce: 0,
	})
	stx.Signature = []byte("bad")

	verdict, err := gv.HandleIncoming(pool, stx)
	if err == nil || verdict != GossipRejected {
		t.Fatal("expected rejected for bad signature")
	}
}

func TestGossipValidator_RejectsLowFee(t *testing.T) {
	as := NewAccountStore()
	as.Credit("alice", 100)
	txv := NewTxValidator(as)
	sv := NewSigVerifier()
	cfg := DefaultGossipValidationConfig()
	cfg.MinFee = 1
	gv := NewGossipValidator(sv, txv, cfg)
	pool := mempool.New(mempool.DefaultConfig())

	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	sv.RegisterKey("alice", pub)
	stx := NewTxSigner(priv).Sign(&mempool.Tx{
		ID: "tx1", Sender: "alice", Recipient: "bob", Amount: 1, Fee: 0.1, Nonce: 0,
	})

	verdict, err := gv.HandleIncoming(pool, stx)
	if err == nil || verdict != GossipRejected {
		t.Fatal("expected low-fee reject")
	}
}

func TestGossipValidator_QuarantinesFutureNonce(t *testing.T) {
	as := NewAccountStore()
	as.Credit("alice", 100)
	txv := NewTxValidator(as)
	sv := NewSigVerifier()
	cfg := DefaultGossipValidationConfig()
	cfg.MaxFutureNonce = 4
	gv := NewGossipValidator(sv, txv, cfg)
	pool := mempool.New(mempool.DefaultConfig())

	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	sv.RegisterKey("alice", pub)
	stx := NewTxSigner(priv).Sign(&mempool.Tx{
		ID: "tx-future", Sender: "alice", Recipient: "bob", Amount: 1, Fee: 1, Nonce: 2,
	})

	verdict, err := gv.HandleIncoming(pool, stx)
	if err != nil {
		t.Fatalf("expected quarantine without error: %v", err)
	}
	if verdict != GossipQuarantined {
		t.Fatalf("expected quarantined verdict, got %s", verdict)
	}
	if gv.QuarantineSize() != 1 {
		t.Fatalf("expected quarantine size 1, got %d", gv.QuarantineSize())
	}
}

func TestGossipValidator_RejectsFarFutureNonce(t *testing.T) {
	as := NewAccountStore()
	as.Credit("alice", 100)
	txv := NewTxValidator(as)
	sv := NewSigVerifier()
	cfg := DefaultGossipValidationConfig()
	cfg.MaxFutureNonce = 1
	gv := NewGossipValidator(sv, txv, cfg)
	pool := mempool.New(mempool.DefaultConfig())

	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	sv.RegisterKey("alice", pub)
	stx := NewTxSigner(priv).Sign(&mempool.Tx{
		ID: "tx-future", Sender: "alice", Recipient: "bob", Amount: 1, Fee: 1, Nonce: 5,
	})

	verdict, err := gv.HandleIncoming(pool, stx)
	if err == nil || verdict != GossipRejected {
		t.Fatal("expected far-future nonce rejection")
	}
}

func TestGossipValidator_PurgeExpired(t *testing.T) {
	as := NewAccountStore()
	as.Credit("alice", 100)
	txv := NewTxValidator(as)
	gv := NewGossipValidator(NewSigVerifier(), txv, DefaultGossipValidationConfig())

	gv.mu.Lock()
	gv.quarantine["x"] = QuarantineEntry{
		TxID: "x", Sender: "alice", Reason: "future nonce",
		ReceivedAt: time.Now().Add(-2 * time.Hour),
		RetryAfter: time.Now().Add(-time.Minute),
	}
	gv.mu.Unlock()

	removed := gv.PurgeExpired()
	if removed != 1 {
		t.Fatalf("expected 1 removed, got %d", removed)
	}
	if gv.QuarantineSize() != 0 {
		t.Fatal("expected quarantine empty")
	}
}

