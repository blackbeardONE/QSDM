package chain

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

func TestTxSigner_SignAndVerify(t *testing.T) {
	signer := newTestMLDSASigner(t)
	verifier := NewSigVerifier()

	// Sender must be the address derived from the signing key. A signature
	// alone does not authorize spending from an arbitrary account.
	tx := &mempool.Tx{ID: "tx1", Sender: signer.Address(), Recipient: "bob", Amount: 10, Fee: 1, Nonce: 0}
	if err := verifier.Verify(signer.Sign(t, tx)); err != nil {
		t.Fatalf("valid ML-DSA signature should verify: %v", err)
	}
}

// ed25519SignedTx builds a correctly signed Ed25519 SignedTx whose sender is
// SHA256(public key), i.e. one the retired verifier would have accepted.
func ed25519SignedTx(t *testing.T, id string) *SignedTx {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(pub)
	tx := &mempool.Tx{ID: id, Sender: hex.EncodeToString(sum[:]), Recipient: "bob", Amount: 1, Fee: 1, Nonce: 0}
	return &SignedTx{Tx: tx, Signature: ed25519.Sign(priv, TxSigningHash(tx)), PublicKey: pub, Algorithm: SigEd25519}
}

// Ed25519 is retired: a correctly signed, sender-bound Ed25519 SignedTx is
// refused with ErrEd25519Retired and counted.
func TestSigVerifier_Ed25519Retired(t *testing.T) {
	verifier := NewSigVerifier()
	before := TxSigEd25519Rejected()
	err := verifier.Verify(ed25519SignedTx(t, "ed-1"))
	if !errors.Is(err, ErrEd25519Retired) {
		t.Fatalf("ed25519 SignedTx must be refused with ErrEd25519Retired, got %v", err)
	}
	if got := TxSigEd25519Rejected(); got != before+1 {
		t.Fatalf("ed25519 rejection counter: %d -> %d", before, got)
	}

	// An ML-DSA key and signature labelled "ed25519" is refused too: the
	// label decides, and the label is retired.
	signer := newTestMLDSASigner(t)
	stx := signer.Sign(t, &mempool.Tx{ID: "ed-2", Sender: signer.Address(), Recipient: "bob", Amount: 1, Fee: 1})
	stx.Algorithm = SigEd25519
	if err := verifier.Verify(stx); !errors.Is(err, ErrEd25519Retired) {
		t.Fatalf("ml-dsa material labelled ed25519 must be refused, got %v", err)
	}
	// Relabelled honestly, the same SignedTx verifies.
	stx.Algorithm = SigMLDSA
	if err := verifier.Verify(stx); err != nil {
		t.Fatalf("ml-dsa SignedTx: %v", err)
	}
}

// An attacker cannot sign for another sender: the sender must be the
// address derived from the presented key.
func TestSigVerifier_MLDSARejectsSenderImpersonation(t *testing.T) {
	attacker := newTestMLDSASigner(t)
	victim := newTestMLDSASigner(t)
	verifier := NewSigVerifier() // empty keyring: the production configuration

	tx := &mempool.Tx{ID: "steal", Sender: victim.Address(), Recipient: attacker.Address(), Amount: 1000, Fee: 1, Nonce: 0}
	if err := verifier.Verify(attacker.Sign(t, tx)); err == nil {
		t.Fatal("attacker must not be able to sign a transaction on behalf of another sender")
	}
}

func TestTxSigner_TamperedTxFails(t *testing.T) {
	signer := newTestMLDSASigner(t)
	verifier := NewSigVerifier()

	tx := &mempool.Tx{ID: "tx1", Sender: signer.Address(), Recipient: "bob", Amount: 10, Fee: 1, Nonce: 0}
	signed := signer.Sign(t, tx)

	// Tamper with amount
	signed.Tx.Amount = 99999
	if err := verifier.Verify(signed); err == nil {
		t.Fatal("tampered tx should fail verification")
	}
}

func TestSigVerifier_WrongKeyFails(t *testing.T) {
	signer1 := newTestMLDSASigner(t)
	signer2 := newTestMLDSASigner(t)
	verifier := NewSigVerifier()

	tx := &mempool.Tx{ID: "tx1", Sender: signer1.Address(), Recipient: "bob", Amount: 10, Fee: 1, Nonce: 0}
	signed := signer1.Sign(t, tx)

	// Replace public key with a different one
	signed.PublicKey = append([]byte(nil), signer2.pub...)
	if err := verifier.Verify(signed); err == nil {
		t.Fatal("wrong public key should fail verification")
	}
}

func TestSigVerifier_RegisteredKeyMismatch(t *testing.T) {
	signer := newTestMLDSASigner(t)
	other := newTestMLDSASigner(t)
	verifier := NewSigVerifier()
	if err := verifier.RegisterMLDSAKey(signer.Address(), other.pub); err != nil { // pin a different key
		t.Fatal(err)
	}

	tx := &mempool.Tx{ID: "tx1", Sender: signer.Address(), Recipient: "bob", Amount: 10, Fee: 1, Nonce: 0}
	signed := signer.Sign(t, tx)
	if err := verifier.Verify(signed); err == nil {
		t.Fatal("registered key mismatch should fail")
	}

	// Now pin the correct key
	if err := verifier.RegisterMLDSAKey(signer.Address(), signer.pub); err != nil {
		t.Fatal(err)
	}
	if err := verifier.Verify(signed); err != nil {
		t.Fatalf("matching registered key should pass: %v", err)
	}
}

func TestSigVerifier_MissingSignature(t *testing.T) {
	verifier := NewSigVerifier()
	for _, alg := range []SigAlgorithm{SigMLDSA, SigEd25519} {
		stx := &SignedTx{
			Tx:        &mempool.Tx{ID: "tx1", Sender: "alice"},
			Signature: nil,
			PublicKey: make([]byte, mldsa87PublicKeyLen),
			Algorithm: alg,
		}
		if err := verifier.Verify(stx); err == nil {
			t.Fatalf("%s: missing signature should fail", alg)
		}
	}
}

func TestSigVerifier_MissingPublicKey(t *testing.T) {
	verifier := NewSigVerifier()
	for _, alg := range []SigAlgorithm{SigMLDSA, SigEd25519} {
		stx := &SignedTx{
			Tx:        &mempool.Tx{ID: "tx1", Sender: "alice"},
			Signature: make([]byte, mldsa87SignatureLen),
			PublicKey: nil,
			Algorithm: alg,
		}
		if err := verifier.Verify(stx); err == nil {
			t.Fatalf("%s: missing public key should fail", alg)
		}
	}
}

func TestSigVerifier_NilTx(t *testing.T) {
	verifier := NewSigVerifier()
	for _, alg := range []SigAlgorithm{SigMLDSA, SigEd25519} {
		stx := &SignedTx{Tx: nil, Signature: []byte{1}, PublicKey: []byte{2}, Algorithm: alg}
		if err := verifier.Verify(stx); err == nil {
			t.Fatalf("%s: nil tx should fail", alg)
		}
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
