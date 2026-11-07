package chain

import (
	"errors"
	"fmt"

	"github.com/blackbeardONE/QSDM/pkg/producerpolicy"
)

// SetProducerTransition installs an immutable policy before any blocks are
// restored or received. It cannot be replaced or cleared in a running producer.
// Restart with a reviewed configuration is required for a subsequent transition.
//
// A version 1 policy is enforced as its equivalent two-epoch schedule; a
// version 2 policy as its ordered epoch list (producerpolicy.Transition.Schedule).
func (bp *BlockProducer) SetProducerTransition(p *producerpolicy.Transition) error {
	if bp == nil {
		return fmt.Errorf("chain: nil producer")
	}
	if err := p.Validate(); err != nil {
		return err
	}
	bp.sealLifecycleMu.Lock()
	defer bp.sealLifecycleMu.Unlock()
	bp.mu.Lock()
	defer bp.mu.Unlock()
	if len(bp.chain) != 0 || bp.producerTransition != nil {
		return fmt.Errorf("chain: producer transition must be configured once before chain initialization")
	}
	if p != nil {
		copy := *p
		copy.Epochs = append([]producerpolicy.Epoch(nil), p.Epochs...)
		bp.producerTransition = &copy
		bp.producerEpochs = copy.Schedule()
	}
	return nil
}

// validateTransitionBlock verifies the height-specific identity, checkpoints,
// and signatures without relying on the process-wide signed-vote rollout flag.
// Block h is authorized iff it is signed by the producer of the epoch that
// contains h. Below the first epoch a block may be unsigned, but a signed one
// must come from the first epoch's producer. Every hand-over is pinned: block
// from_height-1 must carry the epoch's checkpoint hash and block from_height
// must extend it.
func validateTransitionBlock(epochs []producerpolicy.Epoch, b *Block) error {
	if err := checkTransitionSchedule(epochs, b); err != nil {
		return err
	}
	return VerifyBlockSignature(b)
}

// checkTransitionSchedule is validateTransitionBlock without the signature
// verification: the checkpoints and the producer the schedule requires.
func checkTransitionSchedule(epochs []producerpolicy.Epoch, b *Block) error {
	if b == nil {
		return fmt.Errorf("chain: nil transition block")
	}
	if len(epochs) == 0 {
		return fmt.Errorf("chain: producer transition has no epochs")
	}
	for _, e := range epochs[1:] {
		if b.Height == e.FromHeight-1 && b.Hash != e.CheckpointHash {
			return fmt.Errorf("chain: producer transition checkpoint hash mismatch at height %d", b.Height)
		}
		if b.Height == e.FromHeight && b.PrevHash != e.CheckpointHash {
			return fmt.Errorf("chain: producer transition first replacement block does not extend checkpoint")
		}
	}
	if want, inSchedule := producerpolicy.ProducerAt(epochs, b.Height); inSchedule {
		if !b.ProducerAuth.Signed() {
			return fmt.Errorf("chain: producer transition at height %d: %w", b.Height, ErrBlockUnsigned)
		}
		if b.ProducerID != want {
			return fmt.Errorf("%w: transition height %d producer %q", ErrExternalProducerNotAuthorized, b.Height, b.ProducerID)
		}
	} else if b.ProducerAuth.Signed() && b.ProducerID != epochs[0].ProducerID {
		return fmt.Errorf("%w: signed historical producer %q", ErrExternalProducerNotAuthorized, b.ProducerID)
	}
	return nil
}

// finalCheckpointHeight is the last pinned hand-over checkpoint (version 1:
// checkpoint_height). Epochs come from a validated policy (len >= 2).
func finalCheckpointHeight(epochs []producerpolicy.Epoch) uint64 {
	return epochs[len(epochs)-1].FromHeight - 1
}

// ValidateProducerTransitionChain authenticates a complete genesis-to-tip
// journal before startup is allowed to replay or repair persisted state. It is
// a no-op for nodes using the original flat allowlist policy.
// VerifyProducerTransitionChain also returns what it verified, so that
// RestoreVerifiedChain does not verify it again.
func (bp *BlockProducer) ValidateProducerTransitionChain(blocks []*Block) error {
	bp.mu.Lock()
	defer bp.mu.Unlock()
	return bp.validateProducerTransitionChainLocked(blocks)
}

