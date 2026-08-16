package chain

import (
	"math"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

// The snapshot must total every balance, so a reader can tell an empty ledger
// from an unwired provider.
func TestSupplySnapshot_totalsEveryBalance(t *testing.T) {
	as := NewAccountStore()
	as.Credit("alice", 100)
	as.Credit("bob", 250.5)
	as.Credit("carol", 0)

	snap := as.SupplySnapshot()
	if snap.Accounts != 3 {
		t.Errorf("Accounts = %d, want 3", snap.Accounts)
	}
	if snap.TotalCELL != 350.5 {
		t.Errorf("TotalCELL = %v, want 350.5", snap.TotalCELL)
	}
	if snap.NegativeAccounts != 0 {
		t.Errorf("NegativeAccounts = %d, want 0", snap.NegativeAccounts)
	}
}

// An empty store must report zero accounts rather than looking identical to a
// node where nothing registered a provider. The collector emits no series at
// all in the unwired case, so these two are distinguishable only if Accounts is
// honest here.
func TestSupplySnapshot_emptyStoreIsDistinguishable(t *testing.T) {
	snap := NewAccountStore().SupplySnapshot()
	if snap.Accounts != 0 || snap.TotalCELL != 0 {
		t.Fatalf("empty store should report zeros, got %+v", snap)
	}
}

// A single sub-ULP transfer is INVISIBLE in this gauge, and that limit is worth
// pinning so nobody later reads a flat line as proof the ledger conserves value.
//
// With a 1e15 funder the float64 ULP is 0.125. The debit rounds away entirely
// and the credit lands on the recipient, but the total cannot represent
// 1e15 + 0.01 at all, so it does not move. I first assumed Kahan summation
// would recover this and wrote that into the code comment; measuring showed it
// does not, because the limit is the result type rather than the algorithm.
//
// The gauge is still the right instrument for the audit's actual finding -- a
// ~190k CELL/yr loss is six orders of magnitude above the ULP and shows plainly
// over days -- it is just not a per-transaction detector.
func TestSupplySnapshot_singleSubULPTransferIsInvisible(t *testing.T) {
	as := NewAccountStore()
	as.Credit("funder", 1e15)
	as.Credit("miner", 0)

	before := as.SupplySnapshot().TotalCELL
	tx := &mempool.Tx{ID: "t1", Sender: "funder", Recipient: "miner", Amount: 0.01, Nonce: 0}
	if err := as.ApplyTx(tx); err != nil {
		t.Fatalf("ApplyTx: %v", err)
	}
	after := as.SupplySnapshot().TotalCELL

	if got := math.Abs(after - before); got != 0 {
		t.Fatalf("expected a sub-ULP transfer to be invisible in the total (float64 cannot "+
			"represent 1e15+0.01), got drift %v -- if this now works the gauge became a "+
			"per-transaction detector and the comments claiming otherwise are stale", got)
	}
	// The recipient really did receive it -- the value is not missing from the
	// ledger, only from a float64 total at funder scale.
	acc, ok := as.Get("miner")
	if !ok || acc.Balance != 0.01 {
		t.Errorf("recipient balance = %v (found=%v), want 0.01", acc, ok)
	}
}

// A drift far above the ULP MUST be visible, or the gauge is useless for the
// thing it was actually built for.
func TestSupplySnapshot_accumulatedDriftIsVisible(t *testing.T) {
	as := NewAccountStore()
	as.Credit("funder", 1e15)
	before := as.SupplySnapshot().TotalCELL

	// Stand in for a year of accumulated loss (~190k CELL per the audit).
	as.Credit("miner", 190_000)

	after := as.SupplySnapshot().TotalCELL
	if after-before != 190_000 {
		t.Fatalf("a 190k CELL change must be visible against a 1e15 total, got %v", after-before)
	}
}

// Every guarded path refuses a negative balance, so the counter should read
// zero. This asserts the GUARD, not the absence of one -- an earlier version of
// this test tried to drive a balance negative via Debit to prove the counter
// worked, and failed because Debit correctly refuses. The metric help text had
// already been written claiming no such constraint existed.
func TestSupplySnapshot_negativeBalancesAreRefusedNotCounted(t *testing.T) {
	as := NewAccountStore()
	as.Credit("solvent", 10)

	if err := as.Debit("solvent", 25); err == nil {
		t.Fatal("Debit must refuse to drive a balance negative")
	}
	snap := as.SupplySnapshot()
	if snap.NegativeAccounts != 0 {
		t.Errorf("NegativeAccounts = %d, want 0: no guarded path can go negative",
			snap.NegativeAccounts)
	}
	if snap.TotalCELL != 10 {
		t.Errorf("a refused debit must not move the total, got %v", snap.TotalCELL)
	}
}
