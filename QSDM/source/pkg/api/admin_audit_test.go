package api

import "testing"

func TestAdminAuditTrail_RecordAndVerify(t *testing.T) {
	at := NewAdminAuditTrail("secret")
	at.Record("admin1", "validator_slash", "val-1", map[string]interface{}{"reason": "double_sign"})
	at.Record("admin1", "config_update", "node", map[string]interface{}{"key": "rate_limit"})
	if at.Count() != 2 {
		t.Fatalf("expected count 2, got %d", at.Count())
	}
	if err := at.VerifyChain(); err != nil {
		t.Fatalf("verify chain failed: %v", err)
	}
}

func TestAdminAuditTrail_Recent(t *testing.T) {
	at := NewAdminAuditTrail("secret")
	a1 := at.Record("admin1", "a1", "t1", nil)
	a2 := at.Record("admin1", "a2", "t2", nil)
	r := at.Recent(1)
	if len(r) != 1 || r[0].ID != a2.ID {
		t.Fatal("expected most recent action")
	}
	r2 := at.Recent(5)
	if len(r2) != 2 || r2[1].ID != a1.ID {
		t.Fatal("expected reverse chronological order")
	}
}

func TestAdminAuditTrail_DetectTamper(t *testing.T) {
	at := NewAdminAuditTrail("secret")
	at.Record("admin1", "a1", "t1", nil)
	at.Record("admin1", "a2", "t2", nil)

	at.mu.Lock()
	at.actions[1].Action = "evil"
	at.mu.Unlock()

	if err := at.VerifyChain(); err == nil {
		t.Fatal("expected tamper detection")
	}
}

func TestAdminAuditTrail_List(t *testing.T) {
	at := NewAdminAuditTrail("secret")
	at.Record("alice", "login", "node", nil)
	at.Record("bob", "logout", "node", nil)
	at.Record("alice", "config", "x", nil)

	all := at.List(10, 0, "", "")
	if len(all) != 3 || all[0].Actor != "alice" || all[0].Action != "config" {
		t.Fatalf("expected newest first, got %#v", all[0])
	}
	alice := at.List(10, 0, "alice", "")
	if len(alice) != 2 {
		t.Fatalf("expected 2 alice entries, got %d", len(alice))
	}
	page := at.List(1, 1, "", "")
	if len(page) != 1 || page[0].Actor != "bob" {
		t.Fatalf("offset 1: expected bob row, got %#v", page[0])
	}
}

