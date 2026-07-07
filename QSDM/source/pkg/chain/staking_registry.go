package chain

// DefaultProducerBlockStakeBonus is voting weight added per sealed block where the
// validator appears as ProducerID (committed-chain staking proxy until a full module exists).
const DefaultProducerBlockStakeBonus = 0.25

func countProducerBlocks(bp *BlockProducer) map[string]int {
	out := make(map[string]int)
	if bp == nil {
		return out
	}
	// Walk the chain once under one lock. The old height loop called GetBlock,
	// which itself scanned the full slice, making restart O(height * blocks).
	bp.mu.Lock()
	defer bp.mu.Unlock()
	for _, b := range bp.chain {
		if b == nil || b.ProducerID == "" {
			continue
		}
		out[b.ProducerID]++
	}
	return out
}

// SyncValidatorStakesFromCommittedChain sets base stake from account balances, then adds
// producer weight from all sealed blocks in bp (on-chain registry proxy).
func SyncValidatorStakesFromCommittedChain(vs *ValidatorSet, as *AccountStore, bp *BlockProducer, producerBonus float64) {
	if vs == nil || as == nil {
		return
	}
	SyncValidatorStakesFromAccounts(vs, as)
	if bp == nil || producerBonus <= 0 {
		return
	}
	counts := countProducerBlocks(bp)
	for _, addr := range vs.RegisteredAddresses() {
		v, ok := vs.GetValidator(addr)
		if !ok {
			continue
		}
		base := v.Stake
		extra := float64(counts[addr]) * producerBonus
		if extra <= 0 {
			continue
		}
		_ = vs.SetStake(addr, base+extra)
	}
}
