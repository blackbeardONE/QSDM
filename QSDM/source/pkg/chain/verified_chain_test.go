package chain

// Tests for verified_chain.go: a chain verified once (here by
// VerifyProducerTransitionChain; by LoadVerifiedJournal in
// block_sig_journal_test.go) restores without being verified again, and only
// the very blocks that were verified do.

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/producerpolicy"
)

// journalFixture is a producer-transition chain and its journal. Block 0 is
// genesis, blocks 1..sigFrom-1 are unsigned history, sigFrom..checkpoint are
// signed by the historical producer and the rest by the replacement. Every
// block but genesis carries a transfer.
type journalFixture struct {
	policy         *producerpolicy.Transition
	blocks         []*Block // as sealed
	path           string   // the journal: one json.Marshal line per block
	lines          [][]byte
	oldKey, newKey BFTSigner
}

const (
	jfSigFrom    = 3
	jfCheckpoint = 6
)

func newJournalFixture(t *testing.T, n int) journalFixture {
	t.Helper()
	if n <= jfCheckpoint+1 {
		t.Fatalf("fixture needs more than %d blocks", jfCheckpoint+1)
	}
	oldKey, oldAddr := newBFTKey(t)
	newKey, newAddr := newBFTKey(t)
	sim := newTestApplier()
	blocks := make([]*Block, n)
	for i := range blocks {
		b := &Block{Height: uint64(i), Timestamp: time.Unix(1700000000+int64(i), 0).UTC(), ProducerID: "legacy-peer"}
		if i > 0 {
			tx := &mempool.Tx{ID: fmt.Sprintf("jtx-%d", i), Sender: "alice", Recipient: shareTestRecipients[i%len(shareTestRecipients)],
				Amount: 1, Nonce: uint64(i), ContractID: "qsdm/journal-test"}
			if err := sim.ApplyTx(tx); err != nil {
				t.Fatal(err)
			}
			b.Transactions = []*mempool.Tx{tx}
			b.PrevHash = blocks[i-1].Hash
		}
		b.StateRoot = sim.StateRoot()
		b.Hash = computeBlockHash(b)
		if i >= jfSigFrom {
			key := BFTSigner(oldKey)
			if i > jfCheckpoint {
				key = newKey
			}
			if err := SignBlock(b, key); err != nil {
				t.Fatal(err)
			}
		}
		blocks[i] = b
	}
	prefix := marshalTransitionBlocks(t, blocks[:jfCheckpoint+1])
	f := journalFixture{
		policy: &producerpolicy.Transition{Version: 1, CheckpointHeight: jfCheckpoint, CheckpointHash: blocks[jfCheckpoint].Hash,
			HistoricalSignatureHeight: jfSigFrom, HistoricalProducer: oldAddr, ReplacementProducer: newAddr, EffectiveHeight: jfCheckpoint + 1,
			HistoricalPrefixBytes: int64(len(prefix)), HistoricalPrefixSHA256: fmt.Sprintf("%x", sha256.Sum256(prefix))},
		blocks: blocks, oldKey: oldKey, newKey: newKey,
	}
	f.path, f.lines = shareTestJournal(t, blocks)
	return f
}

// applierAfter is the test applier's state after blocks.
func applierAfter(t *testing.T, blocks []*Block) *testApplier {
	t.Helper()
	ta := newTestApplier()
	for _, b := range blocks {
		for _, tx := range b.Transactions {
			if err := ta.ApplyTx(tx); err != nil {
				t.Fatal(err)
			}
		}
	}
	return ta
}

// producer is a producer that enforces f's transition, with its applier at
// the state after blocks.
func (f journalFixture) producer(t *testing.T, blocks []*Block) *BlockProducer {
	t.Helper()
	bp := NewBlockProducer(mempool.New(mempool.DefaultConfig()), applierAfter(t, blocks), DefaultProducerConfig())
	if err := bp.SetProducerTransition(f.policy); err != nil {
		t.Fatal(err)
	}
	return bp
}

