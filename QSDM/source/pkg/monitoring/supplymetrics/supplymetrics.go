// Package supplymetrics is a zero-dependency leaf for CELL supply
// observability. Mirrors the repmetrics/netmetrics leaf pattern: it cannot live
// in pkg/monitoring because pkg/monitoring imports pkg/chain (chain_recorder.go)
// and the provider is implemented BY pkg/chain, so the reverse arrow would be a
// cycle. This leaf has zero non-stdlib imports.
//
// WHY THIS EXISTS. The audit's standing finding is that there is no total-supply
// invariant on any mint path, and that the money layer silently loses value:
// the funder debit quantises to a 0.125-CELL ULP while the miner credit is
// exact, destroying roughly 190k CELL a year. None of that is currently
// observable -- a node reports peers, blocks, reputation and storage latency,
// and reports nothing at all about how much CELL exists.
//
// This does NOT enforce a cap. SupplyLedger in pkg/chain/dustfork.go already
// implements capped issuance correctly, with an atomic compare-and-swap that
// concurrent minters cannot race past, and it has zero production callers
// because it is dust-denominated and the dust transition is gated shut (the
// 1e15 CELL synthetic funder is 1e23 dust against a uint64 ceiling of
// 1.845e19). Enforcement waits on that transition. Measurement does not, and
// measuring first is what makes the drift arguable with numbers instead of
// adjectives.
//
//	qsdm_cell_supply_total          — summed float64 balances across all accounts
//	qsdm_cell_supply_accounts       — number of accounts contributing
//	qsdm_cell_supply_negative_total — accounts holding a negative balance
package supplymetrics

import "sync"

// SupplySnapshot is the view the scrape pulls at evaluation time. Computed
// under the account store's read lock and returned by value, so the scrape
// renders a coherent snapshot without holding a lock past the call.
type SupplySnapshot struct {
	// TotalCELL is the sum of every account balance. Float64 by necessity:
	// that is the type the ledger uses, and rendering it as anything else
	// would hide the very precision loss this metric exists to expose.
	TotalCELL float64
	// Accounts is how many balances went into the sum, so a reader can tell
	// a genuinely empty ledger from a provider that is not wired.
	Accounts int
	// NegativeAccounts counts balances below zero. The float64 path has no
	// non-negative constraint (unlike the SQLite balances table, which gained
	// CHECK(balance >= 0) in the v0.4.1 migration), so this should be zero
	// forever and any non-zero value is a bug worth paging on.
	NegativeAccounts int
}

// SupplyProvider is the interface pkg/chain implements so the monitoring layer
// can pull supply state on demand. Implementations must be safe to call
// concurrently.
type SupplyProvider interface {
	SupplySnapshot() SupplySnapshot
}

var (
	mu       sync.RWMutex
	provider SupplyProvider
)

// RegisterSupplyProvider wires an account store into the metrics layer. Pass
// nil to unregister. Idempotent: a later call replaces the prior registration.
func RegisterSupplyProvider(p SupplyProvider) {
	mu.Lock()
	defer mu.Unlock()
	provider = p
}

// Provider returns the registered provider, or nil when none is wired.
func Provider() SupplyProvider {
	mu.RLock()
	defer mu.RUnlock()
	return provider
}
