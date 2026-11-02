package chain

import (
	"fmt"

	"github.com/blackbeardONE/QSDM/pkg/producerpolicy"
)

// SetProducerTransition installs an immutable policy before any blocks are
// restored or received. It cannot be replaced or cleared in a running producer.
// Restart with a reviewed configuration is required for a subsequent transition.
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
		bp.producerTransition = &copy
	}
	return nil
}

// validateTransitionBlock verifies the height-specific identity, checkpoint,
// and signatures without relying on the process-wide signed-vote rollout flag.
func validateTransitionBlock(p *producerpolicy.Transition, b *Block) error {
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

// ValidateProducerTransitionChain authenticates a complete genesis-to-tip
// journal before startup is allowed to replay or repair persisted state. It is
// a no-op for nodes using the original flat allowlist policy.
func (bp *BlockProducer) ValidateProducerTransitionChain(blocks []*Block) error {
	bp.mu.Lock()
	defer bp.mu.Unlock()
	return bp.validateProducerTransitionChainLocked(blocks)
}

func (bp *BlockProducer) validateProducerTransitionChainLocked(blocks []*Block) error {
	p := bp.producerTransition
	if p == nil {
		return nil
	}
	if len(blocks) == 0 || blocks[0] == nil || blocks[0].Height != 0 || blocks[0].PrevHash != "" {
		return fmt.Errorf("chain: producer transition requires history starting at genesis")
	}
	if blocks[len(blocks)-1] == nil || blocks[len(blocks)-1].Height < p.CheckpointHeight {
		return fmt.Errorf("chain: producer transition requires the complete approved checkpoint")
	}
	for i, b := range blocks {
		if b == nil || b.Height != uint64(i) {
			return fmt.Errorf("chain: producer transition history is not contiguous at index %d", i)
		}
		if b.Hash != computeBlockHash(b) {
			return fmt.Errorf("chain: producer transition invalid block hash at height %d", b.Height)
		}
		if i > 0 && b.PrevHash != blocks[i-1].Hash {
			return fmt.Errorf("chain: producer transition broken linkage at height %d", b.Height)
		}
		if err := validateTransitionBlock(p, b); err != nil {
			return err
		}
	}
	return nil
}

// checkTransitionExtensionLocked prevents any suffix from being accepted unless
// this producer already holds the approved checkpoint in its contiguous history.
func (bp *BlockProducer) checkTransitionExtensionLocked(height uint64) error {
	p := bp.producerTransition
	if p == nil || height < p.EffectiveHeight {
		return nil
	}
	if uint64(len(bp.chain)) <= p.CheckpointHeight || bp.chain[p.CheckpointHeight] == nil || bp.chain[p.CheckpointHeight].Hash != p.CheckpointHash {
		return fmt.Errorf("chain: producer transition checkpoint is not established")
	}
	return nil
}

// checkTransitionSealLocked runs before mempool drain or state mutation. Only
// the replacement key may produce, and only after the checkpoint was restored.
func (bp *BlockProducer) checkTransitionSealLocked(signer BFTSigner) error {
	p := bp.producerTransition
	if p == nil {
		return nil
	}
	if len(bp.chain) == 0 || bp.chain[len(bp.chain)-1].Height < p.CheckpointHeight {
		return fmt.Errorf("chain: local production requires approved transition checkpoint")
	}
	if err := bp.checkTransitionExtensionLocked(p.EffectiveHeight); err != nil {
		return err
	}
	if signer == nil {
		return ErrBlockUnsigned
	}
	if BFTValidatorAddress(signer.GetPublicKey()) != p.ReplacementProducer {
		return fmt.Errorf("%w: local transition signer is not replacement producer", ErrExternalProducerNotAuthorized)
	}
	return nil
}
