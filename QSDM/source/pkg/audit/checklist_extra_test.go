package audit

import "testing"

// runtimeVerifiedItems lists every checklist ID that ships pre-flipped to
// StatusPassed in defaultItems() because the underlying control either has
// (a) named test coverage that ran green in the most recent verification
// matrix, or (b) live-deployment evidence captured in CHANGELOG.md /
// NEXT_STEPS.md (typically referenced by session number and / or commit
// hash). Adding an item to this list without populating Status / ReviewedBy /
// ReviewedAt in defaultItems() is a CI failure.
var runtimeVerifiedItems = []string{
	"net-01", "net-02", "net-03", "net-04", "net-05",
	"bridge-01", "bridge-02", "bridge-03", "bridge-04",
	"rotation-01", "rotation-02", "rotation-03", "rotation-04", "rotation-05",
	"store-01", "store-02", "store-03", "store-04", "store-05",
	"api-01", "api-02", "api-03", "api-04", "api-05", "api-06",
	"auth-01", "auth-02", "auth-03", "auth-04", "auth-05",
	"authz-01", "authz-02", "authz-03", "authz-04",
	"crypto-01", "crypto-02", "crypto-03", "crypto-04", "crypto-05",
	"sc-01", "sc-02", "sc-03", "sc-04",
	"gov-01", "gov-02", "gov-03",
	"rebrand-01", "rebrand-02", "rebrand-04", "rebrand-05", "rebrand-06", "rebrand-07",
	"tok-02", "tok-03",
	"mining-02", "mining-03", "mining-04",
	"trust-01", "trust-02", "trust-03", "trust-04", "trust-05", "trust-06",
	"supply-01", "supply-02", "supply-03", "supply-04", "supply-05", "supply-06", "supply-07", "supply-08",
	"infra-01", "infra-02", "infra-03",
	"runtime-01", "runtime-02", "runtime-03", "runtime-04", "runtime-05", "runtime-06", "runtime-07",
}

func TestChecklist_RuntimeVerifiedItemsPassed(t *testing.T) {
	cl := NewChecklist()
	for _, id := range runtimeVerifiedItems {
		item, ok := cl.Get(id)
		if !ok {
			t.Errorf("item %q not found in checklist", id)
			continue
		}
		if item.Status != StatusPassed {
			t.Errorf("item %q: expected StatusPassed, got %q", id, item.Status)
		}
		if item.ReviewedBy == "" {
			t.Errorf("item %q: ReviewedBy must be set on a pre-passed item", id)
		}
		if item.ReviewedAt == nil {
			t.Errorf("item %q: ReviewedAt must be set on a pre-passed item", id)
		}
		if item.Notes == "" {
			t.Errorf("item %q: Notes must point at the evidence (session N, test name, or commit)", id)
		}
	}
}

func TestChecklist_RuntimeVerifiedReviewerProvenance(t *testing.T) {
	allowedReviewers := map[string]bool{
		"evidence:live-deploy":   true, // verified live on api.qsdm.tech / dashboard.qsdm.tech
		"evidence:in-tree-tests": true, // covered by named tests, green in latest matrix
		"evidence:in-tree":       true, // CI workflow or build-tag machinery in tree
	}
	cl := NewChecklist()
	for _, id := range runtimeVerifiedItems {
		item, ok := cl.Get(id)
		if !ok {
			continue
		}
		if !allowedReviewers[item.ReviewedBy] {
			t.Errorf("item %q: ReviewedBy=%q is not one of the allowed evidence: prefixes", id, item.ReviewedBy)
		}
	}
}

func TestChecklist_PassedCountMatchesRuntimeVerifiedList(t *testing.T) {
	cl := NewChecklist()
	s := cl.Summary()
	if s["passed"] != len(runtimeVerifiedItems) {
		t.Fatalf("passed count drift: summary[\"passed\"]=%d, runtimeVerifiedItems=%d — keep them in sync",
			s["passed"], len(runtimeVerifiedItems))
	}
}

func TestChecklist_IncludesNewCategories(t *testing.T) {
	cl := NewChecklist()
	cases := map[Category]int{
		CatSupplyChain:    8,
		CatRuntime:        7,
		CatSecretRotation: 5,
	}
	for cat, want := range cases {
		got := len(cl.ByCategory(cat))
		if got != want {
			t.Errorf("category %s: expected %d items, got %d", cat, want, got)
		}
	}
}

func TestChecklist_TotalCountReflectsExtensions(t *testing.T) {
	cl := NewChecklist()
	s := cl.Summary()
	if s["total"] < 55 {
		t.Fatalf("expected at least 55 items after extension, got %d", s["total"])
	}
}

func TestChecklist_SeverityFilter_CoversSupplyChain(t *testing.T) {
	cl := NewChecklist()
	critical := cl.BySeverity(SevCritical)
	var sawSupply bool
	for _, it := range critical {
		if it.Category == CatSupplyChain {
			sawSupply = true
			break
		}
	}
	if !sawSupply {
		t.Fatal("expected at least one critical supply-chain item")
	}
}
