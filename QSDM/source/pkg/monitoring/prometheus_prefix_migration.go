package monitoring

// This file implements the Prometheus metric-prefix dual-emit cutover
// staged in docs/docs/REBRAND_NOTES.md §3.7.
//
// Background. Every metric exposed on /api/metrics/prometheus today
// carries the legacy `qsdmplus_` prefix. The Major Update rebrand
// renamed the project to QSDM, but renaming a Prometheus metric is a
// breaking change for every downstream Grafana dashboard and
// Alertmanager rule. The acceptable migration path is to emit each
// metric **under both prefixes simultaneously** for a well-announced
// window, then flip the default to "new only" after operators have
// updated their scrapers.
//
// Knobs (environment variables, not config file, to keep ops surface
// low for the migration):
//
//   - QSDM_METRICS_EMIT_LEGACY — default "1". When "1"/"true" the
//     legacy `qsdmplus_*` series are emitted. Set to "0" to suppress
//     legacy emission on a node whose scrapers have already been
//     cut over.
//   - QSDM_METRICS_EMIT_QSDM — default "1". When "1"/"true" the
//     new `qsdm_*` series are emitted. Setting this to "0" is
//     unusual but supported (e.g. a node running on an older scraper
//     fleet). At least one of the two MUST be enabled; if both are
//     disabled the emitter falls back to legacy-only and emits a
//     self-observability counter so the misconfiguration is visible.
//
// Both knobs accept the legacy alias spellings (`QSDMPLUS_*`) via the
// pkg/envcompat shim exactly like every other rebrand-affected env
// var, but operators should prefer the `QSDM_*` spelling.
//
// The emitter exposes its own state as two gauges (always emitted,
// under BOTH prefixes) so Grafana can tell which nodes still run
// legacy-on:
//
//   qsdm_metrics_legacy_emission_enabled{} 0|1
//   qsdm_metrics_qsdm_emission_enabled{}   0|1
//
// The emitter is deliberately stateless — it reads env on every call
// so an operator can flip a systemd env drop-in without restarting
// the node. This matches the existing hot-reload discipline of the
// rest of the monitoring package.

import (
	"os"
	"strings"
	"sync"
	"sync/atomic"
)

const (
	legacyPrefix = "qsdmplus_"
	newPrefix    = "qsdm_"
)

// metricPrefixModeCache avoids re-parsing env vars on every single
// metric within one call to PrometheusExposition. It is refreshed at
// the start of each exposition render; see
// renderWithPrefixMigration.
type metricPrefixMode struct {
	emitLegacy bool
	emitNew    bool
}

var (
	prefixModeMu       sync.Mutex
	prefixModeLastRead metricPrefixMode
	// metricsBothSuppressedTotal counts observation windows during
	// which *both* knobs were set to "0". We fall back to legacy in
	// that case so the scrape doesn't go silent, but we also want
	// Grafana to flag the misconfiguration loudly.
	metricsBothSuppressedTotal uint64
)

// ReadMetricPrefixMode returns the currently-configured emission
// mode, re-reading environment variables each call. Exported so the
// tests and external health checks can introspect it.
func ReadMetricPrefixMode() metricPrefixMode {
	emitLegacy := envBoolDefault("QSDM_METRICS_EMIT_LEGACY", true)
	emitNew := envBoolDefault("QSDM_METRICS_EMIT_QSDM", true)
	if !emitLegacy && !emitNew {
		// Both off is treated as "legacy only" with a loud counter.
		// Anything else would make the scrape endpoint go completely
		// silent and break operator alerting.
		atomic.AddUint64(&metricsBothSuppressedTotal, 1)
		emitLegacy = true
	}
	prefixModeMu.Lock()
	prefixModeLastRead = metricPrefixMode{emitLegacy: emitLegacy, emitNew: emitNew}
	prefixModeMu.Unlock()
	return prefixModeLastRead
}

// LastReadMetricPrefixMode returns the most-recently-read knob state
// without touching env. Used by the self-observability emitter so
// the gauges reflect the state that was used for *this* exposition.
func LastReadMetricPrefixMode() metricPrefixMode {
	prefixModeMu.Lock()
	defer prefixModeMu.Unlock()
	return prefixModeLastRead
}

// BothSuppressedTotal returns the number of exposition renders that
// occurred while both knobs were disabled (and the emitter therefore
// force-enabled legacy to keep the scrape surface alive).
func BothSuppressedTotal() uint64 {
	return atomic.LoadUint64(&metricsBothSuppressedTotal)
}

