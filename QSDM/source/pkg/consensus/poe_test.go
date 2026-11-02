package consensus

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/internal/logging"
	"github.com/blackbeardONE/QSDM/pkg/poe"
)

func walletAddress(pub []byte) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:])
}

var (
	testParentA = strings.Repeat("a", 32)
	testParentB = strings.Repeat("b", 32)
	testParentC = strings.Repeat("c", 32)
)

// signedBy builds a SignedTransaction signed by signer and carrying signer's
// own public key and wallet address.
func signedBy(t *testing.T, signer *ProofOfEntanglement, parents ...string) SignedTransaction {
	t.Helper()
	msg := []byte("canonical envelope bytes")
	sig, err := signer.Sign(msg)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	pub := signer.MLDSAPublicKey()
	return SignedTransaction{
		ID:           "tx-0000000000000001",
		Sender:       walletAddress(pub),
		SigningBytes: msg,
		ParentCells:  parents,
		Signatures:   [][]byte{sig},
		PublicKey:    pub,
	}
}

func newPoE(t *testing.T) *ProofOfEntanglement {
	t.Helper()
	p := NewProofOfEntanglement()
	if p == nil {
		t.Skip("ProofOfEntanglement unavailable (no ML-DSA-87 backend)")
	}
	return p
}

func TestValidateSignedTransaction_AcceptsKeyBoundTransaction(t *testing.T) {
	signer := newPoE(t)
	for _, parents := range [][]string{
		{testParentA, testParentB},              // wallet transfers
		{testParentA, testParentB, testParentC}, // mesh companions
	} {
		if err := ValidateSignedTransaction(signedBy(t, signer, parents...)); err != nil {
			t.Fatalf("%d parents: %v", len(parents), err)
		}
	}
}

// The verifying node's own key must play no part: a transaction signed by
// someone else validates on this node, and a nil instance can validate.
func TestValidateTransaction_DoesNotUseTheNodeKey(t *testing.T) {
	signer := newPoE(t)
	node := newPoE(t)
	logger := logging.NewSilentLogger()
	tx := signedBy(t, signer, testParentA, testParentB)
	if ok, err := node.ValidateTransaction(tx, logger); err != nil || !ok {
		t.Fatalf("third-party transaction rejected by a different node: ok=%v err=%v", ok, err)
	}
	var nilPoE *ProofOfEntanglement
	if ok, err := nilPoE.ValidateTransaction(tx, nil); err != nil || !ok {
		t.Fatalf("nil instance: ok=%v err=%v", ok, err)
	}
	// The node's own signature over the same bytes, presented with the
	// sender's key, is not the sender's signature.
	nodeSig, err := node.Sign(tx.SigningBytes)
	if err != nil {
		t.Fatal(err)
	}
	forged := tx
	forged.Signatures = [][]byte{nodeSig}
	if err := ValidateSignedTransaction(forged); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("node-key signature under the sender's key: err=%v, want ErrBadSignature", err)
	}
}

func TestValidateSignedTransaction_WrongSignerKey(t *testing.T) {
	signer := newPoE(t)
	other := newPoE(t)
	tx := signedBy(t, signer, testParentA, testParentB)
	// Present another wallet's key (and its matching address): the signature
	// was not made by that key.
	tx.PublicKey = other.MLDSAPublicKey()
	tx.Sender = walletAddress(tx.PublicKey)
	if err := ValidateSignedTransaction(tx); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("err=%v, want ErrBadSignature", err)
	}
}

func TestValidateSignedTransaction_SenderKeyMismatch(t *testing.T) {
	signer := newPoE(t)
	victim := newPoE(t)
	tx := signedBy(t, signer, testParentA, testParentB)
	// A valid signature by the attacker's key cannot spend from another
	// address.
	tx.Sender = walletAddress(victim.MLDSAPublicKey())
	if err := ValidateSignedTransaction(tx); !errors.Is(err, ErrSenderMismatch) {
		t.Fatalf("err=%v, want ErrSenderMismatch", err)
	}
}

func TestValidateSignedTransaction_RequiresKeyAndSignature(t *testing.T) {
	signer := newPoE(t)
	tx := signedBy(t, signer, testParentA, testParentB)
	noKey := tx
	noKey.PublicKey = nil
	if err := ValidateSignedTransaction(noKey); !errors.Is(err, ErrNoPublicKey) {
		t.Fatalf("missing key: err=%v", err)
	}
	noSig := tx
	noSig.Signatures = nil
	if err := ValidateSignedTransaction(noSig); !errors.Is(err, ErrNoSignature) {
		t.Fatalf("missing signature: err=%v", err)
	}
	tampered := tx
	tampered.SigningBytes = []byte("different bytes")
	if err := ValidateSignedTransaction(tampered); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("tampered message: err=%v", err)
	}
}

func TestValidateSignedTransaction_ParentShape(t *testing.T) {
	signer := newPoE(t)
	cases := []struct {
		name    string
		parents []string
		want    error
	}{
		{"zero", nil, poe.ErrParentCount},
		{"one", []string{testParentA}, poe.ErrParentCount},
		{"duplicate", []string{testParentA, testParentA}, poe.ErrDuplicateParent},
		{"self", []string{testParentA, "tx-0000000000000001"}, poe.ErrSelfParent},
		{"placeholder", []string{"parent1", "parent2"}, poe.ErrParentFormat},
		{"too-many", []string{
			strings.Repeat("1", 16), strings.Repeat("2", 16), strings.Repeat("3", 16), strings.Repeat("4", 16),
			strings.Repeat("5", 16), strings.Repeat("6", 16), strings.Repeat("7", 16), strings.Repeat("8", 16),
			strings.Repeat("9", 16), strings.Repeat("a", 16), strings.Repeat("b", 16),
		}, poe.ErrParentCount},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateSignedTransaction(signedBy(t, signer, tc.parents...))
			if !errors.Is(err, tc.want) || !poe.IsViolation(err) {
				t.Fatalf("err=%v, want %v", err, tc.want)
			}
		})
	}
}
