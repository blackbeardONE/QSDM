package chain

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/cloudflare/circl/sign/mldsa/mldsa87"
)

// testMLDSASigner signs SignedTx with a fresh ML-DSA-87 key (FIPS 204, empty
// context: the encoding verifyMLDSA accepts). It replaces the retired Ed25519
// TxSigner in tests.
type testMLDSASigner struct {
	sk      *mldsa87.PrivateKey
	pub     []byte
	address string
}

func newTestMLDSASigner(t *testing.T) *testMLDSASigner {
	t.Helper()
	pk, sk, err := mldsa87.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := pk.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(pub)
	return &testMLDSASigner{sk: sk, pub: pub, address: hex.EncodeToString(sum[:])}
}

// Address is SHA256(public key) as lower-case hex: the only sender this key
// can spend from.
func (s *testMLDSASigner) Address() string { return s.address }

func (s *testMLDSASigner) Sign(t *testing.T, tx *mempool.Tx) *SignedTx {
	t.Helper()
	sig := make([]byte, mldsa87.SignatureSize)
	if err := mldsa87.SignTo(s.sk, TxSigningHash(tx), nil, true, sig); err != nil {
		t.Fatal(err)
	}
	return &SignedTx{
		Tx:        tx,
		Signature: sig,
		PublicKey: append([]byte(nil), s.pub...),
		Algorithm: SigMLDSA,
	}
}
