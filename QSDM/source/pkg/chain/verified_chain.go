package chain

import (
	"errors"
	"fmt"

	"github.com/blackbeardONE/QSDM/pkg/producerpolicy"
)

// Verify once.
//
// A node's startup authenticated every block of its journal twice under a
// producer transition: ValidateProducerTransitionChain, then RestoreChain
// again (validateProducerTransitionChainLocked), each time with an ML-DSA-87
// verification per signed block and two block hash computations per block,
// plus RestoreChain's own hash loop. A VerifiedChain carries the first
// verification to the restore, so each signature is verified and each hash
// computed once.

// VerifiedChain is a chain that a producer transition has authenticated in
// full: every block's height, hash, link to its parent, producer and
// signature, from genesis to a tip at or above the final approved checkpoint
// (validateProducerTransitionChainLocked). LoadVerifiedJournal and
// VerifyProducerTransitionChain return one; RestoreVerifiedChain installs its
// blocks without verifying them again.
//
// It is not a flag: it keeps its own copy of the verified slice, and
// RestoreVerifiedChain accepts only those very *Block values, in their order
// (all of them, or a prefix that still reaches the final checkpoint), on a
// producer that enforces the same producer transition under the same package
// consensus settings. Any other slice is refused, so a VerifiedChain cannot
// carry a block past verification that it did not verify. Nothing changes a
// block after it is decoded or sealed, so the blocks it verified are the
// blocks it restores.
type VerifiedChain struct {
	epochs   []producerpolicy.Epoch // the schedule the blocks were verified under
	settings verifySettings         // and the package settings
	blocks   []*Block               // the verified blocks, genesis first
	journal  string                 // the journal that holds their stripped signatures, "" if none
}

// Blocks returns the verified blocks, genesis first, in a new slice that the
// caller owns. Nil-safe.
func (v *VerifiedChain) Blocks() []*Block {
	if v == nil {
		return nil
	}
	return append([]*Block(nil), v.blocks...)
}

// verifySettings are the package consensus settings that decide whether a
// block verifies: the content-root activation height changes block hashes,
// and the signed-block policy decides whether an unsigned block passes.
type verifySettings struct {
	txContentRootActivation uint64
	requireSignedBlocks     bool
	signedBlockActivation   uint64
}

func currentVerifySettings() verifySettings {
	return verifySettings{
		txContentRootActivation: TxContentRootActivationHeight(),
		requireSignedBlocks:     RequireSignedBlocks(),
		signedBlockActivation:   SignedBlockActivationHeight(),
	}
}

// VerifyProducerTransitionChain is ValidateProducerTransitionChain, returning
// what it verified for RestoreVerifiedChain. It is (nil, nil) for a producer
// without a producer transition, which verifies nothing here: RestoreChain
// then checks the blocks.
func (bp *BlockProducer) VerifyProducerTransitionChain(blocks []*Block) (*VerifiedChain, error) {
	if bp == nil {
		return nil, errors.New("chain: nil producer")
	}
	bp.mu.Lock()
	defer bp.mu.Unlock()
	if bp.producerTransition == nil {
		return nil, nil
	}
	settings := currentVerifySettings()
	if err := bp.validateProducerTransitionChainLocked(blocks); err != nil {
		return nil, err
	}
	return &VerifiedChain{
		epochs:   bp.producerEpochs,
		settings: settings,
		blocks:   append([]*Block(nil), blocks...),
	}, nil
}

// RestoreVerifiedChain is RestoreChain for blocks that v verified. blocks
// must be v's blocks, or a prefix of them whose last block is at or above
// the final approved checkpoint, element for element (the same *Block
// values), and bp must enforce the producer transition, under the package
// consensus settings, that v verified them with; anything else is an error
// and installs nothing. The blocks are not verified again. With v nil it is
// RestoreChain, which verifies everything.
func (bp *BlockProducer) RestoreVerifiedChain(blocks []*Block, v *VerifiedChain) error {
	if v == nil {
		return bp.RestoreChain(blocks)
	}
	if bp == nil {
		return fmt.Errorf("chain: nil producer")
	}
	bp.mu.Lock()
	defer bp.mu.Unlock()
	if len(bp.chain) != 0 {
		return fmt.Errorf("chain: RestoreChain called on a non-empty producer (have %d blocks)", len(bp.chain))
	}
	if err := v.coversLocked(bp, blocks); err != nil {
		return err
	}
	bp.installChainLocked(blocks)
	bp.journalPath = v.journal
	return nil
}

// coversLocked reports why blocks may not be restored on bp without being
// verified again, or nil. bp.mu must be held.
func (v *VerifiedChain) coversLocked(bp *BlockProducer, blocks []*Block) error {
	if bp.producerTransition == nil || !sameEpochs(v.epochs, bp.producerEpochs) {
		return errors.New("chain: these blocks were verified under another producer transition")
	}
	if v.settings != currentVerifySettings() {
		return errors.New("chain: the consensus settings changed after these blocks were verified")
	}
	if len(blocks) == 0 || len(blocks) > len(v.blocks) {
		return fmt.Errorf("chain: %d blocks are not a prefix of the %d verified blocks", len(blocks), len(v.blocks))
	}
	for i, b := range blocks {
		if b != v.blocks[i] {
			return fmt.Errorf("chain: the block at index %d is not the verified block", i)
		}
	}
	if blocks[len(blocks)-1].Height < finalCheckpointHeight(bp.producerEpochs) {
		return errTransitionIncomplete
	}
	return nil
}

func sameEpochs(a, b []producerpolicy.Epoch) bool {
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
