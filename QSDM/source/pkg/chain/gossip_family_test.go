package chain

import (
	"errors"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

// Private finding #3: tx gossip must refuse contract families that peers do
// not relay, before the tx reaches the mempool.
func TestGossipValidator_RefusesOtherContractFamilies(t *testing.T) {
	signer := newTestMLDSASigner(t)
	alice := signer.Address()
	as := NewAccountStore()
	as.Credit(alice, 100)
	pool := mempool.New(mempool.DefaultConfig())
	gv := NewGossipValidator(NewSigVerifier(), NewTxValidator(as), DefaultGossipValidationConfig())

	for i, cid := range []string{MiningRewardContractID, StreamContractID, RecoveryCapsuleContractID, "qsdm/slash/v1", "qsdm/gov/v1", "anything"} {
		stx := signer.Sign(t, &mempool.Tx{
			ID: "fam-" + cid, Sender: alice, Recipient: "bob", Amount: 1, Fee: 0.1, Nonce: uint64(i), ContractID: cid,
		})
		verdict, err := gv.HandleIncoming(pool, stx)
		if verdict != GossipRejected || !errors.Is(err, ErrGossipContractFamily) {
			t.Fatalf("%q: verdict %s err %v, want rejected with ErrGossipContractFamily", cid, verdict, err)
		}
	}
	if pool.Size() != 0 {
		t.Fatalf("pool size %d, want 0", pool.Size())
	}
	for _, cid := range []string{"", WalletTransferContractID, TaskContractID} {
		if !gossipFamilyAllowed(cid) {
			t.Fatalf("%q must stay admitted", cid)
		}
	}
}
