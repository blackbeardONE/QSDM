package api

// handlers_audit.go: public-API mirror of the operator audit
// checklist exposed by the dashboard server (see
// internal/dashboard/audit.go). Closes the transparency-public
// gap left by Session 76: the dashboard endpoints
// /api/audit/{summary,items} are bearer-gated, so SDK consumers,
// the public landing page widget, and third-party audit
// aggregators couldn't read the score without an operator
// session. This file lifts the same data onto the public API
// server's /api/v1/* surface, matching the
// /api/v1/trust/attestations/* precedent for transparency
// endpoints.
//
// Two endpoints, both registered in publicPaths (see
// middleware.go) and rate-limited by the existing per-IP
// limiter in security.go:
//
//   GET /api/v1/audit/summary  — bucket counts (total/passed/
//                                 pending/failed/waived),
//                                 score (0..100), has_blocking,
//                                 blocking_count, top-N
//                                 blocking_preview, 4-bucket
//                                 evidence_provenance map,
//                                 generated_at.
//   GET /api/v1/audit/items    — filterable items list with
//                                 closed-enum ?category= /
//                                 ?severity= / ?status= filters
//                                 (400 on a typo'd value — no
//                                 silent passthrough).
//
// Wire shape is byte-for-byte identical to the dashboard's
// dashboardAuditSummaryView / dashboardAuditItemsView so any
// consumer can drop-replace https://dashboard.qsdm.tech/api/audit/...
// with https://api.qsdm.tech/api/v1/audit/... and get the same
// JSON. The two surfaces are NOT cross-imported (it would
// invert the dependency arrow — internal/dashboard imports
// pkg/api today); the structs are duplicated, and a drift
// guard in handlers_audit_test.go pins the field set so a
// future divergence fails CI.
//
// Process-singleton checklist: a sync.Once-guarded
// audit.Checklist lives at the package level so a future
// admin endpoint that mutates checklist state via
// UpdateStatus has a single source of truth visible to both
// /api/v1/audit/summary and /api/v1/audit/items. The
// underlying Checklist is RWMutex-guarded internally.

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/audit"
)

// AuditSummary is the payload of GET /api/v1/audit/summary.
//
// Field tags below are the public API contract; renaming any
// of them is a breaking change for SDK consumers, the landing
// page widget, and the dashboard tile (which speaks the same
// shape via the bearer-gated /api/audit/summary endpoint).
type AuditSummary struct {
	// Summary is the bucket-count dictionary
	// {total, passed, pending, failed, waived} returned by
	// audit.Checklist.Summary(). Always populated, even when
	// every count is zero.
	Summary map[string]int `json:"summary"`

	// Score is (passed + waived) / total expressed as a
	// 0..100 float, matching audit.Checklist.Score().
	Score float64 `json:"score"`

	// HasBlockingFindings mirrors audit.Checklist.HasBlockingFindings():
	// true if ANY critical/high item is still pending or
	// failed. The single-bit "is the audit gated?" answer
	// for clients that don't want to count buckets.
	HasBlockingFindings bool `json:"has_blocking_findings"`

	// BlockingCount is the total number of critical+high
	// items still pending or failed. Distinct from
	// len(BlockingPreview) which is capped at
	// auditBlockingPreviewCap for response-size bounds.
	BlockingCount int `json:"blocking_count"`

	// BlockingPreview is the first N critical/high pending
	// items in checklist order, capped at
	// auditBlockingPreviewCap. Empty (not nil) slice when
	// nothing blocks so the JSON renders [] rather than
	// null.
	BlockingPreview []AuditBlockingItem `json:"blocking_preview"`

	// EvidenceProvenance is a count breakdown of the passed
	// items by their ReviewedBy prefix. Always emits the
	// three canonical evidence:* buckets (even at zero) so
	// a client renders a stable layout; an unknown
	// ReviewedBy lands in "other" so a future drift would
	// surface rather than disappear silently.
	EvidenceProvenance map[string]int `json:"evidence_provenance"`

	// GeneratedAt is the wall-clock RFC3339 timestamp
	// recorded when the handler built the view. Lets
	// clients render "as of HH:MM:SS" without trusting
	// browser clock skew or HTTP caching layers.
	GeneratedAt string `json:"generated_at"`
}