// DualEmit rewrites every metric produced by `inner` to optionally
// emit under both the legacy `qsdmplus_*` and new `qsdm_*` prefixes,
// according to the knobs read by ReadMetricPrefixMode. Metrics whose
// names do not start with either prefix are passed through unchanged
// — this keeps forward-compatible room for metrics that are intro-
// duced after the cutover and only ever carry the `qsdm_*` name.
//
// Help text on the legacy copy is annotated so `curl .../prometheus`
// shows the deprecation window in-band.
func DualEmit(inner []Metric, mode metricPrefixMode) []Metric {
	if len(inner) == 0 {
		return inner
	}
	out := make([]Metric, 0, len(inner)*2)
	for _, m := range inner {
		switch {
		case strings.HasPrefix(m.Name, legacyPrefix):
			base := strings.TrimPrefix(m.Name, legacyPrefix)
			if mode.emitLegacy {
				legacy := m
				legacy.Help = m.Help + " [DEPRECATED alias of qsdm_" + base + "; set QSDM_METRICS_EMIT_LEGACY=0 to suppress.]"
				out = append(out, legacy)
			}
			if mode.emitNew {
				next := m
				next.Name = newPrefix + base
				out = append(out, next)
			}
		case strings.HasPrefix(m.Name, newPrefix):
			// Emit as-is — also synthesize a legacy alias when the
			// legacy window is still open, so operators who scrape
			// only legacy names continue to see new metrics.
			if mode.emitNew {
				out = append(out, m)
			}
			if mode.emitLegacy {
				legacy := m
				legacy.Name = legacyPrefix + strings.TrimPrefix(m.Name, newPrefix)
				legacy.Help = m.Help + " [DEPRECATED alias of " + m.Name + "; set QSDM_METRICS_EMIT_LEGACY=0 to suppress.]"
				out = append(out, legacy)
			}
		default:
			out = append(out, m)
		}
	}
	return out
}

// prefixModeSelfObservability returns two gauges that report which
// prefixes the current exposition is emitting under. These gauges
// are emitted by the exporter itself, unconditionally, under BOTH
// the qsdm_ and qsdmplus_ prefixes so Grafana finds them regardless
// of which scraper is installed.
func prefixModeSelfObservability(mode metricPrefixMode) []Metric {
	gauge := func(v bool) float64 {
		if v {
			return 1
		}
		return 0
	}
	// Produced twice on purpose — once under each prefix — without
	// going through DualEmit so a misconfiguration that accidentally
	// suppressed DualEmit still leaves self-observability intact.
	return []Metric{
		{
			Name: newPrefix + "metrics_legacy_emission_enabled",
			Help: "Whether this node emits qsdmplus_* metrics for operator dashboards during the rebrand deprecation window. See REBRAND_NOTES.md §3.7.",
			Type: MetricGauge, Value: gauge(mode.emitLegacy),
		},
		{
			Name: newPrefix + "metrics_qsdm_emission_enabled",
			Help: "Whether this node emits qsdm_* metrics. See REBRAND_NOTES.md §3.7.",
			Type: MetricGauge, Value: gauge(mode.emitNew),
		},
		{
			Name: legacyPrefix + "metrics_legacy_emission_enabled",
			Help: "[DEPRECATED alias of qsdm_metrics_legacy_emission_enabled.] Whether this node emits qsdmplus_* metrics.",
			Type: MetricGauge, Value: gauge(mode.emitLegacy),
		},
		{
			Name: legacyPrefix + "metrics_qsdm_emission_enabled",
			Help: "[DEPRECATED alias of qsdm_metrics_qsdm_emission_enabled.] Whether this node emits qsdm_* metrics.",
			Type: MetricGauge, Value: gauge(mode.emitNew),
		},
		{
			Name: newPrefix + "metrics_emit_both_suppressed_total",
			Help: "Count of exposition renders where both emission knobs were disabled; legacy is force-re-enabled to keep the scrape endpoint responsive.",
			Type: MetricCounter, Value: float64(BothSuppressedTotal()),
		},
	}
}

// envBoolDefault reads a boolean environment variable, accepting
// "1"/"true"/"yes" and "0"/"false"/"no" (case-insensitive). Any
// other value, including empty, resolves to `def`. We do not import
// pkg/envcompat to avoid a monitoring→envcompat→monitoring cycle;
// envcompat normalises QSDMPLUS_* → QSDM_* at process start before
// this function is ever called.
func envBoolDefault(key string, def bool) bool {
	switch v := strings.ToLower(strings.TrimSpace(os.Getenv(key))); v {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return def
	}
}
