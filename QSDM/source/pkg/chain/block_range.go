package chain

// Height lookups.
//
// bp.chain is contiguous by height: every append is its tip's height plus one
// (ProduceBlock, TryAppendExternalBlock,
// ReceiptProducer.ProduceBlockWithReceipts), and RestoreChain and
// RestoreVerifiedChain install only contiguous, linked blocks. So the block at
// height h is bp.chain[h-bp.chain[0].Height], found by an index computation
// instead of a walk of the whole chain under bp.mu. The first block is genesis
// (height 0) for every chain a node builds, receives or verifies under a
// producer transition; RestoreChain without one also accepts a journal whose
// first block is above it.

// blockAtLocked returns the block at height. bp.mu must be held.
func (bp *BlockProducer) blockAtLocked(height uint64) (*Block, bool) {
	n := len(bp.chain)
	if n == 0 {
		return nil, false
	}
	base := bp.chain[0].Height
	if height < base || height-base >= uint64(n) {
		return nil, false
	}
	b := bp.chain[height-base]
	if b.Height != height {
		return nil, false // unreachable while the chain is contiguous
	}
	return b, true
}

// blockRangeLocked returns, in a new slice, the blocks whose heights lie in
// [from, to], in height order: the first limit of them when limit is
// positive. bp.mu must be held.
func (bp *BlockProducer) blockRangeLocked(from, to uint64, limit int) []*Block {
	n := len(bp.chain)
	if n == 0 || from > to {
		return nil
	}
	base, tip := bp.chain[0].Height, bp.chain[n-1].Height
	if to < base || from > tip {
		return nil
	}
	from, to = max(from, base), min(to, tip)
	count := to - from + 1
	if limit > 0 && uint64(limit) < count {
		count = uint64(limit)
	}
	i := from - base
	return append([]*Block(nil), bp.chain[i:i+count]...)
}

// BlockRange returns the blocks whose heights lie in [from, to], in height
// order, in a new slice. Only those pointers are copied. A block that
// LoadVerifiedJournal loaded, or that was appended and journaled before the
// tip (KeepSignaturesInJournal), may not hold its producer signature in
// memory: relay blocks with RelayBlocks.
func (bp *BlockProducer) BlockRange(from, to uint64) []*Block {
	if bp == nil {
		return nil
	}
	bp.mu.Lock()
	defer bp.mu.Unlock()
	return bp.blockRangeLocked(from, to, 0)
}
