package chain

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func makeReceipt(txID string, height uint64, status ReceiptStatus) *TxReceipt {
	return &TxReceipt{
		TxID:        txID,
		BlockHeight: height,
		BlockHash:   "abc",
		Status:      status,
		GasUsed:     1000,
		Fee:         0.5,
		Timestamp:   time.Now(),
	}
}

func TestReceiptStore_StoreAndGet(t *testing.T) {
	rs := NewReceiptStore()
	r := makeReceipt("tx1", 0, ReceiptSuccess)
	rs.Store(r)

	got, ok := rs.Get("tx1")
	if !ok {
		t.Fatal("expected receipt")
	}
	if got.TxID != "tx1" {
		t.Fatalf("expected tx1, got %s", got.TxID)
	}
}

func TestReceiptStore_GetByBlock(t *testing.T) {
	rs := NewReceiptStore()
	rs.Store(makeReceipt("tx1", 0, ReceiptSuccess))
	rs.Store(makeReceipt("tx2", 0, ReceiptSuccess))
	rs.Store(makeReceipt("tx3", 1, ReceiptFailed))

	block0 := rs.GetByBlock(0)
	if len(block0) != 2 {
		t.Fatalf("expected 2 receipts for block 0, got %d", len(block0))
	}
	block1 := rs.GetByBlock(1)
	if len(block1) != 1 {
		t.Fatalf("expected 1 receipt for block 1, got %d", len(block1))
	}
}

func TestReceiptStore_GetByContract(t *testing.T) {
	rs := NewReceiptStore()
	r := makeReceipt("tx1", 0, ReceiptSuccess)
	r.ContractID = "token1"
	rs.Store(r)

	r2 := makeReceipt("tx2", 0, ReceiptSuccess)
	r2.ContractID = "token1"
	rs.Store(r2)

	r3 := makeReceipt("tx3", 1, ReceiptSuccess)
	r3.ContractID = "voting1"
	rs.Store(r3)

	tok := rs.GetByContract("token1")
	if len(tok) != 2 {
		t.Fatalf("expected 2 for token1, got %d", len(tok))
	}
	vot := rs.GetByContract("voting1")
	if len(vot) != 1 {
		t.Fatalf("expected 1 for voting1, got %d", len(vot))
	}
}

func TestReceiptStore_Recent(t *testing.T) {
	rs := NewReceiptStore()
	for i := 0; i < 5; i++ {
		rs.Store(makeReceipt("tx"+string(rune('a'+i)), uint64(i), ReceiptSuccess))
	}

	recent := rs.Recent(3)
	if len(recent) != 3 {
		t.Fatalf("expected 3 recent, got %d", len(recent))
	}
	// Most recent first
	if recent[0].TxID != "txe" {
		t.Fatalf("expected txe first, got %s", recent[0].TxID)
	}
}

func TestReceiptStore_SearchLogs(t *testing.T) {
	rs := NewReceiptStore()
	r := makeReceipt("tx1", 0, ReceiptSuccess)
	r.Logs = []LogEntry{{Topic: "Transfer", Data: map[string]interface{}{"to": "bob"}, Index: 0}}
	rs.Store(r)

	r2 := makeReceipt("tx2", 0, ReceiptSuccess)
	r2.Logs = []LogEntry{{Topic: "Approve", Index: 0}}
	rs.Store(r2)

	transfers := rs.SearchLogs("Transfer")
	if len(transfers) != 1 {
		t.Fatalf("expected 1 Transfer receipt, got %d", len(transfers))
	}
	if transfers[0].TxID != "tx1" {
		t.Fatalf("expected tx1, got %s", transfers[0].TxID)
	}
}

func TestReceiptStore_Stats(t *testing.T) {
	rs := NewReceiptStore()
	rs.Store(makeReceipt("tx1", 0, ReceiptSuccess))
	rs.Store(makeReceipt("tx2", 0, ReceiptFailed))
	rs.Store(makeReceipt("tx3", 1, ReceiptSuccess))

	stats := rs.Stats()
	if stats["total"] != 3 {
		t.Fatalf("expected 3 total, got %v", stats["total"])
	}
	if stats["failed"] != 1 {
		t.Fatalf("expected 1 failed, got %v", stats["failed"])
	}
}

func TestReceiptStore_SaveAndLoad(t *testing.T) {
	dir := filepath.Join(os.TempDir(), "qsdm_receipt_test")
	os.MkdirAll(dir, 0755)
	defer os.RemoveAll(dir)

	rs := NewReceiptStore()
	r := makeReceipt("tx1", 0, ReceiptSuccess)
	r.Logs = []LogEntry{{Topic: "Transfer", Index: 0}}
	rs.Store(r)
	rs.Store(makeReceipt("tx2", 1, ReceiptFailed))

	path := filepath.Join(dir, "receipts.json")
	if err := rs.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	rs2 := NewReceiptStore()
	loaded, err := rs2.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded != 2 {
		t.Fatalf("expected 2 loaded, got %d", loaded)
	}
	got, _ := rs2.Get("tx1")
	if len(got.Logs) != 1 {
		t.Fatal("expected 1 log after load")
	}
}

func TestReceiptStore_Count(t *testing.T) {
	rs := NewReceiptStore()
	if rs.Count() != 0 {
		t.Fatal("expected 0")
	}
	rs.Store(makeReceipt("tx1", 0, ReceiptSuccess))
	if rs.Count() != 1 {
		t.Fatal("expected 1")
	}
}
