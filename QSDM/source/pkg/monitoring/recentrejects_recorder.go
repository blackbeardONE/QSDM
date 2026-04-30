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
// recentrejects.MetricsRecorder plus three optional
// extension surfaces by forwarding to the package-level
// Record*/Set* functions in recentrejects_metrics.go.
//
// All three optional surfaces are probed by the
// recentrejects package via type-assertion at runtime; the
// adapter satisfying them all lets the production scrape
// expose the full persistence lifecycle:
//
//   - PersistErrorRecorder       → persist_errors_total
//   - PersistCompactionRecorder  → persist_compactions_total
//   - PersistRecordsRecorder     → persist_records_on_disk (gauge)
//
// A future refactor that drops one of the methods
// silently breaks the relevant counter without a build
// failure (interface satisfaction is by structural match);
// the compile-time assertions in
// recentrejects_metrics_test.go ship-stop on any such
// regression.
type recentRejectsMetricsAdapter struct{}

func (recentRejectsMetricsAdapter) ObserveField(field string, runes int, truncated bool) {
	RecordRecentRejectField(field, runes, truncated)
}

func (recentRejectsMetricsAdapter) RecordPersistError(err error) {
	RecordRecentRejectPersistError(err)
}

func (recentRejectsMetricsAdapter) RecordPersistCompaction(recordsAfter int) {
	RecordRecentRejectPersistCompaction(recordsAfter)
}

func (recentRejectsMetricsAdapter) SetPersistRecordsOnDisk(n uint64) {
	SetRecentRejectPersistRecordsOnDisk(n)
}
