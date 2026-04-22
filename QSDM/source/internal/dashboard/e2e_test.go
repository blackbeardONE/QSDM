package dashboard

import (
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/monitoring"
)

// TestDashboardE2E tests the dashboard with a real HTTP server
func TestDashboardE2E(t *testing.T) {
	// Setup
	metrics := monitoring.GetMetrics()
	healthChecker := monitoring.NewHealthChecker(metrics)
	
	// Register and set up components
	healthChecker.RegisterComponent("network")
	healthChecker.RegisterComponent("storage")
	healthChecker.RegisterComponent("test")
	healthChecker.UpdateComponentHealth("network", monitoring.HealthStatusHealthy, "OK")
	healthChecker.UpdateComponentHealth("storage", monitoring.HealthStatusHealthy, "OK")
	
	// Add some test metrics
	metrics.IncrementTransactionsProcessed()
	metrics.IncrementTransactionsValid()
	metrics.IncrementNetworkMessagesSent()
	metrics.IncrementNetworkMessagesRecv()
	metrics.IncrementProposalsCreated()
	metrics.IncrementVotesCast()

	// Create dashboard
	dash := NewDashboard(metrics, healthChecker, "0", false, DashboardNvidiaLock{}, "", "", false, "", nil)

	// Use httptest for reliable testing
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			dash.handleDashboard(w, r)
		case "/api/metrics":
			dash.handleMetrics(w, r)
		case "/api/health":
			dash.handleHealth(w, r)
		case "/static/dashboard.js":
			staticFS, _ := fs.Sub(staticFiles, "static")
			http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))).ServeHTTP(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	baseURL := ts.URL

	// Test 1: Dashboard HTML
	t.Run("Dashboard HTML", func(t *testing.T) {
		resp, err := http.Get(baseURL + "/")
		if err != nil {
			t.Fatalf("Failed to get dashboard: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("Expected status 200, got %d", resp.StatusCode)
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("Failed to read body: %v", err)
		}

		html := string(body)
		if !strings.Contains(html, "QSDM Monitoring Dashboard") {
			t.Errorf("HTML missing title; want %q", "QSDM Monitoring Dashboard")
		}
		if !strings.Contains(html, "dashboard.js") {
			t.Error("HTML missing JavaScript reference")
		}
		if !strings.Contains(html, "Transaction Metrics") {
			t.Error("HTML missing transaction metrics")
		}
	})

	// Test 2: Metrics API
	t.Run("Metrics API", func(t *testing.T) {
		resp, err := http.Get(baseURL + "/api/metrics")
		if err != nil {
			t.Fatalf("Failed to get metrics: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("Expected status 200, got %d", resp.StatusCode)
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("Failed to read body: %v", err)
		}

		json := string(body)
		if !strings.Contains(json, "transactions_processed") {
			t.Error("Metrics missing transactions_processed")
		}
		if !strings.Contains(json, "network_messages_sent") {
			t.Error("Metrics missing network_messages_sent")
		}
		if !strings.Contains(json, "uptime_seconds") {
			t.Error("Metrics missing uptime_seconds")
		}
	})

	// Test 3: Health API
	t.Run("Health API", func(t *testing.T) {
		resp, err := http.Get(baseURL + "/api/health")
		if err != nil {
			t.Fatalf("Failed to get health: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("Expected status 200, got %d", resp.StatusCode)
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("Failed to read body: %v", err)
		}

		json := string(body)
		if !strings.Contains(json, "overall_status") {
			t.Error("Health missing overall_status")
		}
		if !strings.Contains(json, "components") {
			t.Error("Health missing components")
		}
	})

	// Test 4: Static JavaScript
	t.Run("Static JavaScript", func(t *testing.T) {
		resp, err := http.Get(baseURL + "/static/dashboard.js")
		if err != nil {
			t.Fatalf("Failed to get JavaScript: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("Expected status 200, got %d", resp.StatusCode)
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("Failed to read body: %v", err)
		}

		js := string(body)
		if !strings.Contains(js, "updateMetrics") {
			t.Error("JavaScript missing updateMetrics function")
		}
		if !strings.Contains(js, "fetch") {
			t.Error("JavaScript missing fetch calls")
		}
		if !strings.Contains(js, "/api/metrics") {
			t.Error("JavaScript missing metrics API call")
		}
	})

}

