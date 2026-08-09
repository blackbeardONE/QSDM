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

func (chainMetricsAdapter) RecordGovParamStaged(param string) {
	RecordGovParamStaged(param)
}

func (chainMetricsAdapter) RecordGovParamActivated(param string, value uint64) {
	RecordGovParamActivated(param, value)
}

func (chainMetricsAdapter) RecordGovParamRejected(reason string) {
	RecordGovParamRejected(reason)
}

func (chainMetricsAdapter) RecordGovAuthorityVoted(op string) {
	RecordGovAuthorityVoted(op)
}

func (chainMetricsAdapter) RecordGovAuthorityCrossed(op string) {
	RecordGovAuthorityCrossed(op)
}

func (chainMetricsAdapter) RecordGovAuthorityActivated(op string, postCount uint64) {
	RecordGovAuthorityActivated(op, postCount)
}

func (chainMetricsAdapter) RecordGovAuthorityRejected(reason string) {
	RecordGovAuthorityRejected(reason)
}

// The three counters below feed the operator dashboard's
// proposals_created / votes_cast / quarantines_triggered figures.
// Metrics.Increment* methods existed for all three and GetStats
// surfaced their values, but nothing in the tree ever called them, so
// they read 0 forever no matter how much work the node did.
func (chainMetricsAdapter) RecordConsensusProposal() {
	GetMetrics().IncrementProposalsCreated()
}

func (chainMetricsAdapter) RecordConsensusVote() {
	GetMetrics().IncrementVotesCast()
}

func (chainMetricsAdapter) RecordQuarantineTriggered() {
	GetMetrics().IncrementQuarantinesTriggered()
}

func (chainMetricsAdapter) RecordBlockSealed(txCount int) {
	GetMetrics().RecordBlockSealed(txCount)
}
