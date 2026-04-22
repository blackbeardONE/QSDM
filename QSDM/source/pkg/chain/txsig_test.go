package chain

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

func makeTestKeypair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func TestTxSigner_SignAndVerify(t *testing.T) {
	_, priv := makeTestKeypair(t)
	signer := NewTxSigner(priv)
	verifier := NewSigVerifier()

	tx := &mempool.Tx{ID: "tx1", Sender: "alice", Recipient: "bob", Amount: 10, Fee: 1, Nonce: 0}
	signed := signer.Sign(tx)

	if err := verifier.Verify(signed); err != nil {
		t.Fatalf("valid signature should verify: %v", err)
	}
}

func TestTxSigner_TamperedTxFails(t *testing.T) {
	_, priv := makeTestKeypair(t)
	signer := NewTxSigner(priv)
	verifier := NewSigVerifier()

	tx := &mempool.Tx{ID: "tx1", Sender: "alice", Recipient: "bob", Amount: 10, Fee: 1, Nonce: 0}
	signed := signer.Sign(tx)

	// Tamper with amount
	signed.Tx.Amount = 99999
	if err := verifier.Verify(signed); err == nil {
		t.Fatal("tampered tx should fail verification")
	}
}

func TestSigVerifier_WrongKeyFails(t *testing.T) {
	_, priv1 := makeTestKeypair(t)
	_, priv2 := makeTestKeypair(t)

	signer1 := NewTxSigner(priv1)
	verifier := NewSigVerifier()

	tx := &mempool.Tx{ID: "tx1", Sender: "alice", Recipient: "bob", Amount: 10, Fee: 1, Nonce: 0}
	signed := signer1.Sign(tx)

	// Replace public key with different one
	signed.PublicKey = priv2.Public().(ed25519.PublicKey)
	if err := verifier.Verify(signed); err == nil {
		t.Fatal("wrong public key should fail verification")
	}
}

func TestSigVerifier_RegisteredKeyMismatch(t *testing.T) {
	pub1, priv1 := makeTestKeypair(t)
	pub2, _ := makeTestKeypair(t)

	signer := NewTxSigner(priv1)
	verifier := NewSigVerifier()
	verifier.RegisterKey("alice", pub2) // register different key

	tx := &mempool.Tx{ID: "tx1", Sender: "alice", Recipient: "bob", Amount: 10, Fee: 1, Nonce: 0}
	signed := signer.Sign(tx)

	if err := verifier.Verify(signed); err == nil {
		t.Fatal("registered key mismatch should fail")
	}

	// Now register the correct key
	verifier.RegisterKey("alice", pub1)
	if err := verifier.Verify(signed); err != nil {
		t.Fatalf("matching registered key should pass: %v", err)
	}
}

func TestSigVerifier_MissingSignature(t *testing.T) {
	verifier := NewSigVerifier()
	stx := &SignedTx{
		Tx:        &mempool.Tx{ID: "tx1", Sender: "alice"},
		Signature: nil,
		PublicKey: make([]byte, 32),
		Algorithm: SigEd25519,
	}
	if err := verifier.Verify(stx); err == nil {
		t.Fatal("missing signature should fail")
	}
}

func TestSigVerifier_MissingPublicKey(t *testing.T) {
	verifier := NewSigVerifier()
	stx := &SignedTx{
		Tx:        &mempool.Tx{ID: "tx1", Sender: "alice"},
		Signature: make([]byte, 64),
		PublicKey: nil,
		Algorithm: SigEd25519,
	}
	if err := verifier.Verify(stx); err == nil {
		t.Fatal("missing public key should fail")
	}
}

func TestSigVerifier_NilTx(t *testing.T) {
	verifier := NewSigVerifier()
	stx := &SignedTx{Tx: nil, Signature: []byte{1}, PublicKey: []byte{2}, Algorithm: SigEd25519}
	if err := verifier.Verify(stx); err == nil {
		t.Fatal("nil tx should fail")
	}
}

func TestSigVerifier_UnsupportedAlgorithm(t *testing.T) {
	verifier := NewSigVerifier()
	stx := &SignedTx{
		Tx:        &mempool.Tx{ID: "tx1"},
		Signature: []byte{1},
		PublicKey: []byte{2},
		Algorithm: "rsa",
	}
	if err := verifier.Verify(stx); err == nil {
		t.Fatal("unsupported algorithm should fail")
	}
}

func TestSigVerifier_MLDSA_WrongPublicKeyLength(t *testing.T) {
	verifier := NewSigVerifier()
	stx := &SignedTx{
		Tx:        &mempool.Tx{ID: "tx1", Sender: "alice"},
		Signature: make([]byte, mldsa87SignatureLen),
		PublicKey: make([]byte, 64),
		Algorithm: SigMLDSA,
	}
	if err := verifier.Verify(stx); err == nil {
		t.Fatal("wrong ML-DSA public key length should fail")
	}
}