// AuditBlockingItem is one entry of AuditSummary.BlockingPreview.
// Slim shape — the full ChecklistItem (with Description, Notes,
// ReviewedBy, ReviewedAt) is reachable via /api/v1/audit/items.
type AuditBlockingItem struct {
	ID       string `json:"id"`
	Category string `json:"category"`
	Severity string `json:"severity"`
	Status   string `json:"status"`
	Title    string `json:"title"`
}

// AuditItemsResponse is the payload of GET /api/v1/audit/items.
// Mirrors dashboardAuditItemsView (internal/dashboard/audit.go).
type AuditItemsResponse struct {
	Items        []audit.ChecklistItem `json:"items"`
	TotalMatches int                   `json:"total_matches"`
	Filters      *AuditEchoedFilters   `json:"filters,omitempty"`
	GeneratedAt  string                `json:"generated_at"`
}

// AuditEchoedFilters echoes back the filters the server
// actually applied. Pointer + omitempty so a bare-call response
// (no filters set) DOES NOT carry a "filters":{} block.
type AuditEchoedFilters struct {
	Category string `json:"category,omitempty"`
	Severity string `json:"severity,omitempty"`
	Status   string `json:"status,omitempty"`
}

// auditBlockingPreviewCap caps the BlockingPreview slice at a
// response-size-friendly N. Clients wanting the full list go
// through /api/v1/audit/items?status=pending&severity=critical
// (or =high) which is uncapped. Mirrors the dashboard tile's
// dashboardAuditBlockingPreviewCap.
const auditBlockingPreviewCap = 5

// allowedAuditAPI{Categories,Severities,Statuses} are the closed-enum
// allow-lists for /api/v1/audit/items's filter query parameters. Each
// is the string form of the corresponding pkg/audit constant. If
// pkg/audit ever adds a new category, the allow-list AND the test
// matrix in handlers_audit_test.go must be extended in the same PR —
// guarded by TestAuditAPI_AllowedFilterEnumsMatchPkgAudit.
var (
	allowedAuditAPICategories = map[string]bool{
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
	allowedAuditAPISeverities = map[string]bool{
		string(audit.SevCritical): true,
		string(audit.SevHigh):     true,
		string(audit.SevMedium):   true,
		string(audit.SevLow):      true,
		string(audit.SevInfo):     true,
	}
	allowedAuditAPIStatuses = map[string]bool{
		string(audit.StatusPending): true,
		string(audit.StatusPassed):  true,
		string(audit.StatusFailed):  true,
		string(audit.StatusWaived):  true,
	}
)

// auditChecklistOnce-guarded process singleton. Lazy-initialised on
// first request so a binary that never serves /api/v1/audit/* pays
// no cost (audit.NewChecklist allocates ~85 items + a map). Once
// instantiated, every handler shares the same pointer and any
// future admin-endpoint UpdateStatus call propagates to both
// /api/v1/audit/summary and /api/v1/audit/items in lock-step.
var (
	auditChecklistOnce      sync.Once
	auditChecklistSingleton *audit.Checklist
)

// currentAuditChecklist returns the process-wide audit checklist,
// constructing it on first call.
func currentAuditChecklist() *audit.Checklist {
	auditChecklistOnce.Do(func() {
		auditChecklistSingleton = audit.NewChecklist()
	})
	return auditChecklistSingleton
}

// resetAuditChecklistForTest is a test-only entry point that
// throws away the process-wide checklist and re-arms the
// sync.Once so the next currentAuditChecklist() call rebuilds
// from defaultItems(). Lets handler tests run in any order
// without leaking state from a future admin-mutation test
// into a parallel summary test. Not exported.
func resetAuditChecklistForTest() {
	auditChecklistOnce = sync.Once{}
	auditChecklistSingleton = nil
}

// AuditSummaryHandler serves GET /api/v1/audit/summary.
//
// 200 OK with AuditSummary on success.
// 405 on non-GET.
//
// No auth: in publicPaths (see middleware.go). Audit checklist
// content is already public from the open-source repo; surfacing
// the runtime-verified state on the public API matches the
// /api/v1/trust/attestations/* "transparency over secrecy"
// posture from MAJOR_UPDATE Phase 5.
func (h *Handlers) AuditSummaryHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	cl := currentAuditChecklist()
	summary := cl.Summary()
	items := cl.Items()

	blockingPreview := make([]AuditBlockingItem, 0, auditBlockingPreviewCap)
	blockingCount := 0
	for _, it := range items {
		if (it.Severity == audit.SevCritical || it.Severity == audit.SevHigh) &&
			(it.Status == audit.StatusPending || it.Status == audit.StatusFailed) {
			blockingCount++
			if len(blockingPreview) < auditBlockingPreviewCap {
				blockingPreview = append(blockingPreview, AuditBlockingItem{
					ID:       it.ID,
					Category: string(it.Category),
					Severity: string(it.Severity),
					Status:   string(it.Status),
					Title:    it.Title,
				})
			}
		}
	}

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

	resp := AuditSummary{
		Summary:             summary,
		Score:               cl.Score(),
		HasBlockingFindings: cl.HasBlockingFindings(),
		BlockingCount:       blockingCount,
		BlockingPreview:     blockingPreview,
		EvidenceProvenance:  provenance,
		GeneratedAt:         time.Now().UTC().Format(time.RFC3339),
	}

	w.Header().Set("Content-Type", "application/json")
	// 60-second cache: a flip is a git commit + redeploy event —
	// pre-deploy stale reads are acceptable; the trust endpoints
	// use 5s but their data refreshes much more frequently. 60s
	// caps origin-fetch QPS at ~SDK-clients/60.
	w.Header().Set("Cache-Control", "public, max-age=60")
	_ = json.NewEncoder(w).Encode(resp)
}

