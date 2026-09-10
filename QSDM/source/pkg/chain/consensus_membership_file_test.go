package chain

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConsensusMembershipScheduleFileAcceptsStrictValidatedSchedule(t *testing.T) {
	first := testConsensusMembership(1)
	first.EffectiveHeight = 50
	second := testConsensusMembership(2)
	second.EffectiveHeight = 100
	document := ConsensusMembershipScheduleFile{
		SchemaVersion: ConsensusMembershipScheduleFileSchemaVersion,
		NetworkID:     first.NetworkID,
		Snapshots:     []ConsensusMembership{second, first},
	}
	path := writeConsensusMembershipJSON(t, document)

	schedule, err := LoadConsensusMembershipScheduleFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := schedule.NetworkID(), first.NetworkID; got != want {
		t.Fatalf("network ID = %q, want %q", got, want)
	}
	if got := schedule.SnapshotHeights(); len(got) != 2 || got[0] != 50 || got[1] != 100 {
		t.Fatalf("snapshot heights = %v, want [50 100]", got)
	}
}

func TestLoadConsensusMembershipScheduleFileRejectsAmbiguousOrUntrustedJSON(t *testing.T) {
	membership := testConsensusMembership(1)

	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "unknown field",
			raw:  `{"schema_version":1,"network_id":"qsdm-mainnet","snapshots":[],"unexpected":true}`,
			want: "unknown field",
		},
		{
			name: "trailing value",
			raw:  `{"schema_version":1,"network_id":"qsdm-mainnet","snapshots":[]} {}`,
			want: "multiple JSON values",
		},
		{
			name: "schema mismatch",
			raw:  mustMembershipScheduleJSON(t, ConsensusMembershipScheduleFile{SchemaVersion: 2, NetworkID: membership.NetworkID, Snapshots: []ConsensusMembership{membership}}),
			want: "schema_version 2",
		},
		{
			name: "document network mismatch",
			raw:  mustMembershipScheduleJSON(t, ConsensusMembershipScheduleFile{SchemaVersion: 1, NetworkID: "qsdm-other", Snapshots: []ConsensusMembership{membership}}),
			want: "does not match snapshot network",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "schedule.json")
			if err := os.WriteFile(path, []byte(test.raw), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadConsensusMembershipScheduleFile(path); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("LoadConsensusMembershipScheduleFile() error = %v, want substring %q", err, test.want)
			}
		})
	}

}

func TestLoadConsensusMembershipFileRejectsScheduleEnvelopeAndDirectory(t *testing.T) {
	membership := testConsensusMembership(1)
	scheduleRaw := mustMembershipScheduleJSON(t, ConsensusMembershipScheduleFile{
		SchemaVersion: 1,
		NetworkID:     membership.NetworkID,
		Snapshots:     []ConsensusMembership{membership},
	})
	path := filepath.Join(t.TempDir(), "schedule.json")
	if err := os.WriteFile(path, []byte(scheduleRaw), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConsensusMembershipFile(path); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("schedule envelope as membership error = %v, want unknown field", err)
	}
	if _, err := LoadConsensusMembershipFile(t.TempDir()); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("directory error = %v, want regular-file rejection", err)
	}
}

func TestLoadConsensusMembershipFileRejectsOversizeArtifact(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oversize-membership.json")
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), int(MaxConsensusMembershipFileBytes+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConsensusMembershipFile(path); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("LoadConsensusMembershipFile() error = %v, want size-limit rejection", err)
	}
}

func writeConsensusMembershipJSON(t *testing.T, value any) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "membership.json")
	if err := os.WriteFile(path, []byte(mustMembershipScheduleJSON(t, value)), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func mustMembershipScheduleJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
