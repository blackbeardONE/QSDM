package networking

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	signMLDSA, alice := mldsaTxSigner(t)
	as := chain.NewAccountStore()
	as.Credit(alice, 100)
	txv := chain.NewTxValidator(as)
	sv := chain.NewSigVerifier()
	gv := chain.NewGossipValidator(sv, txv, chain.DefaultGossipValidationConfig())

	pool := mempool.New(mempool.DefaultConfig())
	rep := NewReputationTracker(DefaultReputationConfig())
	ing := NewTxGossipIngress(gv, pool, rep)

	stx := signMLDSA(&mempool.Tx{
		ID: "t1", Sender: alice, Recipient: "bob", Amount: 1, Fee: 1, Nonce: 0,
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
	signMLDSA, alice := mldsaTxSigner(t)
	as := chain.NewAccountStore()
	as.Credit(alice, 100)
	txv := chain.NewTxValidator(as)
	sv := chain.NewSigVerifier()
	gv := chain.NewGossipValidator(sv, txv, chain.DefaultGossipValidationConfig())
	ing := NewTxGossipIngress(gv, mempool.New(mempool.DefaultConfig()), NewReputationTracker(DefaultReputationConfig()))
	stx := signMLDSA(&mempool.Tx{
		ID: "gossip-dedupe-1", Sender: alice, Recipient: "bob", Amount: 1, Fee: 1, Nonce: 0,
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

// mldsaTxSigner returns an ML-DSA-87 SignedTx signer and the wallet address
// (SHA256 of the public key) it may spend from.
func mldsaTxSigner(t *testing.T) (func(*mempool.Tx) *chain.SignedTx, string) {
	t.Helper()
	pk, sk, err := mldsa87.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	pub, _ := pk.MarshalBinary()
	sum := sha256.Sum256(pub)
	return func(tx *mempool.Tx) *chain.SignedTx {
		sig := make([]byte, mldsa87.SignatureSize)
		if err := mldsa87.SignTo(sk, chain.TxSigningHash(tx), nil, true, sig); err != nil {
			t.Fatal(err)
		}
		return &chain.SignedTx{Tx: tx, Signature: sig, PublicKey: pub, Algorithm: chain.SigMLDSA}
	}, hex.EncodeToString(sum[:])
}

// The follower ingress drops a correctly signed Ed25519 gossip tx (Ed25519 is
// retired) and scores the relaying peer for an invalid tx.
func TestTxGossipIngress_RejectsEd25519SignedTx(t *testing.T) {
	gossipDedupeReset(t)
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	sum := sha256.Sum256(pub)
	sender := hex.EncodeToString(sum[:])
	as := chain.NewAccountStore()
	as.Credit(sender, 100)
	gv := chain.NewGossipValidator(chain.NewSigVerifier(), chain.NewTxValidator(as), chain.DefaultGossipValidationConfig())
	pool := mempool.New(mempool.DefaultConfig())
	rep := NewReputationTracker(DefaultReputationConfig())
	ing := NewTxGossipIngress(gv, pool, rep)

	tx := &mempool.Tx{ID: "ed-gossip-1", Sender: sender, Recipient: "bob", Amount: 1, Fee: 1, Nonce: 0}
	stx := &chain.SignedTx{Tx: tx, Signature: ed25519.Sign(priv, chain.TxSigningHash(tx)), PublicKey: pub, Algorithm: chain.SigEd25519}
	raw, _ := json.Marshal(stx)
	before := rep.GetScore("peer-ed")
	verdict, err := ing.HandlePeerMessage("peer-ed", raw)
	if verdict != chain.GossipRejected || !errors.Is(err, chain.ErrEd25519Retired) {
		t.Fatalf("expected ed25519 gossip tx rejected, verdict=%s err=%v", verdict, err)
	}
	if pool.Size() != 0 {
		t.Fatal("ed25519 gossip tx entered the mempool")
	}
	if after := rep.GetScore("peer-ed"); after >= before {
		t.Fatalf("relaying peer not scored for an invalid tx: %v -> %v", before, after)
	}
	if ing.TryConsumeGossip("peer-ed", raw) {
		t.Fatal("live ingress must not admit an ed25519 gossip tx")
	}
	if pool.Size() != 0 {
		t.Fatal("ed25519 gossip tx entered the mempool via TryConsumeGossip")
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
