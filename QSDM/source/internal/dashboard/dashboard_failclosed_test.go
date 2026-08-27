package dashboard

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/api"
	"github.com/blackbeardONE/QSDM/pkg/monitoring"
)

// A missing authenticator must never mean "serve it anyway".
//
// authManager is nil only when no AuthManager was shared in and
// api.NewAuthManager() returned an error -- an infrastructure failure.
// requireAuth used to log a warning and call the protected handler unless
// strictDashboardAuth was set, and that flag comes from cfg.DashboardStrictAuth
// which defaults false. So on a default deployment, a failure to build the
// authenticator silently downgraded every protected dashboard route to
// unauthenticated.
//
// TestStrictDashboardAuthWithoutManager already covered strict=true. This is
// the case that was open: strict=false, which is the shipped default.
func TestDashboardFailsClosedWhenAuthUnavailable(t *testing.T) {
	m := monitoring.GetMetrics()
	hc := monitoring.NewHealthChecker(m)

	newDash := func() *Dashboard {
		return &Dashboard{
			metrics:       m,
			healthChecker: hc,
			port:          "0",
			authManager:   nil,
			rateLimiter:   api.NewRateLimiter(50, time.Minute),
			// Deliberately NOT strict: this is the default posture.
			strictDashboardAuth: false,
		}
	}

	t.Run("protected route", func(t *testing.T) {
		served := false
		d := newDash()
		req := httptest.NewRequest(http.MethodGet, "/api/metrics", nil)
		req.Header.Set("Accept", "application/json")
		rr := httptest.NewRecorder()

		d.requireAuth(func(w http.ResponseWriter, r *http.Request) {
			served = true
			w.WriteHeader(http.StatusOK)
		})(rr, req)

		if served {
			t.Error("the protected handler ran without authentication")
		}
		if rr.Code != http.StatusServiceUnavailable {
			t.Errorf("want 503 when the authenticator is unavailable, got %d", rr.Code)
		}
	})

	// The Prometheus route falls back to normal auth when the scrape secret is
	// absent or wrong. That fallback must fail closed too.
	t.Run("prometheus route without a scrape secret", func(t *testing.T) {
		served := false
		d := newDash()
		req := httptest.NewRequest(http.MethodGet, "/api/metrics/prometheus", nil)
		rr := httptest.NewRecorder()

		d.requireMetricsScrapeOrAuth(func(w http.ResponseWriter, r *http.Request) {
			served = true
			w.WriteHeader(http.StatusOK)
		})(rr, req)

		if served {
			t.Error("the metrics handler ran without authentication")
		}
		if rr.Code != http.StatusServiceUnavailable {
			t.Errorf("want 503 when the authenticator is unavailable, got %d", rr.Code)
		}
	})

	t.Run("prometheus route with a wrong scrape secret", func(t *testing.T) {
		served := false
		d := newDash()
		d.metricsScrapeSecret = "correct-secret"
		req := httptest.NewRequest(http.MethodGet, "/api/metrics/prometheus", nil)
		req.Header.Set("X-QSDM-Metrics-Scrape-Secret", "wrong-secret")
		rr := httptest.NewRecorder()

		d.requireMetricsScrapeOrAuth(func(w http.ResponseWriter, r *http.Request) {
			served = true
			w.WriteHeader(http.StatusOK)
		})(rr, req)

		if served {
			t.Error("the metrics handler ran with a wrong scrape secret and no authenticator")
		}
		if rr.Code == http.StatusOK {
			t.Error("a wrong scrape secret must not yield 200")
		}
	})

	// Failing closed must not be achieved by refusing everything: a correct
	// scrape secret is an independent credential and must still work, or
	// operators lose Prometheus whenever JWT init fails.
	t.Run("a correct scrape secret still works", func(t *testing.T) {
		served := false
		d := newDash()
		d.metricsScrapeSecret = "correct-secret"
		req := httptest.NewRequest(http.MethodGet, "/api/metrics/prometheus", nil)
		req.Header.Set("X-QSDM-Metrics-Scrape-Secret", "correct-secret")
		rr := httptest.NewRecorder()

		d.requireMetricsScrapeOrAuth(func(w http.ResponseWriter, r *http.Request) {
			served = true
			w.WriteHeader(http.StatusOK)
		})(rr, req)

		if !served {
			t.Errorf("a valid scrape secret must still be honoured, got %d", rr.Code)
		}
	})
}
