package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/cloudflare/circl/sign/mldsa/mldsa87"
)

type qsdm20ReplayWallet struct {
	private *mldsa87.PrivateKey
	pub     []byte
	address string
}

func newQSDM20ReplayWallet(t *testing.T) qsdm20ReplayWallet {
	t.Helper()
	public, private, err := mldsa87.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pub, _ := public.MarshalBinary()
	sum := sha256.Sum256(pub)
	return qsdm20ReplayWallet{private: private, pub: pub, address: hex.EncodeToString(sum[:])}
}

func (w qsdm20ReplayWallet) tx(t *testing.T, nonce uint64, a chain.QSDM20Action) *mempool.Tx {
	t.Helper()
	payload, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	digest := chain.QSDM20SigningDigest(w.address, nonce, 0, a)
	sig := make([]byte, mldsa87.SignatureSize)
	if err := mldsa87.SignTo(w.private, digest, nil, true, sig); err != nil {
		t.Fatal(err)
	}
	return &mempool.Tx{
		ID: chain.QSDM20TxID(digest), Sender: w.address, Nonce: nonce, Payload: payload,
		ContractID: chain.QSDM20ContractID, Signature: hex.EncodeToString(sig), PublicKey: hex.EncodeToString(w.pub),
	}
}

func setQSDM20ActivationForReplayTest(t *testing.T, h uint64) {
	t.Helper()
	prev := chain.QSDM20ActivationHeight()
	chain.SetQSDM20ActivationHeight(h)
	t.Cleanup(func() { chain.SetQSDM20ActivationHeight(prev) })
}

// liveQSDM20Applier is the applier main() runs, with the token store.
func liveQSDM20Applier(accounts *chain.AccountStore, height *uint64) *chain.EnrollmentAwareApplier {
	aware := chain.NewEnrollmentAwareApplier(accounts, nil)
	aware.SetTaskStateStore(chain.NewTaskStateStore())
	aware.SetStreamStateStore(chain.NewStreamStateStore())
	aware.SetRecoveryCapsuleStateStore(chain.NewRecoveryCapsuleStateStore())
	aware.SetQSDM20StateStore(chain.NewQSDM20StateStore())
	aware.SetHeightFn(func() uint64 { return *height })
	return aware
}

// The boot restore replays token transactions from the journal and
// reproduces the published state root, token state included.
func TestEvaluatePersistedStateRestoresQSDM20Tokens(t *testing.T) {
	setQSDM20ActivationForReplayTest(t, 5)
	alice, bob := newQSDM20ReplayWallet(t), newQSDM20ReplayWallet(t)
	accounts := chain.NewAccountStore()
	accounts.Credit(alice.address, 1)
	height := uint64(5)
	live := liveQSDM20Applier(accounts, &height)

	create := alice.tx(t, 0, chain.QSDM20Action{Action: chain.QSDM20ActionCreate, Symbol: "BOOT", Name: "Boot",
		Decimals: chain.QSDM20Decimals(2), MaxSupply: "100", InitialSupply: "100", Mint: chain.QSDM20MintNone})
	if err := live.ApplyTx(create); err != nil {
		t.Fatal(err)
	}
	snapshotAfterFirst := accounts.Clone()
	block5 := &chain.Block{Height: 5, Transactions: []*mempool.Tx{create}, StateRoot: live.StateRoot()}
	id := chain.QSDM20TokenID(alice.address, 0)
	height = 6
	transfer := alice.tx(t, 1, chain.QSDM20Action{Action: chain.QSDM20ActionTransfer, Token: id, To: bob.address, Amount: "40"})
	if err := live.ApplyTx(transfer); err != nil {
		t.Fatal(err)
	}
	block6 := &chain.Block{Height: 6, Transactions: []*mempool.Tx{transfer}, StateRoot: live.StateRoot()}
	blocks := []*chain.Block{block5, block6}

	restored, err := evaluatePersistedState(accounts.Clone(), blocks)
	if err != nil {
		t.Fatalf("evaluate persisted token state: %v", err)
	}
	if restored.stateRoot != block6.StateRoot || restored.qsdm20Actions != 2 {
		t.Fatalf("restored root %s (want %s), %d token actions", restored.stateRoot, block6.StateRoot, restored.qsdm20Actions)
	}
	tok, ok := restored.qsdm20State.Token(id)
	if !ok || tok.CreatedHeight != 5 || tok.Holders != 2 || tok.Supply != "100" {
		t.Fatalf("restored token: %+v %v", tok, ok)
	}
	if restored.qsdm20State.StateRoot() != live.QSDM20StateStore().StateRoot() {
		t.Fatal("restored token root differs from the live one")
	}

	// An account snapshot one block behind the journal is found at block 5
	// by the per-block scan, token state included.
	if idx, err := findPersistedSnapshotBlock(snapshotAfterFirst, blocks); err != nil || idx != 0 {
		t.Fatalf("snapshot scan: index %d, %v", idx, err)
	}

	// A node whose activation height does not match the chain refuses to
	// restore it instead of building a different state. Switched off, it
	// replays no token transaction, so its root is not the journal's and
	// no block matches the account snapshot; with a later height the
	// transfer names a token it never made.
	chain.SetQSDM20ActivationHeight(0)
	off, err := evaluatePersistedState(accounts.Clone(), blocks)
	if err != nil || off.stateRoot == block6.StateRoot || off.qsdm20Actions != 0 {
		t.Fatalf("restore with tokens switched off: root %s, %d token actions, %v", off.stateRoot, off.qsdm20Actions, err)
	}
	chainPath := filepath.Join(t.TempDir(), "chain.ndjson")
	if _, err := reconcilePersistedStateTail(chainPath, accounts.Clone(), blocks, time.Now()); err == nil {
		t.Fatal("restore with tokens switched off was accepted")
	}
	if _, err := os.Stat(chainPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("refused restore wrote the journal: %v", err)
	}
	chain.SetQSDM20ActivationHeight(6)
	if _, err := evaluatePersistedState(accounts.Clone(), blocks); !errors.Is(err, chain.ErrQSDM20UnknownToken) {
		t.Fatalf("restore with a later activation height: %v", err)
	}
}

