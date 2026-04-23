package quarantine

import (
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/monitoring"
)

// findMetric returns the named metric from the slice, or nil if absent.
// Tests assert on presence, value, type, and help text via this helper
// rather than relying on slice ordering (which the collector doesn't
// guarantee).
func findMetric(t *testing.T, ms []monitoring.Metric, name string) *monitoring.Metric {
	t.Helper()
	for i := range ms {
		if ms[i].Name == name {
			return &ms[i]
		}
	}
	return nil
}

func TestMetricsCollector_NilManagerEmitsNothing(t *testing.T) {
	// A node with the quarantine subsystem disabled should publish
	// absent gauges, not zero-valued gauges. Prometheus treats absence
	// as "not applicable" and absence keeps ratio alerts from firing
	// on a denominator that doesn't exist.
	c := MetricsCollector(nil)
	if got := c(); len(got) != 0 {
		t.Fatalf("nil manager should emit zero metrics, got %d: %+v", len(got), got)
	}
}

func TestMetricsCollector_EmptyManagerShape(t *testing.T) {
	// Fresh manager, no RecordTransaction calls. All four gauges
	// should be present with well-defined zero values; the ratio
	// gauge specifically must NOT divide by zero (we return 0, not NaN).
	qm := NewQuarantineManager(0.5)
	ms := MetricsCollector(qm)()

	expectNames := []string{
		"qsdm_quarantine_submeshes",
		"qsdm_quarantine_submeshes_tracked",
		"qsdm_quarantine_submeshes_ratio",
		"qsdm_quarantine_threshold",
	}
	for _, n := range expectNames {
		m := findMetric(t, ms, n)
		if m == nil {
			t.Errorf("expected gauge %q to be emitted", n)
			continue
		}
		if m.Type != monitoring.MetricGauge {
			t.Errorf("%s: expected gauge, got %s", n, m.Type)
		}
		if m.Help == "" {
			t.Errorf("%s: help text is empty", n)
		}
	}

	if v := findMetric(t, ms, "qsdm_quarantine_submeshes").Value; v != 0 {
		t.Errorf("qsdm_quarantine_submeshes on empty manager: got %v, want 0", v)
	}
	if v := findMetric(t, ms, "qsdm_quarantine_submeshes_tracked").Value; v != 0 {
		t.Errorf("qsdm_quarantine_submeshes_tracked on empty manager: got %v, want 0", v)
	}
	if v := findMetric(t, ms, "qsdm_quarantine_submeshes_ratio").Value; v != 0 {
		t.Errorf("qsdm_quarantine_submeshes_ratio on empty manager: got %v, want 0 (no divide-by-zero)", v)
	}
	if v := findMetric(t, ms, "qsdm_quarantine_threshold").Value; v != 0.5 {
		t.Errorf("qsdm_quarantine_threshold: got %v, want 0.5", v)
	}
}

func TestMetricsCollector_TracksWithoutQuarantine(t *testing.T) {
	// A submesh that has only had a few transactions (fewer than the
	// 10-tx window) is in txCounts but not yet in quarantined. The
	// "tracked" gauge must count it; "submeshes" must not.
	qm := NewQuarantineManager(0.5)
	for i := 0; i < 5; i++ {
		qm.RecordTransaction("alpha", true)
	}
	ms := MetricsCollector(qm)()

	if v := findMetric(t, ms, "qsdm_quarantine_submeshes").Value; v != 0 {
		t.Errorf("qsdm_quarantine_submeshes: got %v, want 0 (no quarantine yet)", v)
	}
	if v := findMetric(t, ms, "qsdm_quarantine_submeshes_tracked").Value; v != 1 {
		t.Errorf("qsdm_quarantine_submeshes_tracked: got %v, want 1", v)
	}
	if v := findMetric(t, ms, "qsdm_quarantine_submeshes_ratio").Value; v != 0 {
		t.Errorf("qsdm_quarantine_submeshes_ratio: got %v, want 0", v)
	}
}

