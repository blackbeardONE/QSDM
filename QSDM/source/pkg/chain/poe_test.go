package chain

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/poe"
	"github.com/blackbeardONE/QSDM/pkg/wallet"
)

// ---------------------------------------------------------------------------
// Harness: wallets, nodes, blocks
// ---------------------------------------------------------------------------

const (
	poeTestFunder    = "poe-test-funder-0000000000000000"
	poeTestRecipient = "abababababababababababababababababababababababababababababababab"
)

// withPoEActivation sets the package-level activation height for one test.
func withPoEActivation(t testing.TB, h uint64) {
	t.Helper()
	prev := PoEActivationHeight()
	SetPoEActivationHeight(h)
	t.Cleanup(func() { SetPoEActivationHeight(prev) })
}

// poeWallet is a self-custody ML-DSA-87 wallet.
type poeWallet struct {
	ws   *wallet.WalletService
	addr string
}

func newPoEWallet(t testing.TB) *poeWallet {
	t.Helper()
	ws, err := wallet.NewWalletService()
	if err != nil {
		t.Fatalf("NewWalletService: %v", err)
	}
	return &poeWallet{ws: ws, addr: ws.GetAddress()}
}

// signEnvelope signs env with w's key and returns the wallet-transfer
// mempool transaction that carries it.
func (w *poeWallet) signEnvelope(t testing.TB, env wallet.TransactionData) *mempool.Tx {
	t.Helper()
	canonical, err := env.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	sig, err := w.ws.SignData(canonical)
	if err != nil {
		t.Fatal(err)
	}
	env.Signature = hex.EncodeToString(sig)
	payload, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	return &mempool.Tx{
		ID: env.ID, Sender: env.Sender, Recipient: env.Recipient,
		Amount: env.Amount, Fee: env.Fee, Nonce: env.Nonce - 1,
		ContractID: WalletTransferContractID, Payload: payload,
		Signature: env.Signature, PublicKey: env.PublicKey,
	}
}

// transfer returns a signed transfer of 1 CELL from w with the given
// envelope nonce (the account's committed nonce + 1) and parents.
func (w *poeWallet) transfer(t testing.TB, id string, nonce uint64, fee float64, parents []string) *mempool.Tx {
	t.Helper()
	return w.signEnvelope(t, wallet.TransactionData{
		ID:          id,
		Sender:      w.addr,
		Recipient:   poeTestRecipient,
		Amount:      1,
		Fee:         fee,
		ParentCells: parents,
		Nonce:       nonce,
		PublicKey:   hex.EncodeToString(w.ws.GetPublicKey()),
		Timestamp:   time.Unix(1_790_000_000, 0).UTC().Format(time.RFC3339),
	})
}

// poeNode is one validator: its own account state, mempool and chain.
type poeNode struct {
	accounts *AccountStore
	aware    *EnrollmentAwareApplier
	pool     *mempool.Mempool
	bp       *BlockProducer
	receipts *ReceiptStore
}

// newPoENode builds a validator whose pre-genesis state is identical on
// every node: the funder and each wallet credited the same amounts.
func newPoENode(wallets ...*poeWallet) *poeNode {
	accounts := NewAccountStore()
	accounts.Credit(poeTestFunder, 1)
	for _, w := range wallets {
		accounts.Credit(w.addr, 1000)
	}
	aware := NewEnrollmentAwareApplier(accounts, nil)
	pool := mempool.New(mempool.DefaultConfig())
	bp := NewBlockProducer(pool, aware, DefaultProducerConfig())
	rs := NewReceiptStore()
	bp.SetAppendReceiptStore(rs)
	return &poeNode{accounts: accounts, aware: aware, pool: pool, bp: bp, receipts: rs}
}

// heartbeat is the producer's per-block system transaction (a funder
// self-transfer of 0), with a deterministic ID.
func (n *poeNode) heartbeat() *mempool.Tx {
	acc, _ := n.accounts.Get(poeTestFunder)
	return &mempool.Tx{
		ID:        fmt.Sprintf("solo-heartbeat-%d-%d", acc.Nonce, 1_790_000_000_000_000_000+acc.Nonce),
		Sender:    poeTestFunder,
		Recipient: poeTestFunder,
		Nonce:     acc.Nonce,
	}
}

// nonceOf returns the next envelope nonce for w on this node.
func (n *poeNode) nonceOf(w *poeWallet) uint64 {
	acc, _ := n.accounts.Get(w.addr)
	return acc.Nonce + 1
}

