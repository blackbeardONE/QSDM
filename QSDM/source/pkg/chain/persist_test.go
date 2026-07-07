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

func TestStateLockRejectsSecondWriterAndReleases(t *testing.T) {
	path := filepath.Join(t.TempDir(), "validator.lock")
	first, err := AcquireStateLock(path)
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}
	if _, err := AcquireStateLock(path); err == nil {
		t.Fatal("second lock on the same state directory should fail")
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close first lock: %v", err)
	}
	second, err := AcquireStateLock(path)
	if err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("close second lock: %v", err)
	}
}

func TestChainJournalRejectsGapWithoutWritingIt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "chain.ndjson")
	blocks := threeBlockFixture()
	if err := AppendBlockToFile(path, blocks[0]); err != nil {
		t.Fatal(err)
	}
	j, err := OpenChainJournal(path, blocks[0])
	if err != nil {
		t.Fatalf("OpenChainJournal: %v", err)
	}
	if err := j.Append(blocks[2]); err == nil {
		t.Fatal("journal should reject a block that skips the guarded tip")
	}
	if err := j.Append(blocks[1]); err != nil {
		t.Fatalf("append contiguous block: %v", err)
	}
	if err := j.Close(); err != nil {
		t.Fatalf("close journal: %v", err)
	}
	loaded, err := LoadChainNDJSON(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 2 || loaded[1].Hash != blocks[1].Hash {
		t.Fatalf("journal contents = %+v, want blocks 0 and 1 only", loaded)
	}
}

func TestReplaceChainFileArchivesForkedJournal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "chain.ndjson")
	backup := filepath.Join(dir, "chain.forked.bak")
	blocks := threeBlockFixture()
	for _, blk := range blocks {
		if err := AppendBlockToFile(path, blk); err != nil {
			t.Fatal(err)
		}
	}
	fork := *blocks[2]
	fork.Timestamp = fork.Timestamp.Add(time.Second)
	fork.Hash = computeBlockHash(&fork)
	if err := AppendBlockToFile(path, &fork); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceChainFile(path, backup, blocks); err != nil {
		t.Fatalf("ReplaceChainFile: %v", err)
	}
	canonical, err := LoadChainNDJSON(path)
	if err != nil {
		t.Fatal(err)
	}
	archived, err := LoadChainNDJSON(backup)
	if err != nil {
		t.Fatal(err)
	}
	if len(canonical) != 3 || len(archived) != 4 {
		t.Fatalf("canonical=%d archived=%d, want 3 and 4", len(canonical), len(archived))
	}
}

func TestRestoreChainRejectsBrokenHashLinkAndTamperedHash(t *testing.T) {
	blocks := threeBlockFixture()
	broken := make([]*Block, len(blocks))
	for i, blk := range blocks {
		cp := *blk
		broken[i] = &cp
	}
	broken[1].PrevHash = "not-the-parent"
	broken[1].Hash = computeBlockHash(broken[1])
	bp := NewBlockProducer(mempool.New(mempool.DefaultConfig()), NewAccountStore(), DefaultProducerConfig())
	if err := bp.RestoreChain(broken); err == nil {
		t.Fatal("broken parent hash should be rejected")
	}

	tampered := make([]*Block, len(blocks))
	for i, blk := range blocks {
		cp := *blk
		tampered[i] = &cp
	}
	tampered[2].Hash = "tampered"
	bp = NewBlockProducer(mempool.New(mempool.DefaultConfig()), NewAccountStore(), DefaultProducerConfig())
	if err := bp.RestoreChain(tampered); err == nil {
		t.Fatal("tampered block hash should be rejected")
	}
}
