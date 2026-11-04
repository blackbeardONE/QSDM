package chain

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/producerpolicy"
)

// legacyValidateTransitionBlockV1 is the version 1 block rule exactly as
// producer_transition.go had it before producer epochs. It is the oracle for
// the claim that a version 1 configuration behaves byte-identically when it
// is enforced as its two-epoch schedule.
func legacyValidateTransitionBlockV1(p *producerpolicy.Transition, b *Block) error {
	if b == nil {
		return fmt.Errorf("chain: nil transition block")
	}
	if b.Height == p.CheckpointHeight && b.Hash != p.CheckpointHash {
		return fmt.Errorf("chain: producer transition checkpoint hash mismatch at height %d", b.Height)
	}
	if b.Height == p.EffectiveHeight && b.PrevHash != p.CheckpointHash {
		return fmt.Errorf("chain: producer transition first replacement block does not extend checkpoint")
	}
	if b.Height >= p.HistoricalSignatureHeight {
		if !b.ProducerAuth.Signed() {
			return fmt.Errorf("chain: producer transition at height %d: %w", b.Height, ErrBlockUnsigned)
		}
		want := p.HistoricalProducer
		if b.Height >= p.EffectiveHeight {
			want = p.ReplacementProducer
		}
		if b.ProducerID != want {
			return fmt.Errorf("%w: transition height %d producer %q", ErrExternalProducerNotAuthorized, b.Height, b.ProducerID)
		}
	}
	if b.Height < p.HistoricalSignatureHeight && b.ProducerAuth.Signed() && b.ProducerID != p.HistoricalProducer {
		return fmt.Errorf("%w: signed historical producer %q", ErrExternalProducerNotAuthorized, b.ProducerID)
	}
	return VerifyBlockSignature(b)
}

// asV2 is the version 2 spelling of a version 1 policy.
func asV2(p *producerpolicy.Transition) *producerpolicy.Transition {
	return &producerpolicy.Transition{Version: 2, HistoricalPrefixBytes: p.HistoricalPrefixBytes,
		HistoricalPrefixSHA256: p.HistoricalPrefixSHA256, Epochs: p.Schedule()}
}

func errString(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}

// A version 1 policy with the heights of the public network's transition
// (signed from 482566, replacement producer from 659977). The producer IDs
// and hashes are synthetic.
func liveShapedTransitionV1() *producerpolicy.Transition {
	return &producerpolicy.Transition{Version: 1, CheckpointHeight: 659976,
		CheckpointHash:            strings.Repeat("a", 64),
		HistoricalSignatureHeight: 482566,
		HistoricalProducer:        strings.Repeat("b", 64),
		ReplacementProducer:       strings.Repeat("c", 64),
		EffectiveHeight:           659977, HistoricalPrefixBytes: 2728741562,
		HistoricalPrefixSHA256: strings.Repeat("d", 64)}
}

// Every combination of height, producer, signedness, block hash and parent
// hash around the public network's boundaries gets the same verdict (and the
// same error text) from the pre-epoch version 1 rule, from version 1 enforced
// as its schedule, and from the equivalent version 2 table.
func TestProducerTransitionV1V2EquivalentOnLiveShapedHeights(t *testing.T) {
	v1 := liveShapedTransitionV1()
	v2 := asV2(v1)
	if err := v1.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := v2.Validate(); err != nil {
		t.Fatal(err)
	}
	other := fmt.Sprintf("%x", sha256.Sum256([]byte("other")))
	heights := []uint64{0, 1, 2, 482564, 482565, 482566, 482567, 600000, 659975, 659976, 659977, 659978, 659979, 900000, 1 << 40}
	producers := []string{v1.HistoricalProducer, v1.ReplacementProducer, other, "", "12D3KooWlegacypeer"}
	hashes := []string{v1.CheckpointHash, other, ""}
	compared := 0
	for _, h := range heights {
		for _, producer := range producers {
			for _, signed := range []bool{false, true} {
				for _, hash := range hashes {
					for _, prev := range hashes {
						b := &Block{Height: h, ProducerID: producer, Hash: hash, PrevHash: prev}
						if signed {
							b.ProducerAuth = BFTWireAuth{PublicKey: []byte{1}, Signature: []byte{2}}
						}
						want := errString(legacyValidateTransitionBlockV1(v1, b))
						for name, epochs := range map[string][]producerpolicy.Epoch{"v1": v1.Schedule(), "v2": v2.Schedule()} {
							if got := errString(validateTransitionBlock(epochs, b)); got != want {
								t.Fatalf("%s height %d producer %q signed %v hash %q prev %q:\n got  %s\n want %s", name, h, producer, signed, hash, prev, got, want)
							}
							compared++
						}
					}
				}
			}
		}
	}
	if compared < 1000 {
		t.Fatalf("only %d comparisons", compared)
	}
}