// verified loads f's journal with LoadChainNDJSON and verifies it on a fresh
// producer.
func (f journalFixture) verified(t *testing.T) (*VerifiedChain, []*Block) {
	t.Helper()
	loaded, err := LoadChainNDJSON(f.path)
	if err != nil {
		t.Fatal(err)
	}
	v, err := f.producer(t, nil).VerifyProducerTransitionChain(loaded)
	if err != nil {
		t.Fatal(err)
	}
	return v, loaded
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func replaceBlock(blocks []*Block, b *Block) []*Block {
	out := append([]*Block(nil), blocks...)
	out[b.Height] = b
	return out
}

// resealFrom relinks and re-signs blocks[from:] after an edit changed the
// hash of the block before them.
func resealFrom(t *testing.T, f journalFixture, blocks []*Block, from int) []*Block {
	t.Helper()
	for i := from; i < len(blocks); i++ {
		b := *blocks[i]
		b.PrevHash = blocks[i-1].Hash
		b.ProducerID = "legacy-peer"
		b.ProducerAuth = BFTWireAuth{}
		b.Hash = computeBlockHash(&b)
		key := f.oldKey
		if i > jfCheckpoint {
			key = f.newKey
		}
		if err := SignBlock(&b, key); err != nil {
			t.Fatal(err)
		}
		blocks[i] = &b
	}
	return blocks
}

// TestRestoreVerifiedChainDoesNotVerifyAgain: RestoreVerifiedChain installs
// the verified blocks without verifying them again, while RestoreChain, and
// RestoreVerifiedChain without a VerifiedChain, verify every block.
func TestRestoreVerifiedChainDoesNotVerifyAgain(t *testing.T) {
	f := newJournalFixture(t, 12)
	v, blocks := f.verified(t)
	// Break block 8's signature in place, after the verification. Nothing in
	// a node changes a block once it is decoded; here it shows whether a
	// restore looks at the signatures again.
	b := blocks[8]
	b.ProducerAuth.Signature = append([]byte(nil), b.ProducerAuth.Signature...)
	b.ProducerAuth.Signature[3] ^= 1
	if err := f.producer(t, nil).RestoreChain(blocks); !errors.Is(err, ErrBlockBadSignature) {
		t.Fatalf("RestoreChain = %v, want ErrBlockBadSignature", err)
	}
	if err := f.producer(t, nil).RestoreVerifiedChain(blocks, nil); !errors.Is(err, ErrBlockBadSignature) {
		t.Fatalf("RestoreVerifiedChain without the verified chain = %v, want ErrBlockBadSignature", err)
	}
	bp := f.producer(t, nil)
	if err := bp.RestoreVerifiedChain(blocks, v); err != nil {
		t.Fatal(err)
	}
	if tip, ok := bp.LatestBlock(); !ok || tip != blocks[len(blocks)-1] || bp.TipHeight() != 11 || !bp.HasTip() {
		t.Fatal("restored tip")
	}
	if err := bp.RestoreVerifiedChain(blocks, v); err == nil {
		t.Fatal("restored onto a non-empty producer")
	}
	// The verification itself refuses the broken signature.
	if _, err := f.producer(t, nil).VerifyProducerTransitionChain(blocks); !errors.Is(err, ErrBlockBadSignature) {
		t.Fatalf("VerifyProducerTransitionChain = %v, want ErrBlockBadSignature", err)
	}
	// Without a producer transition nothing is verified here; RestoreChain
	// checks the blocks.
	plain := NewBlockProducer(mempool.New(mempool.DefaultConfig()), newTestApplier(), DefaultProducerConfig())
	if v, err := plain.VerifyProducerTransitionChain(blocks); v != nil || err != nil {
		t.Fatalf("without a producer transition: %v %v", v, err)
	}
}

// TestRestoreVerifiedChainOnlyRestoresWhatWasVerified: the restore skips
// verification only for the very blocks that were verified, under the same
// producer transition and consensus settings, and refuses anything else
// without installing it.
func TestRestoreVerifiedChainOnlyRestoresWhatWasVerified(t *testing.T) {
	f := newJournalFixture(t, 12)

	t.Run("a prefix that reaches the checkpoint", func(t *testing.T) {
		v, blocks := f.verified(t)
		bp := f.producer(t, nil)
		if err := bp.RestoreVerifiedChain(blocks[:jfCheckpoint+1], v); err != nil {
			t.Fatal(err)
		}
		if bp.TipHeight() != jfCheckpoint {
			t.Fatal("prefix tip")
		}
		if err := f.producer(t, nil).RestoreVerifiedChain(blocks[:jfCheckpoint], v); !errors.Is(err, errTransitionIncomplete) {
			t.Fatalf("prefix below the checkpoint = %v", err)
		}
	})
	for _, tc := range []struct {
		name   string
		blocks func(t *testing.T, verified []*Block) []*Block
	}{
		{"an equal copy of a block", func(t *testing.T, verified []*Block) []*Block {
			out := append([]*Block(nil), verified...)
			cp := *out[5]
			out[5] = &cp
			return out
		}},
		{"another block in its place", func(t *testing.T, verified []*Block) []*Block {
			out := append([]*Block(nil), verified...)
			out[4], out[5] = out[5], out[4]
			return out
		}},
		{"a block decoded from the journal", func(t *testing.T, verified []*Block) []*Block {
			out := append([]*Block(nil), verified...)
			var b Block
			if err := json.Unmarshal(f.lines[7], &b); err != nil {
				t.Fatal(err)
			}
			out[7] = &b
			return out
		}},
		{"a longer chain", func(t *testing.T, verified []*Block) []*Block {
			var extra Block
			if err := json.Unmarshal(mustMarshal(t, f.blocks[11]), &extra); err != nil {
				t.Fatal(err)
			}
			return append(append([]*Block(nil), verified...), &extra)
		}},
		{"not a prefix", func(t *testing.T, verified []*Block) []*Block { return verified[1:] }},
		{"empty", func(t *testing.T, verified []*Block) []*Block { return nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v, verified := f.verified(t)
			bp := f.producer(t, nil)
			if err := bp.RestoreVerifiedChain(tc.blocks(t, verified), v); err == nil {
				t.Fatal("restored blocks that were not verified")
			}
			if bp.HasTip() || len(bp.AllBlocks()) != 0 {
				t.Fatal("a refused restore installed blocks")
			}
		})
	}
	t.Run("the caller's slice changed after the verification", func(t *testing.T) {
		v, blocks := f.verified(t)
		saved := blocks[3]
		blocks[3] = blocks[4]
		bp := f.producer(t, nil)
		if err := bp.RestoreVerifiedChain(blocks, v); err == nil {
			t.Fatal("restored a slice changed after verification")
		}
		blocks[3] = saved
		if err := bp.RestoreVerifiedChain(blocks, v); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("another producer transition", func(t *testing.T) {
		v, blocks := f.verified(t)
		other := *f.policy
		other.Version = 2
		other.Epochs = []producerpolicy.Epoch{
			{FromHeight: jfSigFrom, ProducerID: f.policy.HistoricalProducer},
			{FromHeight: jfCheckpoint + 1, ProducerID: f.policy.ReplacementProducer, CheckpointHash: f.policy.CheckpointHash},
			{FromHeight: 100, ProducerID: strings.Repeat("ab", 32), CheckpointHash: strings.Repeat("cd", 32)},
		}
		other.CheckpointHeight, other.CheckpointHash, other.HistoricalSignatureHeight = 0, "", 0
		other.HistoricalProducer, other.ReplacementProducer, other.EffectiveHeight = "", "", 0
		bp := NewBlockProducer(mempool.New(mempool.DefaultConfig()), newTestApplier(), DefaultProducerConfig())
		if err := bp.SetProducerTransition(&other); err != nil {
			t.Fatal(err)
		}
		if err := bp.RestoreVerifiedChain(blocks, v); err == nil || !strings.Contains(err.Error(), "another producer transition") {
			t.Fatalf("restore under another transition = %v", err)
		}
		plain := NewBlockProducer(mempool.New(mempool.DefaultConfig()), newTestApplier(), DefaultProducerConfig())
		if err := plain.RestoreVerifiedChain(blocks, v); err == nil {
			t.Fatal("restored verified blocks on a producer without a transition")
		}
	})
	t.Run("changed consensus settings", func(t *testing.T) {
		v, blocks := f.verified(t)
		prev := TxContentRootActivationHeight()
		SetTxContentRootActivationHeight(prev + 5)
		defer SetTxContentRootActivationHeight(prev)
		if err := f.producer(t, nil).RestoreVerifiedChain(blocks, v); err == nil || !strings.Contains(err.Error(), "consensus settings") {
			t.Fatalf("restore after a settings change = %v", err)
		}
	})
}
