package dashboard

// attest_rejections.go: dashboard tile data endpoint for the
// pkg/mining/attest/recentrejects ring buffer + its truncation
// telemetry.
//
// One JSON envelope per request that combines:
//
//   - The most recent N rejection records (RecordView shape
//     copied verbatim from pkg/api so the JSON contract is
//     identical to GET /api/v1/attest/recent-rejections),
//   - The cumulative per-field truncation counters published
//     as qsdm_attest_rejection_field_runes_observed_total,
//     qsdm_attest_rejection_field_truncated_total, and
//     qsdm_attest_rejection_field_runes_max,
//   - The cumulative qsdm_attest_rejection_persist_errors_total
//     for on-disk persister failures.
//
// Why this lives in the dashboard package and not as a frontend
// poll over /api/metrics/prometheus or the websocket metrics
// push: the rejection RECORDS are not Prometheus series — they
// are structured rows in a bounded ring. The websocket pushes
// counter snapshots only. Operators investigating an
// attestation-rejection burst need the row data and the rate
// counters together; this endpoint is the cheapest way to
// deliver both atomically without chaining two requests in the
// browser.
//
// Auth: same posture as /api/ngc-proofs — d.requireAuth wraps
// the handler so only authenticated dashboard users see the
// detail (rejection records can include claimed-but-rejected
// gpu_name / cert_subject substrings that are operationally
// sensitive).

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"

	"github.com/blackbeardONE/QSDM/pkg/api"
	"github.com/blackbeardONE/QSDM/pkg/monitoring"
)

// dashboardAttestRejectionsView is the wire shape for
// GET /api/attest/rejections (dashboard endpoint).
//
// JSON tag names below are the dashboard tile's contract;
// renaming any of them is a breaking change for the static/*
// frontend.
type dashboardAttestRejectionsView struct {
	// Available is false when the v2 recent-rejections store
	// has not been wired (v1-only deployment, or
	// internal/v2wiring opted out of the persister). When
	// false, Records is always empty but Metrics still
	// surfaces zero-valued counters so the tile can render
	// "0 / 0 / 0" instead of "—".
	Available bool `json:"available"`

	// Records is the most recent page from the ring buffer
	// (newest first if the underlying lister returns them
	// that way; pkg/api's lister returns ascending Seq, so
	// the dashboard reverses for tile-friendly presentation).
	// Empty slice (not nil) when the buffer is empty so the
	// JSON renders []` rather than null.
	Records []api.RecentRejectionView `json:"records"`

	// TotalMatches is the lister's total-records count
	// (always equal to len(Records) when no filters are
	// applied; preserved here so the tile can show "showing
	// last N of M observed").
	TotalMatches uint64 `json:"total_matches"`

	// Limit is the effective server-side page size after
	// clamping. Clients can confirm the server didn't
	// silently cap a too-large request.
	Limit int `json:"limit"`

	// Metrics is the per-field truncation telemetry plus the
	// persist-error counter. See
	// monitoring.RecentRejectMetricsView for field semantics.
	Metrics monitoring.RecentRejectMetricsView `json:"metrics"`
}

const (
	// dashboardAttestRejectionsDefaultLimit is the page size
	// used when the request omits ?limit=. Tuned for a
	// dashboard tile (small) rather than for forensic export
	// (where /api/v1/attest/recent-rejections with a large
	// limit is the right tool).
	dashboardAttestRejectionsDefaultLimit = 50

	// dashboardAttestRejectionsMaxLimit caps server-side page
	// size for the dashboard endpoint. Smaller than
	// pkg/api.MaxRecentRejectionListLimit (500) because this
	// endpoint is for tile rendering, not bulk export.
	dashboardAttestRejectionsMaxLimit = 200
)

// handleAttestRejections serves GET /api/attest/rejections.
//
// Query parameters:
//
//	limit : optional. Defaults to 50. Clamped to
//	        [1, dashboardAttestRejectionsMaxLimit].
//
// 200 OK with dashboardAttestRejectionsView on success — even
// when the v2 store is not wired (Available=false in that
// case so the frontend can display "feature unavailable" but
// still render the metrics row).
// 400 on a malformed limit query parameter.
// 405 on non-GET.
//
// No 503: the dashboard renders gracefully when the store is
// missing (Available=false) rather than blanking the tile,
// because operators on v1-only deployments still want to see
// "metrics: all zeros, store: not wired".
func (d *Dashboard) handleAttestRejections(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	limit := dashboardAttestRejectionsDefaultLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			http.Error(w,
				"limit must be a positive integer",
				http.StatusBadRequest)
			return
		}
		if n > dashboardAttestRejectionsMaxLimit {
			n = dashboardAttestRejectionsMaxLimit
		}
		limit = n
	}

	view := dashboardAttestRejectionsView{
		Records: []api.RecentRejectionView{},
		Limit:   limit,
		Metrics: monitoring.RecentRejectMetricsSnapshot(),
	}

	if lister := api.CurrentRecentRejectionLister(); lister != nil {
		view.Available = true
		page := lister.List(api.RecentRejectionListOptions{
			Limit: limit,
		})
		// pkg/api's lister returns rows in ascending Seq;
		// reverse so the tile renders newest-first.
		records := page.Records
		if len(records) > 0 {
			view.Records = make([]api.RecentRejectionView, len(records))
			for i, rec := range records {
				view.Records[len(records)-1-i] = rec
			}
		}
		view.TotalMatches = page.TotalMatches
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if err := json.NewEncoder(w).Encode(view); err != nil {
		log.Printf("ERROR: Failed to encode attest rejections view: %v", err)
		http.Error(w, "failed to encode response", http.StatusInternalServerError)
	}
}
