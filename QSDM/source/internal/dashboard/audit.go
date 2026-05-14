package dashboard

// audit.go: dashboard tile data endpoints for the operator audit
// checklist (`pkg/audit`). Closes the operator-facing gap from
// session 75: the 27→58→passed flips landed in pkg/audit but had
// no live surface, so the score (31.8% as of 2026-05-13) was only
// visible through `cmd/auditreport` on a developer's laptop.
//
// Two endpoints, both bearer-gated through d.requireAuth:
//
//	GET /api/audit/summary  → tile-render envelope: bucket counts,
//	                          score, has_blocking, top-N blocking
//	                          findings preview, evidence-provenance
//	                          breakdown, generated_at.
//	GET /api/audit/items    → full items list with optional
//	                          ?category= / ?severity= / ?status=
//	                          filters validated against closed
//	                          enums. 400 on a typo'd filter (no
//	                          silent passthrough — operator typing
//	                          severity=hi must NOT see all items).
//
// The Dashboard owns a single audit.Checklist (constructed in
// NewDashboard, see field auditChecklist) so a future admin
// endpoint can call UpdateStatus and have the change reflected
// in the tile across requests without re-reading defaultItems().
// audit.Checklist is internally guarded by an RWMutex, so the
// per-instance pointer is concurrency-safe for the polling
// frontend.

import (
	"encoding/json"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/audit"
)

// dashboardAuditSummaryView is the wire shape for
// GET /api/audit/summary.
//
// JSON tag names below are the dashboard tile's contract;
// renaming any of them is a breaking change for the static/*
// frontend. Mirrors the pattern in attest_rejections.go.
type dashboardAuditSummaryView struct {
	// Summary is the bucket-count dictionary
	// {total, passed, pending, failed, waived} returned by
	// audit.Checklist.Summary(). Always populated, even when
	// every count is zero, so the tile can render "0 / 0"
	// instead of "—".
	Summary map[string]int `json:"summary"`

	// Score is the (passed + waived) / total ratio expressed
	// as a 0..100 float, matching audit.Checklist.Score().
	// 0 when total == 0 (defensive; defaultItems() is never
	// empty in production).
	Score float64 `json:"score"`

	// HasBlockingFindings mirrors audit.Checklist.HasBlockingFindings():
	// true if ANY critical/high item is still pending or
	// failed. Drives the tile's red/green pill — a non-zero
	// passed count is meaningful, but operators care most
	// about whether any P0/P1 item still gates production.
	HasBlockingFindings bool `json:"has_blocking_findings"`

	// BlockingCount is the total number of critical+high
	// items still pending or failed. Distinct from
	// len(BlockingPreview) which is capped at
	// dashboardAuditBlockingPreviewCap for tile-render bounds.
	BlockingCount int `json:"blocking_count"`

	// BlockingPreview is the first N critical/high pending
	// items in checklist order, capped at
	// dashboardAuditBlockingPreviewCap. Empty (not nil) slice
	// when nothing blocks so the JSON renders [] rather than
	// null. Each entry carries the minimum the tile needs to
	// render a one-line "still pending: crypto-01 / ML-DSA
	// key generation" row without a second request.
	BlockingPreview []dashboardAuditBlockingItem `json:"blocking_preview"`

	// EvidenceProvenance is a count breakdown of the passed
	// items by their ReviewedBy prefix — the closed-enum
	// {evidence:live-deploy, evidence:in-tree-tests,
	// evidence:in-tree} guarded by
	// TestChecklist_RuntimeVerifiedReviewerProvenance in
	// pkg/audit/checklist_extra_test.go. Lets the tile show
	// "11 verified live, 8 by tests, 8 in-tree" so the
	// passed count isn't a black box. Always populated; an
	// unknown ReviewedBy lands in the "other" bucket so a
	// silent-flip drift would show up rather than disappear.
	EvidenceProvenance map[string]int `json:"evidence_provenance"`

	// GeneratedAt is the wall-clock RFC3339 timestamp
	// recorded when the handler built this view. Lets the
	// frontend render "as of HH:MM:SS" without trusting
	// browser clock skew.
	GeneratedAt string `json:"generated_at"`
}

// dashboardAuditBlockingItem is the slim per-item shape used in
// BlockingPreview. Excludes Description / Notes because the tile
// renders one line per item; full item detail is reachable via
// /api/audit/items?status=pending&severity=critical instead.
type dashboardAuditBlockingItem struct {
	ID       string `json:"id"`
	Category string `json:"category"`
	Severity string `json:"severity"`
	Status   string `json:"status"`
	Title    string `json:"title"`
}

