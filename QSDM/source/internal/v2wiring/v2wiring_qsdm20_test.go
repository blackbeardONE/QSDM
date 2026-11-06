package v2wiring_test

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/internal/v2wiring"
	"github.com/blackbeardONE/QSDM/pkg/api"
	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/cloudflare/circl/sign/mldsa/mldsa87"
)

type qsdm20WireWallet struct {
	private *mldsa87.PrivateKey
	pub     []byte
	address string
}

func newQSDM20WireWallet(t *testing.T) qsdm20WireWallet {
	t.Helper()
	public, private, err := mldsa87.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pub, _ := public.MarshalBinary()
	sum := sha256.Sum256(pub)
	return qsdm20WireWallet{private: private, pub: pub, address: hex.EncodeToString(sum[:])}
}

func (w qsdm20WireWallet) tx(t *testing.T, nonce uint64, a chain.QSDM20Action) *mempool.Tx {
	t.Helper()
	sig := make([]byte, mldsa87.SignatureSize)
	if err := mldsa87.SignTo(w.private, chain.QSDM20SigningDigest(w.address, nonce, 0, a), nil, true, sig); err != nil {
		t.Fatal(err)
	}
	tx, err := api.QSDM20TxFromRequest(api.QSDM20SubmitRequest{
		Sender: w.address, Nonce: nonce, Payload: a,
		PublicKey: hex.EncodeToString(w.pub), Signature: hex.EncodeToString(sig),
	})
	if err != nil {
		t.Fatal(err)
	}
	return tx
}

// Wire installs the token store on the applier, the QSDM-20 admission layer
// (judged at the producer's next block) and the API; ReinstallAdmissionGate
// keeps the layer and its height source.
func TestWire_QSDM20(t *testing.T) {
	r := buildRig(t, 20)
	prev := chain.QSDM20ActivationHeight()
	t.Cleanup(func() {
		chain.SetQSDM20ActivationHeight(prev)
		api.SetQSDM20Mempool(nil)
		api.SetQSDM20StateProvider(nil)
		api.SetQSDM20TxChecker(nil)
	})
	if r.w.QSDM20State == nil || r.w.Aware.QSDM20StateStore() != r.w.QSDM20State {
		t.Fatal("Wire did not install the QSDM-20 token store on the applier")
	}

	issuer := newQSDM20WireWallet(t)
	r.accounts.Credit(issuer.address, 1)
	create := issuer.tx(t, 0, chain.QSDM20Action{Action: chain.QSDM20ActionCreate, Symbol: "WIRE", Name: "Wired",
		Decimals: chain.QSDM20Decimals(0), MaxSupply: "10", InitialSupply: "10", Mint: chain.QSDM20MintNone})

	chain.SetQSDM20ActivationHeight(0)
	if err := r.pool.Add(create); err == nil || !strings.Contains(err.Error(), "not active") {
		t.Fatalf("switched off, the pool admitted a token transaction: %v", err)
	}

	// Seal block 0 so the next block is 1, then switch tokens on from 1.
	if err := r.pool.Add(&mempool.Tx{ID: "wire-plain", Sender: tAlice, Recipient: "bob", Amount: 1, Fee: 0.001, Nonce: 0}); err != nil {
		t.Fatal(err)
	}
	produce(t, r)
	chain.SetQSDM20ActivationHeight(2)
	if err := r.pool.Add(create); err == nil || !strings.Contains(err.Error(), "below the activation height") {
		t.Fatalf("next block 1 is below 2: %v", err)
	}
	chain.SetQSDM20ActivationHeight(1)
	if err := r.pool.Add(create); err != nil {
		t.Fatalf("active at the next block: %v", err)
	}
	blk := produce(t, r)
	if len(blk.Transactions) != 1 || blk.Transactions[0].ID != create.ID {
		t.Fatalf("block did not carry the token transaction: %+v", blk.Transactions)
	}
	id := chain.QSDM20TokenID(issuer.address, 0)
	if tok, ok := r.w.QSDM20State.Token(id); !ok || tok.CreatedHeight != 1 {
		t.Fatalf("token after the block: %+v %v", tok, ok)
	}

	// ReinstallAdmissionGate (what main() calls) keeps the layer and the
	// producer's height source.
	v2wiring.ReinstallAdmissionGate(r.pool, nil)
	burn := issuer.tx(t, 1, chain.QSDM20Action{Action: chain.QSDM20ActionBurn, Token: id, Amount: "1"})
	chain.SetQSDM20ActivationHeight(3)
	if err := r.pool.Add(burn); err == nil || !strings.Contains(err.Error(), "below the activation height") {
		t.Fatalf("after reinstall, next block 2 is below 3: %v", err)
	}
	chain.SetQSDM20ActivationHeight(2)
	if err := r.pool.Add(burn); err != nil {
		t.Fatalf("after reinstall, active at the next block: %v", err)
	}

	// A pool Wire never configured has no height source: fail closed.
	other := mempool.New(mempool.DefaultConfig())
	v2wiring.ReinstallAdmissionGate(other, nil)
	if err := other.Add(burn); err == nil {
		t.Fatal("a pool with no height source admitted a token transaction")
	}
}
