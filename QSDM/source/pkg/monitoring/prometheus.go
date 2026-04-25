package monitoring

import (
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/envcompat"
)

// MetricType differentiates Prometheus metric types.
type MetricType string

const (
	MetricGauge   MetricType = "gauge"
	MetricCounter MetricType = "counter"
)

// Metric holds a single named metric value.
type Metric struct {
	Name   string
	Help   string
	Type   MetricType
	Value  float64
	Labels map[string]string
}

// MetricCollector provides current metric values from a subsystem.
type MetricCollector func() []Metric

// PrometheusExporter exposes metrics in Prometheus text format.
type PrometheusExporter struct {
	mu          sync.RWMutex
	collectors  map[string]MetricCollector
	gauges      map[string]*gaugeValue
	metricCanon map[string]MetricType // bare metric name -> first registered type (strict mode)
}

type gaugeValue struct {
	name   string
	help   string
	value  float64
	labels map[string]string
}

// NewPrometheusExporter creates an exporter.
func NewPrometheusExporter() *PrometheusExporter {
	return &PrometheusExporter{
		collectors:  make(map[string]MetricCollector),
		gauges:      make(map[string]*gaugeValue),
		metricCanon: make(map[string]MetricType),
	}
}

func metricsRegisterStrict() bool {
	return envcompat.Truthy("QSDM_METRICS_REGISTER_STRICT", "QSDM_METRICS_REGISTER_STRICT")
}

func (pe *PrometheusExporter) noteCanonMetric(name string, t MetricType) {
	if !metricsRegisterStrict() || name == "" {
		return
	}
	if pe.metricCanon == nil {
		pe.metricCanon = make(map[string]MetricType)
	}
	if prev, ok := pe.metricCanon[name]; ok && prev != t {
		panic(fmt.Sprintf("prometheus: metric %q already registered as %s, conflicting %s", name, prev, t))
	}
	pe.metricCanon[name] = t
}

func (pe *PrometheusExporter) noteCanonFromSample(ms []Metric) {
	for _, m := range ms {
		pe.noteCanonMetric(m.Name, m.Type)
	}
}

// RegisterCollector adds a subsystem metric collector.
func (pe *PrometheusExporter) RegisterCollector(name string, collector MetricCollector) {
	pe.mu.Lock()
	defer pe.mu.Unlock()
	if collector != nil {
		pe.noteCanonFromSample(collector())
	}
	pe.collectors[name] = collector
}

// SetGauge sets a manual gauge value.
func (pe *PrometheusExporter) SetGauge(name, help string, value float64, labels map[string]string) {
	pe.mu.Lock()
	defer pe.mu.Unlock()
	pe.noteCanonMetric(name, MetricGauge)
	pe.gauges[name] = &gaugeValue{name: name, help: help, value: value, labels: labels}
}

// IncrCounter increments a manual gauge (used as a counter proxy).
func (pe *PrometheusExporter) IncrCounter(name, help string, delta float64) {
	pe.mu.Lock()
	defer pe.mu.Unlock()
	pe.noteCanonMetric(name, MetricGauge)
	g, ok := pe.gauges[name]
	if !ok {
		g = &gaugeValue{name: name, help: help}
		pe.gauges[name] = g
	}
	g.value += delta
}

// Collect gathers all metrics from registered collectors plus manual gauges.
func (pe *PrometheusExporter) Collect() []Metric {
	pe.mu.RLock()
	defer pe.mu.RUnlock()

	var all []Metric

	// Manual gauges
	for _, g := range pe.gauges {
		all = append(all, Metric{
			Name:   g.name,
			Help:   g.help,
			Type:   MetricGauge,
			Value:  g.value,
			Labels: g.labels,
		})
	}

	// Registered collectors
	for _, collector := range pe.collectors {
		all = append(all, collector()...)
	}

	sort.Slice(all, func(i, j int) bool {
		if all[i].Name != all[j].Name {
			return all[i].Name < all[j].Name
		}
		if all[i].Type != all[j].Type {
			return all[i].Type < all[j].Type
		}
		if all[i].Value != all[j].Value {
			return all[i].Value < all[j].Value
		}
		return labelKeyLess(all[i].Labels, all[j].Labels)
	})
	return all
}

