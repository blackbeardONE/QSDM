package chain

import (
	"reflect"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

// Every consensus-relevant field must change the digest. A field added to
// mempool.Tx and forgotten here is unbound -- the exact defect this file
// exists to fix, reintroduced one field at a time.
//
// The reflection check below makes that omission fail: it enumerates the
// struct's fields and requires each to be either exercised by a case here or
// named in the deliberate-exclusion list.
func TestTxContentDigest_BindsEveryConsensusField(t *testing.T) {
	base := &mempool.Tx{
		ID: "tx-1", Sender: "alice", Recipient: "bob",
		Amount: 10, Fee: 0.5, GasLimit: 21000, Nonce: 7,
		Payload: []byte("p"), ContractID: "qsdm/tasks/v1",
		Signature: "sig", PublicKey: "pub",
		AddedAt: time.Unix(1000, 0),
	}
	baseline := TxContentDigest(base)

	mutations := map[string]func(*mempool.Tx){
		"ID":         func(x *mempool.Tx) { x.ID = "tx-2" },
		"Sender":     func(x *mempool.Tx) { x.Sender = "mallory" },
		"Recipient":  func(x *mempool.Tx) { x.Recipient = "mallory" },
		"Amount":     func(x *mempool.Tx) { x.Amount = 10.0000001 },
		"Fee":        func(x *mempool.Tx) { x.Fee = 0.6 },
		"GasLimit":   func(x *mempool.Tx) { x.GasLimit = 21001 },
		"Nonce":      func(x *mempool.Tx) { x.Nonce = 8 },
		"Payload":    func(x *mempool.Tx) { x.Payload = []byte("q") },
		"ContractID": func(x *mempool.Tx) { x.ContractID = "qsdm/wallet-transfer/v1" },
		"Signature":  func(x *mempool.Tx) { x.Signature = "other" },
		"PublicKey":  func(x *mempool.Tx) { x.PublicKey = "other" },
	}
	// Node-local admission time. Including it would make the tx root differ
	// between nodes that saw the same block -- a fork, worse than the defect.
	excluded := map[string]bool{"AddedAt": true}

	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			x := *base
			mutate(&x)
			if TxContentDigest(&x) == baseline {
				t.Errorf("%s is not bound by the digest: it can be rewritten in flight", name)
			}
		})
	}

	// AddedAt must NOT change it.
	local := *base
	local.AddedAt = time.Unix(999999, 0)
	if TxContentDigest(&local) != baseline {
		t.Error("AddedAt changed the digest; it is node-local and would fork the tx root")
	}

	// The omission guard.
	tt := reflect.TypeOf(mempool.Tx{})
	for i := 0; i < tt.NumField(); i++ {
		f := tt.Field(i).Name
		if mutations[f] == nil && !excluded[f] {
			t.Errorf("mempool.Tx field %q is neither exercised nor deliberately excluded; "+
				"if it reached consensus unbound, contents could be rewritten in flight", f)
		}
	}
}

// The gate must be off by default and must change the root only at or above
// the activation height, or enabling it would rewrite historical block hashes.
func TestComputeTxRoot_ContentRootIsHeightGated(t *testing.T) {
	prev := TxContentRootActivationHeight()
	t.Cleanup(func() { SetTxContentRootActivationHeight(prev) })

	txs := []*mempool.Tx{
		{ID: "a", Sender: "s", Amount: 1},
		{ID: "b", Sender: "s", Amount: 2},
	}
	// Same IDs, different contents: the legacy root cannot tell these apart,
	// which is the defect in one line.
	rewritten := []*mempool.Tx{
		{ID: "a", Sender: "mallory", Amount: 1000},
		{ID: "b", Sender: "s", Amount: 2},
	}

	SetTxContentRootActivationHeight(0)
	if computeTxRoot(txs, 5) != computeTxRoot(rewritten, 5) {
		t.Error("with the gate off the roots should match; the legacy root binds IDs only")
	}

	SetTxContentRootActivationHeight(100)
	if computeTxRoot(txs, 99) != computeTxRoot(rewritten, 99) {
		t.Error("below the activation height the legacy root must still apply")
	}
	if computeTxRoot(txs, 100) == computeTxRoot(rewritten, 100) {
		t.Error("at the activation height rewritten contents must change the root")
	}
	if computeTxRoot(txs, 101) == computeTxRoot(rewritten, 101) {
		t.Error("above the activation height rewritten contents must change the root")
	}

	// Historical hashes must not move when the gate is enabled.
	SetTxContentRootActivationHeight(0)
	legacy := computeTxRoot(txs, 42)
	SetTxContentRootActivationHeight(100)
	if computeTxRoot(txs, 42) != legacy {
		t.Error("enabling the gate changed a below-activation root; that would invalidate history")
	}
}

// Block validation during propagation must derive the SAME hash the producer
// signed, at every height on both sides of the activation boundary.
//
// recomputeHash (propagation.go) used to carry its own copy of the hash
// derivation, including its own tx-root loop over tx.ID. When computeTxRoot
// gained the height gate, that copy did not follow. Activating the gate would
// then have made every node reject every valid block at or above the
// activation height -- a network-wide propagation halt caused by the mechanism
// meant to close a consensus defect. Review found it by computing both hashes;
// nothing in the suite compared them.
func TestRecomputeHash_MatchesTheProducerHashAcrossActivation(t *testing.T) {
	prev := TxContentRootActivationHeight()
	t.Cleanup(func() { SetTxContentRootActivationHeight(prev) })

	mk := func(height uint64) *Block {
		return &Block{
			Height: height, PrevHash: "prev", StateRoot: "sr",
			ProducerID: "producer", Timestamp: time.Unix(1700000000, 0).UTC(),
			Transactions: []*mempool.Tx{
				{ID: "a", Sender: "s1", Recipient: "r1", Amount: 1, Nonce: 1},
				{ID: "b", Sender: "s2", Recipient: "r2", Amount: 2, Nonce: 2},
			},
		}
	}

	for _, activation := range []uint64{0, 100} {
		SetTxContentRootActivationHeight(activation)
		for _, height := range []uint64{1, 99, 100, 101} {
			b := mk(height)
			canonical := computeBlockHash(b)
			validated := recomputeHash(b)
			if canonical != validated {
				t.Errorf("activation=%d height=%d: the propagation validator derives a different "+
					"hash than the producer signed -- every node would reject this block\n"+
					"  producer  %s\n  validator %s", activation, height, canonical, validated)
			}
		}
	}
}
