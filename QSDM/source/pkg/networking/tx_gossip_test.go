package networking

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/walletp2p"
)

func gossipDedupeReset(t *testing.T) {
	t.Helper()
	walletp2p.ResetForTest()
	t.Cleanup(walletp2p.ResetForTest)
}

func TestTxGossipIngress_AcceptsValidPayload(t *testing.T) {
	gossipDedupeReset(t)
	as := chain.NewAccountStore()
	as.Credit("alice", 100)
	txv := chain.NewTxValidator(as)
	sv := chain.NewSigVerifier()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	sv.RegisterKey("alice", pub)
	gv := chain.NewGossipValidator(sv, txv, chain.DefaultGossipValidationConfig())

	pool := mempool.New(mempool.DefaultConfig())
	rep := NewReputationTracker(DefaultReputationConfig())
	ing := NewTxGossipIngress(gv, pool, rep)

	stx := chain.NewTxSigner(priv).Sign(&mempool.Tx{
		ID: "t1", Sender: "alice", Recipient: "bob", Amount: 1, Fee: 1, Nonce: 0,
	})
	raw, _ := json.Marshal(stx)
	verdict, err := ing.HandlePeerMessage("peer-1", raw)
	if err != nil || verdict != chain.GossipAccepted {
		t.Fatalf("expected accepted, got verdict=%s err=%v", verdict, err)
	}
	if pool.Size() != 1 {
		t.Fatal("expected tx added to pool")
	}
}

func TestTxGossipIngress_AcceptedSharesIngressDedupeWithLegacyWalletPath(t *testing.T) {
	gossipDedupeReset(t)
	as := chain.NewAccountStore()
	as.Credit("alice", 100)
	txv := chain.NewTxValidator(as)
	sv := chain.NewSigVerifier()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	sv.RegisterKey("alice", pub)
	gv := chain.NewGossipValidator(sv, txv, chain.DefaultGossipValidationConfig())
	ing := NewTxGossipIngress(gv, mempool.New(mempool.DefaultConfig()), NewReputationTracker(DefaultReputationConfig()))
	stx := chain.NewTxSigner(priv).Sign(&mempool.Tx{
		ID: "gossip-dedupe-1", Sender: "alice", Recipient: "bob", Amount: 1, Fee: 1, Nonce: 0,
	})
	raw, _ := json.Marshal(stx)
	verdict, err := ing.HandlePeerMessage("peer-1", raw)
	if err != nil || verdict != chain.GossipAccepted {
		t.Fatalf("expected accepted, got verdict=%s err=%v", verdict, err)
	}
	if walletp2p.Reserve("gossip-dedupe-1") {
		t.Fatal("legacy wallet ingress Reserve should fail after gossip path ingested same tx id")
	}
}

func TestTxGossipIngress_TryConsumeGossipFalseWhenNotJSONTx(t *testing.T) {
	as := chain.NewAccountStore()
	as.Credit("alice", 100)
	ing := NewTxGossipIngress(
		chain.NewGossipValidator(chain.NewSigVerifier(), chain.NewTxValidator(as), chain.DefaultGossipValidationConfig()),
		mempool.New(mempool.DefaultConfig()),
		NewReputationTracker(DefaultReputationConfig()),
	)
	if ing.TryConsumeGossip("p", []byte("not-json")) {
		t.Fatal("non-JSON should not be consumed")
	}
}

func TestTxGossipIngress_RejectsMalformedPayload(t *testing.T) {
	as := chain.NewAccountStore()
	as.Credit("alice", 100)
	ing := NewTxGossipIngress(
		chain.NewGossipValidator(chain.NewSigVerifier(), chain.NewTxValidator(as), chain.DefaultGossipValidationConfig()),
		mempool.New(mempool.DefaultConfig()),
		NewReputationTracker(DefaultReputationConfig()),
	)

	verdict, err := ing.HandlePeerMessage("peer-1", []byte("{broken"))
	if err == nil || verdict != chain.GossipRejected {
		t.Fatal("expected malformed payload rejection")
	}
}

