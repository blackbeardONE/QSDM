package chain

// receipt_ndjson_test.go — round-trip + edge-case coverage for
// AppendBlockNDJSON / LoadNDJSON. The append-only NDJSON path
// replaces the legacy whole-store JSON Save in cmd/qsdm/main.go's
// post-seal hook; the regression cost of getting it wrong is
// "operator restarts validator and loses every tx receipt sealed
// since the last good snapshot," so the tests below pin the
// invariants that prevent that.

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestReceiptStore_AppendBlockNDJSON_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "receipts.ndjson")

	src := NewReceiptStore()
	src.Store(makeReceipt("tx-h0-a", 0, ReceiptSuccess))
	src.Store(makeReceipt("tx-h0-b", 0, ReceiptFailed))
	src.Store(makeReceipt("tx-h1-a", 1, ReceiptSuccess))
	src.Store(makeReceipt("tx-h2-a", 2, ReceiptSuccess))

	for _, h := range []uint64{0, 1, 2} {
		n, err := src.AppendBlockNDJSON(path, h)
		if err != nil {
			t.Fatalf("AppendBlockNDJSON(h=%d): %v", h, err)
		}
		want := len(src.GetByBlock(h))
		if n != want {
			t.Fatalf("h=%d wrote %d, expected %d", h, n, want)
		}
	}

	// Loader sees every receipt back, in append order, indexed
	// by both TxID and BlockHeight just like the source store.
	dst := NewReceiptStore()
	loaded, err := dst.LoadNDJSON(path)
	if err != nil {
		t.Fatalf("LoadNDJSON: %v", err)
	}
	if loaded != 4 {
		t.Fatalf("loaded %d, want 4", loaded)
	}
	for _, txid := range []string{"tx-h0-a", "tx-h0-b", "tx-h1-a", "tx-h2-a"} {
		if _, ok := dst.Get(txid); !ok {
			t.Errorf("Get(%q) miss after LoadNDJSON", txid)
		}
	}
	if got := len(dst.GetByBlock(0)); got != 2 {
		t.Errorf("GetByBlock(0) returned %d, want 2", got)
	}
}

func TestReceiptStore_AppendBlockNDJSON_EmptyHeightIsNoOp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "receipts.ndjson")

	src := NewReceiptStore()
	src.Store(makeReceipt("only-h0", 0, ReceiptSuccess))

	// Heights 1 and 2 have no receipts → AppendBlockNDJSON
	// must return (0, nil) and MUST NOT create the file.
	for _, h := range []uint64{1, 2} {
		n, err := src.AppendBlockNDJSON(path, h)
		if err != nil {
			t.Fatalf("h=%d: %v", h, err)
		}
		if n != 0 {
			t.Fatalf("h=%d wrote %d, want 0", h, n)
		}
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("file should not exist for empty appends, stat err=%v", err)
	}
}

func TestReceiptStore_LoadNDJSON_MissingFileNoError(t *testing.T) {
	dst := NewReceiptStore()
	n, err := dst.LoadNDJSON(filepath.Join(t.TempDir(), "does-not-exist.ndjson"))
	if err != nil {
		t.Fatalf("LoadNDJSON missing file: %v", err)
	}
	if n != 0 {
		t.Fatalf("LoadNDJSON missing file loaded %d, want 0", n)
	}
}

func TestReceiptStore_LoadNDJSON_TruncatedTailReturnsPartialAndErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "receipts.ndjson")

	src := NewReceiptStore()
	src.Store(makeReceipt("clean-1", 0, ReceiptSuccess))
	src.Store(makeReceipt("clean-2", 1, ReceiptSuccess))
	if _, err := src.AppendBlockNDJSON(path, 0); err != nil {
		t.Fatalf("seed h=0: %v", err)
	}
	if _, err := src.AppendBlockNDJSON(path, 1); err != nil {
		t.Fatalf("seed h=1: %v", err)
	}
	// Append a partial JSON line — simulates a process crash
	// mid-write.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open for append: %v", err)
	}
	if _, err := f.WriteString(`{"tx_id":"partial","block_h`); err != nil {
		t.Fatalf("write partial: %v", err)
	}
	_ = f.Close()

	dst := NewReceiptStore()
	n, err := dst.LoadNDJSON(path)
	if err == nil {
		t.Fatal("expected parse error on truncated tail; got nil")
	}
	// Both clean lines must be in the store even though the
	// partial one failed — that's the recovery posture.
	if n != 2 {
		t.Fatalf("partial-load count = %d, want 2", n)
	}
	if _, ok := dst.Get("clean-1"); !ok {
		t.Error("clean-1 should be loaded despite trailing partial line")
	}
	if _, ok := dst.Get("clean-2"); !ok {
		t.Error("clean-2 should be loaded despite trailing partial line")
	}
}

func TestReceiptStore_LoadNDJSON_SkipsBlankLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "receipts.ndjson")
	// File with two valid lines separated by a blank line —
	// exercises the len(raw)==0 continue branch.
	body := `{"tx_id":"a","block_height":0,"status":1,"timestamp":"2026-05-07T00:00:00Z"}` + "\n" +
		"\n" +
		`{"tx_id":"b","block_height":1,"status":1,"timestamp":"2026-05-07T00:00:01Z"}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	dst := NewReceiptStore()
	n, err := dst.LoadNDJSON(path)
	if err != nil {
		t.Fatalf("LoadNDJSON: %v", err)
	}
	if n != 2 {
		t.Fatalf("loaded %d, want 2 (blank line should skip)", n)
	}
}

func TestReceiptStore_AppendBlockNDJSON_EmptyPathErrors(t *testing.T) {
	src := NewReceiptStore()
	src.Store(makeReceipt("any", 0, ReceiptSuccess))
	if _, err := src.AppendBlockNDJSON("", 0); err == nil {
		t.Fatal("empty path should error")
	}
}

func TestReceiptStore_LoadNDJSON_EmptyPathErrors(t *testing.T) {
	dst := NewReceiptStore()
	if _, err := dst.LoadNDJSON(""); err == nil {
		t.Fatal("empty path should error")
	}
}

func TestReceiptStore_AppendBlockNDJSON_OrderingPreserved(t *testing.T) {
	// Receipts are stored with explicit IndexInBlock; we want
	// the on-disk order within a single block to match the
	// in-memory byBlock slice order, which is insertion order.
	// A future change that switches to map iteration would
	// non-deterministically reorder lines; this test pins it.
	dir := t.TempDir()
	path := filepath.Join(dir, "receipts.ndjson")

	src := NewReceiptStore()
	for i := 0; i < 10; i++ {
		r := makeReceipt(rune2id(i), 0, ReceiptSuccess)
		r.IndexInBlock = i
		src.Store(r)
	}
	if _, err := src.AppendBlockNDJSON(path, 0); err != nil {
		t.Fatalf("append: %v", err)
	}

	dst := NewReceiptStore()
	if _, err := dst.LoadNDJSON(path); err != nil {
		t.Fatalf("load: %v", err)
	}
	got := dst.GetByBlock(0)
	for i, r := range got {
		if r.IndexInBlock != i {
			t.Fatalf("position %d carries IndexInBlock=%d, want %d (NDJSON order regression)", i, r.IndexInBlock, i)
		}
	}
}

func rune2id(i int) string {
	return string(rune('a' + i))
}
