package chain

import (
	"reflect"
	"strings"
	"testing"
)

// fakeBonded is a ValidatorMembershipSource backed by a literal map.
type fakeBonded map[string]float64

func (f fakeBonded) BondedByValidator() map[string]float64 {
	out := make(map[string]float64, len(f))
	for k, v := range f {
		out[k] = v
	}
	return out
}

func testCfg() ValidatorSetConfig {
	c := DefaultValidatorSetConfig()
	c.MinStake = 100
	c.MaxValidators = 10
	return c
}

// TestDeriveValidatorMembership_isDeterministic is the property the whole fix
// rests on.
//
// Membership used to be built locally — Register("bootstrap") plus the node's
// OWN wallet address — so node A believed the set was {bootstrap, A} and node
// B believed {bootstrap, B}. Two peers never agreed on who the validators
// were, so a 2/3-of-stake quorum could not be computed across them and no
// home node could ever become a canonical validator.
//
// Derivation must therefore be a pure function of committed state: same
// bonded stake in, byte-identical membership out, on every node.
func TestDeriveValidatorMembership_isDeterministic(t *testing.T) {
	bonded := map[string]float64{
		"alice": 500,
		"bob":   500, // tie with alice -> must break on address, not map order
		"carol": 900,
		"dave":  50, // below MinStake
	}

	first := DeriveValidatorMembership(testCfg(), bonded)

	// Re-derive many times: Go randomises map iteration order, so a
	// non-deterministic implementation shows up quickly here.
	for i := 0; i < 200; i++ {
		got := DeriveValidatorMembership(testCfg(), bonded)
		if !reflect.DeepEqual(got, first) {
			t.Fatalf("derivation must be deterministic; got %v then %v", first, got)
		}
	}

	want := []string{"carol", "alice", "bob"} // stake desc, then address asc
	if !reflect.DeepEqual(first, want) {
		t.Fatalf("membership = %v, want %v", first, want)
	}
}

// Two independently-constructed nodes reading the same committed state must
// agree exactly. This is the cross-node property that was broken.
func TestValidatorSetFromChainState_nodesAgree(t *testing.T) {
	state := fakeBonded{"alice": 300, "bob": 250, "carol": 100}

	nodeA, err := ValidatorSetFromChainState(testCfg(), state)
	if err != nil {
		t.Fatalf("node A: %v", err)
	}
	nodeB, err := ValidatorSetFromChainState(testCfg(), state)
	if err != nil {
		t.Fatalf("node B: %v", err)
	}

	a := nodeA.RegisteredAddresses()
	b := nodeB.RegisteredAddresses()
	if len(a) != len(b) {
		t.Fatalf("sets differ in size: %v vs %v", a, b)
	}
	seen := map[string]bool{}
	for _, addr := range a {
		seen[addr] = true
	}
	for _, addr := range b {
		if !seen[addr] {
			t.Fatalf("node B has %s which node A does not; sets: %v vs %v", addr, a, b)
		}
	}

	// And stake weights must match, or quorum arithmetic diverges.
	for _, addr := range a {
		va, _ := nodeA.GetValidator(addr)
		vb, _ := nodeB.GetValidator(addr)
		if va.Stake != vb.Stake {
			t.Fatalf("stake for %s differs: %v vs %v", addr, va.Stake, vb.Stake)
		}
	}
}

// Below-minimum stake must not confer membership.
func TestDeriveValidatorMembership_enforcesMinStake(t *testing.T) {
	got := DeriveValidatorMembership(testCfg(), map[string]float64{
		"rich": 100, // exactly at the minimum -> admitted
		"poor": 99.9,
	})
	if len(got) != 1 || got[0] != "rich" {
		t.Fatalf("only addresses at or above MinStake qualify, got %v", got)
	}
}

