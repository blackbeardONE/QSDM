package chain

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/poe"
)

// withTxIDUnique sets the all-contract ID rule's activation height for one test.
func withTxIDUnique(t testing.TB, h uint64) {
	t.Helper()
	prev := txIDUniqueFrom.Load()
	txIDUniqueFrom.Store(h)
	t.Cleanup(func() { txIDUniqueFrom.Store(prev) })
}

func r2TaskTx(id, sender string) *mempool.Tx {
	payload, _ := json.Marshal(TaskAction{ID: id, Sender: sender, TaskID: "qsdm-edge-worker", Action: "start"})
	return &mempool.Tx{ID: id, Sender: sender, ContractID: TaskContractID, Payload: payload}
}

func transferReceipt(id string, h uint64) *TxReceipt {
	return &TxReceipt{TxID: id, BlockHeight: h, BlockHash: "block-" + id, Status: ReceiptSuccess, ContractID: WalletTransferContractID,
		Logs: []LogEntry{{Topic: "TxApplied", Data: map[string]interface{}{"sender": "alice", "recipient": "bob", "amount": 0.5}}}}
}

// --- the receipt store -----------------------------------------------------------------------------------------

func TestReceiptStoreKeepsCommittedReceipt(t *testing.T) {
	before := TxIDUniqueStats().ReceiptsKept
	rs := NewReceiptStore()
	orig := transferReceipt("committed-transfer-0001", 2)
	rs.Store(orig)
	for _, next := range []*TxReceipt{
		{TxID: orig.TxID, BlockHeight: 3, Status: ReceiptFailed, ContractID: TaskContractID, Error: "task stake required"},
		{TxID: orig.TxID, BlockHeight: 4, Status: ReceiptSuccess, ContractID: TaskContractID},
		{TxID: orig.TxID, BlockHeight: 5, Status: ReceiptFailed, ContractID: WalletTransferContractID},
	} {
		rs.Store(next)
		if got, _ := rs.Get(orig.TxID); got != orig {
			t.Fatalf("a later receipt (contract %q, status %d) replaced the committed one: %+v", next.ContractID, next.Status, got)
		}
	}
	if n := len(rs.GetByBlock(2)); n != 1 {
		t.Fatalf("block 2 holds %d receipts, want 1", n)
	}
	for _, h := range []uint64{3, 4, 5} {
		if n := len(rs.GetByBlock(h)); n != 0 {
			t.Fatalf("block %d holds %d receipts, want 0", h, n)
		}
	}
	if n := len(rs.GetByContract(TaskContractID)); n != 0 {
		t.Fatalf("the task contract lists %d receipts, want 0", n)
	}
	if rs.Count() != 1 {
		t.Fatalf("Count = %d, want 1", rs.Count())
	}
	if got := TxIDUniqueStats().ReceiptsKept - before; got != 3 {
		t.Fatalf("receipts kept counter grew by %d, want 3", got)
	}
	// The same receipt stored again is not a conflict.
	rs.Store(transferReceipt("committed-transfer-0001", 2))
	if got := TxIDUniqueStats().ReceiptsKept - before; got != 3 {
		t.Fatalf("an identical re-store counted as a conflict (%d)", got)
	}
}

func TestReceiptStoreFailedThenAppliedSameContract(t *testing.T) {
	rs := NewReceiptStore()
	failed := &TxReceipt{TxID: "retry-0001", BlockHeight: 5, Status: ReceiptFailed, ContractID: WalletTransferContractID, Error: "nonce gap"}
	rs.Store(failed)
	applied := &TxReceipt{TxID: "retry-0001", BlockHeight: 6, BlockHash: "h6", Status: ReceiptSuccess, ContractID: WalletTransferContractID}
	rs.Store(applied)
	if got, _ := rs.Get("retry-0001"); got != applied {
		t.Fatalf("the resubmitted transaction's receipt did not replace its failed one: %+v", got)
	}
	if len(rs.GetByBlock(5)) != 0 || len(rs.GetByBlock(6)) != 1 {
		t.Fatal("the replaced receipt did not move to its new block")
	}
	// A failed receipt is not replaced by another contract's receipt.
	rs2 := NewReceiptStore()
	rs2.Store(failed)
	rs2.Store(&TxReceipt{TxID: "retry-0001", BlockHeight: 6, Status: ReceiptSuccess, ContractID: TaskContractID})
	if got, _ := rs2.Get("retry-0001"); got != failed {
		t.Fatalf("another contract's receipt replaced a failed one: %+v", got)
	}
}