// AuditItemsHandler serves GET /api/v1/audit/items.
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
// 200 OK with AuditItemsResponse on success.
// 400 on a typo'd filter value — clients that mis-type a filter
// must NOT see "all items" silently; the closed-enum allow-lists
// guarantee a 400 in that case.
// 405 on non-GET.
//
// No auth: in publicPaths.
func (h *Handlers) AuditItemsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	q := r.URL.Query()
	rawCategory := strings.TrimSpace(q.Get("category"))
	rawSeverity := strings.TrimSpace(q.Get("severity"))
	rawStatus := strings.TrimSpace(q.Get("status"))

	if rawCategory != "" && !allowedAuditAPICategories[rawCategory] {
		writeErrorResponse(w, http.StatusBadRequest,
			"category must be one of the closed-enum audit categories")
		return
	}
	if rawSeverity != "" && !allowedAuditAPISeverities[rawSeverity] {
		writeErrorResponse(w, http.StatusBadRequest,
			"severity must be one of: critical, high, medium, low, info")
		return
	}
	if rawStatus != "" && !allowedAuditAPIStatuses[rawStatus] {
		writeErrorResponse(w, http.StatusBadRequest,
			"status must be one of: pending, passed, failed, waived")
		return
	}

	cl := currentAuditChecklist()
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

	// Preserve the canonical defaultItems() insertion order
	// regardless of map iteration nondeterminism in
	// audit.Checklist.Items(). Mirrors the dashboard tile's
	// stable-sort approach so the API and tile responses agree.
	idIndex := make(map[string]int, len(items))
	for i, it := range items {
		idIndex[it.ID] = i
	}
	sort.SliceStable(out, func(i, j int) bool {
		return idIndex[out[i].ID] < idIndex[out[j].ID]
	})

	resp := AuditItemsResponse{
		Items:        out,
		TotalMatches: len(out),
		GeneratedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	if rawCategory != "" || rawSeverity != "" || rawStatus != "" {
		resp.Filters = &AuditEchoedFilters{
			Category: rawCategory,
			Severity: rawSeverity,
			Status:   rawStatus,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=60")
	_ = json.NewEncoder(w).Encode(resp)
}
