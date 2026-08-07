package chain

import (
	"fmt"
	"math"
	"sort"
)

// Fork-gated integer-dust accounting for AccountStore.
//
// Why the ARITHMETIC is gated, not just the state-root encoding: the
// float64 defect is in the arithmetic itself. `sender.Balance -= total` on a
// balance near 1e15 quantises the debit to the float64 ULP there (0.125
// CELL) while the recipient's credit is exact, so every reward block
// destroys ~0.06 CELL. Doing that subtraction in integers produces a
// DIFFERENT balance — correct, but different. Applying the correct
// arithmetic to historical blocks would therefore change historical state
// roots and split the chain.
//
// So the fork switches the whole accounting model at a height:
//
//	height <  ForkDustHeight  -> float64 Balance is authoritative (legacy,
//	                             bug-for-bug identical to today)
//	height >= ForkDustHeight  -> uint64 BalanceDust is authoritative
//
// At activation every account is converted once, deterministically, by
// flooring its float balance to whole dust. Flooring never creates money;
// the sub-dust remainder is dropped, which is the only choice that is both
// deterministic across nodes and non-inflationary.

// SetHeightFn installs the chain-height source used to decide whether dust
// accounting is active. Without it the store behaves as pre-fork forever,
// which is the safe default for tests and for nodes that never activate.
func (as *AccountStore) SetHeightFn(fn func() uint64) {
	as.mu.Lock()
	defer as.mu.Unlock()
	as.heightFn = fn
}

// dustActive reports whether this store should use integer accounting.
func (as *AccountStore) dustActive() bool {
	fn := as.heightFn
	if fn == nil {
		return false
	}
	return IsDustAccounting(fn())
}

// dustActiveLocked is dustActive for callers already holding as.mu.
func (as *AccountStore) dustActiveLocked() bool { return as.dustActive() }

// MigrateToDust converts every account's float balance to integer dust,
// flooring any sub-dust remainder. Idempotent: an already-migrated store is
// left untouched.
//
// Deterministic across nodes because it depends only on the float64 bits
// already agreed in the pre-fork state root and on floor semantics.
// Returns the total dust dropped by flooring, which callers should log —
// it is the one-off, bounded accounting difference introduced by the fork.
func (as *AccountStore) MigrateToDust() (droppedDust uint64, err error) {
	as.mu.Lock()
	defer as.mu.Unlock()
	if as.dustMigrated {
		return 0, nil
	}

	// Deterministic iteration order so any logging or metric derived from
	// this is reproducible.
	addrs := make([]string, 0, len(as.accounts))
	for a := range as.accounts {
		addrs = append(addrs, a)
	}
	sort.Strings(addrs)

	var totalDropped float64
	for _, addr := range addrs {
		acc := as.accounts[addr]
		if acc.Balance <= 0 || math.IsNaN(acc.Balance) {
			acc.BalanceDust = 0
			acc.Balance = 0
			continue
		}
		scaled := acc.Balance * float64(DustPerCellInt)
		if scaled >= float64(math.MaxUint64) {
			// A balance this large cannot exist under the cap and cannot
			// be represented in dust at all. Refuse rather than clamp: a
			// silent clamp here would mint or destroy an unbounded amount.
			return 0, fmt.Errorf(
				"chain: account %s holds %v CELL, which exceeds the representable dust range; "+
					"genesis must be re-derived to the 90M/10M split before activating the dust fork",
				addr, acc.Balance)
		}
		dust := uint64(math.Floor(scaled))
		totalDropped += scaled - math.Floor(scaled)
		acc.BalanceDust = dust
		// Keep the float mirror consistent with the authoritative dust so
		// JSON consumers and pre-fork readers agree.
		acc.Balance = DustToCell(dust)
	}

	as.dustMigrated = true
	return uint64(totalDropped), nil
}

// DustMigrated reports whether MigrateToDust has run on this store.
func (as *AccountStore) DustMigrated() bool {
	as.mu.RLock()
	defer as.mu.RUnlock()
	return as.dustMigrated
}

// balanceDustLocked returns an account's authoritative balance in dust,
// deriving it from the float mirror when the store has not migrated yet.
func balanceDustLocked(acc *Account) uint64 {
	if acc == nil {
		return 0
	}
	return acc.BalanceDust
}

// setBalanceDustLocked writes the authoritative dust balance and keeps the
// float mirror in sync so JSON, APIs and AllAccounts stay coherent.
func setBalanceDustLocked(acc *Account, dust uint64) {
	acc.BalanceDust = dust
	acc.Balance = DustToCell(dust)
}

// floorToDust converts a whole-CELL float to dust, flooring. Used on the
// transfer path where the amount comes from a mempool.Tx whose float value
// may not land exactly on a dust boundary. Flooring is the non-inflationary
// choice: it can only ever move less than requested, never more.
func floorToDust(cell float64) uint64 {
	if cell <= 0 || math.IsNaN(cell) {
		return 0
	}
	scaled := cell * float64(DustPerCellInt)
	if scaled >= float64(math.MaxUint64) {
		return math.MaxUint64
	}
	return uint64(math.Floor(scaled))
}

// dustStateRootSegment renders one account for the post-fork state root.
// Integers, so nothing is lost to %f's six decimal places — under which any
// balance below 1e-6 CELL was invisible even though the protocol unit is
// 1e-8.
func dustStateRootSegment(acc *Account) string {
	return fmt.Sprintf("%s:%d:%d;", acc.Address, acc.BalanceDust, acc.Nonce)
}
