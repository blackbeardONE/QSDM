package chain

import (
	"math"
	"os"
	"path/filepath"
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

// The negative-balance counter must actually fire, and there is a real path
// that reaches it.
//
// Every guarded write refuses a negative balance, so an earlier version of this
// file only asserted the guards and never exercised the increment -- a reviewer
// disabled `NegativeAccounts++` entirely and all five tests stayed green. The
// counter was a canary nothing proved could sing.
//
// The reachable path is Load: it unmarshals persisted state and writes it
// straight into the account map (account.go:453-469) with no validation of any
// kind, and it runs against the live store at cmd/qsdm/main.go:2001. So a
// corrupted, truncated or tampered accounts state file injects balances that
// bypass Debit and ApplyTx completely. That is exactly the condition this gauge
// exists to surface, and it is why the counter is justified rather than dead
// weight.
func TestSupplySnapshot_countsNegativeBalanceInjectedByLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "accounts.json")

	// A state file a healthy node would never write, but nothing rejects.
	payload := `[{"address":"solvent","balance":100,"nonce":0},
	             {"address":"underwater","balance":-42.5,"nonce":0}]`
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatalf("write state: %v", err)
	}

	as := NewAccountStore()
	n, err := as.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if n != 2 {
		t.Fatalf("loaded %d accounts, want 2", n)
	}

	snap := as.SupplySnapshot()
	if snap.NegativeAccounts != 1 {
		t.Errorf("NegativeAccounts = %d, want 1: Load accepted a negative balance and the "+
			"counter is the only thing that would surface it", snap.NegativeAccounts)
	}
	if snap.TotalCELL != 57.5 {
		t.Errorf("TotalCELL = %v, want 57.5 (100 + -42.5)", snap.TotalCELL)
	}
}

// Load performs no validation whatsoever. Pinned so the gap is visible in the
// test suite rather than only in a comment: if someone later makes Load reject
// negative balances, this test fails and tells them the supply canary above may
// no longer have a reachable path.
func TestAccountStore_LoadDoesNotValidateBalances(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.json")
	if err := os.WriteFile(path, []byte(`[{"address":"a","balance":-1,"nonce":0}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	as := NewAccountStore()
	if _, err := as.Load(path); err != nil {
		t.Fatalf("Load currently accepts negative balances without error; it returned %v. "+
			"If that changed deliberately, update TestSupplySnapshot_countsNegativeBalanceInjectedByLoad "+
			"which relies on this path to reach the counter.", err)
	}
	acc, ok := as.Get("a")
	if !ok || acc.Balance != -1 {
		t.Errorf("expected the negative balance to be stored verbatim, got %+v (found=%v)", acc, ok)
	}
}

// Load must report how many DISTINCT accounts it contributed, not how many
// entries the file held.
//
// It returned len(accounts), so a file carrying the same address twice reported
// two. That number is logged at boot as "accounts_loaded"
// (cmd/qsdm/main.go:2120), so the inflation was operator-facing: the log claimed
// more state was restored than the store actually held. Found by a reviewer
// while checking a different claim.
func TestAccountStore_LoadCountsDistinctAddresses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dupes.json")
	payload := `[{"address":"a","balance":10,"nonce":0},
	             {"address":"a","balance":25,"nonce":1},
	             {"address":"b","balance":5,"nonce":0}]`
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}

	as := NewAccountStore()
	n, err := as.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if n != 2 {
		t.Errorf("Load reported %d accounts, want 2 distinct (the file has 3 entries, "+
			"two sharing one address)", n)
	}
	// Last write wins, which is pre-existing behaviour this pins rather than changes.
	acc, ok := as.Get("a")
	if !ok || acc.Balance != 25 {
		t.Errorf("duplicate address should last-write-win to 25, got %+v (found=%v)", acc, ok)
	}
	if snap := as.SupplySnapshot(); snap.Accounts != 2 {
		t.Errorf("supply snapshot Accounts = %d, want 2", snap.Accounts)
	}
}

// The count must reflect THIS file, not the whole store. A store already
// holding accounts from genesis or an earlier restore would otherwise inflate
// the number a second way -- which is the bug I introduced while fixing the
// first one, by returning len(as.accounts).
func TestAccountStore_LoadCountIsPerFileNotPerStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "one.json")
	if err := os.WriteFile(path, []byte(`[{"address":"new","balance":1,"nonce":0}]`), 0o600); err != nil {
		t.Fatal(err)
	}

	as := NewAccountStore()
	as.Credit("pre-existing-1", 100)
	as.Credit("pre-existing-2", 100)

	n, err := as.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if n != 1 {
		t.Errorf("Load reported %d, want 1: the store already held 2 unrelated accounts, "+
			"so returning the store size would report 3", n)
	}
}