func TestMetricsCollector_QuarantinedSubmeshFlipsGauges(t *testing.T) {
	// Two submeshes; only one exceeds the threshold. Drive each
	// through a full 10-tx window so RecordTransaction's boundary
	// check fires. "bad" gets 6 invalid + 4 valid (0.6 > 0.5 threshold
	// → quarantined); "good" gets 10 valid (0.0 → not quarantined).
	qm := NewQuarantineManager(0.5)
	for i := 0; i < 6; i++ {
		qm.RecordTransaction("bad", false)
	}
	for i := 0; i < 4; i++ {
		qm.RecordTransaction("bad", true)
	}
	for i := 0; i < 10; i++ {
		qm.RecordTransaction("good", true)
	}

	if !qm.IsQuarantined("bad") {
		t.Fatal("test fixture broken: 'bad' should be quarantined after 6/10 invalid")
	}
	if qm.IsQuarantined("good") {
		t.Fatal("test fixture broken: 'good' should not be quarantined")
	}

	ms := MetricsCollector(qm)()
	if v := findMetric(t, ms, "qsdm_quarantine_submeshes").Value; v != 1 {
		t.Errorf("qsdm_quarantine_submeshes: got %v, want 1", v)
	}
	if v := findMetric(t, ms, "qsdm_quarantine_submeshes_tracked").Value; v != 2 {
		t.Errorf("qsdm_quarantine_submeshes_tracked: got %v, want 2", v)
	}
	if v := findMetric(t, ms, "qsdm_quarantine_submeshes_ratio").Value; v != 0.5 {
		t.Errorf("qsdm_quarantine_submeshes_ratio: got %v, want 0.5 (1/2)", v)
	}
}

func TestMetricsCollector_ClearedQuarantineIsReflected(t *testing.T) {
	// Quarantine a submesh, then RemoveQuarantine. The
	// "submeshes" gauge should drop to 0; "tracked" stays at 1
	// because the window bookkeeping persists across removals.
	qm := NewQuarantineManager(0.5)
	for i := 0; i < 6; i++ {
		qm.RecordTransaction("bad", false)
	}
	for i := 0; i < 4; i++ {
		qm.RecordTransaction("bad", true)
	}
	if !qm.IsQuarantined("bad") {
		t.Fatal("test fixture broken: 'bad' should be quarantined")
	}

	if err := qm.RemoveQuarantine("bad"); err != nil {
		t.Fatalf("RemoveQuarantine: %v", err)
	}

	ms := MetricsCollector(qm)()
	if v := findMetric(t, ms, "qsdm_quarantine_submeshes").Value; v != 0 {
		t.Errorf("qsdm_quarantine_submeshes after removal: got %v, want 0", v)
	}
	if v := findMetric(t, ms, "qsdm_quarantine_submeshes_tracked").Value; v != 1 {
		t.Errorf("qsdm_quarantine_submeshes_tracked after removal: got %v, want 1", v)
	}
}

func TestStats_AllZeroQuarantineStateIsSkippedInCount(t *testing.T) {
	// RecordTransaction writes quarantined[submesh] = false for a
	// clean window. Stats.Quarantined must count only true entries,
	// not all keys. This test guards against a future refactor that
	// naively uses len(quarantined) as the gauge value.
	qm := NewQuarantineManager(0.5)
	for i := 0; i < 10; i++ {
		qm.RecordTransaction("clean", true)
	}
	s := qm.Stats()
	if s.Quarantined != 0 {
		t.Errorf("Stats.Quarantined: got %d, want 0 (submesh has quarantined[k]=false)", s.Quarantined)
	}
	if s.Tracked != 1 {
		t.Errorf("Stats.Tracked: got %d, want 1", s.Tracked)
	}
	if s.Threshold != 0.5 {
		t.Errorf("Stats.Threshold: got %v, want 0.5", s.Threshold)
	}
}
