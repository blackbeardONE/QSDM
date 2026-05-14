package dashboard

// Tests for handleAuditSummary / handleAuditItems — the
// dashboard's audit-checklist tile data endpoints.
//
// Coverage:
//   - Method gating (405 on non-GET).
//   - Summary envelope shape: bucket counts, score,
//     has_blocking_findings, blocking_preview cap, evidence
//     provenance buckets.
//   - Items endpoint full list + closed-enum filters
//     (category / severity / status), with both happy paths
//     and 400-on-typo per filter.
//   - Filters block elided when no filters applied; included
//     when at least one filter is set (matches the
//     dashboardAttestRejectionsView omitempty contract).
//   - Defensive path: a Dashboard literal with nil
//     auditChecklist still answers 200 with a fresh
//     audit.NewChecklist() snapshot rather than panicking.
//
// All tests run against direct handler invocation
// (httptest.NewRequest + handler call), bypassing
// requireAuth because the auth wrapper is exercised separately
// in v1_auth_route_test.go and is shared across every
// dashboard endpoint.

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/audit"
)

// newAuditTestDashboard returns a Dashboard whose audit
// checklist is wired and whose other dependencies are stubbed
// out the same way newTestDashboard does for the rejection
// tile. Kept separate so a future audit-test-specific tweak
// doesn't drag the rejection-tile harness with it.
func newAuditTestDashboard() *Dashboard {
	d := newTestDashboard()
	if d.auditChecklist == nil {
		// newTestDashboard goes through NewDashboard, which
		// always sets the field; the nil-guard below is for
		// the literal-construction defensive path.
		d.auditChecklist = audit.NewChecklist()
	}
	return d
}

func TestHandleAuditSummary_MethodNotAllowed(t *testing.T) {
	d := newAuditTestDashboard()
	req := httptest.NewRequest(http.MethodPost, "/api/audit/summary", nil)
	w := httptest.NewRecorder()
	d.handleAuditSummary(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d: %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Allow"); got != http.MethodGet {
		t.Fatalf("expected Allow: GET, got %q", got)
	}
}

