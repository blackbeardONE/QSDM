package networking

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/mining"
	"github.com/blackbeardONE/QSDM/pkg/mining/enrollment"
	"github.com/blackbeardONE/QSDM/pkg/walletp2p"
	"github.com/cloudflare/circl/sign/mldsa/mldsa87"
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

func signedEnrollmentGossip(t *testing.T, id string) []byte {
	t.Helper()
	payload, err := enrollment.EncodeEnrollPayload(enrollment.EnrollPayload{
		Kind: enrollment.PayloadKindEnroll, NodeID: "gossip-rig-1",
		GPUUUID:   "GPU-12345678-1234-1234-1234-123456789abc",
		HMACKey:   []byte("0123456789abcdef0123456789abcdef"),
		StakeDust: mining.MinEnrollStakeDust,
	})
	if err != nil {
		t.Fatal(err)
	}
	pk, sk, err := mldsa87.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	pub, _ := pk.MarshalBinary()
	sum := sha256.Sum256(pub)
	env := enrollment.SignedEnvelope{
		ID: id, Sender: hex.EncodeToString(sum[:]), Nonce: 0, Fee: 0.01,
		ContractID: enrollment.SignedContractID,
		PayloadB64: base64.StdEncoding.EncodeToString(payload),
	}
	canonical, _ := env.CanonicalBytes()
	sig := make([]byte, mldsa87.SignatureSize)
	if err := mldsa87.SignTo(sk, canonical, nil, true, sig); err != nil {
		t.Fatal(err)
	}
	env.Signature = hex.EncodeToString(sig)
	env.PublicKey = hex.EncodeToString(pub)
	raw, _ := json.Marshal(env)
	return raw
}

func TestTxGossipIngress_AcceptsSignedEnrollmentEnvelope(t *testing.T) {
	gossipDedupeReset(t)
	pool := mempool.New(mempool.DefaultConfig())
	ing := NewTxGossipIngress(nil, pool, NewReputationTracker(DefaultReputationConfig()))
	raw := signedEnrollmentGossip(t, "enroll-gossip-1")
	verdict, err := ing.HandlePeerMessage("peer-enroll", raw)
	if err != nil || verdict != chain.GossipAccepted {
		t.Fatalf("expected enrollment gossip accepted, verdict=%s err=%v", verdict, err)
	}
	if pool.Size() != 1 {
		t.Fatalf("expected enrollment in pool, size=%d", pool.Size())
	}
	if walletp2p.Reserve("enroll-gossip-1") {
		t.Fatal("accepted enrollment gossip must reserve its transaction ID")
	}
}

func TestTxGossipIngress_RejectsTamperedEnrollmentEnvelope(t *testing.T) {
	pool := mempool.New(mempool.DefaultConfig())
	ing := NewTxGossipIngress(nil, pool, NewReputationTracker(DefaultReputationConfig()))
	raw := signedEnrollmentGossip(t, "enroll-gossip-tampered")
	var env enrollment.SignedEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	env.Nonce++
	raw, _ = json.Marshal(env)
	verdict, err := ing.HandlePeerMessage("peer-enroll", raw)
	if err == nil || verdict != chain.GossipRejected {
		t.Fatalf("expected tampered enrollment rejected, verdict=%s err=%v", verdict, err)
	}
	if pool.Size() != 0 {
		t.Fatal("tampered enrollment entered mempool")
	}
}
