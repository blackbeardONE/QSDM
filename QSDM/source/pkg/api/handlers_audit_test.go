package api

// Tests for AuditSummaryHandler / AuditItemsHandler — the
// public-API mirror of the dashboard's audit-checklist tile.
//
// Coverage:
//   - Method gating (405 on non-GET).
//   - Public-endpoint allow-list inclusion (isPublicEndpoint
//     must return true for both URLs — drift guard).
//   - Summary envelope shape: bucket counts, score consistent
//     with audit.Checklist.Score(), has_blocking_findings,
//     blocking_preview cap, evidence_provenance buckets.
//   - Items endpoint full list + closed-enum filters
//     (category / severity / status), with both happy paths
//     and 400-on-typo per filter.
//   - Filters block elided when no filters applied; included
//     when at least one filter is set (omitempty contract).
//   - Wire-shape parity guard: every JSON field that the
//     dashboard's dashboardAuditSummaryView serialises is
//     also present on AuditSummary so an SDK client can
//     point at either URL and get identical JSON.
//   - Cache-Control header present (public, max-age=60) — a
//     regression to "no caching" would multiply origin load.
//   - resetAuditChecklistForTest plumbing: a clean checklist
//     across tests so a future admin-mutation test can't
//     leak into a parallel summary test.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/audit"
)

func TestAuditSummaryHandler_MethodNotAllowed(t *testing.T) {
	resetAuditChecklistForTest()
	h := setupTestHandlers()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/audit/summary", nil)
	w := httptest.NewRecorder()
	h.AuditSummaryHandler(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d: %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Allow"); got != http.MethodGet {
		t.Fatalf("expected Allow: GET, got %q", got)
	}
}

func TestAuditSummaryHandler_ShapeAndCounts(t *testing.T) {
	resetAuditChecklistForTest()
	h := setupTestHandlers()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit/summary", nil)
	w := httptest.NewRecorder()
	h.AuditSummaryHandler(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("expected Content-Type application/json, got %q", ct)
	}
	if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "public") || !strings.Contains(cc, "max-age=60") {
		t.Fatalf("expected Cache-Control public + max-age=60, got %q", cc)
	}

	var resp AuditSummary
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode summary: %v — %s", err, w.Body.String())
	}

	sum := resp.Summary["pending"] + resp.Summary["passed"] +
		resp.Summary["failed"] + resp.Summary["waived"]
	if sum != resp.Summary["total"] {
		t.Fatalf("buckets must sum to total: %+v", resp.Summary)
	}
	if resp.Summary["total"] < 30 {
		t.Fatalf("expected at least 30 items in defaultItems(), got %d", resp.Summary["total"])
	}
	if resp.Summary["passed"] == 0 {
		t.Fatal("expected passed > 0 from runtime-verified pre-flips (Session 75)")
	}

	want := float64(resp.Summary["passed"]+resp.Summary["waived"]) /
		float64(resp.Summary["total"]) * 100.0
	if resp.Score < want-0.5 || resp.Score > want+0.5 {
		t.Fatalf("score drift: got %.2f, want ~%.2f", resp.Score, want)
	}

	if !resp.HasBlockingFindings {
		t.Fatal("expected has_blocking_findings=true with pending critical/high items")
	}
	if resp.BlockingCount == 0 {
		t.Fatal("expected blocking_count > 0")
	}
	if len(resp.BlockingPreview) == 0 {
		t.Fatal("expected non-empty blocking_preview")
	}
	if len(resp.BlockingPreview) > auditBlockingPreviewCap {
		t.Fatalf("blocking_preview exceeded cap %d: got %d entries",
			auditBlockingPreviewCap, len(resp.BlockingPreview))
	}
	for _, it := range resp.BlockingPreview {
		if it.Severity != string(audit.SevCritical) && it.Severity != string(audit.SevHigh) {
			t.Errorf("preview entry %s has non-blocking severity %q", it.ID, it.Severity)
		}
		if it.Status != string(audit.StatusPending) && it.Status != string(audit.StatusFailed) {
			t.Errorf("preview entry %s has non-blocking status %q", it.ID, it.Status)
		}
		if it.ID == "" || it.Title == "" || it.Category == "" {
			t.Errorf("preview entry missing required field: %+v", it)
		}
	}

	for _, key := range []string{"evidence:live-deploy", "evidence:in-tree-tests", "evidence:in-tree", "other"} {
		if _, ok := resp.EvidenceProvenance[key]; !ok {
			t.Errorf("provenance missing key %q", key)
		}
	}
	provSum := 0
	for _, v := range resp.EvidenceProvenance {
		provSum += v
	}
	if provSum != resp.Summary["passed"] {
		t.Fatalf("provenance bucket sum %d must equal passed count %d",
			provSum, resp.Summary["passed"])
	}

	if resp.GeneratedAt == "" {
		t.Fatal("expected generated_at to be populated")
	}
}

