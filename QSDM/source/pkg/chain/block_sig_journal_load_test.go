package chain

// Tests for the parallel startup load (LoadVerifiedJournal on several
// workers): the same chain whatever the number of workers, and the first
// failure in journal order whatever order the workers finish in.

import (
	"strings"
	"testing"
)

// TestLoadVerifiedJournalWorkerCountsAgree: the load decodes and verifies on
// any number of workers and returns the same chain, in journal order, with
// the same signatures stripped.
func TestLoadVerifiedJournalWorkerCountsAgree(t *testing.T) {
	f := newJournalFixture(t, 40)
	for _, workers := range []int{1, 2, 3, 16} {
		bp := f.producer(t, f.blocks)
		v, err := bp.loadVerifiedJournal(f.path, workers)
		if err != nil {
			t.Fatalf("%d workers: %v", workers, err)
		}
		blocks := v.Blocks()
		if len(blocks) != len(f.blocks) {
			t.Fatalf("%d workers: %d blocks", workers, len(blocks))
		}
		for i, b := range blocks {
			if b.Height != uint64(i) || b.Hash != f.blocks[i].Hash {
				t.Fatalf("%d workers: block %d out of order", workers, i)
			}
			wantStripped := f.blocks[i].ProducerAuth.Signed() && i < len(blocks)-1
			if b.ProducerAuth.Stripped() != wantStripped {
				t.Fatalf("%d workers: block %d stripped %v", workers, i, b.ProducerAuth.Stripped())
			}
		}
		requireKeysShared(t, blocks[jfSigFrom:])
		if err := bp.RestoreVerifiedChain(blocks, v); err != nil {
			t.Fatal(err)
		}
		requireRelaysJournal(t, bp, f.blocks, f.lines)
	}
}

// TestLoadVerifiedJournalReportsTheFirstFailure: blocks are verified out of
// order on several workers, and the error is still the first failure in
// journal order, whichever kind it is.
func TestLoadVerifiedJournalReportsTheFirstFailure(t *testing.T) {
	f := newJournalFixture(t, 30)
	tamper := func(blocks []*Block, h int) []*Block {
		b := *blocks[h]
		b.ProducerAuth.Signature = append([]byte(nil), b.ProducerAuth.Signature...)
		b.ProducerAuth.Signature[50] ^= 1
		return replaceBlock(blocks, &b)
	}
	relink := func(blocks []*Block, h int) []*Block {
		// Block h re-signed on another parent: valid on its own, but it does
		// not extend block h-1.
		b := *blocks[h]
		b.PrevHash = blocks[h-2].Hash
		b.ProducerID = "legacy-peer"
		b.ProducerAuth = BFTWireAuth{}
		b.Hash = computeBlockHash(&b)
		if err := SignBlock(&b, f.newKey); err != nil {
			t.Fatal(err)
		}
		return replaceBlock(blocks, &b)
	}
	for _, tc := range []struct {
		name    string
		journal func() []byte
		want    string
	}{
		{"two bad signatures", func() []byte {
			return marshalTransitionBlocks(t, tamper(tamper(f.blocks, 9), 24))
		}, "line 10)"},
		{"a bad signature before a parse error", func() []byte {
			return append(marshalTransitionBlocks(t, tamper(f.blocks, 9)), "{not json\n"...)
		}, "line 10)"},
		{"a broken link before a bad signature", func() []byte {
			return marshalTransitionBlocks(t, tamper(relink(f.blocks, 12), 25))
		}, "broken linkage at height 12"},
		{"a bad signature before a broken link", func() []byte {
			return marshalTransitionBlocks(t, relink(tamper(f.blocks, 8), 20))
		}, "line 9)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			journalEdit(t, f.path, func([]byte) []byte { return tc.journal() })
			for _, workers := range []int{1, 4, 16} {
				for round := 0; round < 3; round++ {
					v, err := f.producer(t, nil).loadVerifiedJournal(f.path, workers)
					if err == nil || v != nil || !strings.Contains(err.Error(), tc.want) {
						t.Fatalf("%d workers: error %v, want %q", workers, err, tc.want)
					}
				}
			}
		})
	}
}
