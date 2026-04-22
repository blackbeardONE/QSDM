package audit

import (
	"testing"
)

func TestNewChecklist_HasItems(t *testing.T) {
	cl := NewChecklist()
	items := cl.Items()
	if len(items) < 30 {
		t.Fatalf("expected at least 30 checklist items, got %d", len(items))
	}
}

func TestChecklist_Summary(t *testing.T) {
	cl := NewChecklist()
	summary := cl.Summary()
	if summary["total"] < 30 {
		t.Fatalf("expected total >= 30, got %d", summary["total"])
	}
	if summary["pending"] != summary["total"] {
		t.Fatalf("all items should be pending initially: pending=%d, total=%d", summary["pending"], summary["total"])
	}
}

func TestChecklist_UpdateStatus(t *testing.T) {
	cl := NewChecklist()
	err := cl.UpdateStatus("crypto-01", StatusPassed, "auditor1", "verified CSPRNG usage")
	if err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}

	item, ok := cl.Get("crypto-01")
	if !ok {
		t.Fatal("item not found")
	}
	if item.Status != StatusPassed {
		t.Fatalf("expected passed, got %s", item.Status)
	}
	if item.ReviewedBy != "auditor1" {
		t.Fatalf("expected auditor1, got %s", item.ReviewedBy)
	}
	if item.ReviewedAt == nil {
		t.Fatal("expected reviewed_at to be set")
	}
}

func TestChecklist_UpdateStatus_NotFound(t *testing.T) {
	cl := NewChecklist()
	err := cl.UpdateStatus("nonexistent", StatusPassed, "x", "")
	if err == nil {
		t.Fatal("expected error for nonexistent item")
	}
}

func TestChecklist_ByCategory(t *testing.T) {
	cl := NewChecklist()
	crypto := cl.ByCategory(CatCryptography)
	if len(crypto) < 4 {
		t.Fatalf("expected at least 4 crypto items, got %d", len(crypto))
	}
	for _, item := range crypto {
		if item.Category != CatCryptography {
			t.Fatalf("expected category cryptography, got %s", item.Category)
		}
	}
}

func TestChecklist_BySeverity(t *testing.T) {
	cl := NewChecklist()
	critical := cl.BySeverity(SevCritical)
	if len(critical) < 5 {
		t.Fatalf("expected at least 5 critical items, got %d", len(critical))
	}
}

func TestChecklist_PendingCritical(t *testing.T) {
	cl := NewChecklist()
	pending := cl.PendingCritical()
	if len(pending) == 0 {
		t.Fatal("expected pending critical items")
	}

	// Mark one as passed
	cl.UpdateStatus(pending[0].ID, StatusPassed, "auditor", "ok")

	pending2 := cl.PendingCritical()
	if len(pending2) != len(pending)-1 {
		t.Fatalf("expected one fewer pending critical: was %d, now %d", len(pending), len(pending2))
	}
}

func TestChecklist_Get(t *testing.T) {
	cl := NewChecklist()
	item, ok := cl.Get("auth-01")
	if !ok {
		t.Fatal("auth-01 not found")
	}
	if item.Category != CatAuthentication {
		t.Fatalf("unexpected category: %s", item.Category)
	}
}

func TestChecklist_OrderPreserved(t *testing.T) {
	cl := NewChecklist()
	items := cl.Items()
	if items[0].ID != "crypto-01" {
		t.Fatalf("expected first item crypto-01, got %s", items[0].ID)
	}
}
