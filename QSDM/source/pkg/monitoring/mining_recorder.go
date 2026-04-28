package monitoring

// mining_recorder.go: glue that registers a real, atomic-
// backed implementation of pkg/mining.MiningMetricsRecorder
// at init time. Companion to chain_recorder.go, same
// dependency-arrow argument: pkg/monitoring imports pkg/mining
// (so the chain adapter can satisfy the chain interface),
// hence the reverse direction must be inverted via this
// recorder pattern.
//
// Tests can override the recorder by calling
// mining.SetMiningMetricsRecorder(...) directly.

import "github.com/blackbeardONE/QSDM/pkg/mining"

func init() {
	mining.SetMiningMetricsRecorder(miningMetricsAdapter{})
}

// miningMetricsAdapter implements mining.MiningMetricsRecorder
// by forwarding to the package-level Record* functions defined
// in archcheck_metrics.go.
type miningMetricsAdapter struct{}

func (miningMetricsAdapter) RecordArchSpoofRejected(reason string) {
	RecordArchSpoofRejected(reason)
}

func (miningMetricsAdapter) RecordHashrateRejected(arch string) {
	RecordHashrateRejected(arch)
}
