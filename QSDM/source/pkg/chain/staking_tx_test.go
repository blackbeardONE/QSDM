package chain

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

func stakingTx(t *testing.T, sender string, p StakingPayload) *mempool.Tx {
	t.Helper()
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return &mempool.Tx{
		ID:         "stake-tx",
		Sender:     sender,
		ContractID: StakingContractID,
		Payload:    raw,
	}
}

// TestApplyStakingTx_bondingMakesAHomeNodeAValidator is the end-to-end
// property behind "QSDM keeps running as long as one peer is up".
//
// Validator membership is derived from bonded stake, but nothing could ever
// bond: StakingLedger.Delegate had zero production callers, so the ledger
// was permanently empty and membership always fell back to the node-local
// bootstrap pair. A qsdm/staking/v1 transaction closes that loop.
func TestApplyStakingTx_bondingMakesAHomeNodeAValidator(t *testing.T) {
	as := NewAccountStore()
	as.Credit("operator", 1000)
	sl := NewStakingLedger()

	cfg := DefaultValidatorSetConfig()
	cfg.MinStake = 100

	// Before bonding, nobody qualifies and the set would be empty.
	if _, err := ValidatorSetFromChainState(cfg, sl); err == nil {
		t.Fatal("precondition: no validator should qualify before any bond")
	}

	tx := stakingTx(t, "operator", StakingPayload{
		Action:    StakingActionDelegate,
		Validator: "home-pc",
		Amount:    250,
	})
	if err := ApplyStakingTx(sl, as, tx, 10); err != nil {
		t.Fatalf("ApplyStakingTx: %v", err)
	}

	vs, err := ValidatorSetFromChainState(cfg, sl)
	if err != nil {
		t.Fatalf("the bonded home node must now form a valid set: %v", err)
	}
	if vs.Size() != 1 {
		t.Fatalf("expected a 1-member set, got %d", vs.Size())
	}
	active := vs.ActiveValidators()
	if len(active) != 1 || active[0].Address != "home-pc" {
		t.Fatalf("home-pc must be the active validator, got %v", active)
	}

	// The operator's balance funded the bond.
	acct, _ := as.Get("operator")
	if acct.Balance != 750 {
		t.Fatalf("bond should debit the delegator: want 750, got %v", acct.Balance)
	}
}

// The delegator is always tx.Sender, never a payload field — otherwise
// anyone could bond someone else's balance.
func TestApplyStakingTx_bondsOnlyTheSendersFunds(t *testing.T) {
	as := NewAccountStore()
	as.Credit("victim", 1000)
	as.Credit("attacker", 0)
	sl := NewStakingLedger()

	// Attacker sends the tx; only the attacker's (empty) balance is at risk.
	tx := stakingTx(t, "attacker", StakingPayload{
		Action:    StakingActionDelegate,
		Validator: "attacker-node",
		Amount:    500,
	})
	if err := ApplyStakingTx(sl, as, tx, 1); err == nil {
		t.Fatal("bonding from an unfunded sender must fail")
	}

	victim, _ := as.Get("victim")
	if victim.Balance != 1000 {
		t.Fatalf("another account's funds must never be touched, got %v", victim.Balance)
	}
}

// Replaying the same block on two nodes must converge on identical state —
// this is what makes membership agree across peers.
func TestApplyStakingTx_isDeterministicAcrossNodes(t *testing.T) {
	build := func() (*AccountStore, *StakingLedger) {
		as := NewAccountStore()
		as.Credit("a", 1000)
		as.Credit("b", 1000)
		return as, NewStakingLedger()
	}

	apply := func(as *AccountStore, sl *StakingLedger) {
		for _, spec := range []struct {
			sender, validator string
			amount            float64
		}{
			{"a", "node-1", 300},
			{"b", "node-2", 300},
			{"a", "node-2", 200},
		} {
			tx := stakingTx(t, spec.sender, StakingPayload{
				Action: StakingActionDelegate, Validator: spec.validator, Amount: spec.amount,
			})
			if err := ApplyStakingTx(sl, as, tx, 5); err != nil {
				t.Fatalf("apply: %v", err)
			}
		}
	}

	asA, slA := build()
	asB, slB := build()
	apply(asA, slA)
	apply(asB, slB)

	cfg := DefaultValidatorSetConfig()
	cfg.MinStake = 100
	setA, err := ValidatorSetFromChainState(cfg, slA)
	if err != nil {
		t.Fatal(err)
	}
	setB, err := ValidatorSetFromChainState(cfg, slB)
	if err != nil {
		t.Fatal(err)
	}

	for _, addr := range setA.RegisteredAddresses() {
		va, okA := setA.GetValidator(addr)
		vb, okB := setB.GetValidator(addr)
		if !okA || !okB {
			t.Fatalf("node B missing validator %s", addr)
		}
		if va.Stake != vb.Stake {
			t.Fatalf("stake for %s diverged: %v vs %v", addr, va.Stake, vb.Stake)
		}
	}
	if setA.Size() != setB.Size() {
		t.Fatalf("set sizes diverged: %d vs %d", setA.Size(), setB.Size())
	}
}

