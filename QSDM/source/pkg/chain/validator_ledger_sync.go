package chain

// SyncValidatorStakesFromCommittedTip rebuilds effective validator voting power from
// locked self-stake plus optional derived weights, then applies delegated power.
func SyncValidatorStakesFromCommittedTip(vs *ValidatorSet, as *AccountStore, bp *BlockProducer, staking *StakingLedger) {
	SyncValidatorStakesFromCommittedChain(vs, as, bp, DefaultProducerBlockStakeBonus)
	applyStakingDelegationWeights(vs, staking)
}
