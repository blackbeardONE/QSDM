package dashboard

// slashing.go: dashboard tile data endpoint for the v2-mining
// slashing pipeline (chain.SlashReceiptStore + the qsdm_slash_*
// Prometheus counters).
//
// One JSON envelope per request that combines:
//
//   - The most recent N slash receipts (api.SlashReceiptView
//     shape, identical to the GET /api/v1/mining/slash/{tx_id}
//     wire contract — receipts can include error strings on
//     rejected outcomes, so the same dashboard auth that
//     protects /api/ngc-proofs applies here too).
//
//   - The full set of qsdm_slash_* counters (applied,
//     drained-dust, rejected, auto-revoked) plus the
//     reward/burn totals, captured in a single
//     monitoring.SlashMetricsView snapshot so a renderer
//     paints "applied/rejected/burned" cells together with
//     the receipt rows.
//
// Auth: same posture as /api/attest/rejections —
// d.requireAuth wraps the handler so only authenticated
// dashboard users see the detail (slash receipts can include
// claimed-but-rejected miner addresses + reject error
// strings that are operationally sensitive).
//
// Why this lives in the dashboard package and not as a
// frontend poll over /api/metrics/prometheus: the slash
// RECEIPTS are not Prometheus series — they are structured
// rows in a bounded chain-side store. Operators triaging a
// slash burst need the row data and the rate counters
// together; this endpoint is the cheapest way to deliver
// both atomically without chaining two requests in the
// browser.
//
// Companion runbook:
// QSDM/docs/docs/runbooks/SLASHING_INCIDENT.md.

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/blackbeardONE/QSDM/pkg/api"
	"github.com/blackbeardONE/QSDM/pkg/monitoring"
)

// dashboardSlashReceiptsView is the wire shape for
// GET /api/mining/slash-receipts (dashboard endpoint).
//
// JSON tag names below are the dashboard tile's contract;
// renaming any of them is a breaking change for the
// static/* frontend.
type dashboardSlashReceiptsView struct {
	// Available is false when the v2 slash-receipt store
	// has not been wired (v1-only deployment, or
	// internal/v2wiring opted out). When false, Records is
	// always empty but Metrics still surfaces zero-valued
	// counters so the tile can render "0 / 0 / 0" instead
	// of "—".
	Available bool `json:"available"`

	// Records is the most recent page from the receipt
	// store, NEWEST FIRST (the lister returns them that way
	// so the dashboard does not have to reverse). Empty
	// slice (not nil) when the store is empty so the JSON
	// renders []` rather than null.
	Records []api.SlashReceiptView `json:"records"`

	// TotalMatches is the lister's count of matched
	// receipts visible to this page (page count + 1 when
	// HasMore is true; not a global scan of the whole
	// store, see chain.SlashReceiptListPage docs).
	TotalMatches uint64 `json:"total_matches"`

	// Limit is the effective server-side page size after
	// clamping. Clients can confirm the server didn't
	// silently cap a too-large request.
	Limit int `json:"limit"`

	// Filters echoes back the outcome / evidence_kind /
	// since filters the server actually applied. Pointer +
	// omitempty so a bare-call response (no filters set)
	// DOES NOT carry a `"filters":{}` block at all —
	// matches the attest-rejections tile's idiom.
	Filters *dashboardSlashReceiptsEchoedFilters `json:"filters,omitempty"`

	// Metrics is the qsdm_slash_* counter snapshot. See
	// monitoring.SlashMetricsView for field semantics.
	Metrics monitoring.SlashMetricsView `json:"metrics"`
}

// dashboardSlashReceiptsEchoedFilters is the dashboard's
// own slim version of the filters block — only the three
// filters this tile supports. Keeping a separate struct
// (rather than reusing pkg/api's options struct) lets the
// dashboard add tile-specific filters later (e.g.
// miner_addr) without breaking pkg/api's wire contract.
type dashboardSlashReceiptsEchoedFilters struct {
	Outcome      string `json:"outcome,omitempty"`
	EvidenceKind string `json:"evidence_kind,omitempty"`
	Since        int64  `json:"since,omitempty"`
}

const (
	// dashboardSlashReceiptsDefaultLimit is the page size
	// used when the request omits ?limit=. Tuned for a
	// dashboard tile (small) rather than for forensic
	// export.
	dashboardSlashReceiptsDefaultLimit = 50

	// dashboardSlashReceiptsMaxLimit caps server-side page
	// size for the dashboard endpoint. Smaller than
	// chain.MaxSlashReceiptListLimit (500) because this
	// endpoint is for tile rendering, not bulk export.
	dashboardSlashReceiptsMaxLimit = 200
)

