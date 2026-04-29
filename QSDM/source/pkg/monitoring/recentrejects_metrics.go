package monitoring

// recentrejects_metrics.go: Prometheus telemetry for the
// pkg/mining/attest/recentrejects ring buffer's defensive
// rune-truncation layer.
//
// Three series families, all keyed by field ∈ {detail,
// gpu_name, cert_subject}:
//
//   qsdm_attest_rejection_field_runes_observed_total{field}
//
//     Increments once per non-empty observed Rejection field
//     across the lifetime of the process. The denominator
//     for the truncation rate.
//
//   qsdm_attest_rejection_field_truncated_total{field}
//
//     Increments only when the field's pre-truncation rune
//     count exceeded its in-store cap. Numerator for the
//     truncation rate; the alert "Detail truncation firing"
//     is rate(...) > 0 sustained for ≥10m.
//
//   qsdm_attest_rejection_field_runes_max{field}
//
//     Process-lifetime monotonic max of the pre-truncation
//     rune count. Lets operators see at a glance "how close
//     did we come to the cap?" without joining a histogram.
//     Atomically updated; resets only on process restart.
//
// Cardinality: 3 series families × 3 fields = 9 series. Well
// under any best-practice ceiling.

import "sync/atomic"

// Field name constants are exported so tests can spell them
// without importing recentrejects (and avoiding a circular
// dep in test-only code). They MUST match
// recentrejects.FieldDetail / FieldGPUName / FieldCertSubject
// verbatim — the recentrejects package is the source of
// truth; this file mirrors the strings to keep the dep arrow
// pointing the right way (monitoring → mining only at init
// time via the recorder).
const (
	RecentRejectFieldDetail      = "detail"
	RecentRejectFieldGPUName     = "gpu_name"
	RecentRejectFieldCertSubject = "cert_subject"
)

var (
	rrFieldObservedDetail      atomic.Uint64
	rrFieldObservedGPUName     atomic.Uint64
	rrFieldObservedCertSubject atomic.Uint64

	rrFieldTruncatedDetail      atomic.Uint64
	rrFieldTruncatedGPUName     atomic.Uint64
	rrFieldTruncatedCertSubject atomic.Uint64

	rrFieldRunesMaxDetail      atomic.Uint64
	rrFieldRunesMaxGPUName     atomic.Uint64
	rrFieldRunesMaxCertSubject atomic.Uint64

	// rrPersistErrors counts on-disk persister.Append
	// failures observed by the recentrejects.Store. Exposed
	// as qsdm_attest_rejection_persist_errors_total. The
	// in-memory ring continues to receive records regardless
	// — this counter measures forensic-record durability,
	// not throughput. Unlabeled because filesystem failures
	// are not field-keyed (the same "disk full" affects all
	// fields uniformly).
	rrPersistErrors atomic.Uint64
)

// RecordRecentRejectField is the package-level entry point
// invoked by the recentrejects→monitoring adapter on every
// Store.Record() call per non-empty observed field.
//
// Negative or absurd rune counts are clamped: runes < 0
// becomes 0, and we cap the max-tracking at MaxInt64 to keep
// the storeMaxIfGreater helper monotonic.
func RecordRecentRejectField(field string, runes int, truncated bool) {
	if runes < 0 {
		runes = 0
	}
	switch field {
	case RecentRejectFieldDetail:
		rrFieldObservedDetail.Add(1)
		if truncated {
			rrFieldTruncatedDetail.Add(1)
		}
		storeMaxIfGreater(&rrFieldRunesMaxDetail, uint64(runes))
	case RecentRejectFieldGPUName:
		rrFieldObservedGPUName.Add(1)
		if truncated {
			rrFieldTruncatedGPUName.Add(1)
		}
		storeMaxIfGreater(&rrFieldRunesMaxGPUName, uint64(runes))
	case RecentRejectFieldCertSubject:
		rrFieldObservedCertSubject.Add(1)
		if truncated {
			rrFieldTruncatedCertSubject.Add(1)
		}
		storeMaxIfGreater(&rrFieldRunesMaxCertSubject, uint64(runes))
	default:
		// Unknown field — silently ignored. Cardinality stays
		// bounded if recentrejects ever introduces a typo.
	}
}

// RecordRecentRejectPersistError increments the
// qsdm_attest_rejection_persist_errors_total counter.
// Invoked by the recentrejects→monitoring adapter when the
// on-disk Persister.Append fails. The error is intentionally
// dropped here — operators care about the rate, not the
// individual error strings (which would expand cardinality
// without value); per-error context is tracked in the
// validator's structured logs.
func RecordRecentRejectPersistError(err error) {
	if err == nil {
		return
	}
	rrPersistErrors.Add(1)
}

// recentRejectPersistErrorsCount returns the current value of
// qsdm_attest_rejection_persist_errors_total for the
// Prometheus scrape path. Unexported because the only legitimate
// reader is prometheus_scrape.go; tests call
// RecentRejectPersistErrorsForTest below.
func recentRejectPersistErrorsCount() uint64 {
	return rrPersistErrors.Load()
}

