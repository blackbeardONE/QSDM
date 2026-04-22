package chain

// SyncValidatorStakesFromCommittedTip reapplies stakes from accounts plus committed-chain
// producer weights (see SyncValidatorStakesFromCommittedChain), then optional staking ledger
// delegated power.
func SyncValidatorStakesFromCommittedTip(vs *ValidatorSet, as *AccountStore, bp *BlockProducer, staking *StakingLedger) {
	SyncValidatorStakesFromCommittedChain(vs, as, bp, DefaultProducerBlockStakeBonus)
	applyStakingDelegationWeights(vs, staking)
}
