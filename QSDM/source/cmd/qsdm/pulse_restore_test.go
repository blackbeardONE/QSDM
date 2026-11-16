package main

import (
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/blackbeardONE/QSDM/internal/pulse"
	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

func pulseJoinTx(t *testing.T, k *pulse.KeyPair, pool string, nonce uint64, id string) *mempool.Tx {
	t.Helper()
	payload, _ := json.Marshal(chain.PulseAction{Op: "join", Pool: pool})
	tx := &mempool.Tx{ID: id, Sender: string(k.ID()), Nonce: nonce, ContractID: chain.PulseContractID,
		Payload: payload, PublicKey: hex.EncodeToString(k.Pub.Bytes())}
	sig, err := k.Sign("", chain.PulseTxSigningBytes(tx))
	if err != nil {
		t.Fatal(err)
	}
	tx.Signature = hex.EncodeToString(sig)
	return tx
}

// The boot restore and the per-block snapshot scan replay PULSE txs next to the QSDM-20 state and reproduce the
// published state root, PULSE state included.
func TestEvaluatePersistedStateRestoresPulse(t *testing.T) {
	poolKey, _ := pulse.NewKeyPair()
	m1, _ := pulse.NewKeyPair()
	m2, _ := pulse.NewKeyPair()
	pool := string(poolKey.ID())
	if err := chain.SetPulseParams(chain.PulseParams{ActivationHeight: 5, PayeeEpochCapDust: 1 << 60, ReclaimAfterBlocks: 100,
		Pools: []pulse.PoolEntry{{ID: poolKey.ID(), PublicKey: hex.EncodeToString(poolKey.Pub.Bytes())}}}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = chain.SetPulseParams(chain.PulseParams{}) })

	accounts := chain.NewAccountStore()
	height := uint64(5)
	live := liveQSDM20Applier(accounts, &height)
	live.SetPulseStateStore(chain.NewPulseStateStore())
	join1 := pulseJoinTx(t, m1, pool, 0, "join-1")
	if err := live.ApplyTx(join1); err != nil {
		t.Fatal(err)
	}
	snapshotAfterFirst := accounts.Clone()
	block5 := &chain.Block{Height: 5, Transactions: []*mempool.Tx{join1}, StateRoot: live.StateRoot()}
	height = 6
	join2 := pulseJoinTx(t, m2, pool, 0, "join-2")
	if err := live.ApplyTx(join2); err != nil {
		t.Fatal(err)
	}
	block6 := &chain.Block{Height: 6, Transactions: []*mempool.Tx{join2}, StateRoot: live.StateRoot()}
	blocks := []*chain.Block{block5, block6}

	restored, err := evaluatePersistedState(accounts.Clone(), blocks)
	if err != nil {
		t.Fatalf("evaluate persisted PULSE state: %v", err)
	}
	if restored.stateRoot != block6.StateRoot || restored.pulseState == nil || restored.pulseState.Count() == 0 {
		t.Fatalf("restored root %s (want %s), pulse state %v", restored.stateRoot, block6.StateRoot, restored.pulseState)
	}
	if restored.pulseState.StateRoot() != live.PulseStateStore().StateRoot() {
		t.Fatal("restored PULSE root differs from the live one")
	}
	if idx, err := findPersistedSnapshotBlock(snapshotAfterFirst, blocks); err != nil || idx != 0 {
		t.Fatalf("snapshot scan: index %d, %v", idx, err)
	}
}
