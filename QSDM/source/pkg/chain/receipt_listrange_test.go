package chain

// receipt_listrange_test.go — pins ReceiptStore.ListByHeightRange,
// the height-range walk that powers the public
// /api/v1/receipts (no tx_id) endpoint and the chain
// dashboard's "recent transactions" tile.

import (
	"testing"
	"time"
)

func mkRangeReceipt(txID string, height uint64, idx int) *TxReceipt {
	return &TxReceipt{
		TxID:         txID,
		BlockHeight:  height,
		BlockHash:    "blk-" + txID,
		Status:       ReceiptSuccess,
		Timestamp:    time.Unix(1_000_000_000+int64(height), 0).UTC(),
		IndexInBlock: idx,
	}
}

func storeWithReceipts(t *testing.T) *ReceiptStore {
	t.Helper()
	rs := NewReceiptStore()
	// Heights 1..5 with 2 receipts each, IndexInBlock 0..1.
	for h := uint64(1); h <= 5; h++ {
		rs.Store(mkRangeReceipt("h"+string(rune('0'+h))+"-a", h, 0))
		rs.Store(mkRangeReceipt("h"+string(rune('0'+h))+"-b", h, 1))
	}
	return rs
}

func TestListByHeightRange_NewestFirst(t *testing.T) {
	rs := storeWithReceipts(t)
	got := rs.ListByHeightRange(1, 5, 100)
	if len(got) != 10 {
		t.Fatalf("len = %d, want 10", len(got))
	}
	// Heights walked from `to` (5) down to `from` (1).
	// First receipt should be height 5, IndexInBlock 0.
	if got[0].BlockHeight != 5 || got[0].IndexInBlock != 0 {
		t.Errorf("got[0] height=%d idx=%d, want height=5 idx=0", got[0].BlockHeight, got[0].IndexInBlock)
	}
	// Last receipt should be height 1, IndexInBlock 1.
	if got[9].BlockHeight != 1 || got[9].IndexInBlock != 1 {
		t.Errorf("got[9] height=%d idx=%d, want height=1 idx=1", got[9].BlockHeight, got[9].IndexInBlock)
	}
}

func TestListByHeightRange_RespectsLimit(t *testing.T) {
	rs := storeWithReceipts(t)
	got := rs.ListByHeightRange(1, 5, 3)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3 (capped at limit)", len(got))
	}
	// First two receipts from height 5, then one from
	// height 4.
	if got[0].BlockHeight != 5 || got[1].BlockHeight != 5 {
		t.Errorf("got[0..1] heights = %d,%d, want 5,5", got[0].BlockHeight, got[1].BlockHeight)
	}
	if got[2].BlockHeight != 4 {
		t.Errorf("got[2].BlockHeight = %d, want 4", got[2].BlockHeight)
	}
}

func TestListByHeightRange_LimitZeroReturnsNil(t *testing.T) {
	rs := storeWithReceipts(t)
	if got := rs.ListByHeightRange(1, 5, 0); got != nil {
		t.Errorf("limit=0 returned %v, want nil", got)
	}
	if got := rs.ListByHeightRange(1, 5, -1); got != nil {
		t.Errorf("limit=-1 returned %v, want nil", got)
	}
}

func TestListByHeightRange_FromGreaterThanToReturnsNil(t *testing.T) {
	rs := storeWithReceipts(t)
	if got := rs.ListByHeightRange(5, 3, 10); got != nil {
		t.Errorf("from>to returned %v, want nil", got)
	}
}

func TestListByHeightRange_NarrowRange(t *testing.T) {
	rs := storeWithReceipts(t)
	got := rs.ListByHeightRange(3, 3, 100)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	for i, r := range got {
		if r.BlockHeight != 3 {
			t.Errorf("got[%d].BlockHeight = %d, want 3", i, r.BlockHeight)
		}
	}
}

func TestListByHeightRange_GapInHeights(t *testing.T) {
	rs := NewReceiptStore()
	rs.Store(mkRangeReceipt("a", 2, 0))
	rs.Store(mkRangeReceipt("b", 4, 0))
	got := rs.ListByHeightRange(1, 5, 100)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (heights 1,3,5 have no receipts)", len(got))
	}
	if got[0].BlockHeight != 4 || got[1].BlockHeight != 2 {
		t.Errorf("ordering: got %d,%d, want 4,2", got[0].BlockHeight, got[1].BlockHeight)
	}
}

func TestListByHeightRange_FromZero(t *testing.T) {
	rs := NewReceiptStore()
	rs.Store(mkRangeReceipt("genesis", 0, 0))
	rs.Store(mkRangeReceipt("first", 1, 0))
	got := rs.ListByHeightRange(0, 1, 100)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (must include height 0)", len(got))
	}
	if got[0].BlockHeight != 1 || got[1].BlockHeight != 0 {
		t.Errorf("ordering: got %d,%d, want 1,0", got[0].BlockHeight, got[1].BlockHeight)
	}
}

func TestListByHeightRange_EmptyStore(t *testing.T) {
	rs := NewReceiptStore()
	got := rs.ListByHeightRange(0, 100, 10)
	if len(got) != 0 {
		t.Errorf("len = %d, want 0", len(got))
	}
}