// RecentRejectPersistErrorsForTest exposes the current value
// of qsdm_attest_rejection_persist_errors_total for unit
// tests. Production code reads via recentRejectPersistErrorsCount.
func RecentRejectPersistErrorsForTest() uint64 {
	return rrPersistErrors.Load()
}

// RecentRejectFieldMetricsView is one row of the per-field
// telemetry snapshot returned by RecentRejectMetricsSnapshot.
// JSON tag names below are the public contract; reordering is
// safe but renaming any of them is a breaking change for the
// operator dashboard tile that consumes this shape.
type RecentRejectFieldMetricsView struct {
	Field          string `json:"field"`
	ObservedTotal  uint64 `json:"observed_total"`
	TruncatedTotal uint64 `json:"truncated_total"`
	RunesMax       uint64 `json:"runes_max"`
}

// RecentRejectMetricsView is the all-fields snapshot of the
// recent-rejection ring's telemetry surface. Returned by
// RecentRejectMetricsSnapshot for in-process consumers (the
// operator dashboard's attestation-rejections tile, primarily)
// that want a coherent view without scraping Prometheus.
//
// This is a snapshot — Fields and PersistErrorsTotal are
// captured atomically per-counter but not as a transaction
// across the whole struct. Two near-simultaneous Record calls
// can interleave such that PersistErrorsTotal reflects a
// failure that has not yet been added to the in-memory ring;
// callers reading both this snapshot and the recentrejects
// list together MUST treat the count and the list as
// independent samples.
type RecentRejectMetricsView struct {
	Fields             []RecentRejectFieldMetricsView `json:"fields"`
	PersistErrorsTotal uint64                         `json:"persist_errors_total"`
}

// RecentRejectMetricsSnapshot returns the current per-field
// observed / truncated / runes-max counts plus the persist-
// error total. Safe for concurrent callers; all reads are
// atomic.Load.
//
// Field order in the returned slice matches
// recentRejectFieldsLabeled (detail, gpu_name, cert_subject)
// for stable rendering in the dashboard tile.
func RecentRejectMetricsSnapshot() RecentRejectMetricsView {
	rows := recentRejectFieldsLabeled()
	out := RecentRejectMetricsView{
		Fields:             make([]RecentRejectFieldMetricsView, 0, len(rows)),
		PersistErrorsTotal: rrPersistErrors.Load(),
	}
	for _, r := range rows {
		out.Fields = append(out.Fields, RecentRejectFieldMetricsView{
			Field:          r.Field,
			ObservedTotal:  r.Observed,
			TruncatedTotal: r.Truncated,
			RunesMax:       r.RunesMax,
		})
	}
	return out
}

// recentRejectFieldLabeled returns the (field, observed,
// truncated, max) tuples in stable order for Prometheus
// exposition.
type recentRejectFieldLabeled struct {
	Field     string
	Observed  uint64
	Truncated uint64
	RunesMax  uint64
}

func recentRejectFieldsLabeled() []recentRejectFieldLabeled {
	return []recentRejectFieldLabeled{
		{
			Field:     RecentRejectFieldDetail,
			Observed:  rrFieldObservedDetail.Load(),
			Truncated: rrFieldTruncatedDetail.Load(),
			RunesMax:  rrFieldRunesMaxDetail.Load(),
		},
		{
			Field:     RecentRejectFieldGPUName,
			Observed:  rrFieldObservedGPUName.Load(),
			Truncated: rrFieldTruncatedGPUName.Load(),
			RunesMax:  rrFieldRunesMaxGPUName.Load(),
		},
		{
			Field:     RecentRejectFieldCertSubject,
			Observed:  rrFieldObservedCertSubject.Load(),
			Truncated: rrFieldTruncatedCertSubject.Load(),
			RunesMax:  rrFieldRunesMaxCertSubject.Load(),
		},
	}
}

// storeMaxIfGreater is a CAS loop that bumps *dst to v iff
// v > current. atomic.Uint64 has no native max op, but the
// CAS form is the standard idiom and is contention-free in
// the common case (v == current or v < current).
func storeMaxIfGreater(dst *atomic.Uint64, v uint64) {
	for {
		cur := dst.Load()
		if v <= cur {
			return
		}
		if dst.CompareAndSwap(cur, v) {
			return
		}
	}
}

// ResetRecentRejectMetricsForTest clears every counter and
// max-tracker in this file. Tests-only; production code MUST
// NOT call this.
func ResetRecentRejectMetricsForTest() {
	rrFieldObservedDetail.Store(0)
	rrFieldObservedGPUName.Store(0)
	rrFieldObservedCertSubject.Store(0)
	rrFieldTruncatedDetail.Store(0)
	rrFieldTruncatedGPUName.Store(0)
	rrFieldTruncatedCertSubject.Store(0)
	rrFieldRunesMaxDetail.Store(0)
	rrFieldRunesMaxGPUName.Store(0)
	rrFieldRunesMaxCertSubject.Store(0)
	rrPersistErrors.Store(0)
}