// dashboardAuditItemsView is the wire shape for
// GET /api/audit/items. Returns the full items list (subject to
// optional filters) plus an echoed filters block so the tile can
// confirm "the server understood my dropdown selection" — same
// posture as dashboardAttestRejectionsView.
type dashboardAuditItemsView struct {
	// Items is the filtered slice in checklist insertion
	// order. Empty (not nil) slice when no items match so
	// the JSON renders [] rather than null.
	Items []audit.ChecklistItem `json:"items"`

	// TotalMatches is the count of items after filtering.
	// Always equal to len(Items) — exposed as a separate
	// field so the tile can render "showing N matches"
	// without having to count the array itself.
	TotalMatches int `json:"total_matches"`

	// Filters echoes back the category / severity / status
	// filters the server actually applied. Pointer +
	// omitempty so a bare-call response (no filters set)
	// DOES NOT carry a "filters":{} block at all — same
	// rationale as dashboardAttestRejectionsView.Filters.
	Filters *dashboardAuditEchoedFilters `json:"filters,omitempty"`

	// GeneratedAt mirrors the Summary view's field for the
	// same render-time-stamping rationale.
	GeneratedAt string `json:"generated_at"`
}

// dashboardAuditEchoedFilters is the dashboard's slim version
// of the filter set this tile supports. Adding a new filter
// requires (a) extending this struct, (b) extending the
// allow-list validation in handleAuditItems, and (c) extending
// the test matrix in audit_test.go so a typo'd filter still
// gets a 400.
type dashboardAuditEchoedFilters struct {
	Category string `json:"category,omitempty"`
	Severity string `json:"severity,omitempty"`
	Status   string `json:"status,omitempty"`
}

// dashboardAuditBlockingPreviewCap caps the BlockingPreview
// slice at a tile-render-friendly N. Operators wanting the
// full list go through /api/audit/items?status=pending&
// severity=critical (or =high) which is uncapped.
const dashboardAuditBlockingPreviewCap = 5

// allowedAuditCategories / Severities / Statuses are the
// closed-enum allow-lists for the items endpoint's filter
// query parameters. Each is the string form of the
// corresponding pkg/audit constant; keeping them as a
// dashboard-side const avoids a runtime-typed lookup on every
// request. If pkg/audit ever adds a new category, both the
// allow-list AND the test matrix in audit_test.go must be
// extended in the same PR.
var (
	allowedAuditCategories = map[string]bool{
		string(audit.CatCryptography):   true,
		string(audit.CatAuthentication): true,
		string(audit.CatAuthorisation):  true,
		string(audit.CatNetwork):        true,
		string(audit.CatSmartContracts): true,
		string(audit.CatBridge):         true,
		string(audit.CatStorage):        true,
		string(audit.CatAPI):            true,
		string(audit.CatGovernance):     true,
		string(audit.CatInfra):          true,
		string(audit.CatSupplyChain):    true,
		string(audit.CatRuntime):        true,
		string(audit.CatSecretRotation): true,
		string(audit.CatRebrand):        true,
		string(audit.CatTokenomics):     true,
		string(audit.CatMiningAudit):    true,
		string(audit.CatTrustAPI):       true,
	}
	allowedAuditSeverities = map[string]bool{
		string(audit.SevCritical): true,
		string(audit.SevHigh):     true,
		string(audit.SevMedium):   true,
		string(audit.SevLow):      true,
		string(audit.SevInfo):     true,
	}
	allowedAuditStatuses = map[string]bool{
		string(audit.StatusPending): true,
		string(audit.StatusPassed):  true,
		string(audit.StatusFailed):  true,
		string(audit.StatusWaived):  true,
	}
)

// handleAuditSummary serves GET /api/audit/summary.
//
// 200 OK with dashboardAuditSummaryView on success.
// 405 on non-GET. No 503 path: the in-process checklist is
// deterministic and always available — Available=true is
// implicit.
//
// Auth: requireAuth wrapper at registration site (see
// dashboard.go buildHandler). Audit checklist text is already
// public (open-source repo), but the operator-facing review
// state including which items have been flipped, what evidence
// was cited, and what's still blocking is operationally
// sensitive enough to gate behind dashboard auth — same
// posture as /api/health and /api/topology.
func (d *Dashboard) handleAuditSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	cl := d.auditChecklist
	if cl == nil {
		// Defensive: NewDashboard always sets this. If a
		// caller constructed Dashboard{} literal-style
		// (some tests do), make the endpoint still answer
		// with an empty checklist rather than panicking.
		cl = audit.NewChecklist()
	}

	summary := cl.Summary()
	items := cl.Items()

	// Compute blocking findings + preview in a single pass.
	blockingPreview := make([]dashboardAuditBlockingItem, 0, dashboardAuditBlockingPreviewCap)
	blockingCount := 0
	for _, it := range items {
		if (it.Severity == audit.SevCritical || it.Severity == audit.SevHigh) &&
			(it.Status == audit.StatusPending || it.Status == audit.StatusFailed) {
			blockingCount++
			if len(blockingPreview) < dashboardAuditBlockingPreviewCap {
				blockingPreview = append(blockingPreview, dashboardAuditBlockingItem{
					ID:       it.ID,
					Category: string(it.Category),
					Severity: string(it.Severity),
					Status:   string(it.Status),
					Title:    it.Title,
				})
			}
		}
	}

	// Evidence-provenance breakdown: bucket the passed items
	// by their ReviewedBy prefix. Always emit the three
	// canonical buckets (even at zero) so the tile can render
	// a stable layout; an unknown ReviewedBy lands in
	// "other" so a future drift surfaces rather than
	// disappears silently.
	provenance := map[string]int{
		"evidence:live-deploy":   0,
		"evidence:in-tree-tests": 0,
		"evidence:in-tree":       0,
		"other":                  0,
	}
	for _, it := range items {
		if it.Status != audit.StatusPassed {
			continue
		}
		switch it.ReviewedBy {
		case "evidence:live-deploy", "evidence:in-tree-tests", "evidence:in-tree":
			provenance[it.ReviewedBy]++
		default:
			provenance["other"]++
		}
	}

	view := dashboardAuditSummaryView{
		Summary:             summary,
		Score:               cl.Score(),
		HasBlockingFindings: cl.HasBlockingFindings(),
		BlockingCount:       blockingCount,
		BlockingPreview:     blockingPreview,
		EvidenceProvenance:  provenance,
		GeneratedAt:         time.Now().UTC().Format(time.RFC3339),
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if err := json.NewEncoder(w).Encode(view); err != nil {
		log.Printf("ERROR: Failed to encode audit summary: %v", err)
		http.Error(w, "failed to encode response", http.StatusInternalServerError)
	}
}