func TestDeriveValidatorMembership_respectsMaxValidators(t *testing.T) {
	cfg := testCfg()
	cfg.MaxValidators = 2
	got := DeriveValidatorMembership(cfg, map[string]float64{
		"a": 900, "b": 800, "c": 700, "d": 600,
	})
	if len(got) != 2 {
		t.Fatalf("expected 2 validators, got %v", got)
	}
	// Highest stake wins the seats.
	if got[0] != "a" || got[1] != "b" {
		t.Fatalf("seats should go to the highest stake, got %v", got)
	}
}

// An empty set must be a loud failure, not a silent one: a node running with
// no validators accepts anything and finalizes nothing.
func TestValidatorSetFromChainState_refusesEmptySet(t *testing.T) {
	_, err := ValidatorSetFromChainState(testCfg(), fakeBonded{"broke": 1})
	if err == nil {
		t.Fatal("deriving an empty validator set must be an error")
	}
	if !strings.Contains(err.Error(), "minimum bonded stake") {
		t.Fatalf("error should explain why nobody qualified, got %v", err)
	}
}

// A single sufficiently-bonded node IS a valid set. This is the property that
// lets one home PC keep the chain alive when everything else is offline.
func TestValidatorSetFromChainState_singleHomeNodeQualifies(t *testing.T) {
	vs, err := ValidatorSetFromChainState(testCfg(), fakeBonded{"home-pc": 100})
	if err != nil {
		t.Fatalf("a single bonded node must form a valid set: %v", err)
	}
	if vs.Size() != 1 {
		t.Fatalf("expected a 1-member set, got %d", vs.Size())
	}
	active := vs.ActiveValidators()
	if len(active) != 1 || active[0].Address != "home-pc" {
		t.Fatalf("the home node must be the active validator, got %v", active)
	}
}

// Membership tracks state over time: new stake admits, lost stake exits.
func TestReconcileValidatorMembership_admitsAndExits(t *testing.T) {
	vs, err := ValidatorSetFromChainState(testCfg(), fakeBonded{"alice": 300, "bob": 200})
	if err != nil {
		t.Fatal(err)
	}

	// bob unbonds below the minimum; carol bonds in.
	admitted, exited, err := ReconcileValidatorMembership(vs, fakeBonded{
		"alice": 300,
		"bob":   10,
		"carol": 500,
	})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(admitted) != 1 || admitted[0] != "carol" {
		t.Fatalf("carol should be admitted, got %v", admitted)
	}
	if len(exited) != 1 || exited[0] != "bob" {
		t.Fatalf("bob should be exited, got %v", exited)
	}

	// Surviving members must be re-weighted to current stake.
	if v, ok := vs.GetValidator("carol"); !ok || v.Stake != 500 {
		t.Fatalf("carol should be registered with stake 500, got %+v", v)
	}
}

// Reconciling to nothing must be refused: a transient read that admits
// nobody must not silently disband consensus.
func TestReconcileValidatorMembership_refusesToEmptyTheSet(t *testing.T) {
	vs, err := ValidatorSetFromChainState(testCfg(), fakeBonded{"alice": 300})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReconcileValidatorMembership(vs, fakeBonded{}); err == nil {
		t.Fatal("reconciling to an empty set must be refused")
	}
	if vs.Size() == 0 {
		t.Fatal("the existing set must be left intact when reconcile refuses")
	}
}

// The real StakingLedger must satisfy the source contract, since it is the
// production origin of bonded stake.
func TestStakingLedger_isAMembershipSource(t *testing.T) {
	as := NewAccountStore()
	as.Credit("delegator", 1000)
	sl := NewStakingLedger()

	if err := sl.Delegate(as, "delegator", "home-pc", 250); err != nil {
		t.Fatalf("Delegate: %v", err)
	}

	bonded := sl.BondedByValidator()
	if bonded["home-pc"] != 250 {
		t.Fatalf("ledger should report 250 bonded to home-pc, got %v", bonded)
	}

	// Mutating the returned map must not corrupt ledger state.
	bonded["home-pc"] = 99999
	if sl.DelegatedPower("home-pc") != 250 {
		t.Fatal("BondedByValidator must return a copy, not aliased state")
	}
}
