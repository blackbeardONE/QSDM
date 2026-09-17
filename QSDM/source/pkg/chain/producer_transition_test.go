package chain

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/producerpolicy"
)

type transitionFixture struct {
	policy         *producerpolicy.Transition
	blocks         []*Block
	oldKey, newKey BFTSigner
}

func newTransitionFixture(t *testing.T) transitionFixture {
	t.Helper()
	oldKey, oldAddr := newBFTKey(t)
	newKey, newAddr := newBFTKey(t)
	root := newTestApplier().StateRoot()
	blocks := make([]*Block, 5)
	for i := range blocks {
		b := &Block{Height: uint64(i), Timestamp: time.Unix(1700000000+int64(i), 0), StateRoot: root, ProducerID: "legacy-peer"}
		if i > 0 {
			b.PrevHash = blocks[i-1].Hash
		}
		b.Hash = computeBlockHash(b)
		if i > 0 {
			key := BFTSigner(oldKey)
			if i >= 3 {
				key = newKey
			}
			if err := SignBlock(b, key); err != nil {
				t.Fatal(err)
			}
		}
		blocks[i] = b
	}
	prefix := marshalTransitionBlocks(t, blocks[:3])
	return transitionFixture{&producerpolicy.Transition{Version: 1, CheckpointHeight: 2, CheckpointHash: blocks[2].Hash,
		HistoricalSignatureHeight: 1, HistoricalProducer: oldAddr, ReplacementProducer: newAddr, EffectiveHeight: 3,
		HistoricalPrefixBytes: int64(len(prefix)), HistoricalPrefixSHA256: fmt.Sprintf("%x", sha256.Sum256(prefix))}, blocks, oldKey, newKey}
}
func marshalTransitionBlocks(t *testing.T, blocks []*Block) []byte {
	t.Helper()
	var out []byte
	for _, b := range blocks {
		data, err := json.Marshal(b)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, append(data, '\n')...)
	}
	return out
}
func transitionProducer(t *testing.T, p *producerpolicy.Transition) *BlockProducer {
	t.Helper()
	bp := NewBlockProducer(mempool.New(mempool.DefaultConfig()), newTestApplier(), DefaultProducerConfig())
	bp.SetAuthorizedBlockProducers([]string{p.HistoricalProducer})
	if err := bp.SetProducerTransition(p); err != nil {
		t.Fatal(err)
	}
	return bp
}

func TestProducerTransitionHistoricalCatchupAndNewSuffix(t *testing.T) {
	f := newTransitionFixture(t)
	bp := transitionProducer(t, f.policy)
	for _, b := range f.blocks {
		if err := bp.TryAppendExternalBlock(b); err != nil {
			t.Fatalf("height %d: %v", b.Height, err)
		}
	}
	if bp.ChainHeight() != 4 {
		t.Fatal("suffix not appended")
	}
	if err := bp.TryAppendExternalBlock(f.blocks[1]); err != nil {
		t.Fatalf("historical duplicate not idempotent: %v", err)
	}
}

func TestProducerTransitionRejectsBeforeExternalStateMutation(t *testing.T) {
	for _, kind := range []string{"old after boundary", "unsigned after boundary", "unsigned signed history", "wrong checkpoint", "wrong first parent", "invalid signature", "new key before boundary"} {
		t.Run(kind, func(t *testing.T) {
			f := newTransitionFixture(t)
			bp := transitionProducer(t, f.policy)
			b := *f.blocks[3]
			switch kind {
			case "old after boundary":
				if err := SignBlock(&b, f.oldKey); err != nil {
					t.Fatal(err)
				}
			case "unsigned after boundary":
				b.ProducerAuth = BFTWireAuth{}
			case "unsigned signed history":
				b = *f.blocks[1]
				b.ProducerAuth = BFTWireAuth{}
			case "wrong checkpoint":
				b = *f.blocks[2]
				b.Timestamp = b.Timestamp.Add(time.Second)
				b.Hash = computeBlockHash(&b)
				if err := SignBlock(&b, f.oldKey); err != nil {
					t.Fatal(err)
				}
			case "wrong first parent":
				b.PrevHash = f.blocks[1].Hash
				b.Hash = computeBlockHash(&b)
				if err := SignBlock(&b, f.newKey); err != nil {
					t.Fatal(err)
				}
			case "invalid signature":
				b.ProducerAuth.Signature = append([]byte{}, b.ProducerAuth.Signature...)
				b.ProducerAuth.Signature[0] ^= 1
			case "new key before boundary":
				b = *f.blocks[1]
				if err := SignBlock(&b, f.newKey); err != nil {
					t.Fatal(err)
				}
			}
			if b.Height == 3 {
				if err := bp.RestoreChain(f.blocks[:3]); err != nil {
					t.Fatal(err)
				}
			}
			rootBefore := bp.applier.StateRoot()
			lenBefore := len(bp.chain)
			if err := bp.TryAppendExternalBlock(&b); err == nil {
				t.Fatal("forbidden block accepted")
			}
			if bp.applier.StateRoot() != rootBefore || len(bp.chain) != lenBefore {
				t.Fatal("rejected block changed live state")
			}
		})
	}
}

