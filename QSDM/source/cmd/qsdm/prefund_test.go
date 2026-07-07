package main

import (
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/chain"
)

func TestParsePrefundAccounts(t *testing.T) {
	entries, err := parsePrefundAccounts("alice:10,bob=2.5\ncarol:1")
	if err != nil {
		t.Fatalf("parsePrefundAccounts: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("entries len = %d, want 3", len(entries))
	}
	if entries[0].Address != "alice" || entries[0].Amount != 10 {
		t.Fatalf("entries[0] = %+v", entries[0])
	}
	if entries[1].Address != "bob" || entries[1].Amount != 2.5 {
		t.Fatalf("entries[1] = %+v", entries[1])
	}
	if entries[2].Address != "carol" || entries[2].Amount != 1 {
		t.Fatalf("entries[2] = %+v", entries[2])
	}
}

func TestParsePrefundAccountsRejectsBadEntries(t *testing.T) {
	for _, raw := range []string{"alice", ":1", "alice:0", "alice:-1", "alice:nope"} {
		if _, err := parsePrefundAccounts(raw); err == nil {
			t.Fatalf("parsePrefundAccounts(%q) expected error", raw)
		}
	}
}

func TestApplyPrefundAccounts(t *testing.T) {
	t.Setenv(qsdmAllowDevelopmentPrefundEnv, "1")
	accounts := chain.NewAccountStore()
	entries, err := applyPrefundAccounts(accounts, "alice:10")
	if err != nil {
		t.Fatalf("applyPrefundAccounts: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries len = %d, want 1", len(entries))
	}
	account, ok := accounts.Get("alice")
	if !ok {
		t.Fatal("alice account not found")
	}
	if account.Balance != 10 {
		t.Fatalf("alice balance = %v, want 10", account.Balance)
	}
}

func TestApplyPrefundAccountsRejectedWithoutDevelopmentGate(t *testing.T) {
	accounts := chain.NewAccountStore()
	if _, err := applyPrefundAccounts(accounts, "alice:10"); err == nil {
		t.Fatal("expected production configuration to reject environment prefund")
	}
	if _, ok := accounts.Get("alice"); ok {
		t.Fatal("rejected prefund mutated account state")
	}
}

func TestApplyPrefundAccountsForbiddenInProduction(t *testing.T) {
	t.Setenv(qsdmAllowDevelopmentPrefundEnv, "1")
	t.Setenv(qsdmProductionModeEnv, "true")
	accounts := chain.NewAccountStore()
	if _, err := applyPrefundAccounts(accounts, "alice:5"); err == nil {
		t.Fatal("expected production mode to reject development prefund")
	}
	if _, ok := accounts.Get("alice"); ok {
		t.Fatal("rejected production prefund mutated account state")
	}
}

func TestRejectDevelopmentFundingInProduction(t *testing.T) {
	t.Setenv(qsdmProductionModeEnv, "true")
	for _, key := range developmentFundingEnvKeys {
		for _, resetKey := range developmentFundingEnvKeys {
			t.Setenv(resetKey, "")
		}
		t.Setenv(key, "configured")
		if err := rejectDevelopmentFundingInProduction(); err == nil {
			t.Fatalf("expected production mode to reject %s", key)
		}
	}
}
