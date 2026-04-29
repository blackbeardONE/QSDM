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

// recentRejectsMetricsAdapter implements both
// recentrejects.MetricsRecorder and the optional
// recentrejects.PersistErrorRecorder by forwarding to the
// package-level Record* functions in recentrejects_metrics.go.
//
// PersistErrorRecorder is the optional extension surface the
// Store probes via type-assertion; implementing it here lets
// the recentrejects ring's filesystem failures surface as
// qsdm_attest_rejection_persist_errors_total without us
// breaking the original ObserveField-only interface.
type recentRejectsMetricsAdapter struct{}

func (recentRejectsMetricsAdapter) ObserveField(field string, runes int, truncated bool) {
	RecordRecentRejectField(field, runes, truncated)
}

func (recentRejectsMetricsAdapter) RecordPersistError(err error) {
	RecordRecentRejectPersistError(err)
}
