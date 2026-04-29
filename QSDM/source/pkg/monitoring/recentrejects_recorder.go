package monitoring

// recentrejects_recorder.go: registers a Prometheus-backed
// implementation of recentrejects.MetricsRecorder at init
// time. Companion to mining_recorder.go (same dependency
// inversion pattern, same reasoning).
//
// pkg/monitoring imports pkg/mining/attest/recentrejects
// transitively through pkg/mining; the reverse arrow is the
// one we cannot draw, hence this adapter.
//
// Tests can override the recorder by calling
// recentrejects.SetMetricsRecorder(...) directly with a fake.

import "github.com/blackbeardONE/QSDM/pkg/mining/attest/recentrejects"

func init() {
	recentrejects.SetMetricsRecorder(recentRejectsMetricsAdapter{})
}

// recentRejectsMetricsAdapter implements
// recentrejects.MetricsRecorder by forwarding to the package-
// level RecordRecentRejectField function defined in
// recentrejects_metrics.go.
type recentRejectsMetricsAdapter struct{}

func (recentRejectsMetricsAdapter) ObserveField(field string, runes int, truncated bool) {
	RecordRecentRejectField(field, runes, truncated)
}
