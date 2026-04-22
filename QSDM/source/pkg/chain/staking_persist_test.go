package chain

import (
	"path/filepath"
	"testing"
)

func TestLoadOrNewStakingLedger_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "staking.json")
	sl := NewStakingLedger()
	sl.SetPersistPath(path)
	as := NewAccountStore()
	as.Credit("d1", 1000)
	as.Credit("d2", 1000)
	if err := sl.Delegate(as, "d1", "v9", 10); err != nil {
		t.Fatal(err)
	}
	if err := sl.Delegate(as, "d2", "v9", 5); err != nil {
		t.Fatal(err)
	}
	if err := SaveStakingLedger(sl, path); err != nil {
		t.Fatal(err)
	}
	sl2, err := LoadOrNewStakingLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	if sl2.DelegatedPower("v9") != 15 {
		t.Fatalf("delegated v9: %v", sl2.DelegatedPower("v9"))
	}
	if sl2.Bonded("d1", "v9") != 10 || sl2.Bonded("d2", "v9") != 5 {
		t.Fatalf("bond index d1=%v d2=%v", sl2.Bonded("d1", "v9"), sl2.Bonded("d2", "v9"))
	}
}