// The behavioural version 1 suite, repeated with the version 2 spelling of
// the same policy: catch-up, rejections, restart and sealing are identical.
func TestProducerTransitionV2EquivalentFixtureBehaviour(t *testing.T) {
	f := newTransitionFixture(t)
	v2 := asV2(f.policy)
	// Same verdict and error text as the pre-epoch rule for every fixture block
	// re-signed by either key or unsigned.
	for _, b := range f.blocks {
		for _, key := range []BFTSigner{nil, f.oldKey, f.newKey} {
			c := *b
			c.ProducerAuth = BFTWireAuth{}
			if key != nil {
				if err := SignBlock(&c, key); err != nil {
					t.Fatal(err)
				}
			}
			want := errString(legacyValidateTransitionBlockV1(f.policy, &c))
			if got := errString(validateTransitionBlock(v2.Schedule(), &c)); got != want {
				t.Fatalf("height %d: got %s want %s", c.Height, got, want)
			}
		}
	}
	bp := transitionProducer(t, v2)
	for _, b := range f.blocks {
		if err := bp.TryAppendExternalBlock(b); err != nil {
			t.Fatalf("height %d: %v", b.Height, err)
		}
	}
	revoked := *f.blocks[4]
	if err := SignBlock(&revoked, f.oldKey); err != nil {
		t.Fatal(err)
	}
	restored := append([]*Block{}, f.blocks...)
	restored[4] = &revoked
	if err := transitionProducer(t, v2).RestoreChain(restored); !errors.Is(err, ErrExternalProducerNotAuthorized) {
		t.Fatalf("v2 restart accepted old producer: %v", err)
	}
	for _, signer := range []BFTSigner{f.oldKey, f.newKey} {
		sealer := transitionProducer(t, v2)
		if err := sealer.RestoreChain(f.blocks[:3]); err != nil {
			t.Fatal(err)
		}
		sealer.SetBlockSigner(signer)
		if err := sealer.pool.Add(makeTx("transition-v2-test", 0)); err != nil {
			t.Fatal(err)
		}
		_, err := sealer.ProduceBlock()
		if (err == nil) != (signer == f.newKey) {
			t.Fatalf("seal with %T: %v", signer, err)
		}
	}
}

// threeEpochFixture: blocks 0..8; 1..2 signed by A, 3..5 by B, 6..8 by C.
type threeEpochFixture struct {
	policy  *producerpolicy.Transition
	blocks  []*Block
	keys    [3]BFTSigner
	addrs   [3]string
	prefix  []byte
	byEpoch func(h uint64) int
}

