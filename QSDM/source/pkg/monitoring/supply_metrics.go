package monitoring

// supply_metrics.go: CELL supply gauges. Mirrors reputation_metrics.go in
// shape -- a thin re-export of the zero-dependency leaf plus a collector hook
// the scrape registers.

import "github.com/blackbeardONE/QSDM/pkg/monitoring/supplymetrics"

// SupplySnapshot re-exports the leaf type so callers outside pkg/chain do not
// need to import the leaf directly.
type SupplySnapshot = supplymetrics.SupplySnapshot

// SupplyProvider re-exports the leaf interface.
type SupplyProvider = supplymetrics.SupplyProvider

// RegisterSupplyProvider wires an account store into the scrape.
func RegisterSupplyProvider(p SupplyProvider) {
	supplymetrics.RegisterSupplyProvider(p)
}

// supplyPrometheusMetrics is the collector hook registered in
// prometheus_scrape.go.
//
// Returns nil when no provider is wired, matching the reputation collector:
// most unit tests never construct an AccountStore, and emitting a zero-valued
// supply row from those would be worse than emitting nothing -- a dashboard
// cannot distinguish "this node has no CELL" from "nobody told the scrape where
// to look", and the first of those is alarming while the second is routine.
func supplyPrometheusMetrics() []Metric {
	p := supplymetrics.Provider()
	if p == nil {
		return nil
	}
	snap := p.SupplySnapshot()

	return []Metric{
		{
			Name: "qsdm_cell_supply_total",
			Help: "Sum of every account balance in the in-memory ledger, in CELL. There is no " +
				"total-supply invariant on any mint path, so this is a measurement, not a " +
				"guarantee. Watch it against expected issuance OVER TIME: with a 1e15 funder the " +
				"float64 ULP is 0.125, so a single sub-ULP transfer cannot move this number at " +
				"all, but the audit's estimated ~190k CELL/yr loss is six orders of magnitude " +
				"above that and shows plainly over days. A slow drift against expected issuance " +
				"is the known float64 defect, not a scrape error.",
			Type:  MetricGauge,
			Value: snap.TotalCELL,
		},
		{
			Name: "qsdm_cell_supply_accounts",
			Help: "Number of accounts contributing to qsdm_cell_supply_total. Lets a reader " +
				"distinguish a genuinely empty ledger from a node where the supply provider was " +
				"never registered (in which case no supply series is emitted at all).",
			Type:  MetricGauge,
			Value: float64(snap.Accounts),
		},
		{
			Name: "qsdm_cell_supply_negative_total",
			Help: "Accounts holding a negative CELL balance. Every guarded path refuses to go " +
				"negative -- AccountStore.Debit returns 'insufficient balance' and ApplyTx checks " +
				"before debiting -- so this is a canary for a path that bypasses them, not for a " +
				"missing constraint. It should be zero permanently; any non-zero value means " +
				"something wrote a balance directly and is worth paging on.",
			Type:  MetricGauge,
			Value: float64(snap.NegativeAccounts),
		},
	}
}
