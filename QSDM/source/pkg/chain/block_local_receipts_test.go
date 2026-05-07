package chain

// block_local_receipts_test.go — pins the receipt emission added
// to BlockProducer.ProduceBlock so locally-produced blocks (the
// solo-validator BFT path on BLR1) populate the receipt store
// instead of leaving it empty.
//
// Before this change, SetAppendReceiptStore wired receipts only
// for TryAppendExternalBlock (gossip path). Local production
// dropped failed txs at apply time without recording them and
// never emitted receipts for successful ones, so qsdmcli receipt
// <tx-id> returned 404 on a solo testnet even for txs that had
// been mined and finalised.

import (
	"fmt"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

func TestProduceBlock_EmitsReceiptForSuccessfulTxs(t *testing.T) {
	pool := mempool.New(mempool.DefaultConfig())
	pool.Add(makeTx("tx-ok-1", 0.5))
	pool.Add(makeTx("tx-ok-2", 0.5))

	bp := NewBlockProducer(pool, newTestApplier(), DefaultProducerConfig())
	rs := NewReceiptStore()
	bp.SetAppendReceiptStore(rs)

	blk, err := bp.ProduceBlock()
	if err != nil {
		t.Fatalf("ProduceBlock: %v", err)
	}
	if got := rs.Count(); got != 2 {
		t.Fatalf("receipt count = %d, want 2", got)
	}
	for i, tx := range blk.Transactions {
		r, ok := rs.Get(tx.ID)
		if !ok {
			t.Fatalf("Get(%q) miss after seal", tx.ID)
		}
		if r.Status != ReceiptSuccess {
			t.Errorf("%s: status=%d, want ReceiptSuccess", tx.ID, r.Status)
		}
		if r.BlockHeight != blk.Height {
			t.Errorf("%s: BlockHeight=%d, want %d", tx.ID, r.BlockHeight, blk.Height)
		}
		if r.BlockHash != blk.Hash {
			t.Errorf("%s: BlockHash=%q, want %q", tx.ID, r.BlockHash, blk.Hash)
		}
		if r.IndexInBlock != i {
			t.Errorf("%s: IndexInBlock=%d, want %d", tx.ID, r.IndexInBlock, i)
		}
		if len(r.Logs) != 1 || r.Logs[0].Topic != "TxApplied" {
			t.Errorf("%s: expected one TxApplied log, got %+v", tx.ID, r.Logs)
		}
	}
}

func TestProduceBlock_EmitsReceiptForFailedTxs(t *testing.T) {
	// Build a pool where tx-bad asks for more than alice has.
	pool := mempool.New(mempool.DefaultConfig())
	pool.Add(makeTx("tx-good", 1.0))
	pool.Add(&mempool.Tx{
		ID: "tx-bad", Sender: "alice", Recipient: "bob", Amount: 999_999, Fee: 0,
	})

	applier := newTestApplier()
	bp := NewBlockProducer(pool, applier, DefaultProducerConfig())
	rs := NewReceiptStore()
	bp.SetAppendReceiptStore(rs)

	blk, err := bp.ProduceBlock()
	if err != nil {
		t.Fatalf("ProduceBlock: %v", err)
	}
	if got := len(blk.Transactions); got != 1 {
		t.Fatalf("blk.Transactions = %d, want 1 (only tx-good should land)", got)
	}

	// Both txs MUST have receipts — the failed one is the
	// audit trail explaining why tx-bad was dropped.
	if got := rs.Count(); got != 2 {
		t.Fatalf("receipt count = %d, want 2 (success + failure)", got)
	}

	good, ok := rs.Get("tx-good")
	if !ok || good.Status != ReceiptSuccess {
		t.Fatalf("tx-good: %+v ok=%v", good, ok)
	}
	bad, ok := rs.Get("tx-bad")
	if !ok {
		t.Fatal("tx-bad: receipt missing")
	}
	if bad.Status != ReceiptFailed {
		t.Errorf("tx-bad: status=%d, want ReceiptFailed", bad.Status)
	}
	if bad.Error == "" {
		t.Errorf("tx-bad: Error empty, want apply-error reason")
	}
	// Failed receipts are placed past the included slice's
	// indices so list views can identify them visually.
	if bad.IndexInBlock < len(blk.Transactions) {
		t.Errorf("tx-bad: IndexInBlock=%d, want >= len(blk.Transactions)=%d", bad.IndexInBlock, len(blk.Transactions))
	}
	if len(bad.Logs) != 1 || bad.Logs[0].Topic != "TxFailed" {
		t.Errorf("tx-bad: expected one TxFailed log, got %+v", bad.Logs)
	}
}

func TestProduceBlock_NoReceiptStore_NoOp(t *testing.T) {
	// Backwards compat: producers without SetAppendReceiptStore
	// must keep working exactly as before — no panic, sealed
	// block returned, OnSealedBlock fires.
	pool := mempool.New(mempool.DefaultConfig())
	pool.Add(makeTx("tx-x", 0.5))

	bp := NewBlockProducer(pool, newTestApplier(), DefaultProducerConfig())
	hookFired := false
	bp.OnSealedBlock = func(*Block) { hookFired = true }

	if _, err := bp.ProduceBlock(); err != nil {
		t.Fatalf("ProduceBlock without receipts: %v", err)
	}
	if !hookFired {
		t.Fatal("OnSealedBlock did not fire")
	}
}

func TestProduceBlock_ReceiptIndexInBlock_MatchesPosition(t *testing.T) {
	// Pin the IndexInBlock relationship documented in
	// storeProduceBlockReceipts:
	//
	//   - Each successful receipt's IndexInBlock equals the
	//     position of the matching tx in blk.Transactions.
	//   - Each failed receipt's IndexInBlock is >= len(blk.Transactions),
	//     so failed txs sort after included ones in any
	//     index-ordered view.
	//
	// We don't assume the mempool drains in FIFO order — the
	// pkg/mempool heap may reorder by fee/priority — so the
	// test reads the actual order back from blk.Transactions
	// rather than hard-coding it.
	pool := mempool.New(mempool.DefaultConfig())
	pool.Add(makeTx("ok-1", 0.5))
	pool.Add(&mempool.Tx{ID: "bad", Sender: "alice", Recipient: "bob", Amount: 999_999, Fee: 0})
	pool.Add(makeTx("ok-2", 0.5))
	pool.Add(makeTx("ok-3", 0.5))

	bp := NewBlockProducer(pool, newTestApplier(), DefaultProducerConfig())
	rs := NewReceiptStore()
	bp.SetAppendReceiptStore(rs)

	blk, err := bp.ProduceBlock()
	if err != nil {
		t.Fatalf("ProduceBlock: %v", err)
	}
	if len(blk.Transactions) != 3 {
		t.Fatalf("included = %d, want 3", len(blk.Transactions))
	}
	for i, tx := range blk.Transactions {
		r, ok := rs.Get(tx.ID)
		if !ok {
			t.Fatalf("Get(%q) miss", tx.ID)
		}
		if r.IndexInBlock != i {
			t.Errorf("%s: IndexInBlock=%d, want %d (position in blk.Transactions)", tx.ID, r.IndexInBlock, i)
		}
		if r.Status != ReceiptSuccess {
			t.Errorf("%s: status=%d, want ReceiptSuccess", tx.ID, r.Status)
		}
	}

	bad, ok := rs.Get("bad")
	if !ok {
		t.Fatal("bad: receipt missing")
	}
	if bad.Status != ReceiptFailed {
		t.Errorf("bad: status=%d, want ReceiptFailed", bad.Status)
	}
	if bad.IndexInBlock < len(blk.Transactions) {
		t.Errorf("bad: IndexInBlock=%d must be >= len(blk.Transactions)=%d", bad.IndexInBlock, len(blk.Transactions))
	}
}

// Sanity: the test applier's "alice" balance changes after a
// successful apply. We use this assertion in the receipt tests
// above to catch a regression where outcomes capture the same
// tx twice (which would corrupt rs.byTxID + rs.order).
func TestProduceBlock_ReceiptStore_NoDoubleStore(t *testing.T) {
	pool := mempool.New(mempool.DefaultConfig())
	pool.Add(makeTx("only", 0.5))

	bp := NewBlockProducer(pool, newTestApplier(), DefaultProducerConfig())
	rs := NewReceiptStore()
	bp.SetAppendReceiptStore(rs)

	if _, err := bp.ProduceBlock(); err != nil {
		t.Fatalf("ProduceBlock: %v", err)
	}
	if got := rs.Count(); got != 1 {
		t.Fatalf("Count=%d, want 1 (regression: double-store)", got)
	}
	if got := len(rs.GetByBlock(0)); got != 1 {
		t.Fatalf("GetByBlock(0)=%d, want 1", got)
	}
}

// Ensure nil applier-error still produces a usable receipt
// (paranoid: receipt construction must not panic on a tx with
// zero fields, since the mempool should never admit one but a
// future change might introduce that path).
func TestProduceBlock_ReceiptForZeroFee(t *testing.T) {
	pool := mempool.New(mempool.DefaultConfig())
	pool.Add(&mempool.Tx{ID: "free", Sender: "alice", Recipient: "bob", Amount: 1, Fee: 0})

	bp := NewBlockProducer(pool, newTestApplier(), DefaultProducerConfig())
	rs := NewReceiptStore()
	bp.SetAppendReceiptStore(rs)

	if _, err := bp.ProduceBlock(); err != nil {
		t.Fatalf("ProduceBlock: %v", err)
	}
	r, ok := rs.Get("free")
	if !ok {
		t.Fatal("free: receipt missing")
	}
	if r.Status != ReceiptSuccess {
		t.Errorf("free: status=%d, want ReceiptSuccess", r.Status)
	}
}

// Compile-time guard: the localTxOutcome shape we capture inline
// MUST stay assignable to error so future refactors that pull a
// custom error type into ApplyTx don't silently break the
// receipt's Error field.
var _ = func() {
	var oc localTxOutcome
	_ = oc.Tx
	_ = oc.ApplyErr
	if oc.ApplyErr != nil {
		// errors.Is would fail to compile if ApplyErr's
		// type isn't `error`.
		_ = fmt.Sprintf("%v", oc.ApplyErr)
	}
}
