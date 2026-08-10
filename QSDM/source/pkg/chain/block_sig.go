package chain

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
)

// Block producer signatures.
//
// Blocks carried no producer signature at all: propagation.go accepted any
// block whose Hash recomputed correctly, and ProducerID was a plain string
// field nothing authenticated. Recomputing a hash proves only internal
// consistency — an attacker can fabricate a whole block, set ProducerID to
// any validator, compute the matching hash, and it verified.
//
// The signature covers the block hash, which computeBlockHash already
// derives from height, prev hash, state root, tx root, timestamp and
// producer. Signing the hash therefore commits to all of them. The
// signature deliberately is NOT an input to computeBlockHash — that would be
// circular, and keeping it out means adding this field does not change any
// existing block hash.

// ErrBlockUnsigned is returned when a block carries no producer signature
// but signatures are required.
var ErrBlockUnsigned = errors.New("chain: block has no producer signature")

// ErrBlockBadSignature is returned when a block's producer signature does
// not verify against its ProducerID.
var ErrBlockBadSignature = errors.New("chain: block producer signature invalid")

// requireSignedBlocks gates inbound enforcement for rolling upgrades.
// Invalid signatures are always fatal.
var requireSignedBlocks atomic.Bool
var signedBlockActivationHeight atomic.Uint64

// SetRequireSignedBlocks controls whether unsigned blocks are accepted from
// peers. Enable once every producer signs its blocks.
func SetRequireSignedBlocks(require bool) { requireSignedBlocks.Store(require) }

// RequireSignedBlocks reports the current policy.
func RequireSignedBlocks() bool { return requireSignedBlocks.Load() }

// SetSignedBlockActivationHeight sets the first height where an unsigned
// block is rejected. Zero means immediate enforcement for compatibility
// with direct callers; production configuration requires a future height.
func SetSignedBlockActivationHeight(height uint64) {
	signedBlockActivationHeight.Store(height)
}

// SignedBlockActivationHeight reports the configured activation height.
func SignedBlockActivationHeight() uint64 { return signedBlockActivationHeight.Load() }

// SignedBlocksRequiredAt reports whether unsigned blocks are forbidden at height.
func SignedBlocksRequiredAt(height uint64) bool {
	if !RequireSignedBlocks() {
		return false
	}
	activation := SignedBlockActivationHeight()
	return activation == 0 || height >= activation
}

// SetBlockSigner installs the key used to authenticate blocks this producer
// seals. When nil, blocks are emitted unsigned — which peers running with
// RequireSignedBlocks will reject.
// Guarded by its own mutex rather than bp.mu: ProduceBlock already holds
// bp.mu when it reaches the signing step, so reusing that lock would
// deadlock.
func (bp *BlockProducer) SetBlockSigner(s BFTSigner) {
	if bp == nil {
		return
	}
	bp.signerMu.Lock()
	defer bp.signerMu.Unlock()
	bp.blockSigner = s
}

// BlockSigner returns the configured block signer, if any.
func (bp *BlockProducer) BlockSigner() BFTSigner {
	if bp == nil {
		return nil
	}
	bp.signerMu.RLock()
	defer bp.signerMu.RUnlock()
	return bp.blockSigner
}

// blockSigDigest is the canonical pre-image signed for a block.
func blockSigDigest(b *Block) []byte {
	var sb strings.Builder
	sb.WriteString("qsdm/block/v1")
	writeTagged(&sb, "h", fmt.Sprintf("%d", b.Height))
	writeTagged(&sb, "bh", b.Hash)
	writeTagged(&sb, "pr", b.ProducerID)
	sum := sha256.Sum256([]byte(sb.String()))
	return sum[:]
}

// SignBlock attaches the producer's signature. The block's Hash must already
// be computed; signing a block whose hash is stale would authenticate the
// wrong contents.
func SignBlock(b *Block, signer BFTSigner) error {
	if b == nil {
		return errors.New("chain: nil block")
	}
	if b.Hash == "" {
		return errors.New("chain: cannot sign a block with no hash")
	}
	if want := computeBlockHash(b); want != b.Hash {
		return fmt.Errorf("chain: refusing to sign block whose hash is stale (have %s want %s)", b.Hash, want)
	}
	auth, err := signAuth(signer, blockSigDigest(b))
	if err != nil {
		return err
	}
	// The producer identity must be the one the key derives, otherwise the
	// signature would authenticate a block attributed to someone else.
	b.ProducerID = BFTValidatorAddress(auth.PublicKey)
	// ProducerID is an input to computeBlockHash, so the hash and then the
	// signature must both be recomputed over the final identity.
	b.Hash = computeBlockHash(b)
	auth, err = signAuth(signer, blockSigDigest(b))
	if err != nil {
		return err
	}
	b.ProducerAuth = auth
	return nil
}

// VerifyBlockSignature checks a block's producer signature. An attached
// signature is always verified; the policy flag governs unsigned blocks.
func VerifyBlockSignature(b *Block) error {
	if b == nil {
		return errors.New("chain: nil block")
	}
	if !b.ProducerAuth.Signed() {
		if SignedBlocksRequiredAt(b.Height) {
			return ErrBlockUnsigned
		}
		return nil
	}
	if want := computeBlockHash(b); want != b.Hash {
		return fmt.Errorf("%w: block hash does not match its contents", ErrBlockBadSignature)
	}
	if err := verifyAuth(b.ProducerAuth, blockSigDigest(b), b.ProducerID); err != nil {
		return fmt.Errorf("%w: %v", ErrBlockBadSignature, err)
	}
	return nil
}
