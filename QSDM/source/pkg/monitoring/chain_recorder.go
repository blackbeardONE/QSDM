package monitoring

// chain_recorder.go: glue that registers a real, atomic-
// backed implementation of pkg/chain.MetricsRecorder at init
// time. Anything that imports pkg/monitoring (i.e. every
// production binary, plus most tests that go anywhere near
// the scrape endpoint) gets real Prometheus counters wired
// into the SlashApplier and EnrollmentApplier in pkg/chain
// for free.
//
// The reason this lives in pkg/monitoring rather than
// pkg/chain is the same dependency-arrow argument documented
// at the top of pkg/chain/events.go: pkg/monitoring already
// imports pkg/networking which imports pkg/chain, so an
// import in the reverse direction (chain -> monitoring) is a
// cycle. monitoring -> chain is fine.
//
// Tests can override the recorder by calling
// chain.SetChainMetricsRecorder(...) directly.

import "github.com/blackbeardONE/QSDM/pkg/chain"

func init() {
	chain.SetChainMetricsRecorder(chainMetricsAdapter{})
}

// chainMetricsAdapter implements chain.MetricsRecorder by
// forwarding to the package-level Record* functions defined
// in slashing_metrics.go and enrollment_metrics.go.
type chainMetricsAdapter struct{}

func (chainMetricsAdapter) RecordSlashApplied(kind string, drainedDust uint64) {
	RecordSlashApplied(kind, drainedDust)
}

func (chainMetricsAdapter) RecordSlashReward(rewardedDust, burnedDust uint64) {
	RecordSlashReward(rewardedDust, burnedDust)
}

func (chainMetricsAdapter) RecordSlashRejected(reason string) {
	RecordSlashRejected(reason)
}

func (chainMetricsAdapter) RecordSlashAutoRevoke(reason string) {
	RecordSlashAutoRevoke(reason)
}

func (chainMetricsAdapter) RecordEnrollmentApplied()   { RecordEnrollmentApplied() }
func (chainMetricsAdapter) RecordUnenrollmentApplied() { RecordUnenrollmentApplied() }

func (chainMetricsAdapter) RecordEnrollmentRejected(reason string) {
	RecordEnrollmentRejected(reason)
}

func (chainMetricsAdapter) RecordUnenrollmentRejected(reason string) {
	RecordUnenrollmentRejected(reason)
}

func (chainMetricsAdapter) RecordEnrollmentUnbondSwept(count uint64) {
	RecordEnrollmentUnbondSwept(count)
}