// The receipts file is replayed line by line at start-up: a replaced receipt
// written after the original must not win on reload (the reporter's restart).
func TestReceiptReloadKeepsTheFirstCommittedReceipt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "qsdm_receipts.ndjson")
	orig := transferReceipt("committed-transfer-0002", 2)
	bad := &TxReceipt{TxID: orig.TxID, BlockHeight: 3, BlockHash: "h3", Status: ReceiptFailed, ContractID: TaskContractID,
		Error: "task stake required", Logs: []LogEntry{{Topic: "TxFailed"}}}
	var lines []string
	for _, r := range []*TxReceipt{orig, bad} {
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, string(b))
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, workers := range []int{1, 4} {
		rs := NewReceiptStore()
		if _, err := rs.loadNDJSON(path, workers, 64); err != nil {
			t.Fatalf("load (%d workers): %v", workers, err)
		}
		got, _ := rs.Get(orig.TxID)
		if got == nil || got.Status != ReceiptSuccess || got.ContractID != WalletTransferContractID || got.BlockHeight != 2 {
			t.Fatalf("after reload (%d workers) the receipt is %+v, want the committed transfer at height 2", workers, got)
		}
	}
}

// --- admission ---------------------------------------------------------------------------------------------------

func TestTaskActionAdmissionRefusesRecordedIDs(t *testing.T) {
	for _, existing := range []*TxReceipt{
		transferReceipt("recorded-id-0001", 2),
		{TxID: "recorded-id-0001", BlockHeight: 2, Status: ReceiptFailed, ContractID: WalletTransferContractID},
		{TxID: "recorded-id-0001", BlockHeight: 2, Status: ReceiptSuccess, ContractID: TaskContractID},
		{TxID: "recorded-id-0001", BlockHeight: 2, Status: ReceiptFailed, ContractID: TaskContractID},
	} {
		bp, pool, receipts := admissionTestProducer()
		receipts.Store(existing)
		err := bp.TaskActionSubmitter().Add(r2TaskTx(existing.TxID, "mallory"))
		if !errors.Is(err, ErrTxIDTaken) || errors.Is(err, mempool.ErrDuplicateTx) {
			t.Fatalf("Add over %d/%s = %v, want ErrTxIDTaken (and not ErrDuplicateTx)", existing.Status, existing.ContractID, err)
		}
		if got, _ := receipts.Get(existing.TxID); got != existing || receipts.Count() != 1 || pool.Size() != 0 {
			t.Fatal("a refused task action changed the receipts or reached the mempool")
		}
	}
}

