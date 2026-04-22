package chain

import (
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

func TestStakingLedger_DelegateAndWeight(t *testing.T) {
	as := NewAccountStore()
	as.Credit("del", 500)
	sl := NewStakingLedger()
	if err := sl.Delegate(as, "del", "val", 50); err != nil {
		t.Fatal(err)
	}
	if sl.DelegatedPower("val") != 50 {
		t.Fatalf("delegated power: %v", sl.DelegatedPower("val"))
	}
	acc, _ := as.Get("del")
	if acc.Balance != 450 {
		t.Fatalf("delegator balance: %v", acc.Balance)
	}
}

func TestStakingLedger_UnbondMaturity(t *testing.T) {
	as := NewAccountStore()
	as.Credit("del", 100)
	sl := NewStakingLedger()
	_ = sl.Delegate(as, "del", "val", 40)
	if err := sl.BeginUnbond(as, "del", "val", 40, 0, 1); err != nil {
		t.Fatal(err)
	}
	if sl.DelegatedPower("val") != 0 {
		t.Fatal("power should drop immediately")
	}
	sl.ProcessCommittedHeight(as, 1, "root-1")
	acc, _ := as.Get("del")
	if acc.Balance != 100 {
		t.Fatalf("expected full refund at maturity, balance %v", acc.Balance)
	}
}

func TestSyncValidatorStakesFromCommittedTip_MergesDelegation(t *testing.T) {
	pid := "producer-1"
	as := NewAccountStore()
	as.Credit(pid, 10_000)
	as.Credit("fan", 5000)
	vs := NewValidatorSet(DefaultValidatorSetConfig())
	if err := vs.Register(pid, 100); err != nil {
		t.Fatal(err)
	}
	pool := mempool.New(mempool.DefaultConfig())
	cfg := DefaultProducerConfig()
	cfg.ProducerID = pid
	bp := NewBlockProducer(pool, as, cfg)
	add := func(id string, n uint64) {
		t.Helper()
		_ = pool.Add(&mempool.Tx{ID: id, Sender: pid, Recipient: "bob", Amount: 1, Fee: 0.01, Nonce: n, GasLimit: 21_000})
	}
	add("t0", 0)
	if _, err := bp.ProduceBlock(); err != nil {
		t.Fatal(err)
	}
	add("t1", 1)
	if _, err := bp.ProduceBlock(); err != nil {
		t.Fatal(err)
	}
	sl := NewStakingLedger()
	if err := sl.Delegate(as, "fan", pid, 100); err != nil {
		t.Fatal(err)
	}
	SyncValidatorStakesFromCommittedTip(vs, as, bp, sl)
	v, _ := vs.GetValidator(pid)
	if v.Stake < 100+100 {
		t.Fatalf("expected delegation merged into stake, got %v", v.Stake)
	}
}

func TestStakingLedger_SlashDelegated(t *testing.T) {
	as := NewAccountStore()
	as.Credit("d", 100)
	sl := NewStakingLedger()
	_ = sl.Delegate(as, "d", "v", 80)
	sl.SlashDelegated("v", 0.25)
	if sl.DelegatedPower("v") != 60 {
		t.Fatalf("after slash want 60, got %v", sl.DelegatedPower("v"))
	}
}
