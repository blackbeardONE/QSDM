package monitoring

import (
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/monitoring/stubactive"
)

// TestStubActiveMetrics_AlwaysIncludesAllCanonicalKinds verifies
// the bridge from stubactive.Snapshot() → qsdm_stub_active gauge
// emits a row per canonical kind even when nothing is currently
// active. Critical for the alert template — `qsdm_stub_active == 1`
// would never fire if the metric was missing-data when no stub
// was loaded.
func TestStubActiveMetrics_AlwaysIncludesAllCanonicalKinds(t *testing.T) {
	stubactive.Reset()
	t.Cleanup(stubactive.Reset)

	got := stubActiveMetrics()
	seenKinds := make(map[string]bool)
	for _, m := range got {
		if m.Name != "qsdm_stub_active" {
			t.Fatalf("unexpected metric name %q", m.Name)
		}
		if m.Type != MetricGauge {
			t.Fatalf("metric type %v; want MetricGauge", m.Type)
		}
		kind := m.Labels["kind"]
		if kind == "" {
			t.Fatal("metric missing 'kind' label")
		}
		seenKinds[kind] = true
	}
	for _, k := range stubactive.AllKinds() {
		if !seenKinds[k] {
			t.Errorf("metric missing canonical kind %q", k)
		}
	}
}

// TestStubActiveMetrics_ReflectsMarkActive sanity-checks that
// flipping the registry flips the emitted gauge value.
func TestStubActiveMetrics_ReflectsMarkActive(t *testing.T) {
	stubactive.Reset()
	t.Cleanup(stubactive.Reset)

	stubactive.MarkActive(stubactive.KindPoE)
	stubactive.MarkActive(stubactive.KindCC)

	got := stubActiveMetrics()
	values := make(map[string]float64)
	for _, m := range got {
		values[m.Labels["kind"]] = m.Value
	}
	if values[stubactive.KindPoE] != 1 {
		t.Errorf("KindPoE value %v; want 1", values[stubactive.KindPoE])
	}
	if values[stubactive.KindCC] != 1 {
		t.Errorf("KindCC value %v; want 1", values[stubactive.KindCC])
	}
	if values[stubactive.KindDilithium] != 0 {
		t.Errorf("KindDilithium value %v; want 0 (untouched)", values[stubactive.KindDilithium])
	}
}

// TestStubActiveMetrics_PrometheusExposition verifies the metric
// surfaces in the actual /api/metrics/prometheus output (i.e. the
// collector is registered with the global exporter).
func TestStubActiveMetrics_PrometheusExposition(t *testing.T) {
	stubactive.Reset()
	t.Cleanup(stubactive.Reset)

	stubactive.MarkActive(stubactive.KindPoE)

	exposition := PrometheusExposition()
	if !strings.Contains(exposition, `qsdm_stub_active{kind="poe"} 1`) {
		t.Fatalf("PrometheusExposition() did not include qsdm_stub_active{kind=\"poe\"} 1\nfull text:\n%s", exposition)
	}
	// Even unmarked kinds must surface (with value 0) so the alert
	// query has a defined time series for every kind.
	for _, k := range stubactive.AllKinds() {
		if k == stubactive.KindPoE {
			continue
		}
		if !strings.Contains(exposition, `qsdm_stub_active{kind="`+k+`"} 0`) {
			t.Errorf("expected qsdm_stub_active{kind=%q} 0 in exposition (alert query needs full series)", k)
		}
	}
}

// TestStubActiveMetrics_ForwardCompatibleKind verifies that a
// future stub registering an unknown kind still surfaces in the
// emitted metrics (so a new stub doesn't require simultaneously
// editing AllKinds()).
func TestStubActiveMetrics_ForwardCompatibleKind(t *testing.T) {
	stubactive.Reset()
	t.Cleanup(stubactive.Reset)

	custom := "future_stub_y"
	stubactive.MarkActive(custom)

	got := stubActiveMetrics()
	for _, m := range got {
		if m.Labels["kind"] == custom && m.Value == 1 {
			return
		}
	}
	t.Fatalf("forward-compatible kind %q absent from metrics", custom)
}
