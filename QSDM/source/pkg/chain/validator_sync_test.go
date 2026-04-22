package chain

import "testing"

func TestSyncValidatorStakesFromAccounts(t *testing.T) {
	vs := NewValidatorSet(DefaultValidatorSetConfig())
	if err := vs.Register("v1", 100); err != nil {
		t.Fatal(err)
	}
	if err := vs.Register("v2", 100); err != nil {
		t.Fatal(err)
	}
	as := NewAccountStore()
	as.Credit("v1", 5000)
	as.Credit("v2", 0)

	SyncValidatorStakesFromAccounts(vs, as)
	a1, _ := vs.GetValidator("v1")
	a2, _ := vs.GetValidator("v2")
	if a1.Stake != 5000 {
		t.Fatalf("v1 stake want 5000 got %v", a1.Stake)
	}
	min := DefaultValidatorSetConfig().MinStake
	if a2.Stake != min {
		t.Fatalf("v2 stake want min %v got %v", min, a2.Stake)
	}
}
