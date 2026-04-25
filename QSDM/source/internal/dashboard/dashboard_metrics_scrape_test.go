package dashboard

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/branding"
	"github.com/blackbeardONE/QSDM/pkg/monitoring"
)

func TestRequireMetricsScrapeOrAuth_validHeader(t *testing.T) {
	m := monitoring.GetMetrics()
	hc := monitoring.NewHealthChecker(m)
	d := NewDashboard(m, hc, "0", false, DashboardNvidiaLock{}, "", "metrics-scrape-test-16", false, "", nil)
	h := d.requireMetricsScrapeOrAuth(d.handleMetricsPrometheus)
	req := httptest.NewRequest(http.MethodGet, "/api/metrics/prometheus", nil)
	req.Header.Set(branding.MetricsScrapeSecretHeader, "metrics-scrape-test-16")
	w := httptest.NewRecorder()
	h(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "qsdm_nvidia_lock_http_blocks_total") {
		t.Fatal("expected prometheus exposition")
	}
}

func TestRequireMetricsScrapeOrAuth_validBearer(t *testing.T) {
	m := monitoring.GetMetrics()
	hc := monitoring.NewHealthChecker(m)
	d := NewDashboard(m, hc, "0", false, DashboardNvidiaLock{}, "", "metrics-scrape-test-16", false, "", nil)
	h := d.requireMetricsScrapeOrAuth(d.handleMetricsPrometheus)
	req := httptest.NewRequest(http.MethodGet, "/api/metrics/prometheus", nil)
	req.Header.Set("Authorization", "Bearer metrics-scrape-test-16")
	w := httptest.NewRecorder()
	h(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestRequireMetricsScrapeOrAuth_wrongBearerRejected(t *testing.T) {
	m := monitoring.GetMetrics()
	hc := monitoring.NewHealthChecker(m)
	d := NewDashboard(m, hc, "0", false, DashboardNvidiaLock{}, "", "metrics-scrape-test-16", false, "", nil)
	h := d.requireMetricsScrapeOrAuth(d.handleMetricsPrometheus)
	req := httptest.NewRequest(http.MethodGet, "/api/metrics/prometheus", nil)
	req.Header.Set("Authorization", "Bearer not-the-secret-value")
	w := httptest.NewRecorder()
	h(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}
