package chain

// StakingLedgerEntries decodes a staking ledger file exactly as
// LoadOrNewStakingLedger decodes it, without falling back to the backup file,
// and returns how many bond and unbonding entries it holds. An error means
// LoadOrNewStakingLedger would not use these bytes: it would load the backup
// file instead. The snapshot import (cmd/qsdm) uses it to refuse a snapshot
// whose staking ledger the node would not read as an empty one.
func StakingLedgerEntries(data []byte) (bonds, unbonding int, err error) {
	doc, err := decodeStakingPersistDoc(data)
	if err != nil {
		return 0, 0, err
	}
	return len(doc.Bonds), len(doc.Unbond), nil
}