func labelKeyLess(a, b map[string]string) bool {
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	ka, kb := sortedLabelKeys(a), sortedLabelKeys(b)
	for i := 0; i < len(ka) && i < len(kb); i++ {
		if ka[i] != kb[i] {
			return ka[i] < kb[i]
		}
		if a[ka[i]] != b[kb[i]] {
			return a[ka[i]] < b[kb[i]]
		}
	}
	return false
}

func sortedLabelKeys(m map[string]string) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Render outputs all metrics in Prometheus text exposition format.
//
// All metrics are emitted under the canonical qsdm_* prefix.
func (pe *PrometheusExporter) Render() string {
	metrics := pe.Collect()
	sort.Slice(metrics, func(i, j int) bool {
		return metrics[i].Name < metrics[j].Name
	})
	typeEmitted := make(map[string]bool)
	canonicalType := make(map[string]MetricType)
	var out string

	for _, m := range metrics {
		if prev, ok := canonicalType[m.Name]; ok && prev != m.Type {
			// First registered type wins; skip conflicting duplicate metric names (invalid exposition).
			continue
		}
		if _, ok := canonicalType[m.Name]; !ok {
			canonicalType[m.Name] = m.Type
		}
		if !typeEmitted[m.Name] {
			if m.Help != "" {
				out += fmt.Sprintf("# HELP %s %s\n", m.Name, m.Help)
			}
			out += fmt.Sprintf("# TYPE %s %s\n", m.Name, canonicalType[m.Name])
			typeEmitted[m.Name] = true
		}
		out += formatMetricLine(m)
	}
	return out
}

// Handler returns an http.Handler for the /metrics endpoint.
func (pe *PrometheusExporter) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		w.Write([]byte(pe.Render()))
	})
}

func formatMetricLine(m Metric) string {
	if len(m.Labels) == 0 {
		return fmt.Sprintf("%s %g\n", m.Name, m.Value)
	}

	keys := make([]string, 0, len(m.Labels))
	for k := range m.Labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var labelParts []string
	for _, k := range keys {
		labelParts = append(labelParts, fmt.Sprintf(`%s="%s"`, k, m.Labels[k]))
	}
	labels := ""
	for i, p := range labelParts {
		if i > 0 {
			labels += ","
		}
		labels += p
	}
	return fmt.Sprintf("%s{%s} %g\n", m.Name, labels, m.Value)
}

// StandardCollectors returns common collectors for chain, mempool, etc.
// Use with RegisterCollector after creating the exporter.

// ChainCollector creates a collector for chain metrics.
func ChainCollector(heightFunc func() uint64, validatorCountFunc func() int) MetricCollector {
	return func() []Metric {
		return []Metric{
			{Name: "qsdm_chain_height", Help: "Current chain height", Type: MetricGauge, Value: float64(heightFunc())},
			{Name: "qsdm_validators_active", Help: "Number of active validators", Type: MetricGauge, Value: float64(validatorCountFunc())},
		}
	}
}

// MempoolCollector creates a collector for mempool metrics.
func MempoolCollector(sizeFunc func() int, statsFunc func() map[string]interface{}) MetricCollector {
	return func() []Metric {
		stats := statsFunc()
		topFee, _ := stats["top_fee"].(float64)
		return []Metric{
			{Name: "qsdm_mempool_size", Help: "Number of pending transactions", Type: MetricGauge, Value: float64(sizeFunc())},
			{Name: "qsdm_mempool_top_fee", Help: "Highest fee in mempool", Type: MetricGauge, Value: topFee},
		}
	}
}

// UptimeCollector creates a collector that reports uptime.
func UptimeCollector(startTime time.Time) MetricCollector {
	return func() []Metric {
		return []Metric{
			{Name: "qsdm_uptime_seconds", Help: "Node uptime in seconds", Type: MetricGauge, Value: time.Since(startTime).Seconds()},
		}
	}
}
