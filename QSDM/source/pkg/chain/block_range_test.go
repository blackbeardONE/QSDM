package chain

// Tests for block_range.go: height lookups and ranges are index computations
// on the contiguous chain, with exactly the results of a walk of the whole
// chain, for a chain that starts at genesis and for one that starts above it.

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

// plainChain is n linked, unsigned blocks from height base.
func plainChain(base uint64, n int) []*Block {
	blocks := make([]*Block, n)
	for i := range blocks {
		b := &Block{Height: base + uint64(i), Timestamp: time.Unix(1700000000+int64(i), 0).UTC(), StateRoot: fmt.Sprintf("root-%d", i)}
		if i > 0 {
			b.PrevHash = blocks[i-1].Hash
		} else if base > 0 {
			b.PrevHash = fmt.Sprintf("%064x", base-1) // the parent of a journal that starts above genesis
		}
		b.Hash = computeBlockHash(b)
		blocks[i] = b
	}
	return blocks
}

// walkBlock and walkRange are the lookups as they were: a walk of the chain.
func walkBlock(chain []*Block, h uint64) (*Block, bool) {
	for _, b := range chain {
		if b.Height == h {
			return b, true
		}
	}
	return nil, false
}

func walkRange(chain []*Block, from, to uint64, limit int) []*Block {
	var out []*Block
	for _, b := range chain {
		if b.Height >= from && b.Height <= to && (limit <= 0 || len(out) < limit) {
			out = append(out, b)
		}
	}
	return out
}

func sameBlocks(a, b []*Block) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// requireIndexMatchesWalk compares every lookup over heights around the
// chain with a walk of it.
func requireIndexMatchesWalk(t *testing.T, bp *BlockProducer) {
	t.Helper()
	chain := bp.AllBlocks()
	if len(chain) == 0 {
		t.Fatal("empty chain")
	}
	lo, hi := chain[0].Height, chain[len(chain)-1].Height
	start := uint64(0)
	if lo > 3 {
		start = lo - 3
	}
	for h := start; h <= hi+3; h++ {
		got, ok := bp.GetBlock(h)
		want, wantOK := walkBlock(chain, h)
		if ok != wantOK || got != want {
			t.Fatalf("GetBlock(%d) = %v/%v, the walk finds %v/%v", h, got, ok, want, wantOK)
		}
	}
	for from := start; from <= hi+2; from++ {
		for to := start; to <= hi+2; to++ {
			if got, want := bp.BlockRange(from, to), walkRange(chain, from, to, 0); !sameBlocks(got, want) {
				t.Fatalf("BlockRange(%d, %d) = %d blocks, the walk finds %d", from, to, len(got), len(want))
			}
			if got, want := bp.Headers(from, to), walkRange(chain, from, to, 0); len(got) != len(want) || (len(got) > 0 && got[0].Hash != want[0].Hash) {
				t.Fatalf("Headers(%d, %d) = %d headers, the walk finds %d", from, to, len(got), len(want))
			}
			for _, limit := range []int{1, 2, 5} {
				bp.mu.Lock()
				got := bp.blockRangeLocked(from, to, limit)
				bp.mu.Unlock()
				if want := walkRange(chain, from, to, limit); !sameBlocks(got, want) {
					t.Fatalf("blockRangeLocked(%d, %d, %d) = %d blocks, the walk finds %d", from, to, limit, len(got), len(want))
				}
			}
		}
	}
	if got := bp.BlockRange(0, ^uint64(0)); !sameBlocks(got, chain) {
		t.Fatal("BlockRange over every height is not the chain")
	}
	if got := bp.BlockRange(hi, ^uint64(0)); len(got) != 1 || got[0].Height != hi {
		t.Fatal("BlockRange to the highest height")
	}
}

func TestBlockIndexMatchesWalk(t *testing.T) {
	for _, base := range []uint64{0, 7} {
		t.Run(fmt.Sprintf("chain from height %d", base), func(t *testing.T) {
			pool := mempool.New(mempool.DefaultConfig())
			bp := NewBlockProducer(pool, newTestApplier(), DefaultProducerConfig())
			if bp.BlockRange(0, 10) != nil || bp.Headers(0, 10) != nil {
				t.Fatal("an empty chain has blocks")
			}
			if _, ok := bp.GetBlock(0); ok {
				t.Fatal("an empty chain has block 0")
			}
			restored := plainChain(base, 6)
			if err := bp.RestoreChain(append([]*Block(nil), restored...)); err != nil {
				t.Fatal(err)
			}
			requireIndexMatchesWalk(t, bp)
			// Appends keep the chain contiguous.
			for i := 0; i < 3; i++ {
				if err := pool.Add(makeTx(fmt.Sprintf("idx-%d", i), 0)); err != nil {
					t.Fatal(err)
				}
				if _, err := bp.ProduceBlock(); err != nil {
					t.Fatal(err)
				}
			}
			requireIndexMatchesWalk(t, bp)
			if tip := bp.TipHeight(); tip != base+8 {
				t.Fatalf("tip %d, want %d", tip, base+8)
			}
		})
	}
}

// TestExternalAppendChecksExistingHeightsByIndex: an external block at a
// height the chain holds is idempotent or a conflict, as with the walk, and
// a block below a chain that starts above genesis is not taken for one.
func TestExternalAppendChecksExistingHeightsByIndex(t *testing.T) {
	for _, base := range []uint64{0, 7} {
		t.Run(fmt.Sprintf("chain from height %d", base), func(t *testing.T) {
			bp := NewBlockProducer(mempool.New(mempool.DefaultConfig()), newTestApplier(), DefaultProducerConfig())
			blocks := plainChain(base, 5)
			if err := bp.RestoreChain(append([]*Block(nil), blocks...)); err != nil {
				t.Fatal(err)
			}
			same := *blocks[2]
			if err := bp.TryAppendExternalBlock(&same); err != nil {
				t.Fatalf("the block already at height %d: %v", same.Height, err)
			}
			other := *blocks[2]
			other.StateRoot = "other"
			other.Hash = computeBlockHash(&other)
			var conflict *ExternalAppendConflictError
			if err := bp.TryAppendExternalBlock(&other); !errors.As(err, &conflict) || conflict.ExistingHash != blocks[2].Hash || conflict.Height != other.Height {
				t.Fatalf("another block at height %d: %v", other.Height, err)
			}
			if base > 0 {
				below := plainChain(0, int(base))[base-1]
				if err := bp.TryAppendExternalBlock(below); err == nil || errors.As(err, &conflict) {
					t.Fatalf("a block below the chain: %v", err)
				}
			}
			if n := len(bp.AllBlocks()); n != 5 {
				t.Fatalf("the chain holds %d blocks, want 5", n)
			}
		})
	}
}
