package chain

import (
	"os"
	"path/filepath"
	"testing"
)

// Re-storing a receipt must replace it, not duplicate it across the secondary
// indexes.
//
// byTxID was always a map, so Get looked correct. byBlock, byContract and order
// appended unconditionally, so GetByBlock returned the SAME TRANSACTION TWICE
// and Recent listed it twice. Save writes from order, so the duplication
// survived a save/load cycle and every restart after it.
func TestReceiptStore_reStoreReplacesAcrossAllIndexes(t *testing.T) {
	rs := NewReceiptStore()
	r := &TxReceipt{TxID: "tx1", BlockHeight: 5, ContractID: "c1"}
	rs.Store(r)
	rs.Store(r)

	if got := len(rs.GetByBlock(5)); got != 1 {
		t.Errorf("GetByBlock(5) = %d receipts for one transaction, want 1", got)
	}
	if got := len(rs.GetByContract("c1")); got != 1 {
		t.Errorf("GetByContract = %d, want 1", got)
	}
	if got := len(rs.Recent(10)); got != 1 {
		t.Errorf("Recent = %d, want 1", got)
	}
	if got := rs.Count(); got != 1 {
		t.Errorf("Count = %d, want 1", got)
	}
}

// A replace must carry the newer field values rather than keeping the first
// write, whatever caused the re-store.
//
// An earlier version of this comment attributed it to ProduceBlock storing a
// receipt before and after the block hash is known. That is false --
// blockreceipts.go:90 and :113 are mutually exclusive within one call. The
// invariant is worth pinning regardless of which caller re-stores.
func TestReceiptStore_reStoreKeepsLatestFields(t *testing.T) {
	rs := NewReceiptStore()
	rs.Store(&TxReceipt{TxID: "tx1", BlockHeight: 5})
	rs.Store(&TxReceipt{TxID: "tx1", BlockHeight: 5, BlockHash: "final-hash"})

	got, ok := rs.Get("tx1")
	if !ok || got.BlockHash != "final-hash" {
		t.Fatalf("Get returned %+v, want BlockHash=final-hash", got)
	}
	byBlock := rs.GetByBlock(5)
	if len(byBlock) != 1 || byBlock[0].BlockHash != "final-hash" {
		t.Errorf("byBlock holds %+v, want one receipt carrying the final hash", byBlock)
	}
}

// A receipt re-stored at a different height must leave the old bucket, or the
// stale height keeps serving a receipt that no longer claims it.
func TestReceiptStore_reStoreAtNewHeightLeavesOldBucket(t *testing.T) {
	rs := NewReceiptStore()
	rs.Store(&TxReceipt{TxID: "tx1", BlockHeight: 5})
	rs.Store(&TxReceipt{TxID: "tx1", BlockHeight: 6})

	if got := len(rs.GetByBlock(5)); got != 0 {
		t.Errorf("height 5 still serves %d receipts after the receipt moved to 6", got)
	}
	if got := len(rs.GetByBlock(6)); got != 1 {
		t.Errorf("height 6 serves %d receipts, want 1", got)
	}
}

// LoadNDJSON's doc comment promises that loading the same file twice leaves the
// same final state as loading it once, and invites operators to do exactly that
// to merge log segments. Measured before the fix, the second load grew order
// 1->2 and byBlock 1->2 -- the documented, invited operation corrupted the
// indexes.
func TestReceiptStore_LoadNDJSONTwiceIsIdempotent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "r.ndjson")
	if err := os.WriteFile(p, []byte(`{"tx_id":"tx1","block_height":5}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rs := NewReceiptStore()
	if _, err := rs.LoadNDJSON(p); err != nil {
		t.Fatal(err)
	}
	firstBlock := len(rs.GetByBlock(5))
	if _, err := rs.LoadNDJSON(p); err != nil {
		t.Fatal(err)
	}
	if got := len(rs.GetByBlock(5)); got != firstBlock {
		t.Errorf("second LoadNDJSON changed byBlock from %d to %d; the doc comment promises "+
			"the same final state as loading once", firstBlock, got)
	}
	if got := rs.Count(); got != 1 {
		t.Errorf("Count = %d after two loads of a one-receipt file, want 1", got)
	}
}

// The legacy JSON loader must report distinct receipts, not file entries: the
// count is logged at boot as "receipts_loaded".
func TestReceiptStore_LoadCountsDistinctReceipts(t *testing.T) {
	p := filepath.Join(t.TempDir(), "r.json")
	payload := `[{"tx_id":"tx1","block_height":5},
	             {"tx_id":"tx1","block_height":5},
	             {"tx_id":"tx2","block_height":5}]`
	if err := os.WriteFile(p, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	rs := NewReceiptStore()
	n, err := rs.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("Load reported %d, want 2 distinct (file has 3 entries, two sharing a tx_id)", n)
	}
	if got := len(rs.GetByBlock(5)); got != 2 {
		t.Errorf("GetByBlock(5) = %d, want 2", got)
	}
}
