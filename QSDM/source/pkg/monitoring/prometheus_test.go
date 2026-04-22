package monitoring

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPrometheusExporter_SetGauge(t *testing.T) {
	pe := NewPrometheusExporter()
	pe.SetGauge("qsdm_test_gauge", "A test gauge", 42, nil)

	metrics := pe.Collect()
	if len(metrics) != 1 {
		t.Fatalf("expected 1 metric, got %d", len(metrics))
	}
	if metrics[0].Value != 42 {
		t.Fatalf("expected 42, got %f", metrics[0].Value)
	}
}

func TestPrometheusExporter_IncrCounter(t *testing.T) {
	pe := NewPrometheusExporter()
	pe.IncrCounter("qsdm_blocks_produced", "Blocks produced", 1)
	pe.IncrCounter("qsdm_blocks_produced", "", 1)
	pe.IncrCounter("qsdm_blocks_produced", "", 1)

	metrics := pe.Collect()
	if len(metrics) != 1 {
		t.Fatalf("expected 1 metric, got %d", len(metrics))
	}
	if metrics[0].Value != 3 {
		t.Fatalf("expected 3, got %f", metrics[0].Value)
	}
}

func TestPrometheusExporter_RegisterCollector(t *testing.T) {
	pe := NewPrometheusExporter()
	pe.RegisterCollector("test", func() []Metric {
		return []Metric{
			{Name: "test_a", Help: "A", Type: MetricGauge, Value: 10},
			{Name: "test_b", Help: "B", Type: MetricCounter, Value: 5},
		}
	})

	metrics := pe.Collect()
	if len(metrics) != 2 {
		t.Fatalf("expected 2 metrics, got %d", len(metrics))
	}
}

func TestPrometheusExporter_Render(t *testing.T) {
	pe := NewPrometheusExporter()
	pe.SetGauge("qsdm_height", "Chain height", 100, nil)
	pe.SetGauge("qsdm_peers", "Peer count", 5, map[string]string{"network": "mainnet"})

	output := pe.Render()

	if !strings.Contains(output, "# HELP qsdm_height Chain height") {
		t.Fatal("expected HELP line for height")
	}
	if !strings.Contains(output, "# TYPE qsdm_height gauge") {
		t.Fatal("expected TYPE line for height")
	}
	if !strings.Contains(output, "qsdm_height 100") {
		t.Fatal("expected height value")
	}
	if !strings.Contains(output, `network="mainnet"`) {
		t.Fatal("expected label in output")
	}
}

func TestPrometheus_RegisterStrictPanicsOnTypeConflict(t *testing.T) {
	t.Setenv("QSDMPLUS_METRICS_REGISTER_STRICT", "1")
	pe := NewPrometheusExporter()
	pe.RegisterCollector("a", func() []Metric {
		return []Metric{{Name: "strict_dup", Type: MetricGauge, Value: 1}}
	})
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on conflicting metric type")
		}
	}()
	pe.RegisterCollector("b", func() []Metric {
		return []Metric{{Name: "strict_dup", Type: MetricCounter, Value: 2}}
	})
}

func TestPrometheus_RenderSkipsConflictingMetricTypes(t *testing.T) {
	t.Setenv("QSDMPLUS_METRICS_REGISTER_STRICT", "0")
	pe := NewPrometheusExporter()
	pe.RegisterCollector("c1", func() []Metric {
		return []Metric{{Name: "dup_metric", Help: "as gauge", Type: MetricGauge, Value: 1}}
	})
	pe.RegisterCollector("c2", func() []Metric {
		return []Metric{{Name: "dup_metric", Help: "as counter", Type: MetricCounter, Value: 2}}
	})
	out := pe.Render()
	// Stable sort orders counter before gauge for same name; first type wins.
	if !strings.Contains(out, "# TYPE dup_metric counter") {
		t.Fatal("expected first collector's type to win")
	}
	if strings.Contains(out, "dup_metric 1") {
		t.Fatal("did not expect conflicting gauge sample to be emitted")
	}
	if !strings.Contains(out, "dup_metric 2") {
		t.Fatal("expected counter sample to remain")
	}
}

func TestPrometheusExporter_Labels(t *testing.T) {
	pe := NewPrometheusExporter()
	pe.SetGauge("qsdm_node", "Node info", 1, map[string]string{"version": "1.0", "role": "validator"})

	output := pe.Render()
	if !strings.Contains(output, `role="validator"`) {
		t.Fatal("expected role label")
	}
	if !strings.Contains(output, `version="1.0"`) {
		t.Fatal("expected version label")
	}
}

func TestPrometheusExporter_Handler(t *testing.T) {
	pe := NewPrometheusExporter()
	pe.SetGauge("qsdm_test", "Test", 99, nil)

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	pe.Handler().ServeHTTP(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if !strings.Contains(resp.Header.Get("Content-Type"), "text/plain") {
		t.Fatal("expected text/plain content type")
	}
	body := w.Body.String()
	if !strings.Contains(body, "qsdm_test 99") {
		t.Fatal("expected metric in response body")
	}
}

func TestChainCollector(t *testing.T) {
	collector := ChainCollector(func() uint64 { return 42 }, func() int { return 3 })
	metrics := collector()
	if len(metrics) != 2 {
		t.Fatalf("expected 2 metrics, got %d", len(metrics))
	}
	if metrics[0].Value != 42 {
		t.Fatalf("expected chain height 42, got %f", metrics[0].Value)
	}
}

func TestMempoolCollector(t *testing.T) {
	collector := MempoolCollector(func() int { return 150 }, func() map[string]interface{} {
		return map[string]interface{}{"top_fee": 2.5}
	})
	metrics := collector()
	if len(metrics) != 2 {
		t.Fatalf("expected 2 metrics, got %d", len(metrics))
	}
	if metrics[0].Value != 150 {
		t.Fatalf("expected 150, got %f", metrics[0].Value)
	}
}

func TestUptimeCollector(t *testing.T) {
	start := time.Now().Add(-10 * time.Second)
	collector := UptimeCollector(start)
	metrics := collector()
	if len(metrics) != 1 {
		t.Fatalf("expected 1 metric, got %d", len(metrics))
	}
	if metrics[0].Value < 9 {
		t.Fatalf("expected uptime >= 9s, got %f", metrics[0].Value)
	}
}

func TestPrometheusExporter_SortedOutput(t *testing.T) {
	pe := NewPrometheusExporter()
	pe.SetGauge("zzz_last", "", 1, nil)
	pe.SetGauge("aaa_first", "", 2, nil)

	metrics := pe.Collect()
	if metrics[0].Name != "aaa_first" {
		t.Fatal("expected sorted output")
	}
}
