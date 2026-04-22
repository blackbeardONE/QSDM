package monitoring

import (
	"strings"
	"testing"
)

func TestDualEmit_BothPrefixesDefault(t *testing.T) {
	t.Setenv("QSDM_METRICS_EMIT_LEGACY", "")
	t.Setenv("QSDM_METRICS_EMIT_QSDM", "")
	mode := ReadMetricPrefixMode()
	if !mode.emitLegacy || !mode.emitNew {
		t.Fatalf("default knobs must have both emissions on, got %+v", mode)
	}
	out := DualEmit([]Metric{
		{Name: "qsdmplus_demo_total", Help: "demo", Type: MetricCounter, Value: 3},
	}, mode)
	names := metricNames(out)
	if !contains(names, "qsdmplus_demo_total") {
		t.Errorf("default mode must keep legacy alias, got %v", names)
	}
	if !contains(names, "qsdm_demo_total") {
		t.Errorf("default mode must also emit new prefix, got %v", names)
	}
}

func TestDualEmit_LegacyOff(t *testing.T) {
	t.Setenv("QSDM_METRICS_EMIT_LEGACY", "0")
	t.Setenv("QSDM_METRICS_EMIT_QSDM", "1")
	mode := ReadMetricPrefixMode()
	out := DualEmit([]Metric{
		{Name: "qsdmplus_demo_total", Help: "demo", Type: MetricCounter, Value: 3},
	}, mode)
	names := metricNames(out)
	if contains(names, "qsdmplus_demo_total") {
		t.Errorf("legacy off: must suppress qsdmplus_*, got %v", names)
	}
	if !contains(names, "qsdm_demo_total") {
		t.Errorf("legacy off: must still emit qsdm_*, got %v", names)
	}
}

func TestDualEmit_QSDMOff(t *testing.T) {
	t.Setenv("QSDM_METRICS_EMIT_LEGACY", "1")
	t.Setenv("QSDM_METRICS_EMIT_QSDM", "0")
	mode := ReadMetricPrefixMode()
	out := DualEmit([]Metric{
		{Name: "qsdmplus_demo_total", Help: "demo", Type: MetricCounter, Value: 3},
	}, mode)
	names := metricNames(out)
	if !contains(names, "qsdmplus_demo_total") {
		t.Errorf("qsdm off: must still emit qsdmplus_*, got %v", names)
	}
	if contains(names, "qsdm_demo_total") {
		t.Errorf("qsdm off: must suppress qsdm_*, got %v", names)
	}
}

func TestDualEmit_BothOffForceFallbackToLegacy(t *testing.T) {
	before := BothSuppressedTotal()
	t.Setenv("QSDM_METRICS_EMIT_LEGACY", "0")
	t.Setenv("QSDM_METRICS_EMIT_QSDM", "0")
	mode := ReadMetricPrefixMode()
	if !mode.emitLegacy {
		t.Fatal("both-off must force legacy back on to keep scrape endpoint responsive")
	}
	if mode.emitNew {
		t.Fatal("both-off must not also re-enable qsdm_*; legacy-only is the fallback")
	}
	if BothSuppressedTotal() <= before {
		t.Errorf("BothSuppressedTotal must have advanced; before=%d after=%d", before, BothSuppressedTotal())
	}
}

func TestDualEmit_LegacyHelpTextAnnotated(t *testing.T) {
	t.Setenv("QSDM_METRICS_EMIT_LEGACY", "1")
	t.Setenv("QSDM_METRICS_EMIT_QSDM", "1")
	mode := ReadMetricPrefixMode()
	out := DualEmit([]Metric{
		{Name: "qsdmplus_demo_total", Help: "Number of demos.", Type: MetricCounter, Value: 1},
	}, mode)
	var legacyHelp, newHelp string
	for _, m := range out {
		if m.Name == "qsdmplus_demo_total" {
			legacyHelp = m.Help
		}
		if m.Name == "qsdm_demo_total" {
			newHelp = m.Help
		}
	}
	if !strings.Contains(legacyHelp, "DEPRECATED") {
		t.Errorf("legacy help must be annotated with DEPRECATED, got %q", legacyHelp)
	}
	if !strings.Contains(legacyHelp, "QSDM_METRICS_EMIT_LEGACY=0") {
		t.Errorf("legacy help must mention cutover knob, got %q", legacyHelp)
	}
	if strings.Contains(newHelp, "DEPRECATED") {
		t.Errorf("new help must NOT be annotated with DEPRECATED, got %q", newHelp)
	}
}