// Unbonding drops voting power immediately; funds return only at maturity.
func TestApplyStakingTx_unbondDropsPowerImmediately(t *testing.T) {
	as := NewAccountStore()
	as.Credit("operator", 1000)
	sl := NewStakingLedger()

	if err := ApplyStakingTx(sl, as, stakingTx(t, "operator", StakingPayload{
		Action: StakingActionDelegate, Validator: "node", Amount: 400,
	}), 10); err != nil {
		t.Fatal(err)
	}
	if sl.DelegatedPower("node") != 400 {
		t.Fatalf("expected 400 bonded, got %v", sl.DelegatedPower("node"))
	}

	if err := ApplyStakingTx(sl, as, stakingTx(t, "operator", StakingPayload{
		Action: StakingActionUnbond, Validator: "node", Amount: 150,
	}), 20); err != nil {
		t.Fatalf("unbond: %v", err)
	}
	if sl.DelegatedPower("node") != 250 {
		t.Fatalf("voting power must drop immediately: want 250, got %v", sl.DelegatedPower("node"))
	}

	// Funds have NOT returned yet — they mature later.
	acct, _ := as.Get("operator")
	if acct.Balance != 600 {
		t.Fatalf("unbonded funds must not credit immediately, balance %v", acct.Balance)
	}
}

func TestApplyStakingTx_rejectsBadInput(t *testing.T) {
	as := NewAccountStore()
	as.Credit("operator", 1000)
	sl := NewStakingLedger()

	t.Run("unwired ledger", func(t *testing.T) {
		err := ApplyStakingTx(nil, as, stakingTx(t, "operator", StakingPayload{
			Action: StakingActionDelegate, Validator: "n", Amount: 1,
		}), 1)
		if !errors.Is(err, ErrStakingNotWired) {
			t.Fatalf("want ErrStakingNotWired, got %v", err)
		}
	})

	t.Run("unknown action", func(t *testing.T) {
		err := ApplyStakingTx(sl, as, stakingTx(t, "operator", StakingPayload{
			Action: "drain", Validator: "n", Amount: 1,
		}), 1)
		if !errors.Is(err, ErrStakingUnknownAction) {
			t.Fatalf("want ErrStakingUnknownAction, got %v", err)
		}
	})

	t.Run("non-positive amount", func(t *testing.T) {
		err := ApplyStakingTx(sl, as, stakingTx(t, "operator", StakingPayload{
			Action: StakingActionDelegate, Validator: "n", Amount: 0,
		}), 1)
		if !errors.Is(err, ErrStakingBadPayload) {
			t.Fatalf("want ErrStakingBadPayload, got %v", err)
		}
	})

	t.Run("missing validator", func(t *testing.T) {
		err := ApplyStakingTx(sl, as, stakingTx(t, "operator", StakingPayload{
			Action: StakingActionDelegate, Amount: 5,
		}), 1)
		if !errors.Is(err, ErrStakingBadPayload) {
			t.Fatalf("want ErrStakingBadPayload, got %v", err)
		}
	})

	// tx.Amount must be zero: Delegate debits the account itself, so a
	// transfer amount on the envelope would move funds twice.
	t.Run("double-spend shape", func(t *testing.T) {
		tx := stakingTx(t, "operator", StakingPayload{
			Action: StakingActionDelegate, Validator: "n", Amount: 5,
		})
		tx.Amount = 5
		if err := ApplyStakingTx(sl, as, tx, 1); !errors.Is(err, ErrStakingBadPayload) {
			t.Fatalf("a staking tx carrying tx.Amount must be refused, got %v", err)
		}
	})
}

// The applier must route the contract, not fall through to a plain transfer
// (which would ignore the payload and corrupt nonce ordering on replay).
func TestEnrollmentAwareApplier_routesStakingContract(t *testing.T) {
	as := NewAccountStore()
	as.Credit("operator", 1000)
	aware := NewEnrollmentAwareApplier(as, nil)
	aware.SetHeightFn(func() uint64 { return 7 })

	tx := stakingTx(t, "operator", StakingPayload{
		Action: StakingActionDelegate, Validator: "home-pc", Amount: 300,
	})

	// Without a ledger the tx must be refused, never silently applied.
	if err := aware.ApplyTx(tx); !errors.Is(err, ErrStakingNotWired) {
		t.Fatalf("want ErrStakingNotWired before wiring, got %v", err)
	}

	sl := NewStakingLedger()
	aware.SetStakingLedger(sl)
	if err := aware.ApplyTx(tx); err != nil {
		t.Fatalf("routed staking tx should apply: %v", err)
	}
	if sl.DelegatedPower("home-pc") != 300 {
		t.Fatalf("expected 300 bonded via the applier, got %v", sl.DelegatedPower("home-pc"))
	}
}
