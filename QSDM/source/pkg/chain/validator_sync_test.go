package chain

import "testing"

func TestSyncValidatorStakesFromAccountsIgnoresLiquidBalances(t *testing.T) {
	vs := NewValidatorSet(DefaultValidatorSetConfig())
	if err := vs.Register("v1", 150); err != nil {
		t.Fatal(err)
	}
	if err := vs.Register("v2", 200); err != nil {
		t.Fatal(err)
	}
	as := NewAccountStore()
	as.Credit("v1", 5000)
	as.Credit("v2", 0)
	if err := vs.SetStake("v1", 777); err != nil {
		t.Fatal(err)
	}
	if err := vs.SetStake("v2", 999); err != nil {
		t.Fatal(err)
	}

	SyncValidatorStakesFromAccounts(vs, as)

	a1, _ := vs.GetValidator("v1")
	a2, _ := vs.GetValidator("v2")
	if a1.Stake != 150 || a1.SelfStake != 150 {
		t.Fatalf("v1 stake = %+v, want effective and self stake 150", a1)
	}
	if a2.Stake != 200 || a2.SelfStake != 200 {
		t.Fatalf("v2 stake = %+v, want effective and self stake 200", a2)
	}
}

func TestSyncValidatorStakesFromAccountsWorksWithoutAccountStore(t *testing.T) {
	vs := NewValidatorSet(DefaultValidatorSetConfig())
	if err := vs.Register("v1", 125); err != nil {
		t.Fatal(err)
	}
	if err := vs.SetStake("v1", 900); err != nil {
		t.Fatal(err)
	}

	SyncValidatorStakesFromAccounts(vs, nil)

	v, _ := vs.GetValidator("v1")
	if v.Stake != 125 || v.SelfStake != 125 {
		t.Fatalf("stake = %+v, want reset to locked self stake 125", v)
	}
}