// handleSlashReceipts serves GET /api/mining/slash-receipts.
//
// Query parameters:
//
//	limit         : optional. Defaults to 50. Clamped to
//	                [1, dashboardSlashReceiptsMaxLimit].
//	outcome       : optional. "applied" or "rejected"
//	                (validated against api.IsKnownSlashOutcome).
//	                400 on a typo.
//	evidence_kind : optional. One of the closed-enum kinds
//	                (validated against
//	                api.IsKnownSlashEvidenceKind). 400 on a
//	                typo so the dashboard tile and the wire
//	                shape agree on the allowlist.
//	since         : optional non-negative unix-seconds
//	                timestamp; drops receipts strictly older.
//	                400 on a non-integer.
//
// 200 OK with dashboardSlashReceiptsView on success — even
// when the v2 store is not wired (Available=false in that
// case so the frontend can display "feature unavailable" but
// still render the metrics row).
// 400 on a malformed limit / outcome / evidence_kind / since
// query parameter.
// 405 on non-GET.
//
// No 503: same posture as handleAttestRejections — the
// dashboard renders gracefully when the store is missing
// (Available=false) rather than blanking the tile, because
// operators on v1-only deployments still want to see
// "metrics: all zeros, store: not wired".
func (d *Dashboard) handleSlashReceipts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	q := r.URL.Query()

	limit := dashboardSlashReceiptsDefaultLimit
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			http.Error(w,
				"limit must be a positive integer",
				http.StatusBadRequest)
			return
		}
		if n > dashboardSlashReceiptsMaxLimit {
			n = dashboardSlashReceiptsMaxLimit
		}
		limit = n
	}

	// Closed-enum validation against pkg/api's authoritative
	// allowlists. A typo'd filter must NOT silently degrade
	// to "no filter applied" — operators triaging an
	// incident would otherwise see all receipts when they
	// thought they were looking only at forged-attestation,
	// and miss the signal entirely.
	rawOutcome := strings.TrimSpace(q.Get("outcome"))
	if rawOutcome != "" && !api.IsKnownSlashOutcome(rawOutcome) {
		http.Error(w,
			"outcome must be one of: "+strings.Join(api.KnownSlashOutcomes(), ", "),
			http.StatusBadRequest)
		return
	}

	rawEvidenceKind := strings.TrimSpace(q.Get("evidence_kind"))
	if rawEvidenceKind != "" && !api.IsKnownSlashEvidenceKind(rawEvidenceKind) {
		http.Error(w,
			"evidence_kind must be one of: "+strings.Join(api.KnownSlashEvidenceKinds(), ", "),
			http.StatusBadRequest)
		return
	}

	var since int64
	if raw := q.Get("since"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 0 {
			http.Error(w,
				"since must be a non-negative unix-seconds timestamp",
				http.StatusBadRequest)
			return
		}
		since = n
	}

	view := dashboardSlashReceiptsView{
		Records: []api.SlashReceiptView{},
		Limit:   limit,
		Metrics: monitoring.SlashMetricsSnapshot(),
	}
	// Only attach the Filters block when at least one filter
	// is actually applied — keeps the bare-call response
	// wire-minimal (matches the attest-rejections tile's
	// "no filters → no filters key" behaviour).
	if rawOutcome != "" || rawEvidenceKind != "" || since != 0 {
		view.Filters = &dashboardSlashReceiptsEchoedFilters{
			Outcome:      rawOutcome,
			EvidenceKind: rawEvidenceKind,
			Since:        since,
		}
	}

	if lister := api.CurrentSlashReceiptLister(); lister != nil {
		view.Available = true
		page := lister.List(api.SlashReceiptListOptions{
			Limit:        limit,
			Outcome:      rawOutcome,
			EvidenceKind: rawEvidenceKind,
			SinceUnixSec: since,
		})
		// Lister returns NEWEST FIRST already, no reversal
		// needed (vs. the attest-rejections tile, where
		// pkg/api's lister is ascending-Seq and the
		// dashboard reverses).
		if len(page.Records) > 0 {
			view.Records = page.Records
		}
		view.TotalMatches = page.TotalMatches
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if err := json.NewEncoder(w).Encode(view); err != nil {
		log.Printf("ERROR: Failed to encode slash receipts view: %v", err)
		http.Error(w, "failed to encode response", http.StatusInternalServerError)
	}
}
