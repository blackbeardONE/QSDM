package audit

import "testing"

func TestChecklist_IncludesNewCategories(t *testing.T) {
	cl := NewChecklist()
	cases := map[Category]int{
		CatSupplyChain:    7,
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
