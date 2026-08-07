package api

import (
	"testing"

	"github.com/blackbeardONE/QSDM/internal/logging"
	"github.com/blackbeardONE/QSDM/pkg/wallet"
)

// fundedProbe reports a fixed canonical balance for every address.
type fundedProbe struct{ bal float64 }

func (f fundedProbe) BalanceOf(string) (float64, uint64, bool) { return f.bal, 0, true }

// TestSendTransaction_preflightBalanceSyncedFromLedger is the regression test
// for the wiring gap that made POST /api/v1/wallet/send always 500.
//
// WalletService constructs with balance 0 and CreateTransaction refuses to
// build a tx it believes is unfunded. Nothing in production ever called
// SyncBalanceFromLedger, so a funded validator wallet still failed with
// "insufficient balance: have 0". The handler now mirrors canonical ledger
// state into the preflight cache first.
func TestSendTransaction_preflightBalanceSyncedFromLedger(t *testing.T) {
	ws, err := wallet.NewWalletService()
	if err != nil {
		t.Skipf("wallet service unavailable: %v", err)
	}

	// Canonical ledger says this wallet holds 500 CELL.
	SetMiningAccountProbe(fundedProbe{bal: 500})
	t.Cleanup(func() { SetMiningAccountProbe(nil) })

	authManager, _ := NewAuthManager()
	h := NewHandlers(authManager, NewUserStore(), ws, newMockStorage(),
		logging.NewLogger("test.log", false),
		"", false, 0, "", "", false, 0, false, nil)

	if got := ws.GetBalance(); got != 0 {
		t.Fatalf("precondition: wallet should start unfunded, got %d", got)
	}

	h.syncWalletPreflightBalance()

	if got := ws.GetBalance(); got != 500 {
		t.Fatalf("preflight cache should mirror the canonical 500 CELL, got %d", got)
	}

	// With the cache synced, transaction construction succeeds where it
	// previously failed closed.
	if _, err := ws.CreateTransaction("recipient_abc", 10, 0.01, "US", []string{"p1", "p2"}); err != nil {
		t.Fatalf("CreateTransaction should succeed once the preflight cache is synced: %v", err)
	}
}

// A wallet the ledger has never seen must not gain a phantom balance.
func TestSyncWalletPreflightBalance_unknownAddressStaysZero(t *testing.T) {
	ws, err := wallet.NewWalletService()
	if err != nil {
		t.Skipf("wallet service unavailable: %v", err)
	}
	SetMiningAccountProbe(nil)

	authManager, _ := NewAuthManager()
	h := NewHandlers(authManager, NewUserStore(), ws, newMockStorage(),
		logging.NewLogger("test.log", false),
		"", false, 0, "", "", false, 0, false, nil)

	h.syncWalletPreflightBalance()

	if got := ws.GetBalance(); got != 0 {
		t.Fatalf("unknown address must not gain a balance, got %d", got)
	}
}