func TestSigVerifier_MLDSA_TooShort(t *testing.T) {
	verifier := NewSigVerifier()
	stx := &SignedTx{
		Tx:        &mempool.Tx{ID: "tx1", Sender: "alice"},
		Signature: make([]byte, 10),
		PublicKey: make([]byte, 64),
		Algorithm: SigMLDSA,
	}
	if err := verifier.Verify(stx); err == nil {
		t.Fatal("short ml-dsa sig should fail")
	}
}

func TestSigVerifier_MLDSA_WrongSignatureLength(t *testing.T) {
	verifier := NewSigVerifier()
	stx := &SignedTx{
		Tx:        &mempool.Tx{ID: "tx1", Sender: "alice"},
		Signature: make([]byte, 100),
		PublicKey: make([]byte, mldsa87PublicKeyLen),
		Algorithm: SigMLDSA,
	}
	if err := verifier.Verify(stx); err == nil {
		t.Fatal("wrong ML-DSA signature length should fail")
	}
}

func TestTxSigningHash_Deterministic(t *testing.T) {
	tx := &mempool.Tx{ID: "tx1", Sender: "alice", Recipient: "bob", Amount: 10, Fee: 1, Nonce: 5}
	h1 := TxSigningHash(tx)
	h2 := TxSigningHash(tx)
	if string(h1) != string(h2) {
		t.Fatal("signing hash should be deterministic")
	}
}

func TestTxSigningHash_DifferentForDifferentTxs(t *testing.T) {
	tx1 := &mempool.Tx{ID: "tx1", Sender: "alice", Recipient: "bob", Amount: 10, Fee: 1, Nonce: 0}
	tx2 := &mempool.Tx{ID: "tx2", Sender: "alice", Recipient: "bob", Amount: 10, Fee: 1, Nonce: 0}
	if string(TxSigningHash(tx1)) == string(TxSigningHash(tx2)) {
		t.Fatal("different tx IDs should produce different hashes")
	}
}

func TestTxSigningHash_IncludesContractIDWhenSet(t *testing.T) {
	base := &mempool.Tx{ID: "tx1", Sender: "alice", Recipient: "bob", Amount: 10, Fee: 1, Nonce: 0}
	withCID := &mempool.Tx{ID: "tx1", Sender: "alice", Recipient: "bob", Amount: 10, Fee: 1, Nonce: 0, ContractID: "c-1"}
	if string(TxSigningHash(base)) == string(TxSigningHash(withCID)) {
		t.Fatal("contract_id should change signing hash when set")
	}
	empty := &mempool.Tx{ID: "tx1", Sender: "alice", Recipient: "bob", Amount: 10, Fee: 1, Nonce: 0, ContractID: ""}
	if string(TxSigningHash(base)) != string(TxSigningHash(empty)) {
		t.Fatal("empty contract_id should match omitted for signing hash")
	}
}

func TestSigVerifier_RegisterMLDSAKey_validation(t *testing.T) {
	sv := NewSigVerifier()
	if err := sv.RegisterMLDSAKey("", make([]byte, mldsa87PublicKeyLen)); err == nil {
		t.Fatal("empty address should fail")
	}
	if err := sv.RegisterMLDSAKey("alice", make([]byte, 10)); err == nil {
		t.Fatal("short ml-dsa key should fail")
	}
	if err := sv.RegisterMLDSAKeyHex("alice", "not-hex"); err == nil {
		t.Fatal("invalid hex should fail")
	}
}

func TestSigVerifier_MLDSAKeyRing_registerRemoveCount(t *testing.T) {
	sv := NewSigVerifier()
	pk := make([]byte, mldsa87PublicKeyLen)
	if err := sv.RegisterMLDSAKey("AbCd", pk); err != nil {
		t.Fatal(err)
	}
	if sv.MLDSAKeyCount() != 1 {
		t.Fatalf("count=%d", sv.MLDSAKeyCount())
	}
	got, ok := sv.GetMLDSAKey("abcd")
	if !ok || len(got) != mldsa87PublicKeyLen {
		t.Fatal("lookup should normalize address case")
	}
	sv.RemoveMLDSAKey("ABCD")
	if sv.MLDSAKeyCount() != 0 {
		t.Fatal("expected 0 after remove")
	}
}

func TestSigVerifier_KeyManagement(t *testing.T) {
	pub, _ := makeTestKeypair(t)
	sv := NewSigVerifier()

	sv.RegisterKey("alice", pub)
	if sv.KeyCount() != 1 {
		t.Fatal("expected 1 key")
	}
	got, ok := sv.GetKey("alice")
	if !ok || !got.Equal(pub) {
		t.Fatal("expected alice's key")
	}

	sv.RemoveKey("alice")
	if sv.KeyCount() != 0 {
		t.Fatal("expected 0 keys after removal")
	}
}

func TestTxSigner_PublicKeyHex(t *testing.T) {
	_, priv := makeTestKeypair(t)
	signer := NewTxSigner(priv)
	h := signer.PublicKeyHex()
	if len(h) != 64 {
		t.Fatalf("expected 64 hex chars, got %d", len(h))
	}
}