func TestDualEmit_NewPrefixInputAddsLegacyAlias(t *testing.T) {
	t.Setenv("QSDM_METRICS_EMIT_LEGACY", "1")
	t.Setenv("QSDM_METRICS_EMIT_QSDM", "1")
	mode := ReadMetricPrefixMode()
	out := DualEmit([]Metric{
		{Name: "qsdm_chain_height", Help: "Current chain height", Type: MetricGauge, Value: 42},
	}, mode)
	names := metricNames(out)
	if !contains(names, "qsdm_chain_height") {
		t.Errorf("must preserve qsdm_* input, got %v", names)
	}
	if !contains(names, "qsdmplus_chain_height") {
		t.Errorf("qsdm_* input must also get a qsdmplus_* legacy alias during the window, got %v", names)
	}
}

func TestDualEmit_UnprefixedMetricPassesThrough(t *testing.T) {
	t.Setenv("QSDM_METRICS_EMIT_LEGACY", "1")
	t.Setenv("QSDM_METRICS_EMIT_QSDM", "1")
	mode := ReadMetricPrefixMode()
	out := DualEmit([]Metric{
		{Name: "go_goroutines", Help: "goroutines", Type: MetricGauge, Value: 7},
	}, mode)
	names := metricNames(out)
	if len(names) != 1 || names[0] != "go_goroutines" {
		t.Errorf("unprefixed metric must pass through unchanged, got %v", names)
	}
}

func TestPrefixModeSelfObservability_AlwaysEmitsFiveGauges(t *testing.T) {
	mode := metricPrefixMode{emitLegacy: true, emitNew: false}
	obs := prefixModeSelfObservability(mode)
	names := metricNames(obs)
	want := []string{
		"qsdm_metrics_legacy_emission_enabled",
		"qsdm_metrics_qsdm_emission_enabled",
		"qsdmplus_metrics_legacy_emission_enabled",
		"qsdmplus_metrics_qsdm_emission_enabled",
		"qsdm_metrics_emit_both_suppressed_total",
	}
	for _, w := range want {
		if !contains(names, w) {
			t.Errorf("self-observability missing %q, got %v", w, names)
		}
	}
}

func TestRenderExposesDualEmitForCoreSeries(t *testing.T) {
	// Smoke test the full Render() path: the legacy names the
	// existing TestPrometheusExposition_containsNvidiaSeries covers
	// must still be present, AND their qsdm_* twins must now appear.
	t.Setenv("QSDM_METRICS_EMIT_LEGACY", "1")
	t.Setenv("QSDM_METRICS_EMIT_QSDM", "1")
	s := PrometheusExposition()
	for _, sub := range []string{
		"qsdmplus_ngc_challenge_issued_total",
		"qsdm_ngc_challenge_issued_total",
		"qsdmplus_transactions_processed_total",
		"qsdm_transactions_processed_total",
		"qsdm_metrics_legacy_emission_enabled",
	} {
		if !strings.Contains(s, sub) {
			t.Errorf("exposition missing %q", sub)
		}
	}
}

func TestEnvBoolDefault_Table(t *testing.T) {
	t.Setenv("X_Y_Z_BOOL", "")
	if !envBoolDefault("X_Y_Z_BOOL", true) {
		t.Error("empty must resolve to default (true here)")
	}
	if envBoolDefault("X_Y_Z_BOOL", false) {
		t.Error("empty must resolve to default (false here)")
	}
	for _, v := range []string{"1", "true", "TRUE", "yes", " on "} {
		t.Setenv("X_Y_Z_BOOL", v)
		if !envBoolDefault("X_Y_Z_BOOL", false) {
			t.Errorf("%q must parse truthy", v)
		}
	}
	for _, v := range []string{"0", "false", "no", "off"} {
		t.Setenv("X_Y_Z_BOOL", v)
		if envBoolDefault("X_Y_Z_BOOL", true) {
			t.Errorf("%q must parse falsy", v)
		}
	}
	t.Setenv("X_Y_Z_BOOL", "garbage")
	if envBoolDefault("X_Y_Z_BOOL", true) != true {
		t.Error("unparseable must fall back to default")
	}
}

// helpers.

func metricNames(ms []Metric) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.Name)
	}
	return out
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
