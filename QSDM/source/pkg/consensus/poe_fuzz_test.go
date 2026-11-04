package consensus

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/crypto"
	"github.com/blackbeardONE/QSDM/pkg/poe"
	"github.com/cloudflare/circl/sign/mldsa/mldsa87"
)

// FuzzValidateSignedTransaction drives the PoE signature check that every
// peer-to-peer wallet transfer passes (cmd/qsdm/transaction). In "harness
// signs" mode it signs the fuzzed bytes with a fixed key, so the fuzzer
// reaches acceptance for any parent shape and sender spelling; otherwise the
// key and signature come from the input as they do off the wire.
func FuzzValidateSignedTransaction(f *testing.F) {
	signer := newPoE(f)
	for _, parents := range [][]string{
		{testParentA, testParentB},
		{testParentA, testParentB, testParentC},
	} {
		tx := signedBy(f, signer, parents...)
		if err := ValidateSignedTransaction(tx); err != nil {
			f.Fatalf("genuine seed rejected: %v", err)
		}
		f.Add(tx.ID, strings.Join(tx.ParentCells, ","), tx.SigningBytes, tx.Sender, tx.PublicKey, tx.Signatures[0], false, false)
		compressed, err := crypto.CompressSignature(tx.Signatures[0])
		if err != nil {
			f.Fatal(err)
		}
		f.Add(tx.ID, strings.Join(tx.ParentCells, ","), tx.SigningBytes, tx.Sender, tx.PublicKey, compressed, true, false)
		f.Add(tx.ID, strings.Join(tx.ParentCells, ","), tx.SigningBytes, " "+strings.ToUpper(tx.Sender), tx.PublicKey, tx.Signatures[0], false, false)
	}
	f.Add("tx-0000000000000001", testParentA+","+testParentB, []byte("envelope"), "", []byte(nil), []byte(nil), false, true)
	f.Add("tx-0000000000000001", testParentA+","+testParentB, []byte("envelope"), "", []byte(nil), []byte(nil), true, true)
	f.Add("tx-0000000000000001", testParentA+","+testParentA, []byte("envelope"), "", []byte(nil), []byte(nil), false, true)
	f.Add(testParentA, testParentA+","+testParentB, []byte(nil), "", []byte(nil), []byte(nil), false, true)
	f.Add("tx-0000000000000001", "parent1,parent2", []byte("x"), "sender", []byte{1}, []byte{2}, false, false)
	f.Add("tx-0000000000000001", testParentA+","+testParentB, []byte("x"), "", []byte(nil), []byte{0x28, 0xb5, 0x2f, 0xfd}, true, false)

	var seed [mldsa87.SeedSize]byte
	public, private := mldsa87.NewKeyFromSeed(&seed)
	harnessKey := public.Bytes()
	harnessSender := walletAddress(harnessKey)

	f.Fuzz(func(t *testing.T, id, parentList string, message []byte, sender string, publicKey, signature []byte,
		compressed, harnessSigns bool) {
		var parents []string
		if parentList != "" {
			parents = strings.Split(parentList, ",")
		}
		if harnessSigns {
			publicKey = harnessKey
			if sender == "" {
				sender = harnessSender
			}
			signature = make([]byte, mldsa87.SignatureSize)
			if err := mldsa87.SignTo(private, message, nil, false, signature); err != nil {
				t.Fatal(err)
			}
			if compressed {
				var err error
				if signature, err = crypto.CompressSignature(signature); err != nil {
					t.Fatal(err)
				}
			}
		}
		tx := SignedTransaction{
			ID: id, Sender: sender, SigningBytes: message, ParentCells: parents,
			Signatures: [][]byte{signature}, PublicKey: publicKey, Compressed: compressed,
		}
		orig := SignedTransaction{
			ID: id, Sender: sender, SigningBytes: bytes.Clone(message), ParentCells: slices.Clone(parents),
			Signatures: [][]byte{bytes.Clone(signature)}, PublicKey: bytes.Clone(publicKey), Compressed: compressed,
		}

		err := ValidateSignedTransaction(tx)
		if !reflect.DeepEqual(tx, orig) {
			t.Fatal("ValidateSignedTransaction modified its input")
		}
		shapeErr := poe.CheckShape(id, parents)
		if shapeErr != nil && !poe.IsViolation(err) {
			t.Fatalf("bad parents (%v) not reported as a PoE violation: %v", shapeErr, err)
		}
		sum := sha256.Sum256(publicKey)
		senderOK := len(publicKey) > 0 && strings.EqualFold(hex.EncodeToString(sum[:]), strings.TrimSpace(sender))
		if harnessSigns && shapeErr == nil && senderOK && err != nil {
			t.Fatalf("rejected a well-formed transaction signed by its sender: %v", err)
		}
		if err != nil {
			return
		}
		if shapeErr != nil || !senderOK {
			t.Fatalf("accepted parents %q (%v) or sender %q", parents, shapeErr, sender)
		}
		raw := signature
		if compressed {
			if raw, err = crypto.DecompressSignature(signature); err != nil {
				t.Fatalf("accepted an undecodable compressed signature: %v", err)
			}
		}
		var pk mldsa87.PublicKey
		if pk.UnmarshalBinary(publicKey) != nil || len(raw) != mldsa87.SignatureSize || !mldsa87.Verify(&pk, message, nil, raw) {
			t.Fatal("accepted a signature the reference ML-DSA-87 verifier rejects")
		}
	})
}
