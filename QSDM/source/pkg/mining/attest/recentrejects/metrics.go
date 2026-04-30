package recentrejects

// metrics.go: dependency-inverted metrics recorder for the
// §4.6 recent-rejections ring. Surfaces three series per
// monitored field (Detail, GPUName, CertSubject):
//
//   - observed_total{field}    — denominator for the truncation
//                                 rate. Increments once per
//                                 Record() call per non-empty
//                                 field.
//   - truncated_total{field}   — numerator for the truncation
//                                 rate. Increments only when
//                                 the field exceeded its cap.
//   - runes_max{field}         — process-lifetime max rune count
//                                 observed on the field, atomic.
//                                 Lets operators answer "are we
//                                 close to the cap?" without
//                                 reasoning about a histogram.
//
// Why dependency inversion (mirror of pkg/mining/metrics.go):
//
//	pkg/mining/attest/recentrejects MUST NOT import pkg/monitoring
//	— monitoring imports recentrejects (transitively, via
//	pkg/mining) for verifier-recorder wiring and would close an
//	import cycle. So this package declares a narrow interface +
//	no-op default; pkg/monitoring's recentrejects_recorder.go
//	registers a Prometheus-backed adapter at init() time.
//
// Why three counters and not a histogram:
//
//	The existing pkg/monitoring exporter (see prometheus.go)
//	supports MetricCounter and MetricGauge only. A histogram
//	requires emitting bucket counters by hand, which works but
//	expands cardinality (3 fields × 6 buckets = 18 series) for
//	a metric whose primary operator question is binary: "is
//	the cap firing?". observed/truncated counters answer that
//	exactly via rate() division and the max gauge gives the
//	supplementary "how close were we?" signal at one series
//	per field.

import (
	"sync/atomic"
)

// MetricsRecorder is the narrow surface
// pkg/mining/attest/recentrejects.Store calls into on every
// Record(). Implementations must be safe for concurrent use;
// the production adapter in pkg/monitoring uses sync/atomic.
//
// ObserveField is invoked BEFORE the store applies its
// length-clamp truncation. fieldName is one of the
// FieldDetail / FieldGPUName / FieldCertSubject constants
// below; runes is the pre-truncation rune count;
// truncated is true iff runes exceeded the per-field cap and
// the store will apply truncation.
type MetricsRecorder interface {
	ObserveField(fieldName string, runes int, truncated bool)
}

// PersistErrorRecorder is the OPTIONAL extension surface a
// MetricsRecorder implementation MAY satisfy to receive
// notifications when the on-disk persister.Append fails. The
// Store detects support via type assertion (see
// notePersistError) — recorders that don't implement it
// simply skip the call, keeping the original
// ObserveField-only contract intact.
//
// Production wiring: pkg/monitoring's adapter implements
// both MetricsRecorder and PersistErrorRecorder so a
// failed Append increments
// qsdm_attest_rejection_persist_errors_total.
//
// The error is passed through verbatim so a future
// implementation could log it; the default Prometheus mirror
// only counts.
type PersistErrorRecorder interface {
	RecordPersistError(error)
}

// PersistCompactionRecorder is the OPTIONAL extension surface
// a MetricsRecorder implementation MAY satisfy to receive
// notifications when the on-disk persister rewrites its log
// to enforce the soft-cap (see FilePersister.compactLocked).
// recordsAfter is the post-compaction record count — i.e.
// the new file size in records, equal to the persister's
// SoftCap when the compaction trimmed the head.
//
// Production wiring: pkg/monitoring's adapter implements
// this so a successful compaction increments
// qsdm_attest_rejection_persist_compactions_total. Operators
// alert on rate(...) > N/min for N tuned to the realistic
// rejection-rate ceiling — a sustained high rate means a
// miner is filling the ring faster than the soft-cap can
// absorb, which is itself an alerting signal independent
// of the truncation-rate alert.
type PersistCompactionRecorder interface {
	RecordPersistCompaction(recordsAfter int)
}

// PersistHardCapDropRecorder is the OPTIONAL extension surface
// a MetricsRecorder implementation MAY satisfy to receive
// notifications when the on-disk persister rejects an Append
// because admitting it would push the JSONL file past its
// hard byte ceiling AND a salvage compaction failed to free
// enough headroom. droppedBytes is the size of the
// would-be-written record in bytes (line + framing newlines)
// — useful for "how much hostile traffic are we shedding?"
// dashboards.
//
// Production wiring: pkg/monitoring's adapter implements
// this so a hard-cap drop increments
// qsdm_attest_rejection_persist_hardcap_drops_total. Operators
// alert on rate(...) > 0 for 10m: ANY sustained drop activity
// is anomalous (the soft-cap compaction loop is sized to
// keep the file an order of magnitude below the hard cap on
// realistic rejection rates), so a non-zero rate means the
// validator is being actively flooded — escalate to operator,
// not just to logs.
//
// The in-memory ring is unaffected: a hard-cap drop only
// rejects the on-disk persistence step. The volatile ring
// continues to receive every record so the live operator
// surface (dashboard tile, /api/v1/attest/recent-rejections)
// stays accurate.
type PersistHardCapDropRecorder interface {
	RecordPersistHardCapDrop(droppedBytes int)
}

