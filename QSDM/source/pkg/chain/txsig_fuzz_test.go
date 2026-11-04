package chain

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

// FuzzHandleGossipSignedTx decodes a qsdm-transactions payload the way the
// gossip ingress does (pkg/networking TxGossipIngress: a SignedTx JSON object
// with a non-null tx) and runs GossipValidator.HandleIncoming, the signature,
// fee and nonce triage a follower applies before its mempool. In "harness
// signs" mode the harness signs the decoded transaction with a funded
// Ed25519 or ML-DSA-87 key (by the algorithm the input names), so the fuzzer
// reaches admission; otherwise the signature is checked as sent.

// Key-pinning options of the pin input.
const (
	fuzzPinNone = iota
	fuzzPinEd25519Same
	fuzzPinEd25519Other
	fuzzPinMLDSA
)

type fuzzGossipKeys struct {
	edPriv  ed25519.PrivateKey
	edPub   ed25519.PublicKey
	edAddr  string
	otherEd ed25519.PublicKey
	ml      fuzzKey
	other   fuzzKey
}

func newFuzzGossipKeys() fuzzGossipKeys {
	seed := bytes.Repeat([]byte{0x44}, ed25519.SeedSize)
	priv := ed25519.NewKeyFromSeed(seed)
	pub := priv.Public().(ed25519.PublicKey)
	other := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x45}, ed25519.SeedSize)).Public().(ed25519.PublicKey)
	return fuzzGossipKeys{edPriv: priv, edPub: pub, edAddr: Ed25519WalletAddress(pub), otherEd: other,
		ml: newFuzzKey(0x46), other: newFuzzKey(0x47)}
}

func (k fuzzGossipKeys) verifier(pin uint8) *SigVerifier {
	sv := NewSigVerifier()
	switch pin % 4 {
	case fuzzPinEd25519Same:
		sv.RegisterKey(k.edAddr, k.edPub)
	case fuzzPinEd25519Other:
		sv.RegisterKey(k.edAddr, k.otherEd)
	case fuzzPinMLDSA:
		if err := sv.RegisterMLDSAKey(k.ml.address, k.other.pub); err != nil {
			panic(err)
		}
	}
	return sv
}

func (k fuzzGossipKeys) sign(stx *SignedTx) {
	if stx.Algorithm == SigMLDSA {
		stx.Tx.Sender, stx.PublicKey = k.ml.address, bytes.Clone(k.ml.pub)
		stx.Signature, _ = k.ml.Sign(TxSigningHash(stx.Tx))
		return
	}
	stx.Tx.Sender, stx.PublicKey = k.edAddr, bytes.Clone(k.edPub)
	stx.Signature = ed25519.Sign(k.edPriv, TxSigningHash(stx.Tx))
}

func cloneSignedTx(stx *SignedTx) *SignedTx {
	cp := *stx
	cp.Tx = cloneTx(stx.Tx)
	cp.Signature = bytes.Clone(stx.Signature)
	cp.PublicKey = bytes.Clone(stx.PublicKey)
	return &cp
}

// checkVerifiedSignedTx asserts what a signature the verifier accepted must
// satisfy.
func checkVerifiedSignedTx(t *testing.T, sv *SigVerifier, stx *SignedTx) {
	t.Helper()
	tx := stx.Tx
	digest := TxSigningHash(tx)
	derived := sha256.Sum256(stx.PublicKey)
	senderIsKey := strings.EqualFold(hex.EncodeToString(derived[:]), tx.Sender)
	switch stx.Algorithm {
	case SigEd25519:
		if len(stx.PublicKey) != ed25519.PublicKeySize || len(stx.Signature) != ed25519.SignatureSize ||
			!ed25519.Verify(stx.PublicKey, digest, stx.Signature) {
			t.Fatal("verified an Ed25519 signature crypto/ed25519 rejects")
		}
		if pinned, ok := sv.GetKey(tx.Sender); ok {
			if !bytes.Equal(pinned, stx.PublicKey) {
				t.Fatalf("verified a key other than the one pinned for %q", tx.Sender)
			}
		} else if !senderIsKey {
			t.Fatalf("verified sender %q, which is not hex(sha256(public_key))", tx.Sender)
		}
	case SigMLDSA:
		if !fuzzVerifyMLDSA(stx.PublicKey, digest, stx.Signature) {
			t.Fatal("verified an ML-DSA-87 signature the reference verifier rejects")
		}
		if !senderIsKey {
			t.Fatalf("verified sender %q, which is not hex(sha256(public_key))", tx.Sender)
		}
		if pinned, ok := sv.GetMLDSAKey(tx.Sender); ok && !bytes.Equal(pinned, stx.PublicKey) {
			t.Fatalf("verified a key other than the one pinned for %q", tx.Sender)
		}
	default:
		t.Fatalf("verified algorithm %q", stx.Algorithm)
	}
	for _, change := range []func(*mempool.Tx){
		func(m *mempool.Tx) { m.Nonce++ },
		func(m *mempool.Tx) { m.ID += "x" },
		func(m *mempool.Tx) { m.Recipient += "x" },
		func(m *mempool.Tx) { m.Payload = append(bytes.Clone(m.Payload), 'x') },
	} {
		changed := cloneSignedTx(stx)
		change(changed.Tx)
		if sv.Verify(changed) == nil {
			t.Fatalf("a signature verified after a signed field changed: %+v", changed.Tx)
		}
	}
}

