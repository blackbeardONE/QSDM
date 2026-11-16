package main

import (
	"fmt"

	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/config"
	"github.com/blackbeardONE/QSDM/pkg/mining/enrollment"
)

// applyPulseParams installs the PULSE consensus parameters from the config.
// main() and hl1ReplayConsensusSettings both call it, before any block is
// applied or replayed. An activation height with incomplete parameters is a
// configuration error: the node must not start.
func applyPulseParams(cfg *config.Config) error {
	p, err := chain.ParsePulseConfig(cfg.PulseActivationHeight, cfg.PulseMaxFeeBPS, cfg.PulsePools,
		cfg.PulsePayeeEpochCapCell, cfg.PulseReclaimAfterBlocks)
	if err != nil {
		return err
	}
	return chain.SetPulseParams(p)
}

// replayPulseStateFromBlocks rebuilds the PULSE state from blocks in order:
// PULSE txs and the producer's escrow rewards. The account snapshot already
// holds their balance effects.
func replayPulseStateFromBlocks(ps *chain.PulseStateStore, blocks []*chain.Block) (int, error) {
	if ps == nil {
		return 0, nil
	}
	n := 0
	for _, blk := range blocks {
		if blk == nil {
			continue
		}
		for _, tx := range blk.Transactions {
			if tx == nil {
				continue
			}
			switch {
			case tx.ContractID == chain.PulseContractID:
				if err := ps.ApplyHistoricalTx(tx, blk.Height); err != nil {
					return n, fmt.Errorf("height %d tx %s: %w", blk.Height, tx.ID, err)
				}
				n++
			case tx.ContractID == chain.MiningRewardContractID:
				if _, _, escrow := chain.ParsePulseEscrow(tx.Recipient); escrow {
					if err := ps.RecordEscrow(tx, blk.Height); err != nil {
						return n, fmt.Errorf("height %d tx %s: %w", blk.Height, tx.ID, err)
					}
					n++
				}
			}
		}
	}
	return n, nil
}

// pulsePayee is blockdriver.Config.PulsePayee: under the active rule, a member
// of an authorized pool without an outstanding deferred bond is paid to its
// escrow account; everyone else directly. The applier re-checks every part of
// this when the block is applied, so a wrong answer here can only fail a
// seal, never pay the wrong account.
func pulsePayee(ps *chain.PulseStateStore, enroll *enrollment.InMemoryState) func(owner string, height uint64) string {
	return func(owner string, height uint64) string {
		if ps == nil || !chain.PulseActiveAt(height) {
			return owner
		}
		pool, ok := ps.MemberPool(owner)
		if !ok {
			return owner
		}
		if enroll != nil && enroll.OwesDeferredBond(owner) {
			return owner
		}
		return chain.PulseEscrowAddress(pool, owner)
	}
}