// PersistRecordsRecorder is the OPTIONAL extension surface a
// MetricsRecorder implementation MAY satisfy to receive
// best-effort gauge updates of the on-disk record count.
// Invoked from the persister at three moments:
//
//   - Construction: NewFilePersister counts existing records
//     and seeds the gauge so operators see a live value
//     immediately after boot, not a zero until the first
//     Append fires.
//   - Append success: gauge increments by one.
//   - Compaction success: gauge resets to the post-compaction
//     count.
//
// The gauge is approximate — it can briefly disagree with
// the disk during concurrent reads — but the producer side
// updates atomically per operation, so monotonic
// behaviour holds for any single operation.
type PersistRecordsRecorder interface {
	SetPersistRecordsOnDisk(n uint64)
}

// notePersistError forwards err to the active recorder iff
// it implements PersistErrorRecorder. Hot path: one
// atomic.Load + one type assertion per persistence failure
// — failures are rare so the cost is negligible.
func notePersistError(err error) {
	if err == nil {
		return
	}
	if pr, ok := currentMetricsRecorder().(PersistErrorRecorder); ok {
		pr.RecordPersistError(err)
	}
}

// notePersistCompaction forwards a post-compaction record
// count to the active recorder iff it implements
// PersistCompactionRecorder. Compactions are rare events
// (one per softCap appends), so the type assertion cost is
// in the noise.
func notePersistCompaction(recordsAfter int) {
	if pr, ok := currentMetricsRecorder().(PersistCompactionRecorder); ok {
		pr.RecordPersistCompaction(recordsAfter)
	}
}

// notePersistHardCapDrop forwards a dropped-record byte count
// to the active recorder iff it implements
// PersistHardCapDropRecorder. Hot path: one atomic.Load + one
// type assertion per drop. Drops are by definition the
// EXCEPTIONAL path (the soft-cap compaction loop should keep
// us well under the hard cap), so the assertion cost is
// irrelevant — the cost we DO care about is making sure the
// telemetry fires at all so operators see the flood.
func notePersistHardCapDrop(droppedBytes int) {
	if pr, ok := currentMetricsRecorder().(PersistHardCapDropRecorder); ok {
		pr.RecordPersistHardCapDrop(droppedBytes)
	}
}

// notePersistRecordsOnDisk forwards the current on-disk
// record count to the active recorder iff it implements
// PersistRecordsRecorder. Called at boot, after every
// Append, and after every compaction — so the cost lives
// on the same hot path as Append. Type assertion is one
// branch per call; recorders that don't implement the
// interface (the package-default no-op, plus any
// non-Prometheus test adapter) skip the work entirely.
func notePersistRecordsOnDisk(n uint64) {
	if pr, ok := currentMetricsRecorder().(PersistRecordsRecorder); ok {
		pr.SetPersistRecordsOnDisk(n)
	}
}

// Field name constants. Pinned to the exact set of fields the
// store truncates so a future store change (e.g. a new
// length-clamped field) is a deliberate, three-line update
// rather than an accidental cardinality blowup.
const (
	FieldDetail      = "detail"
	FieldGPUName     = "gpu_name"
	FieldCertSubject = "cert_subject"
)

// noopMetricsRecorder is the package-default. Pure unit tests
// of the store run with this so they never accumulate metrics
// state across runs.
type noopMetricsRecorder struct{}

func (noopMetricsRecorder) ObserveField(string, int, bool) {}

// metricsRecorderHolder satisfies atomic.Value's "all stored
// values must share an identical concrete type" constraint —
// the standard idiom for atomic.Value of an interface.
type metricsRecorderHolder struct {
	r MetricsRecorder
}

var metricsRecorderAtomic atomic.Value // holds metricsRecorderHolder

func init() {
	metricsRecorderAtomic.Store(metricsRecorderHolder{r: noopMetricsRecorder{}})
}

// SetMetricsRecorder installs the recorder. pkg/monitoring
// calls this from its init() with a real Prometheus-backed
// adapter; tests can call it with a fake. Pass nil to detach
// (recorder reverts to the no-op default).
//
// Safe for concurrent use with the read path
// (atomic.Value.Store / Load).
func SetMetricsRecorder(r MetricsRecorder) {
	if r == nil {
		metricsRecorderAtomic.Store(metricsRecorderHolder{r: noopMetricsRecorder{}})
		return
	}
	metricsRecorderAtomic.Store(metricsRecorderHolder{r: r})
}

// currentMetricsRecorder returns the active recorder, never
// nil. Hot path: a single atomic.Load + interface dispatch
// per Store.Record() call per non-empty observed field.
func currentMetricsRecorder() MetricsRecorder {
	v := metricsRecorderAtomic.Load()
	if v == nil {
		return noopMetricsRecorder{}
	}
	h, ok := v.(metricsRecorderHolder)
	if !ok || h.r == nil {
		return noopMetricsRecorder{}
	}
	return h.r
}