// A journal whose block below the activation height carries a
// qsdm/token/v1 transaction, applied by a node without QSDM-20 as a plain
// CELL transfer, restores exactly as before: the published root, no token
// actions and no token state, whether tokens are off or start later.
func TestEvaluatePersistedStateBelowActivationAsBefore(t *testing.T) {
	alice := newQSDM20ReplayWallet(t)
	tx := alice.tx(t, 0, chain.QSDM20Action{Action: chain.QSDM20ActionCreate, Symbol: "OLD", Name: "Old node",
		Decimals: chain.QSDM20Decimals(0), MaxSupply: "1", InitialSupply: "1", Mint: chain.QSDM20MintNone})
	accounts := chain.NewAccountStore()
	accounts.Credit(alice.address, 1)
	if err := accounts.ApplyTx(tx); err != nil { // what a node without QSDM-20 does
		t.Fatal(err)
	}
	height := uint64(3)
	block := &chain.Block{Height: 3, Transactions: []*mempool.Tx{tx}, StateRoot: liveQSDM20Applier(accounts, &height).StateRoot()}
	for _, activation := range []uint64{0, 4, 1 << 40} {
		setQSDM20ActivationForReplayTest(t, activation)
		restored, err := evaluatePersistedState(accounts.Clone(), []*chain.Block{block})
		if err != nil {
			t.Fatalf("activation %d: %v", activation, err)
		}
		if restored.stateRoot != block.StateRoot || restored.qsdm20Actions != 0 || restored.qsdm20State.Count() != 0 {
			t.Fatalf("activation %d: restored %+v", activation, restored)
		}
	}
}

// A journal with no token transactions restores exactly as before: no
// token actions, an empty token state and the published root.
func TestEvaluatePersistedStateWithoutTokensUnchanged(t *testing.T) {
	setQSDM20ActivationForReplayTest(t, 0)
	accounts := chain.NewAccountStore()
	accounts.Credit("alice", 10)
	height := uint64(1)
	live := liveQSDM20Applier(accounts, &height)
	tx := &mempool.Tx{ID: "plain-1", Sender: "alice", Recipient: "bob", Amount: 2, Fee: 0.5, Nonce: 0}
	if err := live.ApplyTx(tx); err != nil {
		t.Fatal(err)
	}
	block := &chain.Block{Height: 1, Transactions: []*mempool.Tx{tx}, StateRoot: accounts.StateRoot()}
	restored, err := evaluatePersistedState(accounts.Clone(), []*chain.Block{block})
	if err != nil {
		t.Fatal(err)
	}
	if restored.stateRoot != block.StateRoot || restored.qsdm20Actions != 0 || restored.qsdm20State.Count() != 0 {
		t.Fatalf("restored %+v", restored)
	}
}
