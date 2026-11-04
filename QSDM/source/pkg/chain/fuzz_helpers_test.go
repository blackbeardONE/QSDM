package chain

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/cloudflare/circl/sign/mldsa/mldsa87"
)

// Helpers shared by the fuzz targets in this package (*_fuzz_test.go).
//
// The structured harnesses sign every input they build. A key that changed
// from one fuzz worker to the next would make a crasher irreproducible and
// the coverage noisy, so fuzzKey derives a fixed ML-DSA-87 key from a tag and
// signs with the deterministic FIPS 204 variant. Seeds still come from the
// package's own helpers (signedStreamTx, signedExhibit, ...), which use fresh
// random keys exactly as the unit tests do.

type fuzzKey struct {
	private *mldsa87.PrivateKey
	pub     []byte // packed ML-DSA-87 public key
	address string // hex(sha256(pub)): the wallet and validator identity
}

func newFuzzKey(tag byte) fuzzKey {
	var seed [mldsa87.SeedSize]byte
	for i := range seed {
		seed[i] = tag
	}
	public, private := mldsa87.NewKeyFromSeed(&seed)
	pub := public.Bytes()
	sum := sha256.Sum256(pub)
	return fuzzKey{private: private, pub: pub, address: hex.EncodeToString(sum[:])}
}

// Sign and GetPublicKey make fuzzKey a BFTSigner.
func (k fuzzKey) Sign(message []byte) ([]byte, error) {
	sig := make([]byte, mldsa87.SignatureSize)
	if err := mldsa87.SignTo(k.private, message, nil, false, sig); err != nil {
		return nil, err
	}
	return sig, nil
}

func (k fuzzKey) GetPublicKey() []byte { return bytes.Clone(k.pub) }

func (k fuzzKey) mustSign(tb testing.TB, message []byte) []byte {
	tb.Helper()
	sig, err := k.Sign(message)
	if err != nil {
		tb.Fatalf("sign: %v", err)
	}
	return sig
}

// fuzzVerifyMLDSA is an independent ML-DSA-87 check that calls circl
// directly instead of going through pkg/crypto, so every signature a
// verifier under test accepts is confirmed by the reference implementation.
func fuzzVerifyMLDSA(pub, message, sig []byte) bool {
	if len(pub) != mldsa87.PublicKeySize || len(sig) != mldsa87.SignatureSize {
		return false
	}
	var pk mldsa87.PublicKey
	if err := pk.UnmarshalBinary(pub); err != nil {
		return false
	}
	return mldsa87.Verify(&pk, message, nil, sig)
}

// cloneTx deep-copies a transaction for "the function did not modify its
// input" checks.
func cloneTx(tx *mempool.Tx) *mempool.Tx {
	if tx == nil {
		return nil
	}
	cp := *tx
	cp.Payload = bytes.Clone(tx.Payload)
	return &cp
}

// fuzzWireString returns s as it arrives after a JSON round trip, the form
// every transaction field takes on its way in from a peer or the API.
func fuzzWireString(s string) string {
	b, _ := json.Marshal(s)
	var out string
	_ = json.Unmarshal(b, &out)
	return out
}

// fuzzIdentifier is the documented identifier rule shared by stream and
// recovery actions: 1..max characters from [A-Za-z0-9-_.:].
func fuzzIdentifier(s string, max int) bool {
	if s == "" || len(s) > max {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '-', c == '_', c == '.', c == ':':
		default:
			return false
		}
	}
	return true
}

// fuzzLowerHex reports whether s is exactly n bytes as lower-case hex.
func fuzzLowerHex(s string, n int) bool {
	if len(s) != 2*n {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// fuzzTime parses a timestamp the way the chain does (RFC 3339, optional
// fractional seconds).
func fuzzTime(s string) (time.Time, bool) {
	ts, err := time.Parse(time.RFC3339Nano, s)
	return ts, err == nil
}
