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

func TestDashboardIntegration(t *testing.T) {
	// Setup
	metrics := monitoring.GetMetrics()
	healthChecker := monitoring.NewHealthChecker(metrics)
	healthChecker.RegisterComponent("test")
	healthChecker.UpdateComponentHealth("test", monitoring.HealthStatusHealthy, "Test component")

	// Add some test data
	metrics.IncrementTransactionsProcessed()
	metrics.IncrementTransactionsValid()
	metrics.IncrementNetworkMessagesSent()
	metrics.IncrementNetworkMessagesRecv()

	dash := NewDashboard(metrics, healthChecker, "0", false, DashboardNvidiaLock{}, "", "", false, "", nil)

	// Test 1: Dashboard HTML page
	t.Run("Dashboard HTML", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()
		dash.handleDashboard(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", w.Code)
		}

		body := w.Body.String()
		if len(body) == 0 {
			t.Error("Dashboard returned empty body")
		}

		// Check for key HTML elements
		if !strings.Contains(body, "QSDM Monitoring Dashboard") {
			t.Errorf("Dashboard HTML missing title; want %q", "QSDM Monitoring Dashboard")
		}
		if !strings.Contains(body, "dashboard.js") {
			t.Error("Dashboard HTML missing JavaScript reference")
		}
		if !strings.Contains(body, "Transaction Metrics") {
			t.Error("Dashboard HTML missing transaction metrics section")
		}
		// Attestation-rejections tile container IDs that the new
		// updateAttestRejections() poller writes into. If any of
		// these go missing the polling loop will silently no-op,
		// so guard them at build time.
		for _, id := range []string{
			`id="attest-rejections-status"`,
			`id="attest-rejections-counters"`,
			`id="attest-rejections-table"`,
			`id="attest-rejections-tbody"`,
			// Triage controls (2026-04-30): dropdown filters,
			// pause toggle, top-miners strip, CSV export. Any
			// missing ID means the JS handlers wire to nothing.
			`id="attest-rejections-filter-kind"`,
			`id="attest-rejections-filter-window"`,
			`id="attest-rejections-pause"`,
			`id="attest-rejections-export"`,
			`id="attest-rejections-top-miners"`,
			`id="attest-rejections-top-miners-list"`,
		} {
			if !strings.Contains(body, id) {
				t.Errorf("Dashboard HTML missing attestation-rejections tile element %s", id)
			}
		}
	})

	// Test 2: Metrics API
	t.Run("Metrics API", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/metrics", nil)
		w := httptest.NewRecorder()
		dash.handleMetrics(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", w.Code)
		}

		body := w.Body.String()
		if !strings.Contains(body, "transactions_processed") {
			t.Error("Metrics API missing transactions_processed")
		}
		if !strings.Contains(body, "network_messages_sent") {
			t.Error("Metrics API missing network_messages_sent")
		}
	})

	// Test 3: Health API
	t.Run("Health API", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/health", nil)
		w := httptest.NewRecorder()
		dash.handleHealth(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", w.Code)
		}

		body := w.Body.String()
		if !strings.Contains(body, "overall_status") {
			t.Error("Health API missing overall_status")
		}
		if !strings.Contains(body, "components") {
			t.Error("Health API missing components")
		}
	})

	// Test 4: Static files
	t.Run("Static Files", func(t *testing.T) {
		// Create a test server
		mux := http.NewServeMux()
		staticFS, err := fs.Sub(staticFiles, "static")
		if err != nil {
			t.Fatalf("Failed to create static filesystem: %v", err)
		}
		mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))

		server := httptest.NewServer(mux)
		defer server.Close()

		// Test JavaScript file
		resp, err := http.Get(server.URL + "/static/dashboard.js")
		if err != nil {
			t.Fatalf("Failed to get JavaScript file: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("Expected status 200 for JavaScript, got %d", resp.StatusCode)
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("Failed to read JavaScript: %v", err)
		}

		if !strings.Contains(string(body), "updateMetrics") {
			t.Error("JavaScript file missing updateMetrics function")
		}
		if !strings.Contains(string(body), "fetch") {
			t.Error("JavaScript file missing fetch calls")
		}
		// Attestation-rejections poller must be present and wired
		// into both the initial-load and the recurring polling
		// loops. Ship-stop if a refactor unhooks either path.
		if !strings.Contains(string(body), "function updateAttestRejections") {
			t.Error("JavaScript file missing updateAttestRejections function")
		}
		if !strings.Contains(string(body), "/api/attest/rejections") {
			t.Error("JavaScript file missing /api/attest/rejections fetch target")
		}
		// The persistence-lifecycle counters (added 2026-04-30:
		// errors / compactions / records-on-disk) must all be
		// rendered by the tile. The buildPersistCell helper is
		// the only call site for these labels — its absence
		// means the dashboard rolled back to the
		// errors-only flavour and operators lose the compaction
		// signal.
		for _, label := range []string{
			`'persist errors'`,
			`'compactions'`,
			`'records on disk'`,
			`'hard-cap drops'`,
			`metrics.persist_compactions_total`,
			`metrics.persist_records_on_disk`,
			`metrics.persist_hardcap_drops_total`,
		} {
			if !strings.Contains(string(body), label) {
				t.Errorf("JavaScript missing persistence-lifecycle label/field %q", label)
			}
		}
		// Triage controls (2026-04-30): the JS state object,
		// the four event-wired functions, the CSV builder, and
		// the top-miners renderer must all be present. A
		// future refactor that drops the controls without
		// updating the HTML would sneak through pkg-level
		// builds; ship-stop on the bundle string here.
		for _, sym := range []string{
			"attestRejectionsState",
			"initAttestRejectionsControls",
			"buildAttestRejectionsCSV",
			"renderAttestRejectionsTopMiners",
			"updateAttestRejectionsExport",
			"csvEscape",
		} {
			if !strings.Contains(string(body), sym) {
				t.Errorf("JavaScript missing triage-control symbol %q", sym)
			}
		}
		// Pause-toggle gate: the polling loop must check
		// attestRejectionsState.paused before firing the
		// rejection fetch. A regression that drops this
		// guard breaks the operator's ability to read a row
		// without it scrolling out from under them.
		if !strings.Contains(string(body), "if (!attestRejectionsState.paused)") {
			t.Error("polling loop missing pause-aware gate around updateAttestRejections")
		}
	})

	// Test 5: 404 for invalid paths
	t.Run("404 Handling", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/invalid-path", nil)
		w := httptest.NewRecorder()
		dash.handleDashboard(w, req)

		if w.Code != http.StatusNotFound {
			t.Errorf("Expected status 404, got %d", w.Code)
		}
	})
}

func TestDashboardServerStart(t *testing.T) {
	metrics := monitoring.GetMetrics()
	healthChecker := monitoring.NewHealthChecker(metrics)

	dash := NewDashboard(metrics, healthChecker, "0", false, DashboardNvidiaLock{}, "", "", false, "", nil)

	// Start server in background
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			dash.handleDashboard(w, r)
		} else if r.URL.Path == "/api/metrics" {
			dash.handleMetrics(w, r)
		} else if r.URL.Path == "/api/health" {
			dash.handleHealth(w, r)
		} else {
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	// Test all endpoints work
	endpoints := []string{"/", "/api/metrics", "/api/health"}
	for _, endpoint := range endpoints {
		resp, err := http.Get(server.URL + endpoint)
		if err != nil {
			t.Errorf("Failed to get %s: %v", endpoint, err)
			continue
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Endpoint %s returned status %d, expected 200", endpoint, resp.StatusCode)
		}
	}
}