func TestTaskActionAdmissionPendingAndFreshIDs(t *testing.T) {
	bp, pool, _ := admissionTestProducer()
	s := bp.TaskActionSubmitter()
	first := r2TaskTx("fresh-task-0001", "alice")
	if err := s.Add(first); err != nil {
		t.Fatalf("fresh ID refused: %v", err)
	}
	// The same signed action again is a plain duplicate.
	again := r2TaskTx("fresh-task-0001", "alice")
	if err := s.Add(again); !errors.Is(err, mempool.ErrDuplicateTx) || errors.Is(err, ErrTxIDTaken) {
		t.Fatalf("same action again = %v, want ErrDuplicateTx", err)
	}
	// Another transaction with that ID is refused as taken.
	if err := s.Add(r2TaskTx("fresh-task-0001", "mallory")); !errors.Is(err, ErrTxIDTaken) {
		t.Fatalf("another action with a pending ID = %v, want ErrTxIDTaken", err)
	}
	// A pending wallet transfer's ID cannot be taken by a task action either.
	if err := pool.Add(admissionTestTx("pending-transfer-01", "carol", 0)); err != nil {
		t.Fatal(err)
	}
	if err := s.Add(r2TaskTx("pending-transfer-01", "mallory")); !errors.Is(err, ErrTxIDTaken) {
		t.Fatalf("task action with a pending transfer's ID = %v, want ErrTxIDTaken", err)
	}
	if pool.Size() != 2 {
		t.Fatalf("pool holds %d txs, want 2", pool.Size())
	}
	// And the wallet route still answers its own duplicate.
	if err := bp.WalletTransferSubmitter().Add(admissionTestTx("fresh-task-0001", "bob", 0)); !errors.Is(err, mempool.ErrDuplicateTx) {
		t.Fatalf("wallet duplicate = %v, want ErrDuplicateTx", err)
	}
}

func TestTaskActionAdmissionFailsClosed(t *testing.T) {
	bp, pool, receipts := admissionTestProducer()
	missingPool := NewBlockProducer(nil, NewAccountStore(), DefaultProducerConfig())
	missingPool.SetAppendReceiptStore(receipts)
	for _, tc := range []struct {
		name string
		s    *taskActionSubmitter
		tx   *mempool.Tx
	}{
		{"nil-adapter", nil, r2TaskTx("t1", "alice")},
		{"nil-producer", (*BlockProducer)(nil).TaskActionSubmitter(), r2TaskTx("t1", "alice")},
		{"missing-receipts", NewBlockProducer(pool, NewAccountStore(), DefaultProducerConfig()).TaskActionSubmitter(), r2TaskTx("t1", "alice")},
		{"missing-pool", missingPool.TaskActionSubmitter(), r2TaskTx("t1", "alice")},
		{"nil-tx", bp.TaskActionSubmitter(), nil},
		{"empty-id", bp.TaskActionSubmitter(), r2TaskTx("", "alice")},
		{"wrong-contract", bp.TaskActionSubmitter(), admissionTestTx("t2", "alice", 0)},
	} {
		if err := tc.s.Add(tc.tx); err == nil {
			t.Fatalf("%s: accepted", tc.name)
		}
	}
	if pool.Size() != 0 {
		t.Fatal("refused input reached the pool")
	}
}

// --- production -----------------------------------------------------------------------------------------------

// The reporter's failure path: a committed transfer, then a task action with
// its ID that would fail (no stake). Production drops it without a receipt and
// the transfer's receipt stays Success.
func TestProduceBlockDropsAReusedIDWithoutAReceipt(t *testing.T) {
	withPoEActivation(t, 0)
	w := newPoEWallet(t)
	n := newPoENode(w)
	n.produce(t)
	tr := w.transfer(t, "committed-transfer-0003", n.nonceOf(w), 0, nil)
	n.produce(t, tr)
	orig, ok := n.receipts.Get(tr.ID)
	if !ok || orig.Status != ReceiptSuccess {
		t.Fatalf("the transfer did not commit: %+v", orig)
	}
	before := TxIDUniqueStats().ProductionSkipped
	// Straight into the pool, as a stale or alternate admission path would.
	blk := n.produce(t, r2TaskTx(tr.ID, "mallory"))
	if included(blk, tr.ID) {
		t.Fatal("the task action with the reused ID entered the block")
	}
	if got, _ := n.receipts.Get(tr.ID); got != orig {
		t.Fatalf("the transfer's receipt changed: %+v", got)
	}
	if TxIDUniqueStats().ProductionSkipped-before != 1 {
		t.Fatal("the skip was not counted")
	}
	if n.pool.Size() != 0 {
		t.Fatal("the dropped transaction went back to the pool")
	}
}

