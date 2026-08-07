package chain

import (
	"math"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

// atHeight returns a store whose height source is pinned, so the fork gate
// is deterministic in tests.
func atHeight(h uint64) *AccountStore {
	as := NewAccountStore()
	as.SetHeightFn(func() uint64 { return h })
	return as
}

// A store with no height source, or below the fork, must behave exactly as
// it did before this change — same arithmetic, same state root bytes.
func TestAccountStore_preForkBehaviourUnchanged(t *testing.T) {
	SetForkDustHeight(1000)
	t.Cleanup(func() { SetForkDustHeight(math.MaxUint64) })

	legacy := NewAccountStore() // no height fn at all
	below := atHeight(999)

	for _, as := range []*AccountStore{legacy, below} {
		as.Credit("alice", 10)
		tx := &mempool.Tx{ID: "t", Sender: "alice", Recipient: "bob", Amount: 3, Fee: 0.5, Nonce: 0}
		if err := as.ApplyTx(tx); err != nil {
			t.Fatalf("legacy apply: %v", err)
		}
	}

	if legacy.StateRoot() != below.StateRoot() {
		t.Fatal("a store below the fork must produce the legacy state root")
	}

	// And the legacy encoding is still the float form.
	acc, _ := legacy.Get("alice")
	if acc.BalanceDust != 0 {
		t.Fatalf("pre-fork accounting must not populate BalanceDust, got %d", acc.BalanceDust)
	}
	if acc.Balance != 6.5 {
		t.Fatalf("pre-fork balance: want 6.5, got %v", acc.Balance)
	}
}

// TestAccountStore_dustTransferIsExact is the regression test for the money
// destruction. At a supply-scale balance the float64 ULP (0.125 CELL at
// ~1e15) exceeded the transfer amount's precision, so the debit was
// quantised while the credit was exact — destroying ~0.06 CELL per reward
// block, ~190k CELL/year. Integer accounting makes debit and credit equal by
// construction.
func TestAccountStore_dustTransferIsExact(t *testing.T) {
	SetForkDustHeight(0) // active from genesis in this test
	t.Cleanup(func() { SetForkDustHeight(math.MaxUint64) })

	as := atHeight(0)
	// A large but representable balance: the full 90M mining supply.
	as.Credit("funder", 90_000_000)

	const reward = 3.56490987 // ~epoch-0 per-block reward, whole dust
	before, _ := as.Get("funder")

	tx := &mempool.Tx{ID: "r", Sender: "funder", Recipient: "miner", Amount: reward, Fee: 0, Nonce: 0}
	if err := as.ApplyTx(tx); err != nil {
		t.Fatalf("ApplyTx: %v", err)
	}

	after, _ := as.Get("funder")
	miner, _ := as.Get("miner")

	debited := before.BalanceDust - after.BalanceDust
	credited := miner.BalanceDust

	if debited != credited {
		t.Fatalf("conservation violated: debited %d dust but credited %d dust (%d destroyed)",
			debited, credited, int64(debited)-int64(credited))
	}
	if want := floorToDust(reward); credited != want {
		t.Fatalf("credited %d dust, want %d", credited, want)
	}
}

// Supply is conserved across many transfers at supply scale — the scenario
// that leaked ~190k CELL/year under float64.
func TestAccountStore_dustConservesSupplyOverManyBlocks(t *testing.T) {
	SetForkDustHeight(0)
	t.Cleanup(func() { SetForkDustHeight(math.MaxUint64) })

	as := atHeight(0)
	as.Credit("funder", 90_000_000)
	start, _ := as.Get("funder")
	total := start.BalanceDust

	const reward = 3.56490987
	const blocks = 5000
	for i := 0; i < blocks; i++ {
		tx := &mempool.Tx{
			ID: "r", Sender: "funder", Recipient: "miner",
			Amount: reward, Fee: 0, Nonce: uint64(i),
		}
		if err := as.ApplyTx(tx); err != nil {
			t.Fatalf("block %d: %v", i, err)
		}
	}

	funder, _ := as.Get("funder")
	miner, _ := as.Get("miner")
	if funder.BalanceDust+miner.BalanceDust != total {
		t.Fatalf("supply drifted over %d blocks: %d + %d != %d (delta %d dust)",
			blocks, funder.BalanceDust, miner.BalanceDust, total,
			int64(funder.BalanceDust+miner.BalanceDust)-int64(total))
	}
}

// The post-fork state root must distinguish balances that differ by a single
// dust — the pre-fork %f encoding (six decimals) could not, since one dust
// is 1e-8 CELL.
func TestStateRoot_dustEncodingDistinguishesSubMicroCell(t *testing.T) {
	SetForkDustHeight(0)
	t.Cleanup(func() { SetForkDustHeight(math.MaxUint64) })

	a, b := atHeight(0), atHeight(0)
	a.Credit("x", 1)
	b.Credit("x", 1)

	// Move one dust in b only.
	accB, _ := b.Get("x")
	b.mu.Lock()
	b.accounts["x"].BalanceDust = accB.BalanceDust + 1
	b.mu.Unlock()

	if a.StateRoot() == b.StateRoot() {
		t.Fatal("post-fork state root must distinguish a one-dust difference")
	}
}

func TestMigrateToDust_flooringIsNonInflationary(t *testing.T) {
	as := NewAccountStore()
	// 1.000000005 CELL = 100000000.5 dust -> floors to 100000000.
	as.Credit("alice", 1.000000005)

	dropped, err := as.MigrateToDust()
	if err != nil {
		t.Fatalf("MigrateToDust: %v", err)
	}
	acc, _ := as.Get("alice")
	if acc.BalanceDust != 100_000_000 {
		t.Fatalf("floored balance: want 100000000 dust, got %d", acc.BalanceDust)
	}
	if acc.BalanceDust > floorToDust(1.000000005)+1 {
		t.Fatal("migration must never round up")
	}
	_ = dropped

	if !as.DustMigrated() {
		t.Fatal("store should report itself migrated")
	}
	// Idempotent.
	if _, err := as.MigrateToDust(); err != nil {
		t.Fatalf("second migration should be a no-op: %v", err)
	}
	acc2, _ := as.Get("alice")
	if acc2.BalanceDust != acc.BalanceDust {
		t.Fatal("re-migrating must not change balances")
	}
}

// The historical genesis allocation cannot be migrated: 1e15 CELL is 1e23
// dust, far outside uint64. Refusing loudly is the only safe option — a
// silent clamp would destroy ~1e23 dust of claimed supply.
func TestMigrateToDust_refusesUnrepresentableGenesisAllocation(t *testing.T) {
	as := NewAccountStore()
	as.Credit("qsdm-system-funder", 1e15)

	_, err := as.MigrateToDust()
	if err == nil {
		t.Fatal("migrating a 1e15 CELL allocation must fail rather than clamp")
	}
	if !strings.Contains(err.Error(), "re-derived") {
		t.Fatalf("error should tell the operator to re-derive genesis, got: %v", err)
	}
	if as.DustMigrated() {
		t.Fatal("a failed migration must not mark the store migrated")
	}
}

func TestMigrateToDust_failureIsAtomic(t *testing.T) {
	as := NewAccountStore()
	// Sorted iteration sees aaa first. The later overflow must not leave aaa
	// partially converted when the transition fails.
	as.Credit("aaa", 1.25)
	as.Credit("zzz", 1e15)
	beforeSmall, _ := as.Get("aaa")
	beforeLarge, _ := as.Get("zzz")

	if _, err := as.MigrateToDust(); err == nil {
		t.Fatal("expected the unrepresentable balance to reject migration")
	}
	afterSmall, _ := as.Get("aaa")
	afterLarge, _ := as.Get("zzz")
	if *afterSmall != *beforeSmall || *afterLarge != *beforeLarge {
		t.Fatalf("failed migration mutated accounts: small=%+v large=%+v", afterSmall, afterLarge)
	}
	if as.DustMigrated() {
		t.Fatal("failed migration must not mark the store migrated")
	}
}

func TestAccountStore_cloneAndRestorePreserveDustMigrationState(t *testing.T) {
	SetForkDustHeight(100)
	t.Cleanup(func() { SetForkDustHeight(math.MaxUint64) })

	as := NewAccountStore()
	as.Credit("alice", 2)
	if _, err := as.MigrateToDust(); err != nil {
		t.Fatal(err)
	}
	as.SetHeightFn(func() uint64 { return 100 })

	clone := as.Clone()
	if !clone.DustMigrated() || !clone.dustActive() {
		t.Fatal("clone lost the migrated marker or fork height source")
	}

	restored := NewAccountStore()
	restored.RestoreFrom(clone)
	if !restored.DustMigrated() {
		t.Fatal("rollback restore lost the migrated marker")
	}
}

// Migration is deterministic: the same input state yields the same result on
// every node, which is what makes a coordinated fork safe.
func TestMigrateToDust_deterministic(t *testing.T) {
	build := func() *AccountStore {
		as := NewAccountStore()
		as.Credit("c", 3.000000007)
		as.Credit("a", 1.000000005)
		as.Credit("b", 2.000000009)
		return as
	}
	x, y := build(), build()
	if _, err := x.MigrateToDust(); err != nil {
		t.Fatal(err)
	}
	if _, err := y.MigrateToDust(); err != nil {
		t.Fatal(err)
	}

	SetForkDustHeight(0)
	t.Cleanup(func() { SetForkDustHeight(math.MaxUint64) })
	x.SetHeightFn(func() uint64 { return 0 })
	y.SetHeightFn(func() uint64 { return 0 })

	if x.StateRoot() != y.StateRoot() {
		t.Fatal("migration must be deterministic across nodes")
	}
}

// The float mirror stays consistent with authoritative dust so JSON, APIs
// and AllAccounts do not disagree with consensus state.
func TestMigrateToDust_keepsFloatMirrorConsistent(t *testing.T) {
	as := NewAccountStore()
	as.Credit("alice", 2.5)
	if _, err := as.MigrateToDust(); err != nil {
		t.Fatal(err)
	}
	acc, _ := as.Get("alice")
	if acc.Balance != DustToCell(acc.BalanceDust) {
		t.Fatalf("float mirror %v disagrees with dust %d", acc.Balance, acc.BalanceDust)
	}
	if acc.Balance != 2.5 {
		t.Fatalf("mirror should still read 2.5, got %v", acc.Balance)
	}
}