func FuzzHandleGossipSignedTx(f *testing.F) {
	keys := newFuzzGossipKeys()
	newGossip := func(pin uint8) (*GossipValidator, *mempool.Mempool, *AccountStore) {
		accounts := NewAccountStore()
		accounts.Credit(keys.edAddr, 1000)
		accounts.Credit(keys.ml.address, 1000)
		return NewGossipValidator(keys.verifier(pin), NewTxValidator(accounts), DefaultGossipValidationConfig()),
			mempool.New(mempool.DefaultConfig()), accounts
	}
	recipient := strings.Repeat("ab", 32)
	signed := func(alg SigAlgorithm, nonce uint64) []byte {
		stx := &SignedTx{Algorithm: alg, Tx: &mempool.Tx{ID: "gossip-tx-1", Recipient: recipient, Amount: 2.5,
			Fee: 0.001, Nonce: nonce, ContractID: "qsdm/memo/v1", Payload: []byte("hello")}}
		keys.sign(stx)
		raw, err := json.Marshal(stx)
		if err != nil {
			f.Fatal(err)
		}
		return raw
	}
	for _, alg := range []SigAlgorithm{SigEd25519, SigMLDSA} {
		gv, pool, _ := newGossip(fuzzPinNone)
		var stx SignedTx
		if err := json.Unmarshal(signed(alg, 0), &stx); err != nil {
			f.Fatal(err)
		}
		if verdict, err := gv.HandleIncoming(pool, &stx); verdict != GossipAccepted {
			f.Fatalf("genuine %s seed rejected: %v", alg, err)
		}
	}
	for _, s := range []struct {
		raw   []byte
		pin   uint8
		signs bool
	}{
		{signed(SigEd25519, 0), fuzzPinNone, false},
		{signed(SigEd25519, 0), fuzzPinEd25519Same, false},
		{signed(SigEd25519, 0), fuzzPinEd25519Other, false},
		{signed(SigEd25519, 3), fuzzPinNone, false},
		{signed(SigEd25519, 9), fuzzPinNone, true},
		{signed(SigMLDSA, 0), fuzzPinNone, false},
		{signed(SigMLDSA, 0), fuzzPinMLDSA, false},
		{signed(SigMLDSA, 1), fuzzPinNone, true},
		{bytes.Replace(signed(SigEd25519, 0), []byte(`"ed25519"`), []byte(`"rsa"`), 1), fuzzPinNone, true},
		{[]byte(`{"tx":{"ID":"t","Sender":"s","Recipient":"r","Amount":1,"Fee":1},"algorithm":"ed25519"}`), fuzzPinNone, true},
		{[]byte(`{"tx":{},"signature":"AA==","public_key":"AA==","algorithm":"ml-dsa"}`), fuzzPinNone, false},
		{[]byte(`{"tx":null}`), fuzzPinNone, true},
	} {
		f.Add(s.raw, s.pin, s.signs)
	}

	f.Fuzz(func(t *testing.T, raw []byte, pin uint8, harnessSigns bool) {
		var stx SignedTx
		if json.Unmarshal(raw, &stx) != nil || stx.Tx == nil {
			return // not consumed as a signed transaction
		}
		if harnessSigns {
			keys.sign(&stx)
		}
		gv, pool, accounts := newGossip(pin)
		before := cloneSignedTx(&stx)
		sigErr := gv.sig.Verify(&stx)
		if !reflect.DeepEqual(before, cloneSignedTx(&stx)) {
			t.Fatal("SigVerifier.Verify modified the transaction")
		}
		verdict, err := gv.HandleIncoming(pool, &stx)
		inPool := pool.Size() == 1
		switch verdict {
		case GossipRejected:
			if inPool || gv.QuarantineSize() != 0 {
				t.Fatalf("a rejected transaction was kept (%v)", err)
			}
			return
		case GossipAccepted, GossipQuarantined:
		default:
			t.Fatalf("unknown verdict %q", verdict)
		}
		if sigErr != nil {
			t.Fatalf("admitted a transaction whose signature does not verify: %v", sigErr)
		}
		checkVerifiedSignedTx(t, gv.sig, &stx)
		tx := stx.Tx
		if !(tx.Fee >= DefaultGossipValidationConfig().MinFee) {
			t.Fatalf("admitted fee %v", tx.Fee)
		}
		acc, funded := accounts.Get(tx.Sender)
		var accountNonce uint64
		if funded {
			accountNonce = acc.Nonce
		}
		if verdict == GossipQuarantined {
			if inPool || gv.QuarantineSize() != 1 || tx.Nonce <= accountNonce ||
				tx.Nonce > accountNonce+DefaultGossipValidationConfig().MaxFutureNonce {
				t.Fatalf("quarantined nonce %d at account nonce %d", tx.Nonce, accountNonce)
			}
			return
		}
		if !funded || !inPool || tx.Nonce != acc.Nonce || tx.Recipient == "" || tx.Amount < 0 || tx.Amount+tx.Fee > acc.Balance {
			t.Fatalf("accepted %+v for account %+v", tx, acc)
		}
		if again, _ := gv.HandleIncoming(pool, cloneSignedTx(&stx)); again == GossipAccepted {
			t.Fatal("the same transaction was accepted twice")
		}
	})
}
