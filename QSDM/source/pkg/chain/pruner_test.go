package chain

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

func makeBlock(height uint64) *Block {
	txs := []*mempool.Tx{{ID: fmt.Sprintf("tx_h%d", height), Sender: "a", Recipient: "b", Amount: 1, Fee: 0.1}}
	b := &Block{
		Height:       height,
		PrevHash:     "prev",
		Hash:         fmt.Sprintf("hash_%d", height),
		Timestamp:    time.Now(),
		Transactions: txs,
		StateRoot:    "sr",
		TotalFees:    0.1,
		GasUsed:      100,
		ProducerID:   "node-0",
	}
	return b
}

func TestChainPruner_AddAndRetain(t *testing.T) {
	cp := NewChainPruner(PrunerConfig{KeepBlocks: 5})

	for i := uint64(0); i < 3; i++ {
		cp.AddBlock(makeBlock(i))
	}

	if cp.TotalHeaders() != 3 {
		t.Fatalf("expected 3 headers, got %d", cp.TotalHeaders())
	}
	if cp.RetainedBlocks() != 3 {
		t.Fatalf("expected 3 retained, got %d", cp.RetainedBlocks())
	}
}

func TestChainPruner_PruneOldBlocks(t *testing.T) {
	dir := filepath.Join(os.TempDir(), "qsdm_pruner_test")
	os.MkdirAll(dir, 0755)
	defer os.RemoveAll(dir)

	cp := NewChainPruner(PrunerConfig{KeepBlocks: 3, ArchiveDir: dir})

	for i := uint64(0); i < 10; i++ {
		cp.AddBlock(makeBlock(i))
	}

	pruned, err := cp.Prune()
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if pruned < 1 {
		t.Fatal("expected at least 1 block pruned")
	}

	// Headers should all be retained
	if cp.TotalHeaders() != 10 {
		t.Fatalf("expected 10 headers, got %d", cp.TotalHeaders())
	}

	// Old block should be pruned from memory
	_, ok := cp.GetFullBlock(0)
	if ok {
		t.Fatal("block 0 should be pruned from full blocks")
	}

	// Recent blocks should still be available
	_, ok = cp.GetFullBlock(9)
	if !ok {
		t.Fatal("block 9 should still be in memory")
	}
}

func TestChainPruner_HeadersNeverPruned(t *testing.T) {
	dir := filepath.Join(os.TempDir(), "qsdm_pruner_hdr")
	os.MkdirAll(dir, 0755)
	defer os.RemoveAll(dir)

	cp := NewChainPruner(PrunerConfig{KeepBlocks: 2, ArchiveDir: dir})

	for i := uint64(0); i < 20; i++ {
		cp.AddBlock(makeBlock(i))
	}
	cp.Prune()

	// All headers retained
	for i := uint64(0); i < 20; i++ {
		h, ok := cp.GetHeader(i)
		if !ok {
			t.Fatalf("header at height %d should be retained", i)
		}
		if h.Height != i {
			t.Fatalf("expected height %d, got %d", i, h.Height)
		}
	}
}

func TestChainPruner_ArchiveToDisk(t *testing.T) {
	dir := filepath.Join(os.TempDir(), "qsdm_pruner_archive")
	os.MkdirAll(dir, 0755)
	defer os.RemoveAll(dir)

	cp := NewChainPruner(PrunerConfig{KeepBlocks: 2, ArchiveDir: dir})

	for i := uint64(0); i < 5; i++ {
		cp.AddBlock(makeBlock(i))
	}
	cp.Prune()

	if cp.ArchivedCount() == 0 {
		t.Fatal("expected archived blocks")
	}

	// Load from archive
	ab, err := cp.LoadArchived(0)
	if err != nil {
		t.Fatalf("LoadArchived: %v", err)
	}
	if ab.Header.Height != 0 {
		t.Fatalf("expected height 0, got %d", ab.Header.Height)
	}
}

func TestChainPruner_IsPruned(t *testing.T) {
	dir := filepath.Join(os.TempDir(), "qsdm_pruner_ispruned")
	os.MkdirAll(dir, 0755)
	defer os.RemoveAll(dir)

	cp := NewChainPruner(PrunerConfig{KeepBlocks: 2, ArchiveDir: dir})
	for i := uint64(0); i < 5; i++ {
		cp.AddBlock(makeBlock(i))
	}
	cp.Prune()

	if !cp.IsPruned(0) {
		t.Fatal("block 0 should be pruned")
	}
	if cp.IsPruned(4) {
		t.Fatal("block 4 should not be pruned")
	}
}

func TestChainPruner_HeaderRange(t *testing.T) {
	cp := NewChainPruner(PrunerConfig{KeepBlocks: 100})
	for i := uint64(0); i < 10; i++ {
		cp.AddBlock(makeBlock(i))
	}

	headers := cp.HeaderRange(3, 6)
	if len(headers) != 4 {
		t.Fatalf("expected 4 headers, got %d", len(headers))
	}
	if headers[0].Height != 3 || headers[3].Height != 6 {
		t.Fatal("unexpected header range")
	}
}

func TestChainPruner_NoPruneWhenUnderLimit(t *testing.T) {
	cp := NewChainPruner(PrunerConfig{KeepBlocks: 100})
	for i := uint64(0); i < 5; i++ {
		cp.AddBlock(makeBlock(i))
	}

	pruned, _ := cp.Prune()
	if pruned != 0 {
		t.Fatalf("expected 0 pruned, got %d", pruned)
	}
}