func TestProducerTransitionRestoreAndRestart(t *testing.T) {
	f := newTransitionFixture(t)
	path := filepath.Join(t.TempDir(), "chain.ndjson")
	if err := os.WriteFile(path, marshalTransitionBlocks(t, f.blocks[:3]), 0600); err != nil {
		t.Fatal(err)
	}
	if err := f.policy.ValidateJournalPrefix(path); err != nil {
		t.Fatal(err)
	}
	first := transitionProducer(t, f.policy)
	blocks, err := LoadChainNDJSON(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.RestoreChain(blocks); err != nil {
		t.Fatal(err)
	}
	journal, err := OpenChainJournal(path, blocks[len(blocks)-1])
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range f.blocks[3:] {
		if err := first.TryAppendExternalBlock(b); err != nil {
			t.Fatal(err)
		}
		if err := journal.Append(b); err != nil {
			t.Fatal(err)
		}
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.policy.ValidateJournalPrefix(path); err != nil {
		t.Fatalf("appending changed immutable prefix: %v", err)
	}
	restored, err := LoadChainNDJSON(path)
	if err != nil {
		t.Fatal(err)
	}
	restart := transitionProducer(t, f.policy)
	if err := restart.ValidateProducerTransitionChain(restored); err != nil {
		t.Fatal(err)
	}
	if err := restart.RestoreChain(restored); err != nil {
		t.Fatal(err)
	}
	if restart.ChainHeight() != 4 {
		t.Fatal("advanced state lost on restart")
	}
	revoked := *f.blocks[4]
	if err := SignBlock(&revoked, f.oldKey); err != nil {
		t.Fatal(err)
	}
	restored[4] = &revoked
	if err := transitionProducer(t, f.policy).RestoreChain(restored); !errors.Is(err, ErrExternalProducerNotAuthorized) {
		t.Fatalf("restart accepted old producer: %v", err)
	}
}

func TestProducerTransitionRestoreRejectsMalformedHistory(t *testing.T) {
	for _, kind := range []string{"missing genesis", "missing checkpoint", "wrong checkpoint", "unsigned history", "tampered signature", "broken link", "unsigned suffix"} {
		t.Run(kind, func(t *testing.T) {
			f := newTransitionFixture(t)
			blocks := append([]*Block{}, f.blocks...)
			p := *f.policy
			switch kind {
			case "missing genesis":
				blocks = blocks[1:]
			case "missing checkpoint":
				blocks = blocks[:2]
			case "wrong checkpoint":
				p.CheckpointHash = f.blocks[1].Hash
			case "unsigned history":
				b := *blocks[1]
				b.ProducerAuth = BFTWireAuth{}
				blocks[1] = &b
			case "tampered signature":
				b := *blocks[1]
				b.ProducerAuth.Signature = append([]byte{}, b.ProducerAuth.Signature...)
				b.ProducerAuth.Signature[0] ^= 1
				blocks[1] = &b
			case "broken link":
				b := *blocks[3]
				b.PrevHash = blocks[1].Hash
				b.Hash = computeBlockHash(&b)
				if err := SignBlock(&b, f.newKey); err != nil {
					t.Fatal(err)
				}
				blocks[3] = &b
			case "unsigned suffix":
				b := *blocks[4]
				b.ProducerAuth = BFTWireAuth{}
				blocks[4] = &b
			}
			bp := transitionProducer(t, &p)
			if err := bp.RestoreChain(blocks); err == nil {
				t.Fatal("bad history restored")
			}
			if bp.HasTip() {
				t.Fatal("failed restore installed tip")
			}
		})
	}
}

func TestProducerTransitionSealChecksBeforeDrain(t *testing.T) {
	for _, kind := range []string{"old signer", "unsigned", "missing checkpoint", "replacement"} {
		t.Run(kind, func(t *testing.T) {
			f := newTransitionFixture(t)
			bp := transitionProducer(t, f.policy)
			if kind != "missing checkpoint" {
				if err := bp.RestoreChain(f.blocks[:3]); err != nil {
					t.Fatal(err)
				}
			}
			switch kind {
			case "old signer":
				bp.SetBlockSigner(f.oldKey)
			case "replacement", "missing checkpoint":
				bp.SetBlockSigner(f.newKey)
			}
			if err := bp.pool.Add(makeTx("transition-test", 0)); err != nil {
				t.Fatal(err)
			}
			before := bp.applier.StateRoot()
			b, err := bp.ProduceBlock()
			if kind == "replacement" {
				if err != nil {
					t.Fatal(err)
				}
				if b.Height != 3 || b.ProducerID != f.policy.ReplacementProducer || VerifyBlockSignature(b) != nil {
					t.Fatal("invalid replacement block")
				}
				follower := transitionProducer(t, f.policy)
				if err := follower.RestoreChain(f.blocks[:3]); err != nil {
					t.Fatal(err)
				}
				if err := follower.TryAppendExternalBlock(b); err != nil {
					t.Fatalf("independent follower rejected produced transfer: %v", err)
				}
				if follower.applier.StateRoot() != bp.applier.StateRoot() {
					t.Fatal("producer and follower state roots diverged")
				}
				return
			}
			if err == nil {
				t.Fatal("forbidden local seal accepted")
			}
			if bp.pool.Size() != 1 || bp.applier.StateRoot() != before {
				t.Fatal("rejected seal drained/applied transactions")
			}
		})
	}
}

func TestProducerTransitionCannotBeClearedOrMutated(t *testing.T) {
	f := newTransitionFixture(t)
	bp := transitionProducer(t, f.policy)
	f.policy.ReplacementProducer = f.policy.HistoricalProducer
	if err := bp.SetProducerTransition(nil); err == nil {
		t.Fatal("running policy cleared")
	}
	for _, b := range f.blocks {
		if err := bp.TryAppendExternalBlock(b); err != nil {
			t.Fatal(err)
		}
	}
	bp.SetAuthorizedBlockProducers(nil)
	b := *f.blocks[4]
	if err := SignBlock(&b, f.oldKey); err != nil {
		t.Fatal(err)
	}
	if err := bp.TryAppendExternalBlock(&b); !errors.Is(err, ErrExternalProducerNotAuthorized) {
		t.Fatalf("flat allowlist change bypassed transition: %v", err)
	}
}
