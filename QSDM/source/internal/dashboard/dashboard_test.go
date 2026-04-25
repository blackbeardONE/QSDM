package dashboard

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/api"
	"github.com/blackbeardONE/QSDM/pkg/monitoring"
)

func TestDashboard(t *testing.T) {
	metrics := monitoring.GetMetrics()
	healthChecker := monitoring.NewHealthChecker(metrics)
	healthChecker.RegisterComponent("test")

	dash := NewDashboard(metrics, healthChecker, "0", false, DashboardNvidiaLock{}, "", "", false, "", nil) // Use 0 for random port in test

	// Test metrics endpoint
	req := httptest.NewRequest("GET", "/api/metrics", nil)
	w := httptest.NewRecorder()
	dash.handleMetrics(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	// Test health endpoint
	req = httptest.NewRequest("GET", "/api/health", nil)
	w = httptest.NewRecorder()
	dash.handleHealth(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	// Test dashboard page
	req = httptest.NewRequest("GET", "/", nil)
	w = httptest.NewRecorder()
	dash.handleDashboard(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	// Verify HTML content
	body := w.Body.String()
	if len(body) == 0 {
		t.Error("Dashboard returned empty body")
	}
	if !contains(body, "QSDM Monitoring Dashboard") {
		t.Errorf("Dashboard HTML missing title; want %q in body", "QSDM Monitoring Dashboard")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 || 
		(len(s) > len(substr) && (s[:len(substr)] == substr || 
		s[len(s)-len(substr):] == substr || 
		indexOf(s, substr) >= 0)))
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

func TestStrictDashboardAuthWithoutManager(t *testing.T) {
	m := monitoring.GetMetrics()
	hc := monitoring.NewHealthChecker(m)
	d := &Dashboard{
		metrics:             m,
		healthChecker:       hc,
		port:                "0",
		authManager:         nil,
		rateLimiter:         api.NewRateLimiter(50, time.Minute),
		strictDashboardAuth: true,
	}

	req := httptest.NewRequest(http.MethodGet, "/api/metrics", nil)
	req.Header.Set("Accept", "application/json")
	rr := httptest.NewRecorder()
	d.requireAuth(d.handleMetrics)(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("strict auth without manager: want 503, got %d", rr.Code)
	}

	d2 := &Dashboard{
		metrics:               m,
		healthChecker:         hc,
		port:                  "0",
		authManager:           nil,
		rateLimiter:           api.NewRateLimiter(50, time.Minute),
		metricsScrapeSecret:   "prom-secret-xyz",
		strictDashboardAuth:   true,
	}
	req2 := httptest.NewRequest(http.MethodGet, "/api/metrics/prometheus", nil)
	req2.Header.Set("X-QSDM-Metrics-Scrape-Secret", "prom-secret-xyz")
	rr2 := httptest.NewRecorder()
	d2.requireMetricsScrapeOrAuth(d2.handleMetricsPrometheus)(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("prometheus with scrape secret under strict+no JWT: want 200, got %d", rr2.Code)
	}
}

