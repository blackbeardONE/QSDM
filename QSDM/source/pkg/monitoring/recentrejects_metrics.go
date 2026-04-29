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
}