// --- consensus ---------------------------------------------------------------------------------------------------

// reusedHeartbeat returns n's next heartbeat (valid nonce) carrying the ID of an
// earlier committed transaction.
func reusedHeartbeat(n *poeNode, id string) *mempool.Tx {
	hb := n.heartbeat()
	hb.ID = id
	return hb
}

func buildChain(t *testing.T, blocks int) (*poeNode, []*Block) {
	t.Helper()
	a := newPoENode()
	var chain []*Block
	for i := 0; i < blocks; i++ {
		chain = append(chain, a.produce(t))
	}
	return a, chain
}

func TestExternalBlockRepeatingACommittedIDFromActivation(t *testing.T) {
	withPoEActivation(t, 1)
	a, chain := buildChain(t, 4)
	committedID := chain[1].Transactions[0].ID
	// From the activation height: refused, whatever the contract.
	withTxIDUnique(t, 4)
	b := newPoENode()
	replayAll(t, b, chain)
	bad := craftBlock(t, b.tip(t), b.aware, reusedHeartbeat(b, committedID))
	err := b.bp.TryAppendExternalBlock(bad)
	if err == nil || !errors.Is(err, poe.ErrDuplicateTxID) {
		t.Fatalf("block %d repeating %s = %v, want ErrDuplicateTxID", bad.Height, committedID, err)
	}
	if b.tip(t).Height != chain[len(chain)-1].Height {
		t.Fatal("the refused block was appended")
	}
	_ = a
}

func TestExternalBlockRepeatingACommittedIDBelowActivation(t *testing.T) {
	withPoEActivation(t, 1)
	_, chain := buildChain(t, 4)
	committedID := chain[1].Transactions[0].ID
	withTxIDUnique(t, 100)
	b := newPoENode()
	replayAll(t, b, chain)
	old := craftBlock(t, b.tip(t), b.aware, reusedHeartbeat(b, committedID))
	if err := b.bp.TryAppendExternalBlock(old); err != nil {
		t.Fatalf("history below the activation height must replay as produced: %v", err)
	}
	// The committed receipt of the original stays.
	if got, ok := b.receipts.Get(committedID); !ok || got.BlockHeight != chain[1].Height {
		t.Fatalf("the original receipt was replaced on replay: %+v", got)
	}
}

func TestExternalBlockRepeatingAnIDInsideTheBlock(t *testing.T) {
	withPoEActivation(t, 1)
	withTxIDUnique(t, 4)
	_, chain := buildChain(t, 4)
	b := newPoENode()
	replayAll(t, b, chain)
	hb1 := b.heartbeat()
	hb2 := &mempool.Tx{ID: hb1.ID, Sender: hb1.Sender, Recipient: hb1.Recipient, Nonce: hb1.Nonce + 1}
	bad := craftBlock(t, b.tip(t), b.aware, hb1, hb2)
	if err := b.bp.TryAppendExternalBlock(bad); err == nil || !errors.Is(err, poe.ErrDuplicateTxID) {
		t.Fatalf("a block repeating an ID inside itself = %v, want ErrDuplicateTxID", err)
	}
}

func TestFreshIDsStillAcceptedFromActivation(t *testing.T) {
	withPoEActivation(t, 1)
	withTxIDUnique(t, 2)
	a, chain := buildChain(t, 5)
	b := newPoENode()
	replayAll(t, b, chain)
	sameState(t, a, b)
	if TxIDUniqueActiveAt(1) || !TxIDUniqueActiveAt(2) {
		t.Fatal("activation gate")
	}
	withPoEActivation(t, 0)
	if TxIDUniqueActiveAt(10) {
		t.Fatal("the rule needs the PoE window: never active without PoE")
	}
}
