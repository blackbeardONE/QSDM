package main

import (
	"fmt"

	"github.com/blackbeardONE/QSDM/pkg/chain"
)

// replayQSDM20StateFromBlocks rebuilds the QSDM-20 token state from committed
// blocks, each token transaction at its block's height
// (chain.QSDM20StateStore.ApplyHistoricalTx). Account balances and nonces
// come from the persisted account snapshot. Blocks below the activation
// height (every block while none is set) are skipped: a qsdm/token/v1
// transaction there was a plain CELL transfer, which the account snapshot
// already holds, and never touched the token state. Every token transaction
// in a committed block at or above it was applied when the block was made,
// so a refusal here means the journal, or this node's
// qsdm20_activation_height, does not match the chain, and the restore stops.
func replayQSDM20StateFromBlocks(tokens *chain.QSDM20StateStore, blocks []*chain.Block) (int, error) {
	if tokens == nil {
		return 0, nil
	}
	replayed := 0
	for _, blk := range blocks {
		if blk == nil || !chain.QSDM20ActiveAt(blk.Height) {
			continue
		}
		for _, tx := range blk.Transactions {
			if tx == nil || tx.ContractID != chain.QSDM20ContractID {
				continue
			}
			if err := tokens.ApplyHistoricalTx(tx, blk.Height); err != nil {
				return replayed, fmt.Errorf("height %d tx %s: %w", blk.Height, tx.ID, err)
			}
			replayed++
		}
	}
	return replayed, nil
}