// produce seals one block holding a heartbeat plus txs.
func (n *poeNode) produce(t testing.TB, txs ...*mempool.Tx) *Block {
	t.Helper()
	if err := n.pool.Add(n.heartbeat()); err != nil {
		t.Fatalf("pool heartbeat: %v", err)
	}
	for _, tx := range txs {
		if err := n.pool.Add(tx); err != nil {
			t.Fatalf("pool add %s: %v", tx.ID, err)
		}
	}
	blk, err := n.bp.ProduceBlock()
	if err != nil {
		t.Fatalf("ProduceBlock: %v", err)
	}
	return blk
}

// tip returns the node's chain tip.
func (n *poeNode) tip(t testing.TB) *Block {
	t.Helper()
	blk, ok := n.bp.LatestBlock()
	if !ok {
		t.Fatal("no tip")
	}
	return blk
}

// recentIDs returns the IDs of the newest committed transactions (parents a
// wallet would fetch from GET /api/v1/chain/parents).
func (n *poeNode) recentIDs(t testing.TB, k int) []string {
	t.Helper()
	refs := n.bp.PoEParentCandidates(n.bp.TipHeight(), k)
	if len(refs) < k {
		t.Fatalf("only %d parent candidates", len(refs))
	}
	ids := make([]string, k)
	for i := range ids {
		ids[i] = refs[i].ID
	}
	return ids
}

// failedReceipt returns the error of tx's TxFailed receipt.
func (n *poeNode) failedReceipt(t testing.TB, id string) string {
	t.Helper()
	r, ok := n.receipts.Get(id)
	if !ok {
		t.Fatalf("no receipt for %s", id)
	}
	if r.Status != ReceiptFailed {
		t.Fatalf("receipt for %s is not failed: %+v", id, r)
	}
	return r.Error
}

// included reports whether a transaction ID is in blk.
func included(blk *Block, id string) bool {
	for _, tx := range blk.Transactions {
		if tx != nil && tx.ID == id {
			return true
		}
	}
	return false
}

// craftBlock builds a hash-valid block extending tip whose transactions are
// applied to a clone of state with no PoE check at all: what a malicious or
// outdated producer could broadcast. The state root is correct, so only the
// PoE rules can reject it.
func craftBlock(t testing.TB, tip *Block, state ChainReplayApplier, txs ...*mempool.Tx) *Block {
	t.Helper()
	h := tip.Height + 1
	spec := state.ChainReplayClone()
	setStateRootHeight(spec, h)
	for _, tx := range txs {
		if err := spec.ApplyTx(tx); err != nil {
			t.Fatalf("craft: apply %s: %v", tx.ID, err)
		}
	}
	setStateRootHeight(spec, h)
	blk := &Block{
		Height:       h,
		PrevHash:     tip.Hash,
		Timestamp:    tip.Timestamp.Add(10 * time.Second),
		Transactions: txs,
		StateRoot:    spec.StateRoot(),
		ProducerID:   "crafted-producer",
	}
	blk.Hash = computeBlockHash(blk)
	return blk
}

// heartbeatFor returns the heartbeat a node at state n would put in its next
// block, for crafted blocks.
func heartbeatFor(n *poeNode) *mempool.Tx { return n.heartbeat() }

// sameState fails unless every node has the same tip hash and state root.
func sameState(t testing.TB, nodes ...*poeNode) {
	t.Helper()
	ref := nodes[0].tip(t)
	for i, n := range nodes[1:] {
		got := n.tip(t)
		if got.Height != ref.Height || got.Hash != ref.Hash || got.StateRoot != ref.StateRoot {
			t.Fatalf("node %d diverged: height %d hash %s root %s, want height %d hash %s root %s",
				i+1, got.Height, got.Hash, got.StateRoot, ref.Height, ref.Hash, ref.StateRoot)
		}
		if root := n.aware.StateRoot(); root != ref.StateRoot {
			t.Fatalf("node %d live state root %s, want %s", i+1, root, ref.StateRoot)
		}
	}
}

// replayAll appends blocks to n in order.
func replayAll(t testing.TB, n *poeNode, blocks []*Block) {
	t.Helper()
	for _, b := range blocks {
		if err := n.bp.TryAppendExternalBlock(b); err != nil {
			t.Fatalf("replay height %d: %v", b.Height, err)
		}
	}
}

// ---------------------------------------------------------------------------
// The rule, the window and the index
// ---------------------------------------------------------------------------

func TestPoEActivationGate(t *testing.T) {
	withPoEActivation(t, 0)
	if PoEActiveAt(0) || PoEActiveAt(1<<62) {
		t.Fatal("zero activation height must never activate")
	}
	withPoEActivation(t, 100)
	if PoEActiveAt(99) || !PoEActiveAt(100) || !PoEActiveAt(101) {
		t.Fatal("activation must start exactly at the configured height")
	}
}

