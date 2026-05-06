package monitoring

// build_capabilities.go exports the qsdm_binary_capabilities
// info-metric: a single time series with one stable label per
// build-tag-selected subsystem backend, value always 1.
//
// Why an info-metric (gauge=1 with descriptive labels) instead of
// per-subsystem booleans?
//
//   - Operators reading the metric directly (curl /api/metrics/
//     prometheus | grep qsdm_binary_capabilities) get the entire
//     binary identity in one line, no joining required:
//
//       qsdm_binary_capabilities{
//           dilithium="circl",
//           wasm="wazero",
//           mesh3d="cpu_fallback"
//       } 1
//
//   - Grafana panels can render "Binary Identity" as a single
//     stat with formatted label values; with per-subsystem gauges
//     you'd need a transform pipeline.
//
//   - A boot-time wrong-binary deploy (e.g., operator accidentally
//     re-deployed a pre-Stage-B binary that still has dilithium
//     stub) is visible *immediately* on first scrape — no need to
//     wait the QSDMStubActive alert's `for: 5m` window. Combined
//     with the deploy runbook's smoke check
//     (STAGE_B_DEPLOY_BLR1.md §"Smoke check"), this gives operators
//     sub-30-second wrong-binary detection.
//
//   - Cardinality is bounded: every label is drawn from a small
//     closed set determined by build tags (dilithium ∈ {liboqs,
//     circl}, wasm ∈ {wazero, browser_stub}, mesh3d ∈ {cuda,
//     cpu_fallback}). Total across all binaries ever built: 8
//     time series.
//
// Relationship to qsdm_stub_active:
//
//   stub_active is a *runtime* signal — flipped to 1 when a stub
//   is actually exercised (e.g., NewStubVerifier called, NewWASMSDK
//   constructed in stub mode). It has 5m alert latency by design.
//
//   binary_capabilities is a *boot-time* signal — pinned the
//   moment package init() runs, so wrong-binary deploys are
//   detectable on the first /metrics scrape.
//
// The two metrics are complementary: stub_active answers "is this
// stub firing right now?", capabilities answers "is this even the
// binary I expected to deploy?".

func buildCapabilitiesMetrics() []Metric {
	return []Metric{
		{
			Name: "qsdm_binary_capabilities",
			Help: "Static build-tag-determined backend identity of this binary; always 1. " +
				"Labels: dilithium in {liboqs, circl}, wasm in {wazero, browser_stub}, " +
				"mesh3d in {cuda, cpu_fallback}. Use to detect wrong-binary deploys " +
				"before the qsdm_stub_active 5m alert window elapses. " +
				"See QSDM/docs/docs/STAGE_B_DEPLOY_BLR1.md (Smoke check section).",
			Type:  MetricGauge,
			Value: 1,
			Labels: map[string]string{
				"dilithium": dilithiumBackend,
				"wasm":      wasmBackend,
				"mesh3d":    mesh3dBackend,
			},
		},
	}
}