func newThreeEpochFixture(t *testing.T) threeEpochFixture {
	t.Helper()
	var f threeEpochFixture
	for i := range f.keys {
		k, a := newBFTKey(t)
		f.keys[i], f.addrs[i] = k, a
	}
	f.byEpoch = func(h uint64) int {
		switch {
		case h >= 6:
			return 2
		case h >= 3:
			return 1
		}
		return 0
	}
	root := newTestApplier().StateRoot()
	f.blocks = make([]*Block, 9)
	for i := range f.blocks {
		b := &Block{Height: uint64(i), Timestamp: time.Unix(1700000000+int64(i), 0), StateRoot: root, ProducerID: "legacy-peer"}
		if i > 0 {
			b.PrevHash = f.blocks[i-1].Hash
		}
		b.Hash = computeBlockHash(b)
		if i > 0 {
			if err := SignBlock(b, f.keys[f.byEpoch(uint64(i))]); err != nil {
				t.Fatal(err)
			}
		}
		f.blocks[i] = b
	}
	f.prefix = marshalTransitionBlocks(t, f.blocks[:6])
	f.policy = &producerpolicy.Transition{Version: 2, HistoricalPrefixBytes: int64(len(f.prefix)),
		HistoricalPrefixSHA256: fmt.Sprintf("%x", sha256.Sum256(f.prefix)),
		Epochs: []producerpolicy.Epoch{
			{FromHeight: 1, ProducerID: f.addrs[0]},
			{FromHeight: 3, ProducerID: f.addrs[1], CheckpointHash: f.blocks[2].Hash},
			{FromHeight: 6, ProducerID: f.addrs[2], CheckpointHash: f.blocks[5].Hash},
		}}
	if err := f.policy.Validate(); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestProducerScheduleThreeEpochsAuthorizesSignerOfEachEpoch(t *testing.T) {
	f := newThreeEpochFixture(t)
	for h := uint64(1); h < uint64(len(f.blocks)); h++ {
		for k := range f.keys {
			for _, unsigned := range []bool{false, true} {
				if unsigned && k > 0 {
					continue
				}
				name := fmt.Sprintf("height %d key %d unsigned %v", h, k, unsigned)
				bp := transitionProducer(t, f.policy)
				for _, b := range f.blocks[:h] {
					if err := bp.TryAppendExternalBlock(b); err != nil {
						t.Fatalf("%s: prefix height %d: %v", name, b.Height, err)
					}
				}
				b := *f.blocks[h]
				b.ProducerAuth = BFTWireAuth{}
				if !unsigned {
					if err := SignBlock(&b, f.keys[k]); err != nil {
						t.Fatal(err)
					}
				}
				rootBefore, lenBefore := bp.applier.StateRoot(), len(bp.chain)
				err := bp.TryAppendExternalBlock(&b)
				wantOK := !unsigned && k == f.byEpoch(h)
				if (err == nil) != wantOK {
					t.Fatalf("%s: err=%v want accepted=%v", name, err, wantOK)
				}
				if !wantOK {
					// Re-signing changes the block hash, so at a pinned
					// checkpoint height the hash pin fires first.
					checkpoint := h == 2 || h == 5
					if !unsigned && !errors.Is(err, ErrExternalProducerNotAuthorized) &&
						!(checkpoint && err.Error() == fmt.Sprintf("chain: producer transition checkpoint hash mismatch at height %d", h)) {
						t.Fatalf("%s: rejected for the wrong reason: %v", name, err)
					}
					if unsigned && !errors.Is(err, ErrBlockUnsigned) {
						t.Fatalf("%s: rejected for the wrong reason: %v", name, err)
					}
					if bp.applier.StateRoot() != rootBefore || len(bp.chain) != lenBefore {
						t.Fatalf("%s: rejected block changed live state", name)
					}
				}
			}
		}
	}
}

func TestProducerScheduleThreeEpochsPinsEveryHandover(t *testing.T) {
	f := newThreeEpochFixture(t)
	for _, kind := range []string{"forged first checkpoint", "forged second checkpoint", "second epoch skips checkpoint", "third epoch skips checkpoint"} {
		t.Run(kind, func(t *testing.T) {
			var h uint64
			var key BFTSigner
			b := Block{}
			switch kind {
			case "forged first checkpoint", "forged second checkpoint":
				h, key = 2, f.keys[0]
				if kind == "forged second checkpoint" {
					h, key = 5, f.keys[1]
				}
				b = *f.blocks[h]
				b.Timestamp = b.Timestamp.Add(time.Second)
				b.Hash = computeBlockHash(&b)
			case "second epoch skips checkpoint", "third epoch skips checkpoint":
				h, key = 3, f.keys[1]
				if kind == "third epoch skips checkpoint" {
					h, key = 6, f.keys[2]
				}
				b = *f.blocks[h]
				b.PrevHash = f.blocks[h-2].Hash
				b.Hash = computeBlockHash(&b)
			}
			b.ProducerAuth = BFTWireAuth{}
			if err := SignBlock(&b, key); err != nil {
				t.Fatal(err)
			}
			bp := transitionProducer(t, f.policy)
			for _, prior := range f.blocks[:h] {
				if err := bp.TryAppendExternalBlock(prior); err != nil {
					t.Fatal(err)
				}
			}
			if err := bp.TryAppendExternalBlock(&b); err == nil {
				t.Fatal("block breaking a pinned hand-over accepted")
			}
		})
	}
	// A follower that never established the second checkpoint cannot accept a
	// third-epoch block even if it is otherwise well-formed.
	bp := transitionProducer(t, f.policy)
	bp.mu.Lock()
	err := bp.checkTransitionExtensionLocked(6)
	bp.mu.Unlock()
	if err == nil || err.Error() != "chain: producer transition checkpoint is not established" {
		t.Fatalf("extension without checkpoint: %v", err)
	}
}

// A chain segment that crosses a hand-over is replayed from a journal, and a
// restart restores it; a version 1 policy (or a schedule that forgets the
// first producer) cannot represent the same history.
func TestProducerScheduleReplayAcrossBoundaryAndRestart(t *testing.T) {
	f := newThreeEpochFixture(t)
	path := filepath.Join(t.TempDir(), "chain.ndjson")
	if err := os.WriteFile(path, f.prefix, 0600); err != nil {
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
	if err := first.ValidateProducerTransitionChain(blocks); err != nil {
		t.Fatal(err)
	}
	if err := first.RestoreChain(blocks); err != nil {
		t.Fatal(err)
	}
	journal, err := OpenChainJournal(path, blocks[len(blocks)-1])
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range f.blocks[6:] {
		if err := first.TryAppendExternalBlock(b); err != nil {
			t.Fatalf("height %d: %v", b.Height, err)
		}
		if err := journal.Append(b); err != nil {
			t.Fatal(err)
		}
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.policy.ValidateJournalPrefix(path); err != nil {
		t.Fatalf("appending changed the pinned prefix: %v", err)
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
	if restart.ChainHeight() != 8 || restart.applier.StateRoot() != first.applier.StateRoot() {
		t.Fatal("restart did not restore the segment across the hand-over")
	}
	// A history shorter than the last checkpoint is refused at restore.
	if err := transitionProducer(t, f.policy).RestoreChain(restored[:5]); err == nil {
		t.Fatal("history without the final checkpoint restored")
	}
	// The version 1 policy that covered the first hand-over rejects the third
	// epoch; a schedule without the first producer rejects its history. This
	// is why a second hand-over needs version 2 with every epoch listed.
	v1 := &producerpolicy.Transition{Version: 1, CheckpointHeight: 2, CheckpointHash: f.blocks[2].Hash,
		HistoricalSignatureHeight: 1, HistoricalProducer: f.addrs[0], ReplacementProducer: f.addrs[1], EffectiveHeight: 3,
		HistoricalPrefixBytes: f.policy.HistoricalPrefixBytes, HistoricalPrefixSHA256: f.policy.HistoricalPrefixSHA256}
	if err := transitionProducer(t, v1).RestoreChain(restored); !errors.Is(err, ErrExternalProducerNotAuthorized) {
		t.Fatalf("v1 accepted the third epoch: %v", err)
	}
	dropped := *f.policy
	dropped.Epochs = []producerpolicy.Epoch{{FromHeight: 1, ProducerID: f.addrs[1]}, f.policy.Epochs[2]}
	if err := transitionProducer(t, &dropped).RestoreChain(restored); !errors.Is(err, ErrExternalProducerNotAuthorized) {
		t.Fatalf("schedule without the first producer accepted its history: %v", err)
	}
	// A restored chain with an epoch-2 block re-signed by the fenced producer
	// is refused at restart.
	forged := append([]*Block{}, restored...)
	b := *restored[7]
	b.ProducerAuth = BFTWireAuth{}
	if err := SignBlock(&b, f.keys[1]); err != nil {
		t.Fatal(err)
	}
	forged[7] = &b
	if err := transitionProducer(t, f.policy).RestoreChain(forged); !errors.Is(err, ErrExternalProducerNotAuthorized) {
		t.Fatalf("restart accepted the fenced producer: %v", err)
	}
}

// Only the final epoch's producer may seal, and only on top of the final
// checkpoint; fenced earlier producers are refused before the mempool drains.
func TestProducerScheduleOnlyFinalEpochSeals(t *testing.T) {
	f := newThreeEpochFixture(t)
	for _, kind := range []string{"first producer", "fenced second producer", "unsigned", "missing checkpoint", "final producer"} {
		t.Run(kind, func(t *testing.T) {
			bp := transitionProducer(t, f.policy)
			if kind != "missing checkpoint" {
				if err := bp.RestoreChain(f.blocks[:6]); err != nil {
					t.Fatal(err)
				}
			}
			switch kind {
			case "first producer":
				bp.SetBlockSigner(f.keys[0])
			case "fenced second producer":
				bp.SetBlockSigner(f.keys[1])
			case "final producer", "missing checkpoint":
				bp.SetBlockSigner(f.keys[2])
			}
			if err := bp.pool.Add(makeTx("schedule-seal-test", 0)); err != nil {
				t.Fatal(err)
			}
			before := bp.applier.StateRoot()
			b, err := bp.ProduceBlock()
			if kind == "final producer" {
				if err != nil {
					t.Fatal(err)
				}
				if b.Height != 6 || b.ProducerID != f.addrs[2] || b.PrevHash != f.blocks[5].Hash {
					t.Fatal("invalid final-epoch block")
				}
				follower := transitionProducer(t, f.policy)
				if err := follower.RestoreChain(f.blocks[:6]); err != nil {
					t.Fatal(err)
				}
				if err := follower.TryAppendExternalBlock(b); err != nil {
					t.Fatalf("follower rejected the final producer's block: %v", err)
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

func TestProducerScheduleCannotBeMutatedAfterInstall(t *testing.T) {
	f := newThreeEpochFixture(t)
	bp := transitionProducer(t, f.policy)
	f.policy.Epochs[2].ProducerID = f.addrs[1]
	f.policy.Epochs[1].CheckpointHash = f.blocks[1].Hash
	for _, b := range f.blocks {
		if err := bp.TryAppendExternalBlock(b); err != nil {
			t.Fatalf("height %d: caller mutation reached the installed schedule: %v", b.Height, err)
		}
	}
	if err := bp.SetProducerTransition(nil); err == nil {
		t.Fatal("running schedule cleared")
	}
}
