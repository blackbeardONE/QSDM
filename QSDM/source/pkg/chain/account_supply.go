package chain

import "github.com/blackbeardONE/QSDM/pkg/monitoring/supplymetrics"

// SupplySnapshot implements supplymetrics.SupplyProvider so the scrape can
// report how much CELL exists.
//
// Nothing measured total supply before this. A node reported peers, blocks,
// reputation and storage latency, and reported nothing about the money. That
// matters here more than it would in most ledgers, because the float64 balance
// path is known to lose value silently: the funder debit quantises to a
// 0.125-CELL ULP while the miner credit is exact, and the audit puts the drift
// at roughly 190k CELL a year. A drift that large is invisible without a total
// to watch, and arguable only with adjectives.
//
// This is measurement, not enforcement. SupplyLedger (dustfork.go) already
// implements a correct capped-issuance check and has zero production callers
// because it is dust-denominated and the dust transition is gated shut behind
// an overflow the config layer refuses. Wiring enforcement waits on that
// transition; counting does not.
//
// Sums under the read lock rather than calling AllAccounts, which copies every
// account into a new slice and sorts it -- an allocation proportional to the
// ledger on every scrape, for a result the scrape immediately discards.
func (as *AccountStore) SupplySnapshot() supplymetrics.SupplySnapshot {
	as.mu.RLock()
	defer as.mu.RUnlock()

	// Kahan compensated summation. It reduces error when adding many balances
	// of wildly different magnitudes, which this ledger has by construction
	// (a 1e15 funder beside accounts holding fractions).
	//
	// WHAT IT DOES NOT DO, measured rather than assumed. It does not make this
	// gauge a per-transaction drift detector. With a 1e15 funder the float64
	// ULP is 0.125, and float64 cannot represent 1e15 + 0.01 at ALL -- so a
	// single sub-ULP transfer is invisible in the total no matter how the sum
	// is computed. Probed directly: transfers of 1, 0.5, 0.1, 0.05 and 0.01
	// CELL each reported a drift of exactly 0.000000000, before and after
	// switching to Kahan. The first version of this comment claimed Kahan fixed
	// that; it does not, and the limit is the result type, not the algorithm.
	//
	// What the gauge IS good for is ACCUMULATED drift. The audit puts the loss
	// at roughly 190k CELL a year, which is six orders of magnitude above the
	// ULP -- so it is plainly visible in this total over days, just not in any
	// single transfer. Watch the series against expected issuance, not
	// transaction by transaction.
	snap := supplymetrics.SupplySnapshot{Accounts: len(as.accounts)}
	var sum, compensation float64
	for _, acc := range as.accounts {
		if acc == nil {
			continue
		}
		y := acc.Balance - compensation
		t := sum + y
		compensation = (t - sum) - y
		sum = t
		if acc.Balance < 0 {
			snap.NegativeAccounts++
		}
	}
	snap.TotalCELL = sum
	return snap
}
