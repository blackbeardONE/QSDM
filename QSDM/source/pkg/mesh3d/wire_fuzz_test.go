package mesh3d

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/poe"
	"github.com/blackbeardONE/QSDM/pkg/wallet"
	"github.com/cloudflare/circl/sign/mldsa/mldsa87"
)

// FuzzMeshPubsubWire decodes a qsdm-transactions payload the way a follower's
// legacy handler does (cmd/qsdm/transaction DispatchInboundP2P:
// ParseMeshPubsubWire) and runs the mesh validator on the transaction. In
// "harness wraps" mode the input is taken as a wallet envelope instead: the
// harness signs it with a fixed key and wraps it the way a wallet's mesh
// companion is built (CompanionFromWalletJSON, EncodeMeshPubsubWire), so the
// fuzzer reaches acceptance.
func FuzzMeshPubsubWire(f *testing.F) {
	var seed [mldsa87.SeedSize]byte
	copy(seed[:], "mesh3d wire fuzz key")
	public, private := mldsa87.NewKeyFromSeed(&seed)
	pub := public.Bytes()
	sum := sha256.Sum256(pub)
	address := hex.EncodeToString(sum[:])
	sign := func(env wallet.TransactionData) []byte {
		env.Sender, env.PublicKey = address, hex.EncodeToString(pub)
		canonical, err := env.CanonicalBytes()
		if err != nil {
			panic(err)
		}
		sig := make([]byte, mldsa87.SignatureSize)
		if err := mldsa87.SignTo(private, canonical, nil, false, sig); err != nil {
			panic(err)
		}
		env.Signature = hex.EncodeToString(sig)
		raw, _ := json.Marshal(env)
		return raw
	}
	wrap := func(walletJSON []byte, submesh string) ([]byte, error) {
		tx, err := CompanionFromWalletJSON(walletJSON)
		if err != nil {
			return nil, err
		}
		return EncodeMeshPubsubWire(tx, submesh)
	}
	validator := NewMesh3DValidator()

	envelope := wallet.TransactionData{
		ID: "hive_wallet_1791611407090_00112233aabbccdd", Recipient: strings.Repeat("ab", 32), Amount: 2, Fee: 0.01,
		ParentCells: []string{meshParentA, meshParentB, "solo-heartbeat-102-1791611427090734188"}, Nonce: 1,
		Timestamp: "2026-08-01T12:00:00Z",
	}
	signed := sign(envelope)
	companionWire, err := wrap(signed, "sm1")
	if err != nil {
		f.Fatal(err)
	}
	if tx, _, err := ParseMeshPubsubWire(companionWire); err != nil {
		f.Fatal(err)
	} else if ok, err := validator.ValidateTransaction(tx); !ok || err != nil {
		f.Fatalf("genuine seed rejected: %v", err)
	}
	// Four mesh parents: the envelope's first three signed parents and the
	// payload digest.
	four := &Transaction{ID: envelope.ID[:20], Data: signed, ParentCells: []ParentCell{
		parentCellDataFromLabel(envelope.ParentCells[0]), parentCellDataFromLabel(envelope.ParentCells[1]),
		parentCellDataFromLabel(envelope.ParentCells[2]), parentCellDataFromLabel(PayloadDigest(signed)),
	}}
	fourWire, err := EncodeMeshPubsubWire(four, "")
	if err != nil {
		f.Fatal(err)
	}
	for _, s := range []struct {
		raw   []byte
		wraps bool
	}{
		{companionWire, false},
		{fourWire, false},
		{signed, true},
		{bytes.Replace(companionWire, []byte(`"sm1"`), []byte(`""`), 1), false},
		{bytes.Replace(companionWire, []byte(`"qsdm_mesh3d_v1"`), []byte(`"qsdm_mesh3d_v2"`), 1), false},
		{[]byte(`{"kind":"qsdm_mesh3d_v1","id":"x","parent_cells":[{"id":"a","data_b64":"=="}],"payload_b64":""}`), false},
		{[]byte(`{"id":"hive_wallet_1791611407090_00112233aabbccdd","parent_cells":["a","a"],"nonce":1,"amount":1}`), true},
		{[]byte(`{"id":"short","parent_cells":[],"nonce":0}`), true},
	} {
		f.Add(s.raw, s.wraps)
	}

	f.Fuzz(func(t *testing.T, raw []byte, harnessWraps bool) {
		if harnessWraps {
			var env wallet.TransactionData
			if json.Unmarshal(raw, &env) != nil {
				return
			}
			var err error
			if raw, err = wrap(sign(env), "sm1"); err != nil {
				return
			}
		}
		tx, submesh, err := ParseMeshPubsubWire(raw)
		if err != nil {
			return
		}
		if tx.ID == "" || len(tx.ParentCells) < MinMeshParents || len(tx.ParentCells) > MaxMeshParents || submesh == "" {
			t.Fatalf("decoded a wire message the decoder must refuse: %+v (submesh %q)", tx, submesh)
		}
		for _, p := range tx.ParentCells {
			if p.ID == "" {
				t.Fatal("decoded a parent cell without an id")
			}
		}
		again, err := EncodeMeshPubsubWire(tx, submesh)
		if err != nil {
			t.Fatalf("a decoded message does not re-encode: %v", err)
		}
		if back, sub, err := ParseMeshPubsubWire(again); err != nil || sub != submesh || !reflect.DeepEqual(back, tx) {
			t.Fatalf("decode(encode(tx)) differs from tx: %v", err)
		}

		env, structErr := ValidateStructure(tx)
		ok, valErr := validator.ValidateTransaction(tx)
		if structErr != nil {
			if !errors.Is(structErr, ErrStructure) || ok || valErr == nil {
				t.Fatalf("structure error %v (validator %v, %v)", structErr, ok, valErr)
			}
			return
		}
		if !ok {
			if valErr == nil || !errors.Is(valErr, ErrStructure) {
				t.Fatalf("validator refused without a structure error: %v", valErr)
			}
		}

		n := len(tx.ParentCells)
		seen := map[string]bool{}
		for _, p := range tx.ParentCells {
			want := sha256.Sum256([]byte(p.ID))
			if seen[p.ID] || !bytes.Equal(p.Data, want[:]) {
				t.Fatalf("accepted parent cell %q twice or with data that is not sha256 of its id", p.ID)
			}
			seen[p.ID] = true
		}
		var decoded wallet.TransactionData
		dec := json.NewDecoder(bytes.NewReader(tx.Data))
		dec.DisallowUnknownFields()
		if dec.Decode(&decoded) != nil || dec.More() || !reflect.DeepEqual(decoded, env) {
			t.Fatal("accepted a payload that is not exactly the envelope ValidateStructure returned")
		}
		pubBytes, perr := hex.DecodeString(env.PublicKey)
		envSum := sha256.Sum256(pubBytes)
		if perr != nil || env.Sender != hex.EncodeToString(envSum[:]) || env.Nonce == 0 || !(env.Amount > 0) || env.Fee < 0 ||
			env.ID == "" || env.Recipient == "" {
			t.Fatalf("accepted envelope %+v", env)
		}
		unsigned := env
		unsigned.Signature, unsigned.PublicKey = "", ""
		msg, _ := json.Marshal(unsigned)
		sig, serr := hex.DecodeString(env.Signature)
		var pk mldsa87.PublicKey
		if serr != nil || pk.UnmarshalBinary(pubBytes) != nil || !mldsa87.Verify(&pk, msg, nil, sig) {
			t.Fatal("accepted a payload signature the reference ML-DSA-87 verifier rejects")
		}
		if poe.CheckShape(env.ID, env.ParentCells) != nil || len(tx.ID) < MinMeshTxIDLen || !strings.HasPrefix(env.ID, tx.ID) {
			t.Fatalf("accepted mesh id %q for envelope %q with parents %q", tx.ID, env.ID, env.ParentCells)
		}
		digest := sha256.Sum256(tx.Data)
		if tx.ParentCells[n-1].ID != hex.EncodeToString(digest[:]) || len(env.ParentCells) < n-1 {
			t.Fatal("accepted a last parent that is not the payload digest")
		}
		for i := 0; i < n-1; i++ {
			if tx.ParentCells[i].ID != env.ParentCells[i] {
				t.Fatalf("accepted mesh parent %d %q, which is not signed parent %q", i, tx.ParentCells[i].ID, env.ParentCells[i])
			}
		}
	})
}
