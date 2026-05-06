package chain

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

// fixture: three contiguous blocks with one tx each.
func threeBlockFixture() []*Block {
	now := time.Date(2026, 5, 7, 0, 0, 0, 0, time.UTC)
	mk := func(h uint64, prevHash, txID string) *Block {
		blk := &Block{
			Height:    h,
			PrevHash:  prevHash,
			Timestamp: now.Add(time.Duration(h) * time.Second),
			Transactions: []*mempool.Tx{
				{ID: txID, Sender: "alice", Recipient: "bob", Amount: 1.0, Nonce: h},
			},
			StateRoot:  "state-" + txID,
			TotalFees:  0.001,
			ProducerID: "test-producer",
		}
		blk.Hash = computeBlockHash(blk)
		return blk
	}
	b0 := mk(0, "", "tx0")
	b1 := mk(1, b0.Hash, "tx1")
	b2 := mk(2, b1.Hash, "tx2")
	return []*Block{b0, b1, b2}
}

func TestAppendBlockToFile_RoundTripsViaLoadChainNDJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "chain.ndjson")

	blocks := threeBlockFixture()
	for _, blk := range blocks {
		if err := AppendBlockToFile(path, blk); err != nil {
			t.Fatalf("AppendBlockToFile(height=%d): %v", blk.Height, err)
		}
	}

	loaded, err := LoadChainNDJSON(path)
	if err != nil {
		t.Fatalf("LoadChainNDJSON: %v", err)
	}
	if got, want := len(loaded), len(blocks); got != want {
		t.Fatalf("loaded len: got %d want %d", got, want)
	}
	for i, blk := range loaded {
		if blk.Height != blocks[i].Height {
			t.Errorf("block[%d] height: got %d want %d", i, blk.Height, blocks[i].Height)
		}
		if blk.Hash != blocks[i].Hash {
			t.Errorf("block[%d] hash: got %s want %s", i, blk.Hash, blocks[i].Hash)
		}
		if blk.PrevHash != blocks[i].PrevHash {
			t.Errorf("block[%d] prev_hash: got %s want %s", i, blk.PrevHash, blocks[i].PrevHash)
		}
		if len(blk.Transactions) != 1 {
			t.Fatalf("block[%d] tx count: got %d want 1", i, len(blk.Transactions))
		}
		if blk.Transactions[0].ID != blocks[i].Transactions[0].ID {
			t.Errorf("block[%d] tx id: got %s want %s",
				i, blk.Transactions[0].ID, blocks[i].Transactions[0].ID)
		}
	}
}

func TestLoadChainNDJSON_MissingFileIsNoError(t *testing.T) {
	dir := t.TempDir()
	out, err := LoadChainNDJSON(filepath.Join(dir, "no-such-file.ndjson"))
	if err != nil {
		t.Fatalf("missing file should be no-error, got: %v", err)
	}
	if out != nil {
		t.Fatalf("missing file should yield nil slice, got %d entries", len(out))
	}
}

func TestLoadChainNDJSON_TruncatedTailReturnsParsedPrefix(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "chain.ndjson")
	blocks := threeBlockFixture()
	for _, blk := range blocks[:2] {
		if err := AppendBlockToFile(path, blk); err != nil {
			t.Fatalf("AppendBlockToFile: %v", err)
		}
	}
	// Simulate a crash mid-write: append a partial JSON line.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open for append: %v", err)
	}
	if _, err := f.WriteString(`{"height":2,"prev_hash":"abc",`); err != nil {
		t.Fatalf("write partial: %v", err)
	}
	f.Close()

	out, err := LoadChainNDJSON(path)
	if err == nil {
		t.Fatal("expected parse error on truncated tail; got nil")
	}
	if got := len(out); got != 2 {
		t.Fatalf("loaded prefix len: got %d want 2 (the two complete lines before the bad tail)", got)
	}
}

func TestRestoreChain_HappyPath(t *testing.T) {
	bp := NewBlockProducer(mempool.New(mempool.DefaultConfig()), NewAccountStore(), DefaultProducerConfig())

	blocks := threeBlockFixture()
	if err := bp.RestoreChain(blocks); err != nil {
		t.Fatalf("RestoreChain: %v", err)
	}
	if got, want := bp.TipHeight(), uint64(2); got != want {
		t.Fatalf("TipHeight: got %d want %d", got, want)
	}
	if !bp.HasTip() {
		t.Fatal("HasTip should be true after RestoreChain")
	}
	if got, want := bp.ChainHeight(), uint64(2); got != want {
		t.Fatalf("ChainHeight: got %d want %d", got, want)
	}
	tip, ok := bp.LatestBlock()
	if !ok {
		t.Fatal("LatestBlock not present after RestoreChain")
	}
	if tip.Hash != blocks[2].Hash {
		t.Errorf("tip.Hash: got %s want %s", tip.Hash, blocks[2].Hash)
	}
}

func TestRestoreChain_RejectsNonEmptyProducer(t *testing.T) {
	bp := NewBlockProducer(mempool.New(mempool.DefaultConfig()), NewAccountStore(), DefaultProducerConfig())
	blocks := threeBlockFixture()
	if err := bp.RestoreChain(blocks[:1]); err != nil {
		t.Fatalf("first RestoreChain: %v", err)
	}
	err := bp.RestoreChain(blocks[1:])
	if err == nil {
		t.Fatal("RestoreChain on non-empty producer should fail")
	}
}

func TestRestoreChain_RejectsNonContiguousHeights(t *testing.T) {
	bp := NewBlockProducer(mempool.New(mempool.DefaultConfig()), NewAccountStore(), DefaultProducerConfig())
	blocks := threeBlockFixture()
	// Skip block at index 1 → heights are 0, 2 (gap).
	gap := []*Block{blocks[0], blocks[2]}
	err := bp.RestoreChain(gap)
	if err == nil {
		t.Fatal("non-contiguous heights should fail RestoreChain")
	}
}

func TestRestoreChain_EmptySliceIsNoop(t *testing.T) {
	bp := NewBlockProducer(mempool.New(mempool.DefaultConfig()), NewAccountStore(), DefaultProducerConfig())
	if err := bp.RestoreChain(nil); err != nil {
		t.Fatalf("nil slice should be a no-op, got %v", err)
	}
	if bp.HasTip() {
		t.Fatal("HasTip should be false after no-op restore")
	}
}

func TestAppendBlockToFile_Validation(t *testing.T) {
	if err := AppendBlockToFile("", threeBlockFixture()[0]); err == nil {
		t.Fatal("empty path should error")
	}
	if err := AppendBlockToFile(filepath.Join(t.TempDir(), "x.ndjson"), nil); err == nil {
		t.Fatal("nil block should error")
	}
}

func TestLoadChainNDJSON_PathIsRequired(t *testing.T) {
	_, err := LoadChainNDJSON("")
	if err == nil {
		t.Fatal("empty path should error")
	}
	if errors.Is(err, os.ErrNotExist) {
		t.Fatal("empty-path error should NOT match os.ErrNotExist (it's a usage error, not a missing file)")
	}
}
