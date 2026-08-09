package miningsvc

import (
	"github.com/blackbeardONE/QSDM/pkg/mining"
	"github.com/blackbeardONE/QSDM/pkg/monitoring"
)

// newObservedQuarantineSet builds the §8.3 fraud-quarantine set with its
// observer wired to the operator dashboard's quarantines_triggered figure.
//
// internal/miningsvc is the composition root for the mining service and may
// import both pkg/mining and pkg/monitoring; pkg/mining itself must not
// import pkg/monitoring (the dependency arrow runs monitoring -> mining, see
// pkg/monitoring/chain_recorder.go for the same argument on the chain side).
//
// Metrics.IncrementQuarantinesTriggered existed and GetStats surfaced its
// value on the dashboard, but a tree-wide search found zero production
// callers, so the figure was structurally incapable of being anything but 0.
func newObservedQuarantineSet() *mining.QuarantineSet {
	qs := mining.NewQuarantineSet()
	qs.SetOnQuarantine(func(addr string, until uint64) {
		monitoring.GetMetrics().IncrementQuarantinesTriggered()
	})
	return qs
}
