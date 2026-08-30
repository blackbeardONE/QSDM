package chain

// SyncValidatorStakesFromAccounts resets each registered validator voting power to
// its locked self-stake. The AccountStore argument is retained for API compatibility;
// liquid wallet balances must not become validator voting power.
func SyncValidatorStakesFromAccounts(vs *ValidatorSet, _ *AccountStore) {
	if vs == nil {
		return
	}
	for _, addr := range vs.RegisteredAddresses() {
		_ = vs.ResetEffectiveStake(addr)
	}
}
