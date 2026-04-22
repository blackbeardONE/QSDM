package chain

// SyncValidatorStakesFromAccounts sets each registered validator's stake from the same
// AccountStore used by the node admin / block pipeline (balance as voting weight proxy).
// Unknown accounts default to MinStake so validators remain eligible.
func SyncValidatorStakesFromAccounts(vs *ValidatorSet, as *AccountStore) {
	if vs == nil || as == nil {
		return
	}
	for _, addr := range vs.RegisteredAddresses() {
		bal := 0.0
		if acc, ok := as.Get(addr); ok && acc != nil {
			bal = acc.Balance
		}
		_ = vs.SetStake(addr, bal)
	}
}
