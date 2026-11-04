package chain

import (
	"os"
	"path/filepath"
	"testing"
)

// StakingLedgerEntries reads what LoadOrNewStakingLedger reads, and refuses
// exactly the files for which the loader falls back to its backup.
func TestStakingLedgerEntries(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.json")
	if err := SaveStakingLedger(NewStakingLedger(), empty); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(empty)
	if err != nil {
		t.Fatal(err)
	}
	if b, u, err := StakingLedgerEntries(data); err != nil || b != 0 || u != 0 {
		t.Fatalf("the empty ledger a node writes: %d %d %v", b, u, err)
	}
	for _, tc := range []struct {
		data      string
		bonds, ub int
	}{
		{`{"v":1,"bonds":[{"d":"del","v":"val","a":5}],"unbond":[]}`, 1, 0},
		{`{"v":1,"bonds":[],"unbond":[{"delegator":"del","amount":5,"mature_at":9}]}`, 0, 1},
		{`{"v":1,"bonds":[{"d":"del","v":"val","a":5}],"bonds":[]}`, 0, 0},
	} {
		if b, u, err := StakingLedgerEntries([]byte(tc.data)); err != nil || b != tc.bonds || u != tc.ub {
			t.Fatalf("%s: %d bond(s) %d unbonding %v, want %d %d", tc.data, b, u, err, tc.bonds, tc.ub)
		}
	}
	for _, bad := range []string{
		`{"v":1,"bonds":[],"unbond":[]} {"v":1}`,
		`{"v":2,"bonds":[],"unbond":[]}`,
		`{"bonds":[],"unbond":[]}`,
		`{"v":1,"bonds":[{"d":"del","v":0}],"bonds":[]}`,
		`null`,
		``,
	} {
		if _, _, err := StakingLedgerEntries([]byte(bad)); err == nil {
			t.Fatalf("%q decoded; LoadOrNewStakingLedger falls back to its backup for it", bad)
		}
		// The loader agrees: with no backup file it fails on the same bytes.
		p := filepath.Join(t.TempDir(), "qsdm_staking.json")
		if err := os.WriteFile(p, []byte(bad), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadOrNewStakingLedger(p); err == nil {
			t.Fatalf("%q: LoadOrNewStakingLedger loaded it without a backup", bad)
		}
	}
}