func TestCheckWalletTransferParents_Rules(t *testing.T) {
	a, b, c := "committed-transaction-a", "committed-transaction-b", "same-block-earlier-c"
	later := "same-block-later-d0000"
	committed := map[string]bool{a: true, b: true, "committed-dup-id-0000": true}
	earlier := map[string]bool{c: true}
	laterSet := map[string]bool{later: true}
	lk := poeLookup{
		committed: func(id string) bool { return committed[id] },
		earlier:   func(id string) bool { return earlier[id] },
		later:     func(id string) bool { return laterSet[id] },
	}
	cases := []struct {
		name    string
		id      string
		parents []string
		want    error
	}{
		{"committed-parents", "new-transfer-0000001", []string{a, b}, nil},
		{"earlier-in-block", "new-transfer-0000002", []string{a, c}, nil},
		{"missing-parent", "new-transfer-0000003", []string{a, "never-existed-000000"}, poe.ErrParentUnknown},
		{"future-parent", "new-transfer-0000004", []string{a, later}, poe.ErrParentNotEarlier},
		{"duplicate-parent", "new-transfer-0000005", []string{a, a}, poe.ErrDuplicateParent},
		{"self-parent", "new-transfer-0000006", []string{a, "new-transfer-0000006"}, poe.ErrSelfParent},
		{"no-parents", "new-transfer-0000007", nil, poe.ErrParentCount},
		{"one-parent", "new-transfer-0000008", []string{a}, poe.ErrParentCount},
		{"placeholder", "new-transfer-0000009", []string{"parent1", "parent2"}, poe.ErrParentFormat},
		{"reused-committed-id", "committed-dup-id-0000", []string{a, b}, poe.ErrDuplicateTxID},
		{"reused-earlier-id", c, []string{a, b}, poe.ErrDuplicateTxID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkWalletTransferParents(tc.id, tc.parents, lk)
			if tc.want == nil {
				if err != nil {
					t.Fatalf("err = %v", err)
				}
				return
			}
			if !errors.Is(err, tc.want) || !poe.IsViolation(err) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

// syntheticChain returns n contiguous blocks from genesis, block h holding
// one transaction "tx-at-height-<h>".
func syntheticChain(n int) []*Block {
	blocks := make([]*Block, n)
	prev := ""
	for h := 0; h < n; h++ {
		b := &Block{
			Height:       uint64(h),
			PrevHash:     prev,
			Timestamp:    time.Unix(1_790_000_000+int64(h)*10, 0).UTC(),
			Transactions: []*mempool.Tx{{ID: fmt.Sprintf("tx-at-height-%010d", h)}},
		}
		b.Hash = computeBlockHash(b)
		prev = b.Hash
		blocks[h] = b
	}
	return blocks
}

// The reference window is exactly ParentWindowBlocks blocks, evicts in
// height order, and an ID re-committed later stays referenceable until its
// latest occurrence ages out.
func TestPoEHistoryWindow(t *testing.T) {
	const W = poe.ParentWindowBlocks
	blocks := syntheticChain(W + 5)
	var h poeHistory
	h.rebuild(blocks[:W+1]) // tip W; a transfer in block W+1 may name heights 1..W
	id := func(height int) string { return fmt.Sprintf("tx-at-height-%010d", height) }
	if h.committed(id(0)) {
		t.Fatal("height 0 is outside the window of block W+1")
	}
	if !h.committed(id(1)) || !h.committed(id(W)) {
		t.Fatal("window edges must be referenceable")
	}
	if !h.readyForHeight(uint64(W+1)) || h.readyForHeight(uint64(W)) {
		t.Fatal("readiness must track the tip")
	}
	// Folding one more block evicts exactly one height.
	h.fold(blocks[W+1])
	if h.committed(id(1)) || !h.committed(id(2)) || !h.committed(id(W+1)) {
		t.Fatal("fold must evict the oldest height only")
	}
	if got := h.size(); got != W {
		t.Fatalf("index holds %d IDs, want %d", got, W)
	}
	// A block that does not extend the tip marks the index stale.
	h.fold(blocks[W+3])
	if h.readyForHeight(uint64(W+4)) || h.readyForHeight(uint64(W+2)) {
		t.Fatal("a gap must leave the index not ready")
	}
	// Incremental folding equals a rebuild over the same blocks.
	var inc, full poeHistory
	inc.rebuild(blocks[:10])
	for _, b := range blocks[10 : W+4] {
		inc.fold(b)
	}
	full.rebuild(blocks[:W+4])
	if inc.size() != full.size() || inc.tip != full.tip {
		t.Fatalf("incremental index (%d ids, tip %d) != rebuild (%d ids, tip %d)", inc.size(), inc.tip, full.size(), full.tip)
	}
	for id := range full.byID {
		if !inc.committed(id) {
			t.Fatalf("incremental index misses %s", id)
		}
	}
}

func TestPoEHistoryDuplicateIDsAgeOutByLatestOccurrence(t *testing.T) {
	const W = poe.ParentWindowBlocks
	blocks := syntheticChain(W + 3)
	// The same ID at heights 1 and 3.
	dup := "duplicate-id-0000000"
	blocks[1].Transactions = []*mempool.Tx{{ID: dup}}
	blocks[3].Transactions = []*mempool.Tx{{ID: dup}}
	var h poeHistory
	h.rebuild(blocks[:W+2]) // evicts heights 0 and 1
	if !h.committed(dup) {
		t.Fatal("the later occurrence (height 3) must keep the ID referenceable")
	}
	h.fold(blocks[W+2]) // evicts height 2 only
	if !h.committed(dup) {
		t.Fatal("still inside the window at height 3")
	}
}

// ---------------------------------------------------------------------------
// Production, validation and replay
// ---------------------------------------------------------------------------

// Below the activation height (and with no height at all) transfers without
// parents, or with placeholder parents, are produced and replayed exactly as
// before.
func TestPoEInactive_HistoricalShapesStillValid(t *testing.T) {
	for _, activation := range []uint64{0, 1_000_000} {
		t.Run(fmt.Sprintf("activation-%d", activation), func(t *testing.T) {
			withPoEActivation(t, activation)
			alice := newPoEWallet(t)
			prod := newPoENode(alice)
			prod.produce(t) // genesis
			txs := []*mempool.Tx{
				alice.transfer(t, "hive_wallet_1784876606130_1a4983ed8ce85b54", 1, 0.01, []string{}),
				alice.transfer(t, "placeholder-parents-000001", 2, 0.01, []string{"parent1", "parent2"}),
			}
			blk := prod.produce(t, txs...)
			for _, tx := range txs {
				if !included(blk, tx.ID) {
					t.Fatalf("%s excluded below activation: %s", tx.ID, prod.failedReceipt(t, tx.ID))
				}
			}
			follower := newPoENode(alice)
			replayAll(t, follower, prod.bp.AllBlocks())
			sameState(t, prod, follower)
		})
	}
}

// Every rule, at production: a failing transfer is dropped with a TxFailed
// receipt naming the rule, never reaches account state, and the block still
// seals with everything else.
func TestPoEProduction_DropsEachViolation(t *testing.T) {
	withPoEActivation(t, 3)
	w := make([]*poeWallet, 10)
	for i := range w {
		w[i] = newPoEWallet(t)
	}
	prod := newPoENode(w...)
	for i := 0; i < 3; i++ {
		prod.produce(t) // heights 0..2
	}
	// [heartbeat@2, heartbeat@1, heartbeat@0]
	recent := prod.recentIDs(t, 3)
	parents := recent[:2]
	n := func(i int) uint64 { return prod.nonceOf(w[i]) }

	valid := w[0].transfer(t, "valid-transfer-00000001", n(0), 0.05, parents)
	// Higher fee: drains and applies before chainChild, which names it.
	chainParent := w[1].transfer(t, "intra-block-parent-0001", n(1), 0.09, parents)
	chainChild := w[2].transfer(t, "intra-block-child-00001", n(2), 0.08, []string{parents[0], chainParent.ID})
	// Lower fee than the transfer it names: that one is later in the block.
	futureRef := w[3].transfer(t, "forward-reference-00001", n(3), 0.07, []string{parents[0], "sits-later-in-block-001"})
	sitsLater := w[4].transfer(t, "sits-later-in-block-001", n(4), 0.01, parents)
	missing := w[5].transfer(t, "missing-parent-00000001", n(5), 0.05, []string{parents[0], "never-committed-0000001"})
	dupParent := w[6].transfer(t, "duplicate-parent-000001", n(6), 0.05, []string{parents[0], parents[0]})
	selfParent := w[7].transfer(t, "self-parent-00000000001", n(7), 0.05, []string{parents[0], "self-parent-00000000001"})
	noParents := w[8].transfer(t, "no-parents-000000000001", n(8), 0.05, []string{})
	// Reuses heartbeat@1's ID while naming other committed parents.
	reusedID := w[9].transfer(t, recent[1], n(9), 0.05, []string{recent[0], recent[2]})

	blk := prod.produce(t, valid, chainParent, chainChild, futureRef, sitsLater, missing, dupParent, selfParent, noParents, reusedID)
	for _, tx := range []*mempool.Tx{valid, chainParent, chainChild, sitsLater} {
		if !included(blk, tx.ID) {
			t.Fatalf("%s excluded: %s", tx.ID, prod.failedReceipt(t, tx.ID))
		}
	}
	for _, c := range []struct {
		tx   *mempool.Tx
		want error
	}{
		{futureRef, poe.ErrParentNotEarlier},
		{missing, poe.ErrParentUnknown},
		{dupParent, poe.ErrDuplicateParent},
		{selfParent, poe.ErrSelfParent},
		{noParents, poe.ErrParentCount},
		{reusedID, poe.ErrDuplicateTxID},
	} {
		if included(blk, c.tx.ID) {
			t.Fatalf("%s included despite %v", c.tx.ID, c.want)
		}
		if msg := prod.failedReceipt(t, c.tx.ID); !strings.Contains(msg, strings.TrimPrefix(c.want.Error(), poe.ErrViolation.Error()+": ")) {
			t.Fatalf("%s receipt %q does not name %v", c.tx.ID, msg, c.want)
		}
		acc, _ := prod.accounts.Get(c.tx.Sender)
		if acc.Nonce != 0 || acc.Balance != 1000 {
			t.Fatalf("rejected %s touched account state: %+v", c.tx.ID, acc)
		}
	}
	// Followers agree with the producer's choices.
	follower := newPoENode(w...)
	replayAll(t, follower, prod.bp.AllBlocks())
	sameState(t, prod, follower)
}

// A parent older than the window is unknown, at admission and in production.
func TestPoEProduction_ParentOlderThanWindowIsUnknown(t *testing.T) {
	if testing.Short() {
		t.Skip("produces ParentWindowBlocks+2 blocks")
	}
	withPoEActivation(t, 1)
	alice := newPoEWallet(t)
	prod := newPoENode(alice)
	prod.produce(t) // genesis
	old := prod.recentIDs(t, 1)[0]
	for i := 0; i < poe.ParentWindowBlocks; i++ {
		prod.produce(t)
	}
	// The next block is genesis+W+1: genesis's transaction is out of the window.
	fresh := prod.recentIDs(t, 1)[0]
	stale := alice.transfer(t, "stale-parent-transfer-01", prod.nonceOf(alice), 0.01, []string{old, fresh})
	if err := prod.bp.CheckWalletTransferAdmission(stale); !errors.Is(err, poe.ErrParentUnknown) {
		t.Fatalf("admission err = %v, want ErrParentUnknown", err)
	}
	blk := prod.produce(t, stale)
	if included(blk, stale.ID) {
		t.Fatal("transfer naming an expired parent was included")
	}
	if got := prod.bp.PoEHistorySize(); got != poe.ParentWindowBlocks {
		t.Fatalf("history index holds %d IDs, want %d (one heartbeat per block)", got, poe.ParentWindowBlocks)
	}
}

// Admission and production agree: a transfer the mempool admits is produced,
// and one the mempool refuses would have been dropped by production.
func TestPoEAdmissionMatchesProduction(t *testing.T) {
	withPoEActivation(t, 2)
	w := make([]*poeWallet, 6)
	for i := range w {
		w[i] = newPoEWallet(t)
	}
	prod := newPoENode(w...)
	prod.produce(t)
	prod.produce(t) // heights 0, 1
	parents := prod.recentIDs(t, 2)
	candidates := []*mempool.Tx{
		w[0].transfer(t, "admission-valid-0000001", 1, 0.01, parents),
		w[1].transfer(t, "admission-unknown-00001", 1, 0.01, []string{parents[0], "unknown-parent-00000001"}),
		w[2].transfer(t, "admission-dup-000000001", 1, 0.01, []string{parents[1], parents[1]}),
		w[3].transfer(t, "admission-none-00000001", 1, 0.01, nil),
		w[4].transfer(t, "admission-self-00000001", 1, 0.01, []string{parents[0], "admission-self-00000001"}),
		w[5].transfer(t, parents[0], 1, 0.01, []string{parents[1], "solo-heartbeat-0-1790000000000000000"}),
	}
	for _, tx := range candidates {
		admitErr := prod.bp.CheckWalletTransferAdmission(tx)
		// Production verdict on a scratch copy of the same chain.
		scratch := newPoENode(w...)
		replayAll(t, scratch, prod.bp.AllBlocks())
		blk := scratch.produce(t, tx)
		if (admitErr == nil) != included(blk, tx.ID) {
			t.Fatalf("%s: admission err=%v but produced=%v", tx.ID, admitErr, included(blk, tx.ID))
		}
		if admitErr != nil && !poe.IsViolation(admitErr) {
			t.Fatalf("%s: admission error is not a PoE violation: %v", tx.ID, admitErr)
		}
	}
}

// The submit path (WalletTransferSubmitter) and the generic mempool layer
// both enforce the rules; system transactions pass the layer untouched.
func TestPoEAdmission_SubmitterAndMempoolLayer(t *testing.T) {
	withPoEActivation(t, 2)
	alice, bob := newPoEWallet(t), newPoEWallet(t)
	prod := newPoENode(alice, bob)
	prod.produce(t)
	prod.produce(t)
	parents := prod.recentIDs(t, 2)
	sub := prod.bp.WalletTransferSubmitter()
	if err := sub.Add(alice.transfer(t, "submit-bad-parents-0001", 1, 0.01, []string{"parent-one-000000000", "parent-two-000000000"})); !errors.Is(err, poe.ErrParentUnknown) {
		t.Fatalf("submitter err = %v, want ErrParentUnknown", err)
	}
	if prod.pool.Size() != 0 {
		t.Fatal("rejected transfer reached the pool")
	}
	if err := sub.Add(alice.transfer(t, "submit-good-parents-001", 1, 0.01, parents)); err != nil {
		t.Fatalf("submitter rejected a valid transfer: %v", err)
	}

	layered := mempool.New(mempool.DefaultConfig())
	layered.SetAdmissionChecker(prod.bp.WalletTransferPoEAdmission(nil))
	if err := layered.Add(bob.transfer(t, "gossip-bad-parents-0001", 1, 0.01, nil)); !errors.Is(err, poe.ErrParentCount) {
		t.Fatalf("mempool layer err = %v, want ErrParentCount", err)
	}
	if err := layered.Add(bob.transfer(t, "gossip-good-parents-001", 1, 0.01, parents)); err != nil {
		t.Fatalf("mempool layer rejected a valid transfer: %v", err)
	}
	if err := layered.Add(prod.heartbeat()); err != nil {
		t.Fatalf("mempool layer touched a system transaction: %v", err)
	}
}

// While an activation height is configured but not reached, admission
// rejects nothing and counts what enforcement would reject.
func TestPoEShadowModeCountsWithoutRejecting(t *testing.T) {
	withPoEActivation(t, 1_000_000)
	alice := newPoEWallet(t)
	prod := newPoENode(alice)
	prod.produce(t)
	prod.produce(t)
	before := PoEStats()
	if err := prod.bp.WalletTransferSubmitter().Add(alice.transfer(t, "shadow-no-parents-00001", 1, 0.01, nil)); err != nil {
		t.Fatalf("shadow mode rejected a transfer: %v", err)
	}
	after := PoEStats()
	if after.ShadowChecked != before.ShadowChecked+1 || after.ShadowWouldReject["parent_count"] != before.ShadowWouldReject["parent_count"]+1 {
		t.Fatalf("shadow counters did not move: before %+v after %+v", before, after)
	}
	if after.Rejected["parent_count"] != before.Rejected["parent_count"] {
		t.Fatal("shadow mode counted an enforcement rejection")
	}
}

// ---------------------------------------------------------------------------
// Cross-validator: identical outcomes on every node
// ---------------------------------------------------------------------------

// Several validators replay the same chain across the activation height,
// including adversarial blocks, and must reach identical state:
//
//   - followers syncing from genesis, and one restored mid-chain from a
//     block prefix plus an account snapshot, all match the producer;
//   - a crafted block below the activation height whose transfer has no or
//     placeholder parents is accepted by every node (historical rules);
//   - crafted blocks at or above it -- missing parent, forward reference,
//     a two-transfer cycle, self reference, a parent from another chain, a
//     sender/key mismatch, a wrong signer key -- are refused by every node
//     with the same error, and leave every node's state untouched;
//   - the honest block at the same height is then accepted everywhere.
func TestPoECrossValidatorReplay(t *testing.T) {
	const activation = 6
	withPoEActivation(t, activation)
	w := make([]*poeWallet, 4)
	for i := range w {
		w[i] = newPoEWallet(t)
	}
	prod := newPoENode(w...)
	followers := []*poeNode{newPoENode(w...), newPoENode(w...)}
	all := func() []*poeNode { return append([]*poeNode{prod}, followers...) }
	sync := func() {
		t.Helper()
		for _, f := range followers {
			for _, b := range prod.bp.AllBlocks() {
				if f.bp.HasTip() && b.Height <= f.bp.TipHeight() {
					continue
				}
				if err := f.bp.TryAppendExternalBlock(b); err != nil {
					t.Fatalf("sync height %d: %v", b.Height, err)
				}
			}
		}
		sameState(t, all()...)
	}

	// Heights 0..3 below activation: historical shapes, produced normally.
	prod.produce(t)
	prod.produce(t, w[0].transfer(t, "legacy-empty-parents-01", prod.nonceOf(w[0]), 0.01, []string{}))
	prod.produce(t)
	prod.produce(t)
	sync()

	// Height 4 (below activation), crafted by an outdated producer with
	// placeholder parents: every node accepts it.
	legacy := craftBlock(t, prod.tip(t), prod.aware, heartbeatFor(prod),
		w[1].transfer(t, "legacy-placeholders-001", prod.nonceOf(w[1]), 0.01, []string{"parent1", "parent2"}))
	for _, n := range all() {
		if err := n.bp.TryAppendExternalBlock(legacy); err != nil {
			t.Fatalf("pre-activation block refused: %v", err)
		}
	}
	sameState(t, all()...)
	prod.produce(t) // height 5
	sync()

	// Heights >= 6: the rules are consensus rules. recent is
	// [heartbeat@5, legacy-placeholders-001, heartbeat@4].
	recent := prod.recentIDs(t, 3)
	parents := recent[:2]
	stranger := newPoENode(w...) // another chain: its transfer is not ours
	stranger.produce(t)
	const foreign = "other-chain-transfer-01"
	if !included(stranger.produce(t, w[3].transfer(t, foreign, 1, 0.01, []string{})), foreign) {
		t.Fatal("stranger chain did not commit its transfer")
	}

	tip := prod.tip(t)
	n2, n3 := prod.nonceOf(w[2]), prod.nonceOf(w[3])
	cycleA := w[2].transfer(t, "cycle-transfer-a-000001", n2, 0.01, []string{parents[0], "cycle-transfer-b-000001"})
	cycleB := w[3].transfer(t, "cycle-transfer-b-000001", n3, 0.01, []string{parents[0], "cycle-transfer-a-000001"})
	victimKey := w[0]
	mismatch := w[2].signEnvelope(t, wallet.TransactionData{ // signed by w[2] but spends from w[0]
		ID: "sender-key-mismatch-001", Sender: victimKey.addr, Recipient: poeTestRecipient, Amount: 1, Fee: 0.01,
		ParentCells: parents, Nonce: prod.nonceOf(victimKey), PublicKey: hex.EncodeToString(w[2].ws.GetPublicKey()),
		Timestamp: time.Unix(1_790_000_000, 0).UTC().Format(time.RFC3339),
	})
	wrongKey := w[2].signEnvelope(t, wallet.TransactionData{ // signed by w[2], presents w[0]'s key and address
		ID: "wrong-signer-key-00001", Sender: victimKey.addr, Recipient: poeTestRecipient, Amount: 1, Fee: 0.01,
		ParentCells: parents, Nonce: prod.nonceOf(victimKey), PublicKey: hex.EncodeToString(victimKey.ws.GetPublicKey()),
		Timestamp: time.Unix(1_790_000_000, 0).UTC().Format(time.RFC3339),
	})

	type adversarial struct {
		name string
		txs  []*mempool.Tx
		want error // nil: the block fails for a non-PoE reason (bad signature)
	}
	cases := []adversarial{
		{"missing-parent", []*mempool.Tx{w[2].transfer(t, "adv-missing-parent-0001", n2, 0.01, []string{parents[0], "never-committed-0000001"})}, poe.ErrParentUnknown},
		{"forward-reference", []*mempool.Tx{
			w[2].transfer(t, "adv-forward-ref-000001", n2, 0.01, []string{parents[0], "adv-forward-target-0001"}),
			w[3].transfer(t, "adv-forward-target-0001", n3, 0.01, parents),
		}, poe.ErrParentNotEarlier},
		{"two-transfer-cycle", []*mempool.Tx{cycleA, cycleB}, poe.ErrParentNotEarlier},
		{"self-reference", []*mempool.Tx{w[2].transfer(t, "adv-self-reference-001", n2, 0.01, []string{parents[0], "adv-self-reference-001"})}, poe.ErrSelfParent},
		{"other-chain-parent", []*mempool.Tx{w[2].transfer(t, "adv-other-chain-00001", n2, 0.01, []string{parents[0], foreign})}, poe.ErrParentUnknown},
		{"no-parents", []*mempool.Tx{w[2].transfer(t, "adv-no-parents-000001", n2, 0.01, nil)}, poe.ErrParentCount},
		{"reused-id", []*mempool.Tx{w[2].transfer(t, recent[1], n2, 0.01, []string{recent[0], recent[2]})}, poe.ErrDuplicateTxID},
		{"sender-key-mismatch", []*mempool.Tx{mismatch}, nil},
		{"wrong-signer-key", []*mempool.Tx{wrongKey}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			txs := append([]*mempool.Tx{heartbeatFor(prod)}, c.txs...)
			var blk *Block
			if c.want == nil {
				// The applier itself refuses these, so build the block by hand
				// with the root the producer would have claimed.
				blk = &Block{Height: tip.Height + 1, PrevHash: tip.Hash, Timestamp: tip.Timestamp.Add(10 * time.Second),
					Transactions: txs, StateRoot: tip.StateRoot, ProducerID: "crafted-producer"}
				blk.Hash = computeBlockHash(blk)
			} else {
				blk = craftBlock(t, tip, prod.aware, txs...)
			}
			var first string
			for i, n := range all() {
				err := n.bp.TryAppendExternalBlock(blk)
				if err == nil {
					t.Fatalf("node %d accepted the %s block", i, c.name)
				}
				if c.want != nil && !errors.Is(err, c.want) {
					t.Fatalf("node %d: err = %v, want %v", i, err, c.want)
				}
				if i == 0 {
					first = err.Error()
				} else if err.Error() != first {
					t.Fatalf("nodes disagree on the reason: %q vs %q", first, err.Error())
				}
				if got := n.tip(t); got.Hash != tip.Hash {
					t.Fatalf("node %d tip moved to %d", i, got.Height)
				}
			}
			sameState(t, all()...)
		})
	}

	// The honest producer's block at the same height, with an intra-block
	// parent, is accepted everywhere.
	first := w[2].transfer(t, "honest-first-transfer01", n2, 0.09, parents)
	second := w[3].transfer(t, "honest-second-transfer1", n3, 0.01, []string{parents[0], first.ID})
	honest := prod.produce(t, first, second)
	if !included(honest, first.ID) || !included(honest, second.ID) {
		t.Fatal("honest transfers were not produced")
	}
	sync()

	// A validator restored mid-chain (block prefix + account snapshot at
	// that height) catches up to the same state.
	blocks := prod.bp.AllBlocks()
	cut := activation // restore at the activation boundary
	replica := newPoENode(w...)
	replayAll(t, replica, blocks[:cut])
	snapshot := replica.accounts.Clone()
	restored := newPoENode()
	restored.accounts = snapshot
	restored.aware = NewEnrollmentAwareApplier(snapshot, nil)
	restored.bp = NewBlockProducer(restored.pool, restored.aware, DefaultProducerConfig())
	restored.bp.SetAppendReceiptStore(restored.receipts)
	if err := restored.bp.RestoreChain(append([]*Block(nil), blocks[:cut]...)); err != nil {
		t.Fatalf("RestoreChain: %v", err)
	}
	replayAll(t, restored, blocks[cut:])
	sameState(t, prod, followers[0], followers[1], restored)

	stats := PoEStats()
	if stats.ExternalBlocksRefused == 0 {
		t.Fatal("refused external blocks were not counted")
	}
}

// Admission starts enforcing PoEAdmissionLeadBlocks before the activation
// height, so nothing admitted without parents can still be pending when the
// rules become consensus rules; production enforces from the height itself.
func TestPoEAdmissionLeadsActivation(t *testing.T) {
	const activation = PoEAdmissionLeadBlocks + 5
	withPoEActivation(t, activation)
	alice := newPoEWallet(t)
	prod := newPoENode(alice)
	for i := 0; i < 4; i++ {
		prod.produce(t) // heights 0..3; next block 4, 4+lead < activation
	}
	noParents := alice.transfer(t, "lead-window-transfer-01", 1, 0.01, nil)
	if err := prod.bp.CheckWalletTransferAdmission(noParents); err != nil {
		t.Fatalf("admission enforced too early: %v", err)
	}
	prod.produce(t) // height 4; next block 5, 5+lead == activation
	if err := prod.bp.CheckWalletTransferAdmission(noParents); !errors.Is(err, poe.ErrParentCount) {
		t.Fatalf("admission inside the lead window: err = %v, want ErrParentCount", err)
	}
	// Production at height 5 is still below activation and accepts it.
	blk := prod.produce(t, noParents)
	if !included(blk, noParents.ID) {
		t.Fatalf("production enforced before activation: %s", prod.failedReceipt(t, noParents.ID))
	}
}

// BenchmarkPoEHistoryRebuild measures the boot-time index build over a full
// reference window (one heartbeat per block, the live chain's shape).
func BenchmarkPoEHistoryRebuild(b *testing.B) {
	blocks := syntheticChain(poe.ParentWindowBlocks + 100)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var h poeHistory
		h.rebuild(blocks)
	}
}

// BenchmarkPoEBlockScopeCheck measures the per-transfer cost the producer and
// every follower pay: strict envelope decode plus the parent lookups.
func BenchmarkPoEBlockScopeCheck(b *testing.B) {
	blocks := syntheticChain(poe.ParentWindowBlocks + 1)
	var h poeHistory
	h.rebuild(blocks)
	w := newPoEWallet(b)
	tx := w.transfer(b, "bench-transfer-000000001", 1, 0.01, []string{
		fmt.Sprintf("tx-at-height-%010d", poe.ParentWindowBlocks),
		fmt.Sprintf("tx-at-height-%010d", poe.ParentWindowBlocks-1),
	})
	withPoEActivation(b, 1)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s := &poeBlockScope{active: true, height: uint64(poe.ParentWindowBlocks + 1), hist: &h,
			earlier: map[string]struct{}{}, remaining: map[string]int{tx.ID: 1}}
		if err := s.check(tx); err != nil {
			b.Fatal(err)
		}
	}
}
