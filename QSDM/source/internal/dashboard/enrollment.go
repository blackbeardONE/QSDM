package dashboard

// enrollment.go: dashboard tile data endpoint for the v2-mining
// enrollment registry (pkg/mining/enrollment.InMemoryState +
// the qsdm_enrollment_* + qsdm_unenrollment_* Prometheus
// counters / gauges).
//
// One JSON envelope per request that combines:
//
//   - The first N enrollment records visible to the registry's
//     paginated lister (api.EnrollmentRecordView shape,
//     identical to the GET /api/v1/mining/enrollments wire
//     contract).
//
//   - The full set of qsdm_enrollment_* counters + gauges
//     (active count, bonded dust, pending unbond pressure,
//     applied/rejected counters, sweep total) captured in a
//     single monitoring.EnrollmentMetricsView snapshot so a
//     renderer paints "active / bonded / pending unbond"
//     cells together with the row data.
//
// Auth: same posture as /api/attest/rejections and
// /api/mining/slash-receipts — d.requireAuth wraps the
// handler so only authenticated dashboard users see the
// detail (enrollment records expose owner addresses + GPU
// UUIDs that are operationally sensitive).
//
// Why this lives in the dashboard package and not as a
// frontend poll over /api/v1/mining/enrollments +
// /api/metrics/prometheus: the operator triaging a
// "registry-empty" or "shrinking-fast" alert needs the row
// data and the rate counters together; this endpoint is the
// cheapest way to deliver both atomically without chaining
// two requests in the browser.
//
// Companion runbook:
// QSDM/docs/docs/runbooks/ENROLLMENT_INCIDENT.md.

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/blackbeardONE/QSDM/pkg/api"
	"github.com/blackbeardONE/QSDM/pkg/mining/enrollment"
	"github.com/blackbeardONE/QSDM/pkg/monitoring"
)

// dashboardEnrollmentOverviewView is the wire shape for
// GET /api/mining/enrollment-overview.
//
// JSON tag names below are the dashboard tile's contract;
// renaming any of them is a breaking change for the static/*
// frontend.
type dashboardEnrollmentOverviewView struct {
	// Available is false when the v2 registry / lister has
	// not been wired (v1-only deployment). When false,
	// Records is always empty but Metrics still surfaces
	// zero-valued gauges so the tile can render
	// "active=0, bonded=0" instead of "—".
	Available bool `json:"available"`

	// Records is the paginated page from the registry, in
	// lexicographic node_id order (matches /api/v1/mining/
	// enrollments). Empty slice (not nil) when the registry
	// is empty so the JSON renders []` rather than null.
	Records []api.EnrollmentRecordView `json:"records"`

	// TotalMatches is the lister's count of records
	// matching the phase filter (all records when phase is
	// empty). NextCursor / HasMore mirror the v1 list
	// endpoint's pagination contract — clients can drill
	// deeper into the registry by passing NextCursor back
	// as ?cursor=.
	TotalMatches uint64 `json:"total_matches"`
	NextCursor   string `json:"next_cursor,omitempty"`
	HasMore      bool   `json:"has_more"`

	// Limit is the effective server-side page size after
	// clamping. Clients can confirm the server didn't
	// silently cap a too-large request.
	Limit int `json:"limit"`

	// Filters echoes back the phase / cursor filters the
	// server actually applied. Pointer + omitempty so a
	// bare-call response (no filters set) DOES NOT carry a
	// `"filters":{}` block — matches the slashing /
	// attest-rejections tiles' wire-payload tightness.
	Filters *dashboardEnrollmentEchoedFilters `json:"filters,omitempty"`

	// Metrics is the qsdm_enrollment_* counter + gauge
	// snapshot. See monitoring.EnrollmentMetricsView for
	// field semantics.
	Metrics monitoring.EnrollmentMetricsView `json:"metrics"`
}

type dashboardEnrollmentEchoedFilters struct {
	Phase  string `json:"phase,omitempty"`
	Cursor string `json:"cursor,omitempty"`
}

