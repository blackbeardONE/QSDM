//go:build cgo
// +build cgo

package chain

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/crypto"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

func TestSigVerifier_MLDSA_RoundTrip(t *testing.T) {
	d := crypto.NewDilithium()
	if d == nil {
		t.Skip("ML-DSA / liboqs not available")
	}
	defer d.Free()

	pk := d.GetPublicKey()
	// Go does not let you slice the unaddressable return value of
	// sha256.Sum256 directly (since ~Go 1.22 semantics); assign to
	// a local first.
	pkHash := sha256.Sum256(pk)
	sender := hex.EncodeToString(pkHash[:])
	tx := &mempool.Tx{
		ID:        strings.Repeat("z", 32),
		Sender:    sender,
		Recipient: strings.Repeat("y", 32),
		Amount:    1,
		Fee:       0.1,
		Nonce:     0,
	}
	msg := TxSigningHash(tx)
	sig, err := d.SignOptimized(msg)
	if err != nil {
		t.Fatal(err)
	}

	verifier := NewSigVerifier()
	stx := &SignedTx{
		Tx:        tx,
		Signature: sig,
		PublicKey: pk,
		Algorithm: SigMLDSA,
	}
	if err := verifier.Verify(stx); err != nil {
		t.Fatalf("valid ML-DSA SignedTx: %v", err)
	}

	stx.Tx.Amount = 999
	if err := verifier.Verify(stx); err == nil {
		t.Fatal("tampered tx should fail ML-DSA verification")
	}

	// Restore tx and optional keyring: wrong registered key must fail after crypto checks.
	stx.Tx.Amount = 1
	wrongPK := make([]byte, mldsa87PublicKeyLen)
	for i := range wrongPK {
		wrongPK[i] = byte((i + 3) % 251)
	}
	if err := verifier.RegisterMLDSAKey(sender, wrongPK); err != nil {
		t.Fatal(err)
	}
	if err := verifier.Verify(stx); err == nil {
		t.Fatal("optional ML-DSA keyring mismatch should fail")
	}
	verifier.RemoveMLDSAKey(sender)
	if err := verifier.Verify(stx); err != nil {
		t.Fatalf("after keyring remove should verify: %v", err)
	}

	// Hex registration path with matching key
	verifier2 := NewSigVerifier()
	if err := verifier2.RegisterMLDSAKeyHex(sender, hex.EncodeToString(pk)); err != nil {
		t.Fatal(err)
	}
	stx2 := &SignedTx{Tx: tx, Signature: sig, PublicKey: pk, Algorithm: SigMLDSA}
	if err := verifier2.Verify(stx2); err != nil {
		t.Fatalf("matching hex keyring: %v", err)
	}
}
