package monitoring

import (
	"strings"
	"testing"
)

// TestBuildCapabilitiesMetrics_ShapeAndLabels verifies the
// qsdm_binary_capabilities info-metric has the contract operators
// and dashboards depend on:
//
//   - exactly one time series (info-metric pattern, value=1)
//   - all three subsystem labels present (dilithium, wasm, mesh3d)
//   - each label value is from the closed enum the runbook
//     references (so the wrong-binary-deploy triage in
//     STAGE_B_DEPLOY_BLR1.md doesn't drift out of sync with code)
func TestBuildCapabilitiesMetrics_ShapeAndLabels(t *testing.T) {
	got := buildCapabilitiesMetrics()
	if len(got) != 1 {
		t.Fatalf("buildCapabilitiesMetrics() returned %d metrics; want exactly 1 (info-metric pattern)", len(got))
	}
	m := got[0]
	if m.Name != "qsdm_binary_capabilities" {
		t.Errorf("name = %q; want %q", m.Name, "qsdm_binary_capabilities")
	}
	if m.Type != MetricGauge {
		t.Errorf("type = %v; want MetricGauge", m.Type)
	}
	if m.Value != 1 {
		t.Errorf("value = %v; want 1 (info-metric)", m.Value)
	}

	wantLabels := []string{"dilithium", "wasm", "mesh3d"}
	for _, k := range wantLabels {
		if _, ok := m.Labels[k]; !ok {
			t.Errorf("missing label %q (have %#v)", k, m.Labels)
		}
	}

	// Closed-set enum check: keep this in sync with the build-tag
	// matrix in build_capabilities_*.go and the operator runbook.
	enums := map[string]map[string]bool{
		"dilithium": {"liboqs": true, "circl": true},
		"wasm":      {"wazero": true, "browser_stub": true},
		"mesh3d":    {"cuda": true, "cpu_fallback": true},
	}
	for k, allowed := range enums {
		v := m.Labels[k]
		if !allowed[v] {
			t.Errorf("label %s=%q not in closed enum %v — runbook drift?", k, v, allowed)
		}
	}
}

// TestBuildCapabilitiesMetrics_PrometheusExposition asserts the
// collector is wired into the global scrape exporter, so
// `curl /api/metrics/prometheus | grep qsdm_binary_capabilities`
// works without additional registration. This is the call site
// the deploy runbook tells operators to use.
func TestBuildCapabilitiesMetrics_PrometheusExposition(t *testing.T) {
	exp := PrometheusExposition()
	if !strings.Contains(exp, "qsdm_binary_capabilities{") {
		t.Fatalf("PrometheusExposition() does not include qsdm_binary_capabilities — collector not registered?\nfull text:\n%s", exp)
	}
	// Sanity-check the single emitted line ends with " 1" (the
	// info-metric value). We don't pin the exact label order
	// because Go's map iteration is non-deterministic; we just
	// verify the value is the conventional "1".
	for _, line := range strings.Split(exp, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "qsdm_binary_capabilities{") {
			continue
		}
		if !strings.HasSuffix(line, " 1") {
			t.Errorf("qsdm_binary_capabilities line does not end with ' 1' (info-metric convention): %q", line)
		}
		return
	}
	t.Fatalf("no qsdm_binary_capabilities line found in exposition")
}

// TestBuildCapabilitiesMetrics_BackendConsistency cross-checks the
// emitted dilithium label against compile-time facts that should
// follow from the same build tag. We rely on `runtime.Compiler` /
// build constants here only as a soft sanity check; the real
// guarantee comes from the build-tag-conditional file selection.
//
// The check: in a !cgo build (which is the default `go test`
// environment on a Windows host without liboqs DLLs in PATH), the
// label MUST be "circl". In a cgo build, it MUST be "liboqs".
//
// We use a build-tag-conditional helper to determine the expected
// value at test time so the test passes under both build modes.
func TestBuildCapabilitiesMetrics_BackendConsistency(t *testing.T) {
	got := buildCapabilitiesMetrics()
	if len(got) != 1 {
		t.Fatalf("len = %d; want 1", len(got))
	}
	if got[0].Labels["dilithium"] != expectedDilithiumBackendForTest {
		t.Errorf("dilithium label = %q; want %q for this build mode",
			got[0].Labels["dilithium"], expectedDilithiumBackendForTest)
	}
	if got[0].Labels["wasm"] != expectedWasmBackendForTest {
		t.Errorf("wasm label = %q; want %q for this build mode",
			got[0].Labels["wasm"], expectedWasmBackendForTest)
	}
}
