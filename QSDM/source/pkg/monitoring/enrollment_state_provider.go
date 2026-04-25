package monitoring

// enrollment_state_provider.go: adapter that turns a
// *enrollment.InMemoryState into the EnrollmentStateProvider
// interface in enrollment_metrics.go. Lives in pkg/monitoring
// rather than pkg/mining/enrollment so the enrollment package
// stays free of any monitoring import (one-way arrow).
//
// Boot sequence: the chain creates the InMemoryState, then
// calls SetEnrollmentStateProvider(NewEnrollmentInMemoryStateProvider(state))
// from main / the node startup path. After that, every
// /api/metrics/prometheus scrape calls .Stats() under the
// state's lock and returns the snapshot to the exporter.

import (
	"github.com/blackbeardONE/QSDM/pkg/mining/enrollment"
)

// EnrollmentInMemoryStateProvider is an EnrollmentStateProvider
// backed by a *enrollment.InMemoryState. Any enrollment-state
// implementation that exposes Stats() can be wrapped trivially
// — see the StatsSource interface below.
type EnrollmentInMemoryStateProvider struct {
	source StatsSource
}

// StatsSource is the narrow surface required to drive the
// enrollment gauge metrics. Implemented by
// *enrollment.InMemoryState; tests can substitute a fake.
type StatsSource interface {
	Stats() enrollment.Stats
}

// NewEnrollmentInMemoryStateProvider wraps a StatsSource. nil
// returns nil so callers can use the result directly with
// SetEnrollmentStateProvider (which interprets nil as "detach").
func NewEnrollmentInMemoryStateProvider(src StatsSource) *EnrollmentInMemoryStateProvider {
	if src == nil {
		return nil
	}
	return &EnrollmentInMemoryStateProvider{source: src}
}

// ActiveCount implements EnrollmentStateProvider.
func (p *EnrollmentInMemoryStateProvider) ActiveCount() uint64 {
	if p == nil || p.source == nil {
		return 0
	}
	return p.source.Stats().ActiveCount
}

// BondedDust implements EnrollmentStateProvider.
func (p *EnrollmentInMemoryStateProvider) BondedDust() uint64 {
	if p == nil || p.source == nil {
		return 0
	}
	return p.source.Stats().BondedDust
}

// PendingUnbondCount implements EnrollmentStateProvider.
func (p *EnrollmentInMemoryStateProvider) PendingUnbondCount() uint64 {
	if p == nil || p.source == nil {
		return 0
	}
	return p.source.Stats().PendingUnbondCount
}

// PendingUnbondDust implements EnrollmentStateProvider.
func (p *EnrollmentInMemoryStateProvider) PendingUnbondDust() uint64 {
	if p == nil || p.source == nil {
		return 0
	}
	return p.source.Stats().PendingUnbondDust
}