func (bp *BlockProducer) validateProducerTransitionChainLocked(blocks []*Block) error {
	if bp.producerTransition == nil {
		return nil
	}
	epochs := bp.producerEpochs
	if len(blocks) == 0 || blocks[0] == nil || blocks[0].Height != 0 || blocks[0].PrevHash != "" {
		return errTransitionNoGenesis
	}
	if blocks[len(blocks)-1] == nil || blocks[len(blocks)-1].Height < finalCheckpointHeight(epochs) {
		return errTransitionIncomplete
	}
	keys := newProducerKeys()
	for i, b := range blocks {
		var prev *Block
		if i > 0 {
			prev = blocks[i-1]
		}
		if err := verifyTransitionChainBlock(epochs, keys, prev, b, uint64(i)); err != nil {
			return err
		}
	}
	return nil
}

var (
	errTransitionNoGenesis  = errors.New("chain: producer transition requires history starting at genesis")
	errTransitionIncomplete = errors.New("chain: producer transition requires the complete approved checkpoint")
)

// verifyTransitionChainBlock authenticates b as the block at index i of a
// chain that starts at genesis, after prev (nil at index 0): its height, its
// hash, its link to prev, the producer schedule and its signature. The hash
// is computed once; the signature check relies on that comparison instead of
// computing it again. keys holds the producer keys of the whole run, each
// prepared once (producer_keys.go); the schedule is checked before the
// signature, so it only ever holds the keys of the schedule's producers.
func verifyTransitionChainBlock(epochs []producerpolicy.Epoch, keys *producerKeys, prev, b *Block, i uint64) error {
	if b == nil || b.Height != i {
		return fmt.Errorf("chain: producer transition history is not contiguous at index %d", i)
	}
	if b.Hash != computeBlockHash(b) {
		return fmt.Errorf("chain: producer transition invalid block hash at height %d", b.Height)
	}
	if prev != nil && b.PrevHash != prev.Hash {
		return fmt.Errorf("chain: producer transition broken linkage at height %d", b.Height)
	}
	if err := checkTransitionSchedule(epochs, b); err != nil {
		return err
	}
	return verifyBlockSignatureWith(b, true, keys)
}

// checkTransitionExtensionLocked prevents any suffix from being accepted unless
// this producer already holds every approved hand-over checkpoint at or below
// height in its contiguous history.
func (bp *BlockProducer) checkTransitionExtensionLocked(height uint64) error {
	if bp.producerTransition == nil {
		return nil
	}
	for _, e := range bp.producerEpochs[1:] {
		if height < e.FromHeight {
			break
		}
		cp := e.FromHeight - 1
		if uint64(len(bp.chain)) <= cp || bp.chain[cp] == nil || bp.chain[cp].Hash != e.CheckpointHash {
			return fmt.Errorf("chain: producer transition checkpoint is not established")
		}
	}
	return nil
}

// checkTransitionSealLocked runs before mempool drain or state mutation. Only
// the final epoch's producer may produce, and only after its hand-over
// checkpoint (and every earlier one) was restored. Earlier epochs are closed
// history: their producers can never seal again under this policy.
func (bp *BlockProducer) checkTransitionSealLocked(signer BFTSigner) error {
	if bp.producerTransition == nil {
		return nil
	}
	final := bp.producerEpochs[len(bp.producerEpochs)-1]
	if len(bp.chain) == 0 || bp.chain[len(bp.chain)-1].Height < final.FromHeight-1 {
		return fmt.Errorf("chain: local production requires approved transition checkpoint")
	}
	if err := bp.checkTransitionExtensionLocked(final.FromHeight); err != nil {
		return err
	}
	if signer == nil {
		return ErrBlockUnsigned
	}
	if BFTValidatorAddress(signer.GetPublicKey()) != final.ProducerID {
		return fmt.Errorf("%w: local transition signer is not replacement producer", ErrExternalProducerNotAuthorized)
	}
	return nil
}
