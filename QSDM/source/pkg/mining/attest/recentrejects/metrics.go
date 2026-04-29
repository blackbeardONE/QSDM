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