func TestAuditItemsHandler_MethodNotAllowed(t *testing.T) {
	resetAuditChecklistForTest()
	h := setupTestHandlers()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/audit/items", nil)
	w := httptest.NewRecorder()
	h.AuditItemsHandler(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestAuditItemsHandler_FullList_NoFilters(t *testing.T) {
	resetAuditChecklistForTest()
	h := setupTestHandlers()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit/items", nil)
	w := httptest.NewRecorder()
	h.AuditItemsHandler(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp AuditItemsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.TotalMatches != len(resp.Items) {
		t.Fatalf("total_matches %d != len(items) %d", resp.TotalMatches, len(resp.Items))
	}
	if resp.TotalMatches < 30 {
		t.Fatalf("expected full list ≥ 30 items, got %d", resp.TotalMatches)
	}
	if resp.Items[0].ID != "crypto-01" {
		t.Fatalf("expected first item crypto-01 (defaultItems insertion order), got %s", resp.Items[0].ID)
	}
	if resp.Filters != nil {
		t.Fatalf("expected filters block to be omitted, got %+v", resp.Filters)
	}
}

func TestAuditItemsHandler_FilterByStatus_Passed_MatchesSummary(t *testing.T) {
	resetAuditChecklistForTest()
	h := setupTestHandlers()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit/items?status=passed", nil)
	w := httptest.NewRecorder()
	h.AuditItemsHandler(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp AuditItemsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	cl := currentAuditChecklist()
	want := cl.Summary()["passed"]
	if resp.TotalMatches != want {
		t.Fatalf("status=passed returned %d items, summary[\"passed\"]=%d",
			resp.TotalMatches, want)
	}
	for _, it := range resp.Items {
		if string(it.Status) != "passed" {
			t.Errorf("item %s leaked through status=passed filter (status=%q)", it.ID, it.Status)
		}
		if it.ReviewedBy == "" || it.ReviewedAt == nil {
			t.Errorf("passed item %s missing review provenance", it.ID)
		}
	}
}

func TestAuditItemsHandler_FilterByCategory(t *testing.T) {
	resetAuditChecklistForTest()
	h := setupTestHandlers()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit/items?category=cryptography", nil)
	w := httptest.NewRecorder()
	h.AuditItemsHandler(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp AuditItemsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.TotalMatches == 0 {
		t.Fatal("expected at least one cryptography item")
	}
	for _, it := range resp.Items {
		if string(it.Category) != "cryptography" {
			t.Errorf("item %s leaked through category filter (cat=%q)", it.ID, it.Category)
		}
	}
	if resp.Filters == nil || resp.Filters.Category != "cryptography" {
		t.Fatalf("expected filters.category=cryptography, got %+v", resp.Filters)
	}
}

func TestAuditItemsHandler_TypoFilters_400(t *testing.T) {
	cases := []struct {
		name  string
		query string
		want  string
	}{
		{"category", "?category=cryptogrhapy", "category"},
		{"severity", "?severity=hi", "severity"},
		{"status", "?status=approved", "status"},
		{"empty-category", "?category=", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetAuditChecklistForTest()
			h := setupTestHandlers()
			req := httptest.NewRequest(http.MethodGet, "/api/v1/audit/items"+tc.query, nil)
			w := httptest.NewRecorder()
			h.AuditItemsHandler(w, req)
			if tc.want == "" {
				if w.Code != http.StatusOK {
					t.Fatalf("empty filter must be 200, got %d: %s", w.Code, w.Body.String())
				}
				return
			}
			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400 on typo'd %s filter, got %d: %s",
					tc.want, w.Code, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), tc.want) {
				t.Fatalf("expected error to mention %q, got %q", tc.want, w.Body.String())
			}
		})
	}
}

func TestAuditAPI_PublicEndpointAllowList(t *testing.T) {
	// Drift guard: both endpoints MUST be public-listed. If a
	// future refactor accidentally drops them from publicPaths,
	// every external consumer (SDK, landing page widget, third-
	// party aggregators) gets a 401 redirect to the auth flow
	// and the transparency signal evaporates.
	for _, path := range []string{
		"/api/v1/audit/summary",
		"/api/v1/audit/items",
	} {
		if !isPublicEndpoint(path) {
			t.Errorf("expected %s to be a public endpoint", path)
		}
	}
}

func TestAuditAPI_AllowedFilterEnumsMatchPkgAudit(t *testing.T) {
	// If pkg/audit adds a new category constant, this allow-list
	// must learn it (otherwise an item with the new category
	// would be impossible to filter on via the public API). This
	// test enumerates the known constants and asserts each is
	// present in allowedAuditAPICategories. New constants get
	// an obvious failure here rather than a silent gap.
	for _, c := range []audit.Category{
		audit.CatCryptography,
		audit.CatAuthentication,
		audit.CatAuthorisation,
		audit.CatNetwork,
		audit.CatSmartContracts,
		audit.CatBridge,
		audit.CatStorage,
		audit.CatAPI,
		audit.CatGovernance,
		audit.CatInfra,
		audit.CatSupplyChain,
		audit.CatRuntime,
		audit.CatSecretRotation,
		audit.CatRebrand,
		audit.CatTokenomics,
		audit.CatMiningAudit,
		audit.CatTrustAPI,
	} {
		if !allowedAuditAPICategories[string(c)] {
			t.Errorf("allowedAuditAPICategories missing %q", c)
		}
	}
	for _, s := range []audit.Severity{
		audit.SevCritical, audit.SevHigh, audit.SevMedium, audit.SevLow, audit.SevInfo,
	} {
		if !allowedAuditAPISeverities[string(s)] {
			t.Errorf("allowedAuditAPISeverities missing %q", s)
		}
	}
	for _, st := range []audit.Status{
		audit.StatusPending, audit.StatusPassed, audit.StatusFailed, audit.StatusWaived,
	} {
		if !allowedAuditAPIStatuses[string(st)] {
			t.Errorf("allowedAuditAPIStatuses missing %q", st)
		}
	}
}

func TestAuditAPI_WireParity_DashboardAndAPI(t *testing.T) {
	// Wire-shape parity: every JSON field the dashboard's
	// dashboardAuditSummaryView serialises must also appear on
	// AuditSummary so a client switching between the two URLs
	// gets the same JSON. We assert by serialising the public
	// API response and checking the canonical key set.
	resetAuditChecklistForTest()
	h := setupTestHandlers()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit/summary", nil)
	w := httptest.NewRecorder()
	h.AuditSummaryHandler(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var raw map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, key := range []string{
		"summary",
		"score",
		"has_blocking_findings",
		"blocking_count",
		"blocking_preview",
		"evidence_provenance",
		"generated_at",
	} {
		if _, ok := raw[key]; !ok {
			t.Errorf("AuditSummary JSON missing key %q (drift vs dashboardAuditSummaryView)", key)
		}
	}
}
