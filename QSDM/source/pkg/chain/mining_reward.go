package chain

import (
	"errors"
	"fmt"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/mining/enrollment"
)

// MiningRewardContractID marks validator-created protocol reward transfers so
// every validator can apply deferred-bond withholding during block replay.
const MiningRewardContractID = "qsdm/mining-reward/v1"

// MiningRewardFunderAddress is the protocol account debited by mining rewards.
const MiningRewardFunderAddress = "qsdm-system-funder"

// ApplyMiningRewardTx atomically coordinates the account transfer with the
// enrollment bond accrual. If account application fails, the enrollment state
// is restored to its pre-transaction snapshot.
func (a *EnrollmentApplier) ApplyMiningRewardTx(tx *mempool.Tx) error {
	if a == nil || a.Accounts == nil || a.State == nil {
		return errors.New("chain: mining reward enrollment state is not wired")
	}
	if tx == nil {
		return errors.New("chain: nil mining reward tx")
	}
	if tx.ContractID != MiningRewardContractID || tx.Sender != MiningRewardFunderAddress {
		return fmt.Errorf("chain: invalid mining reward contract or sender")
	}
	if tx.Fee != 0 || tx.Amount <= 0 || tx.Recipient == "" {
		return fmt.Errorf("chain: invalid mining reward shape")
	}

	cloneable, ok := a.State.(enrollment.CloneableState)
	if !ok {
		return errors.New("chain: mining reward requires cloneable enrollment state")
	}
	before := cloneable.Clone()
	rewardDust := balanceToDust(tx.Amount)
	lockedDust := a.State.AccrueBondFromReward(tx.Recipient, rewardDust)
	if lockedDust > rewardDust {
		_ = cloneable.Restore(before)
		return errors.New("chain: enrollment locked more than the protocol reward")
	}
	liquidAmount := tx.Amount - dustToBalance(lockedDust)
	if liquidAmount < 0 {
		liquidAmount = 0
	}
	if err := a.Accounts.ApplyProtocolReward(tx, liquidAmount); err != nil {
		if restoreErr := cloneable.Restore(before); restoreErr != nil {
			return fmt.Errorf("chain: apply mining reward: %v (enrollment rollback failed: %w)", err, restoreErr)
		}
		return fmt.Errorf("chain: apply mining reward: %w", err)
	}
	return nil
}