func TestHandleAuditSummary_ShapeAndCounts(t *testing.T) {
	d := newAuditTestDashboard()
	req := httptest.NewRequest(http.MethodGet, "/api/audit/summary", nil)
	w := httptest.NewRecorder()
	d.handleAuditSummary(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("expected Content-Type application/json, got %q", ct)
	}

	var view dashboardAuditSummaryView
	if err := json.NewDecoder(w.Body).Decode(&view); err != nil {
		t.Fatalf("decode summary: %v — %s", err, w.Body.String())
	}

	// Summary buckets must sum to total — invariant carried
	// over from pkg/audit.TestChecklist_Summary.
	sum := view.Summary["pending"] + view.Summary["passed"] +
		view.Summary["failed"] + view.Summary["waived"]
	if sum != view.Summary["total"] {
		t.Fatalf("buckets must sum to total: %+v", view.Summary)
	}
	if view.Summary["total"] < 30 {
		t.Fatalf("expected at least 30 items, got %d", view.Summary["total"])
	}
	// pkg/audit pre-flips runtime-verified items (see
	// runtimeVerifiedItems in checklist_extra_test.go), so
	// passed must be > 0 in a fresh checklist.
	if view.Summary["passed"] == 0 {
		t.Fatal("expected passed > 0 from runtime-verified pre-flips")
	}
	if view.Summary["pending"] == 0 {
		t.Fatal("expected pending > 0 (audit work is in flight)")
	}

	// Score is (passed+waived)/total*100 in pkg/audit;
	// re-derive and assert within float-rounding tolerance.
	want := float64(view.Summary["passed"]+view.Summary["waived"]) /
		float64(view.Summary["total"]) * 100.0
	if view.Score < want-0.5 || view.Score > want+0.5 {
		t.Fatalf("score drift: got %.2f, want ~%.2f", view.Score, want)
	}

	// Critical/high pending items still exist (crypto-01,
	// auth-01, sc-01, bridge-01, etc.) so blocking must be true.
	if !view.HasBlockingFindings {
		t.Fatal("expected has_blocking_findings=true with pending critical/high items")
	}
	if view.BlockingCount == 0 {
		t.Fatal("expected blocking_count > 0")
	}
	if len(view.BlockingPreview) == 0 {
		t.Fatal("expected non-empty blocking_preview")
	}
	if len(view.BlockingPreview) > dashboardAuditBlockingPreviewCap {
		t.Fatalf("blocking_preview exceeded cap %d: got %d entries",
			dashboardAuditBlockingPreviewCap, len(view.BlockingPreview))
	}
	// Every preview entry must be critical or high AND
	// pending or failed (the definition of "blocking").
	for _, it := range view.BlockingPreview {
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

	// Provenance buckets must include all three canonical
	// keys (even at zero) plus "other".
	for _, key := range []string{"evidence:live-deploy", "evidence:in-tree-tests", "evidence:in-tree", "other"} {
		if _, ok := view.EvidenceProvenance[key]; !ok {
			t.Errorf("provenance missing key %q", key)
		}
	}
	provSum := 0
	for _, v := range view.EvidenceProvenance {
		provSum += v
	}
	if provSum != view.Summary["passed"] {
		t.Fatalf("provenance bucket sum %d must equal passed count %d",
			provSum, view.Summary["passed"])
	}

	if view.GeneratedAt == "" {
		t.Fatal("expected generated_at to be populated")
	}
}

func TestHandleAuditSummary_NilChecklistDefensive(t *testing.T) {
	// Construct a Dashboard literal-style with a nil
	// auditChecklist (bypassing NewDashboard). The handler
	// must answer with a fresh checklist rather than
	// panicking, because some test paths use literal
	// construction.
	d := &Dashboard{}
	req := httptest.NewRequest(http.MethodGet, "/api/audit/summary", nil)
	w := httptest.NewRecorder()
	d.handleAuditSummary(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 even with nil checklist, got %d", w.Code)
	}
}

func TestHandleAuditItems_MethodNotAllowed(t *testing.T) {
	d := newAuditTestDashboard()
	req := httptest.NewRequest(http.MethodPut, "/api/audit/items", nil)
	w := httptest.NewRecorder()
	d.handleAuditItems(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestHandleAuditItems_FullList_NoFilters(t *testing.T) {
	d := newAuditTestDashboard()
	req := httptest.NewRequest(http.MethodGet, "/api/audit/items", nil)
	w := httptest.NewRecorder()
	d.handleAuditItems(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var view dashboardAuditItemsView
	if err := json.NewDecoder(w.Body).Decode(&view); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if view.TotalMatches != len(view.Items) {
		t.Fatalf("total_matches %d != len(items) %d", view.TotalMatches, len(view.Items))
	}
	if view.TotalMatches < 30 {
		t.Fatalf("expected full list ≥ 30 items, got %d", view.TotalMatches)
	}
	// First item must match defaultItems()[0] insertion
	// order (currently crypto-01); covers the
	// preserve-insertion-order path of handleAuditItems.
	if view.Items[0].ID != "crypto-01" {
		t.Fatalf("expected first item crypto-01, got %s", view.Items[0].ID)
	}
	// No-filters call must NOT carry a filters block
	// (omitempty contract).
	if view.Filters != nil {
		t.Fatalf("expected filters block to be omitted, got %+v", view.Filters)
	}
}

func TestHandleAuditItems_FilterByCategory(t *testing.T) {
	d := newAuditTestDashboard()
	req := httptest.NewRequest(http.MethodGet, "/api/audit/items?category=cryptography", nil)
	w := httptest.NewRecorder()
	d.handleAuditItems(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var view dashboardAuditItemsView
	if err := json.NewDecoder(w.Body).Decode(&view); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if view.TotalMatches == 0 {
		t.Fatal("expected at least one cryptography item")
	}
	for _, it := range view.Items {
		if string(it.Category) != "cryptography" {
			t.Errorf("item %s leaked through category filter (cat=%q)", it.ID, it.Category)
		}
	}
	if view.Filters == nil || view.Filters.Category != "cryptography" {
		t.Fatalf("expected filters.category=cryptography, got %+v", view.Filters)
	}
}

func TestHandleAuditItems_FilterBySeverity_Critical(t *testing.T) {
	d := newAuditTestDashboard()
	req := httptest.NewRequest(http.MethodGet, "/api/audit/items?severity=critical", nil)
	w := httptest.NewRecorder()
	d.handleAuditItems(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var view dashboardAuditItemsView
	if err := json.NewDecoder(w.Body).Decode(&view); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if view.TotalMatches == 0 {
		t.Fatal("expected at least one critical item")
	}
	for _, it := range view.Items {
		if string(it.Severity) != "critical" {
			t.Errorf("item %s leaked through severity filter (sev=%q)", it.ID, it.Severity)
		}
	}
}

func TestHandleAuditItems_FilterByStatus_Passed_MatchesPreFlippedCount(t *testing.T) {
	d := newAuditTestDashboard()
	req := httptest.NewRequest(http.MethodGet, "/api/audit/items?status=passed", nil)
	w := httptest.NewRecorder()
	d.handleAuditItems(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var view dashboardAuditItemsView
	if err := json.NewDecoder(w.Body).Decode(&view); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Count must match summary["passed"] from the same
	// checklist instance — the items endpoint and the
	// summary endpoint must agree on what's passed.
	cl := d.auditChecklist
	want := cl.Summary()["passed"]
	if view.TotalMatches != want {
		t.Fatalf("status=passed returned %d items, summary[\"passed\"]=%d",
			view.TotalMatches, want)
	}
	for _, it := range view.Items {
		if string(it.Status) != "passed" {
			t.Errorf("item %s leaked through status=passed filter (status=%q)", it.ID, it.Status)
		}
		// Every passed item MUST carry review provenance —
		// this is the contract pkg/audit's
		// TestChecklist_RuntimeVerifiedItemsPassed enforces.
		if it.ReviewedBy == "" || it.ReviewedAt == nil {
			t.Errorf("passed item %s missing review provenance", it.ID)
		}
	}
}

func TestHandleAuditItems_FilterByStatus_Pending(t *testing.T) {
	d := newAuditTestDashboard()
	req := httptest.NewRequest(http.MethodGet, "/api/audit/items?status=pending", nil)
	w := httptest.NewRecorder()
	d.handleAuditItems(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var view dashboardAuditItemsView
	if err := json.NewDecoder(w.Body).Decode(&view); err != nil {
		t.Fatalf("decode: %v", err)
	}
	cl := d.auditChecklist
	want := cl.Summary()["pending"]
	if view.TotalMatches != want {
		t.Fatalf("status=pending returned %d items, summary[\"pending\"]=%d",
			view.TotalMatches, want)
	}
}

func TestHandleAuditItems_TypoFilters_400(t *testing.T) {
	cases := []struct {
		name  string
		query string
		want  string
	}{
		{"category", "?category=cryptogrhapy", "category"}, // typo'd
		{"severity", "?severity=hi", "severity"},
		{"status", "?status=approved", "status"},
		{"category-empty-space-ok", "?category=", ""}, // empty must be accepted (treated as no filter)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := newAuditTestDashboard()
			req := httptest.NewRequest(http.MethodGet, "/api/audit/items"+tc.query, nil)
			w := httptest.NewRecorder()
			d.handleAuditItems(w, req)
			if tc.want == "" {
				// Empty filter must succeed (200).
				if w.Code != http.StatusOK {
					t.Fatalf("empty filter must be 200, got %d: %s", w.Code, w.Body.String())
				}
				return
			}
			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400 on typo'd %s filter, got %d: %s",
					tc.want, w.Code, w.Body.String())
			}
			body := w.Body.String()
			if !strings.Contains(body, tc.want) {
				t.Fatalf("expected error to mention %q, got %q", tc.want, body)
			}
		})
	}
}

// TestAuditTile_StaticAssetsContainRequiredSymbols guards the
// frontend wiring against a refactor that drops the audit
// poller, the card markup, or the bridge between them. Same
// idiom as the attestation-rejections "Static Files"
// integration check — a future refactor that unhooks any of
// these symbols would silently render an empty tile and an
// operator looking at dashboard.qsdm.tech wouldn't notice
// until the daily standup.
func TestAuditTile_StaticAssetsContainRequiredSymbols(t *testing.T) {
	staticFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		t.Fatalf("static FS: %v", err)
	}

	jsBytes, err := fs.ReadFile(staticFS, "dashboard.js")
	if err != nil {
		t.Fatalf("read dashboard.js: %v", err)
	}
	js := string(jsBytes)

	// JS-side: poller function, fetch URL, polling-loop wiring,
	// initial-load wiring, and every DOM id the renderer writes
	// to. Drop any one and the tile silently regresses.
	for _, sym := range []string{
		"function updateAuditChecklist",
		"/api/audit/summary",
		"updateAuditChecklist();", // appears in startPolling AND startUpdates
		"audit-score",
		"audit-passed",
		"audit-pending",
		"audit-failed-waived",
		"audit-blocking-count",
		"audit-blocking-preview",
		"audit-provenance",
		"evidence:live-deploy",
		"evidence:in-tree-tests",
		"evidence:in-tree",
	} {
		if !strings.Contains(js, sym) {
			t.Errorf("dashboard.js missing audit-tile symbol %q", sym)
		}
	}

	htmlBytes, err := fs.ReadFile(staticFS, "index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	html := string(htmlBytes)
	for _, id := range []string{
		`id="audit-score"`,
		`id="audit-passed"`,
		`id="audit-pending"`,
		`id="audit-failed-waived"`,
		`id="audit-blocking-count"`,
		`id="audit-blocking-preview"`,
		`id="audit-provenance"`,
	} {
		if !strings.Contains(html, id) {
			t.Errorf("index.html missing audit-tile DOM %s", id)
		}
	}
	// Card heading must also be present so a layout refactor
	// that drops the entire card is caught even if the IDs
	// above survived in a different surface.
	if !strings.Contains(html, "Audit Checklist Progress") {
		t.Error("index.html missing 'Audit Checklist Progress' card heading")
	}
}

func TestHandleAuditItems_CombinedFilters(t *testing.T) {
	d := newAuditTestDashboard()
	req := httptest.NewRequest(http.MethodGet,
		"/api/audit/items?category=trust_api&status=passed", nil)
	w := httptest.NewRecorder()
	d.handleAuditItems(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var view dashboardAuditItemsView
	if err := json.NewDecoder(w.Body).Decode(&view); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, it := range view.Items {
		if string(it.Category) != "trust_api" || string(it.Status) != "passed" {
			t.Errorf("item %s leaked: cat=%q status=%q", it.ID, it.Category, it.Status)
		}
	}
	if view.Filters == nil ||
		view.Filters.Category != "trust_api" ||
		view.Filters.Status != "passed" {
		t.Fatalf("filters echo wrong: %+v", view.Filters)
	}
	// Severity was not provided so it must be the zero
	// value in the echo (omitempty already strips it from
	// JSON).
	if view.Filters.Severity != "" {
		t.Fatalf("expected severity echo empty, got %q", view.Filters.Severity)
	}
}