// handleAuditItems serves GET /api/audit/items.
//
// Query parameters (all optional; closed-enum, 400 on typo):
//
//	category: cryptography | authentication | authorisation |
//	          network | smart_contracts | bridge | storage |
//	          api | governance | infrastructure | supply_chain |
//	          runtime | secret_rotation | rebrand | tokenomics |
//	          mining_audit | trust_api
//	severity: critical | high | medium | low | info
//	status:   pending | passed | failed | waived
//
// 200 OK with dashboardAuditItemsView on success.
// 400 on a typo'd filter parameter — operators triaging a
// regression must NOT see "all items" when they typed a typo;
// the closed-enum allow-lists above guarantee a 400 in that
// case.
// 405 on non-GET.
func (d *Dashboard) handleAuditItems(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	q := r.URL.Query()
	rawCategory := strings.TrimSpace(q.Get("category"))
	rawSeverity := strings.TrimSpace(q.Get("severity"))
	rawStatus := strings.TrimSpace(q.Get("status"))

	if rawCategory != "" && !allowedAuditCategories[rawCategory] {
		http.Error(w,
			"category must be one of the closed-enum audit categories",
			http.StatusBadRequest)
		return
	}
	if rawSeverity != "" && !allowedAuditSeverities[rawSeverity] {
		http.Error(w,
			"severity must be one of: critical, high, medium, low, info",
			http.StatusBadRequest)
		return
	}
	if rawStatus != "" && !allowedAuditStatuses[rawStatus] {
		http.Error(w,
			"status must be one of: pending, passed, failed, waived",
			http.StatusBadRequest)
		return
	}

	cl := d.auditChecklist
	if cl == nil {
		cl = audit.NewChecklist()
	}

	items := cl.Items()
	out := make([]audit.ChecklistItem, 0, len(items))
	for _, it := range items {
		if rawCategory != "" && string(it.Category) != rawCategory {
			continue
		}
		if rawSeverity != "" && string(it.Severity) != rawSeverity {
			continue
		}
		if rawStatus != "" && string(it.Status) != rawStatus {
			continue
		}
		out = append(out, it)
	}

	// Stable insertion order is already guaranteed by
	// cl.Items(); the explicit Sort below pins it for the
	// edge case where a future Checklist implementation
	// returns items in a less stable order. Cheap (~85 items
	// max), keeps the wire contract simple, and lets a
	// future expansion (e.g. severity-first ordering) be a
	// single-line change.
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	// Restore insertion order if and only if no severity
	// filter was applied (severity-grouped responses are
	// nicer alphabetised; full-list responses match the
	// canonical defaultItems() ordering, which we've just
	// stomped on with the alphabetic sort above). Iterate
	// the pre-sort original to rebuild the order.
	if rawSeverity == "" && rawStatus == "" {
		idIndex := make(map[string]int, len(items))
		for i, it := range items {
			idIndex[it.ID] = i
		}
		sort.SliceStable(out, func(i, j int) bool {
			return idIndex[out[i].ID] < idIndex[out[j].ID]
		})
	}

	view := dashboardAuditItemsView{
		Items:        out,
		TotalMatches: len(out),
		GeneratedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	if rawCategory != "" || rawSeverity != "" || rawStatus != "" {
		view.Filters = &dashboardAuditEchoedFilters{
			Category: rawCategory,
			Severity: rawSeverity,
			Status:   rawStatus,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if err := json.NewEncoder(w).Encode(view); err != nil {
		log.Printf("ERROR: Failed to encode audit items: %v", err)
		http.Error(w, "failed to encode response", http.StatusInternalServerError)
	}
}
