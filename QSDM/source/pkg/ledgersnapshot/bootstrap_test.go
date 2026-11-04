package ledgersnapshot

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func captureFixture(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "snapshot")
	if _, err := Capture(Options{StateDir: writeFixture(t, requiredFiles), OutDir: out}); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestStageVerifiedCopiesExactBytes(t *testing.T) {
	snap := captureFixture(t)
	stage := filepath.Join(t.TempDir(), "stage")
	m, err := StageVerified(snap, stage)
	if err != nil {
		t.Fatalf("StageVerified: %v", err)
	}
	for _, f := range m.Files {
		sum, n, err := FileSHA256(filepath.Join(stage, f.Name))
		if err != nil || sum != f.SHA256 || n != f.SizeBytes {
			t.Fatalf("staged %s: %s %d %v", f.Name, sum, n, err)
		}
	}
	if _, err := StageVerified(snap, stage); err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("second stage into the same directory: %v", err)
	}
}

func TestStageVerifiedRefusesMismatchAndCleansUp(t *testing.T) {
	snap := captureFixture(t)
	f, err := os.OpenFile(filepath.Join(snap, requiredFiles[1]), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("x")
	_ = f.Close()
	stage := filepath.Join(t.TempDir(), "stage")
	if _, err := StageVerified(snap, stage); err == nil || !strings.Contains(err.Error(), "does not match the manifest") {
		t.Fatalf("StageVerified error = %v", err)
	}
	if _, err := os.Lstat(stage); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staging directory left behind: %v", err)
	}
}

func TestReadManifestIsStrict(t *testing.T) {
	snap := captureFixture(t)
	path := filepath.Join(snap, manifestFileName)
	good, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for name, edit := range map[string]func(string) string{
		"unknown field":  func(s string) string { return strings.Replace(s, "{", `{"extra":1,`, 1) },
		"schema version": func(s string) string { return strings.Replace(s, `"schema_version": 1`, `"schema_version": 2`, 1) },
		"not stopped": func(s string) string {
			return strings.Replace(s, `"validator_stopped_verified": true`, `"validator_stopped_verified": false`, 1)
		},
		"renamed file":  func(s string) string { return strings.Replace(s, requiredFiles[0], "other.json", 1) },
		"trailing data": func(s string) string { return s + "{}" },
		"malformed sha": func(s string) string { return strings.Replace(s, `"sha256": "`, `"sha256": "zz`, 1) },
	} {
		if err := os.WriteFile(path, []byte(edit(string(good))), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadManifest(snap); err == nil {
			t.Fatalf("%s: manifest accepted", name)
		}
	}
	if err := os.WriteFile(path, good, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadManifest(snap); err != nil {
		t.Fatalf("restored manifest: %v", err)
	}
}

func TestCheckpointRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if _, ok, err := ReadCheckpoint(dir); ok || err != nil {
		t.Fatalf("missing checkpoint: ok=%v err=%v", ok, err)
	}
	cp := Checkpoint{Height: 7, BlockHash: strings.Repeat("a", 64), StateRoot: strings.Repeat("b", 64), ProducerID: "p", TipSigned: true, Blocks: 8, JournalBytes: 99}
	if err := WriteCheckpoint(dir, cp); err != nil {
		t.Fatal(err)
	}
	if err := WriteCheckpoint(dir, cp); err == nil {
		t.Fatal("checkpoint overwritten")
	}
	got, ok, err := ReadCheckpoint(dir)
	if err != nil || !ok || got != cp {
		t.Fatalf("read back %+v ok=%v err=%v", got, ok, err)
	}
}
