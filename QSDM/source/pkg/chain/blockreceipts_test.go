package chain

import (
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

func TestReceiptProducer_ContractIDOnReceipt(t *testing.T) {
	as := NewAccountStore()
	as.Credit("alice", 10000)
	pool := mempool.New(mempool.DefaultConfig())
	rs := NewReceiptStore()
	pool.Add(&mempool.Tx{ID: "c1", Sender: "alice", Recipient: "bob", Amount: 5, Fee: 1, Nonce: 0, ContractID: "escrow-9"})

	rp := NewReceiptProducer(pool, as, DefaultProducerConfig(), rs)
	_, _, err := rp.ProduceBlockWithReceipts()
	if err != nil {
		t.Fatalf("ProduceBlockWithReceipts: %v", err)
	}
	got, ok := rs.Get("c1")
	if !ok || got.ContractID != "escrow-9" {
		t.Fatalf("receipt: ok=%v %#v", ok, got)
	}
	if got.Logs[0].Data["contract_id"] != "escrow-9" {
		t.Fatalf("log data: %#v", got.Logs[0].Data)
	}
}

func TestReceiptProducer_ProduceWithReceipts(t *testing.T) {
	as := NewAccountStore()
	as.Credit("alice", 10000)
	pool := mempool.New(mempool.DefaultConfig())
	rs := NewReceiptStore()

	pool.Add(&mempool.Tx{ID: "tx1", Sender: "alice", Recipient: "bob", Amount: 50, Fee: 2, Nonce: 0})
	pool.Add(&mempool.Tx{ID: "tx2", Sender: "alice", Recipient: "carol", Amount: 30, Fee: 1, Nonce: 1})

	rp := NewReceiptProducer(pool, as, DefaultProducerConfig(), rs)
	block, receipts, err := rp.ProduceBlockWithReceipts()
	if err != nil {
		t.Fatalf("ProduceBlockWithReceipts: %v", err)
	}
	if block == nil {
		t.Fatal("expected a block")
	}
	if len(receipts) != 2 {
		t.Fatalf("expected 2 receipts, got %d", len(receipts))
	}

	for _, r := range receipts {
		if r.Status != ReceiptSuccess {
			t.Fatalf("expected success for %s, got error: %s", r.TxID, r.Error)
		}
		if r.BlockHash == "" {
			t.Fatal("receipt should have block hash")
		}
		if r.BlockHash != block.Hash {
			t.Fatal("receipt block hash should match block")
		}
	}

	// Check receipt store
	if rs.Count() != 2 {
		t.Fatalf("expected 2 in store, got %d", rs.Count())
	}
}

func TestReceiptProducer_FailedTxReceipt(t *testing.T) {
	as := NewAccountStore()
	as.Credit("alice", 100)
	pool := mempool.New(mempool.DefaultConfig())
	rs := NewReceiptStore()

	pool.Add(&mempool.Tx{ID: "good", Sender: "alice", Recipient: "bob", Amount: 10, Fee: 1, Nonce: 0})
	pool.Add(&mempool.Tx{ID: "bad", Sender: "broke", Recipient: "bob", Amount: 999, Fee: 1, Nonce: 0, ContractID: "fail-scoped"})

	rp := NewReceiptProducer(pool, as, DefaultProducerConfig(), rs)
	block, receipts, err := rp.ProduceBlockWithReceipts()
	if err != nil {
		t.Fatalf("ProduceBlockWithReceipts: %v", err)
	}

	if len(block.Transactions) != 1 {
		t.Fatalf("expected 1 included tx, got %d", len(block.Transactions))
	}
	if len(receipts) != 2 {
		t.Fatalf("expected 2 receipts (1 success + 1 fail), got %d", len(receipts))
	}

	var failed, success int
	for _, r := range receipts {
		if r.Status == ReceiptFailed {
			failed++
			if r.Error == "" {
				t.Fatal("failed receipt should have error message")
			}
			if r.TxID == "bad" {
				if r.ContractID != "fail-scoped" {
					t.Fatalf("failed tx should carry contract_id on receipt, got %#v", r)
				}
				if r.Logs[0].Data["contract_id"] != "fail-scoped" {
					t.Fatalf("failed log should include contract_id: %#v", r.Logs[0].Data)
				}
			}
		} else {
			success++
		}
	}
	if failed != 1 || success != 1 {
		t.Fatalf("expected 1 failed + 1 success, got %d failed + %d success", failed, success)
	}
}

func TestReceiptProducer_AllFailedStillStoresReceipts(t *testing.T) {
	as := NewAccountStore()
	pool := mempool.New(mempool.DefaultConfig())
	rs := NewReceiptStore()

	pool.Add(&mempool.Tx{ID: "bad1", Sender: "nobody", Recipient: "bob", Amount: 10, Fee: 1, Nonce: 0})

	rp := NewReceiptProducer(pool, as, DefaultProducerConfig(), rs)
	_, receipts, err := rp.ProduceBlockWithReceipts()
	if err == nil {
		t.Fatal("expected error when all txs fail")
	}
	if len(receipts) != 1 {
		t.Fatalf("expected 1 receipt even on all-fail, got %d", len(receipts))
	}
	if receipts[0].Status != ReceiptFailed {
		t.Fatal("expected failed status")
	}
	if rs.Count() != 1 {
		t.Fatal("failed receipts should still be stored")
	}
}

func TestReceiptProducer_ReceiptLogs(t *testing.T) {
	as := NewAccountStore()
	as.Credit("alice", 1000)
	pool := mempool.New(mempool.DefaultConfig())
	rs := NewReceiptStore()

	pool.Add(&mempool.Tx{ID: "tx1", Sender: "alice", Recipient: "bob", Amount: 10, Fee: 1, Nonce: 0})

	rp := NewReceiptProducer(pool, as, DefaultProducerConfig(), rs)
	_, receipts, _ := rp.ProduceBlockWithReceipts()

	if len(receipts[0].Logs) == 0 {
		t.Fatal("expected at least one log entry")
	}
	if receipts[0].Logs[0].Topic != "TxApplied" {
		t.Fatalf("expected TxApplied topic, got %s", receipts[0].Logs[0].Topic)
	}
}

func TestReceiptProducer_SearchByTopic(t *testing.T) {
	as := NewAccountStore()
	as.Credit("alice", 1000)
	pool := mempool.New(mempool.DefaultConfig())
	rs := NewReceiptStore()

	pool.Add(&mempool.Tx{ID: "tx1", Sender: "alice", Recipient: "bob", Amount: 10, Fee: 1, Nonce: 0})
	pool.Add(&mempool.Tx{ID: "bad", Sender: "ghost", Recipient: "x", Amount: 1, Fee: 0, Nonce: 0})

	rp := NewReceiptProducer(pool, as, DefaultProducerConfig(), rs)
	rp.ProduceBlockWithReceipts()

	applied := rs.SearchLogs("TxApplied")
	failed := rs.SearchLogs("TxFailed")

	if len(applied) != 1 {
		t.Fatalf("expected 1 TxApplied, got %d", len(applied))
	}
	if len(failed) != 1 {
		t.Fatalf("expected 1 TxFailed, got %d", len(failed))
	}
}

func TestReceiptProducer_EmptyPool(t *testing.T) {
	pool := mempool.New(mempool.DefaultConfig())
	rs := NewReceiptStore()
	rp := NewReceiptProducer(pool, NewAccountStore(), DefaultProducerConfig(), rs)

	_, _, err := rp.ProduceBlockWithReceipts()
	if err == nil {
		t.Fatal("expected error for empty pool")
	}
}

func TestReceiptProducer_TryAppendExternalBlockDelegates(t *testing.T) {
	as := NewAccountStore()
	as.Credit("alice", 100)
	pool := mempool.New(mempool.DefaultConfig())
	rs := NewReceiptStore()
	rp := NewReceiptProducer(pool, as, DefaultProducerConfig(), rs)
	if rp.UnderlyingProducer() == nil {
		t.Fatal("expected underlying producer")
	}
	sr := as.StateRoot()
	blk := &Block{
		Height: 0, PrevHash: "", Timestamp: time.Unix(1700000300, 0),
		Transactions: nil, StateRoot: sr, ProducerID: "peer",
	}
	blk.Hash = computeBlockHash(blk)
	if err := rp.TryAppendExternalBlock(blk); err != nil {
		t.Fatal(err)
	}
	if h := rp.UnderlyingProducer().ChainHeight(); h != 0 {
		t.Fatalf("chain height: %d", h)
	}
}
