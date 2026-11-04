package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/chain"
)

// fuzzSnapArgGiven reports whether v is one of args, or the value after the
// first "=" of one of them: the two ways a flag value can be written.
func fuzzSnapArgGiven(args []string, v string) bool {
	for _, a := range args {
		if a == v {
			return true
		}
		if i := strings.IndexByte(a, '='); i >= 0 && a[i+1:] == v {
			return true
		}
	}
	return false
}

// FuzzSnapshotArgs feeds the snapshot-mode argument parser any command line
// (its arguments separated by NUL bytes). Whatever the input:
//   - it never panics, and every refusal is the usage error;
//   - a command line it accepts also reaches it through main()'s dispatch
//     (snapshotModeRequested), names one mode in its first argument, and has
//     every path that mode needs and none of the other mode's;
//   - every accepted path was given on the command line, as an argument or
//     after "=" in one.
func FuzzSnapshotArgs(f *testing.F) {
	for _, args := range [][]string{
		{"--snapshot-export", "--state-dir", "/var/lib/qsdm", "--out", "/srv/snapshot"},
		{"-snapshot-export", "-state-dir=a", "-out=b=c"},
		{"--snapshot-import", "--from", "/srv/snapshot"},
		{"--snapshot-import", "--from=/srv/snapshot", "extra"},
		{"--snapshot-import", "--from", "a", "--from", "b"},
		{"--snapshot-import", "--from", "-x"},
		{"--snapshot-import", "--from", ""},
		{"--snapshot-import", "--", "--from", "a"},
		{"--snapshot-import", "-h"},
		{"--snapshot-import"},
		{"--snapshot-importx", "--from", "a"},
		{"--snapshot-export", "--from", "a"},
		{"---snapshot-export", "--out", "a", "--state-dir", "b"},
		{"snapshot-import", "--from", "--state-dir"},
		{"--version", "--snapshot-import"},
		{""},
	} {
		f.Add(strings.Join(args, "\x00"))
	}
	// Not vacuous: both documented command lines parse.
	for _, args := range [][]string{
		{"--snapshot-export", "--state-dir", "s", "--out", "o"},
		{"--snapshot-import", "--from", "d"},
	} {
		if _, err := snapshotParseArgs(args); err != nil {
			f.Fatalf("%q: %v", args, err)
		}
	}
	f.Fuzz(func(t *testing.T, line string) {
		args := strings.Split(line, "\x00")
		a, err := snapshotParseArgs(args)
		if err != nil {
			if !strings.HasPrefix(err.Error(), "usage: qsdm --snapshot-export ") {
				t.Fatalf("%q: the refusal is not the usage error: %v", args, err)
			}
			return
		}
		if !snapshotModeRequested(args) {
			t.Fatalf("%q parses as %+v, but main() would not dispatch it", args, a)
		}
		if strings.TrimLeft(args[0], "-") != a.Mode {
			t.Fatalf("%q parses as mode %q", args, a.Mode)
		}
		switch a.Mode {
		case snapshotExportMode:
			if a.StateDir == "" || a.OutDir == "" || a.From != "" {
				t.Fatalf("%q: export with %+v", args, a)
			}
		case snapshotImportMode:
			if a.From == "" || a.StateDir != "" || a.OutDir != "" {
				t.Fatalf("%q: import with %+v", args, a)
			}
		default:
			t.Fatalf("%q: accepted mode %q", args, a.Mode)
		}
		for _, v := range []string{a.StateDir, a.OutDir, a.From} {
			if v != "" && !fuzzSnapArgGiven(args[1:], v) {
				t.Fatalf("%q: the path %q was not given", args, v)
			}
		}
	})
}

// FuzzSnapshotStakingLedger feeds the import's staking-ledger check any file
// content, as a snapshot from an untrusted source can carry it. The check
// exists because the staking ledger is local state that the state root does
// not commit, so it may pass only a file that the node itself reads as an
// empty ledger. Whatever the input, a file the check accepts:
//   - loads with the node's own loader (chain.LoadOrNewStakingLedger) from
//     its own bytes. The loader falls back to a backup file when it cannot
//     decode them; a snapshot carries no backup, and the backup here holds a
//     bond, so a fallback shows;
//   - holds no bond and no unbonding entry once loaded (saved back with
//     chain.SaveStakingLedger and read).
func FuzzSnapshotStakingLedger(f *testing.F) {
	for _, s := range []string{
		`{"v":1,"bonds":[],"unbond":[]}`,
		"{\n  \"v\": 1,\n  \"bonds\": null,\n  \"unbond\": null\n}\n",
		`{"v":1}`,
		`{"v":1,"bonds":[{"d":"del","v":"val","a":5}],"unbond":[]}`,
		`{"v":1,"bonds":[],"unbond":[{"delegator":"del","amount":5,"mature_at":1}]}`,
		`{"V":1,"BONDS":[{"d":"del","v":"val","a":5}]}`,
		`{"v":1,"bonds":[{"d":"del","v":"val","a":5}],"bonds":[]}`,
		`{"v":1,"bonds":[],"unbond":[],"extra":1}`,
		`{"v":1,"bonds":[],"unbond":[]} {"v":1,"bonds":[{"d":"del","v":"val","a":5}],"unbond":[]}`,
		`{"v":1,"bonds":[],"unbond":[]}}`,
		`{"v":2,"bonds":[],"unbond":[]}`,
		`{"bonds":[],"unbond":[]}`,
		// Found by this target: a repeated key empties the list a raw decode
		// counts, but the node's typed decode fails on the bond it replaced.
		`{"v":1,"Bonds":[{"0":"","v":0}],"Bonds":[]}`,
		"null", "{}", "[]", "",
	} {
		f.Add([]byte(s))
	}
	dir := f.TempDir()
	in, saved := filepath.Join(dir, "qsdm_staking.json"), filepath.Join(dir, "saved.json")
	// Not vacuous: the empty ledger a node writes passes the check.
	if err := chain.SaveStakingLedger(chain.NewStakingLedger(), saved); err != nil {
		f.Fatal(err)
	}
	empty, err := os.ReadFile(saved)
	if err != nil {
		f.Fatal(err)
	}
	if err := snapshotCheckEmptyStaking(empty); err != nil {
		f.Fatalf("the empty ledger a node writes (%q) is refused: %v", empty, err)
	}
	f.Add(empty)
	if err := os.WriteFile(in+".last-good", []byte(`{"v":1,"bonds":[{"d":"backup-delegator","v":"backup-validator","a":1}],"unbond":[]}`), 0o600); err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if snapshotCheckEmptyStaking(data) != nil {
			return
		}
		if err := os.WriteFile(in, data, 0o600); err != nil {
			t.Fatal(err)
		}
		sl, err := chain.LoadOrNewStakingLedger(in)
		if err != nil {
			t.Fatalf("the check accepted %q, which the node cannot load: %v", data, err)
		}
		if err := chain.SaveStakingLedger(sl, saved); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(saved)
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Bonds  []json.RawMessage `json:"bonds"`
			Unbond []json.RawMessage `json:"unbond"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		if len(doc.Bonds) != 0 || len(doc.Unbond) != 0 {
			t.Fatalf("the check accepted %q, but the node loads %d bond(s) and %d unbonding entr(ies)", data, len(doc.Bonds), len(doc.Unbond))
		}
	})
}