const (
	// dashboardEnrollmentOverviewDefaultLimit is the page
	// size used when the request omits ?limit=. Tuned for
	// a dashboard tile (small) rather than for forensic
	// export.
	dashboardEnrollmentOverviewDefaultLimit = 50

	// dashboardEnrollmentOverviewMaxLimit caps server-side
	// page size for the dashboard endpoint. Smaller than
	// enrollment.MaxListLimit (500) because this endpoint
	// is for tile rendering, not bulk export — the v1 list
	// endpoint at /api/v1/mining/enrollments stays the
	// path for indexers that want larger pages.
	dashboardEnrollmentOverviewMaxLimit = 200
)

// handleEnrollmentOverview serves
// GET /api/mining/enrollment-overview.
//
// Query parameters:
//
//	limit  : optional. Defaults to 50. Clamped to
//	         [1, dashboardEnrollmentOverviewMaxLimit].
//	phase  : optional. One of "active", "pending_unbond",
//	         "revoked". Empty omits the filter. 400 on
//	         a typo so the dashboard tile and the v1 list
//	         handler agree on the closed enum.
//	cursor : optional. Lexicographic exclusive lower bound
//	         on node_id. Forwarded to the lister verbatim
//	         (length-clamped to enrollment.MaxNodeIDLen).
//
// 200 OK with dashboardEnrollmentOverviewView on success —
// even when the v2 registry is not wired (Available=false
// in that case so the frontend can display "feature
// unavailable" but still render the metrics row).
// 400 on a malformed limit / phase / oversized cursor.
// 405 on non-GET.
//
// No 503: the dashboard renders gracefully when the
// registry is missing (Available=false) rather than
// blanking the tile, because operators on v1-only
// deployments still want to see "metrics: all zeros,
// registry: not wired".
func (d *Dashboard) handleEnrollmentOverview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	q := r.URL.Query()

	limit := dashboardEnrollmentOverviewDefaultLimit
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			http.Error(w,
				"limit must be a positive integer",
				http.StatusBadRequest)
			return
		}
		if n > dashboardEnrollmentOverviewMaxLimit {
			n = dashboardEnrollmentOverviewMaxLimit
		}
		limit = n
	}

	// Closed-enum validation against the same allowlist
	// the v1 list endpoint enforces. Mirror the wording so
	// operators see the same error string regardless of
	// which endpoint they hit during triage.
	rawPhase := strings.TrimSpace(q.Get("phase"))
	var phase enrollment.ListPhase
	switch rawPhase {
	case "":
		phase = enrollment.PhaseAny
	case string(enrollment.PhaseActive):
		phase = enrollment.PhaseActive
	case string(enrollment.PhasePendingUnbond):
		phase = enrollment.PhasePendingUnbond
	case string(enrollment.PhaseRevoked):
		phase = enrollment.PhaseRevoked
	default:
		http.Error(w,
			"phase must be one of: active, pending_unbond, revoked",
			http.StatusBadRequest)
		return
	}

	rawCursor := q.Get("cursor")
	if len(rawCursor) > enrollment.MaxNodeIDLen {
		http.Error(w, "cursor too long", http.StatusBadRequest)
		return
	}

	view := dashboardEnrollmentOverviewView{
		Records: []api.EnrollmentRecordView{},
		Limit:   limit,
		Metrics: monitoring.EnrollmentMetricsSnapshot(),
	}
	// Only attach the Filters block when at least one
	// filter is actually applied — keeps the bare-call
	// response wire-minimal and consistent with the
	// sibling tiles' "no filters → no filters key"
	// behaviour.
	if rawPhase != "" || rawCursor != "" {
		view.Filters = &dashboardEnrollmentEchoedFilters{
			Phase:  rawPhase,
			Cursor: rawCursor,
		}
	}

	if lister := api.CurrentEnrollmentLister(); lister != nil {
		view.Available = true
		page := lister.List(enrollment.ListOptions{
			Cursor: rawCursor,
			Limit:  limit,
			Phase:  phase,
		})
		records := make([]api.EnrollmentRecordView, 0, len(page.Records))
		for i := range page.Records {
			records = append(records, api.EnrollmentViewFromRecord(&page.Records[i]))
		}
		if len(records) > 0 {
			view.Records = records
		}
		view.TotalMatches = page.TotalMatches
		view.NextCursor = page.NextCursor
		view.HasMore = page.HasMore
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if err := json.NewEncoder(w).Encode(view); err != nil {
		log.Printf("ERROR: Failed to encode enrollment overview view: %v", err)
		http.Error(w, "failed to encode response", http.StatusInternalServerError)
	}
}
