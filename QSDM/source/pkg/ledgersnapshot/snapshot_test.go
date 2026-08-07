package ledgersnapshot

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/chain"
)

func TestCaptureCopiesRequiredFilesAndWritesPrivateEvidence(t *testing.T) {
	stateDir := writeFixture(t, requiredFiles)
	outDir := filepath.Join(t.TempDir(), "snapshot")
	now := time.Date(2026, time.August, 8, 1, 2, 3, 4, time.UTC)

	got, err := Capture(Options{StateDir: stateDir, OutDir: outDir, Now: now})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if !got.ValidatorStoppedVerified || got.CreatedAt != now.Format(time.RFC3339Nano) {
		t.Fatalf("unexpected manifest header: %+v", got)
	}
	if len(got.Files) != len(requiredFiles) {
		t.Fatalf("files=%d, want %d", len(got.Files), len(requiredFiles))
	}
	for i, name := range requiredFiles {
		source, err := os.ReadFile(filepath.Join(stateDir, name))
		if err != nil {
			t.Fatal(err)
		}
		copied, err := os.ReadFile(filepath.Join(outDir, name))
		if err != nil {
			t.Fatal(err)
		}
		if string(copied) != string(source) {
			t.Fatalf("copied %s does not match source", name)
		}
		sum := sha256.Sum256(source)
		if got.Files[i].Name != name || got.Files[i].SizeBytes != int64(len(source)) || got.Files[i].SHA256 != hex.EncodeToString(sum[:]) {
			t.Fatalf("unexpected evidence for %s: %+v", name, got.Files[i])
		}
	}

	raw, err := os.ReadFile(filepath.Join(outDir, manifestFileName))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var persisted Manifest
	if err := json.Unmarshal(raw, &persisted); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	if persisted.CreatedAt != got.CreatedAt || len(persisted.Files) != len(got.Files) {
		t.Fatalf("persisted manifest differs: %+v", persisted)
	}
	if strings.Contains(string(raw), stateDir) || strings.Contains(string(raw), outDir) {
		t.Fatal("manifest leaked a local filesystem path")
	}
}

func TestCaptureRefusesRunningValidator(t *testing.T) {
	stateDir := writeFixture(t, requiredFiles)
	lock, err := chain.AcquireStateLock(filepath.Join(stateDir, stateLockFileName))
	if err != nil {
		t.Fatalf("acquire fixture lock: %v", err)
	}
	defer lock.Close()
	outDir := filepath.Join(t.TempDir(), "snapshot")

	_, err = Capture(Options{StateDir: stateDir, OutDir: outDir})
	if err == nil || !strings.Contains(err.Error(), "validator must be stopped") {
		t.Fatalf("Capture error=%v, want stopped-validator refusal", err)
	}
	if _, statErr := os.Stat(outDir); !os.IsNotExist(statErr) {
		t.Fatalf("destination should not exist after refusal, stat error=%v", statErr)
	}
}

func TestCaptureRefusesExistingDestination(t *testing.T) {
	stateDir := writeFixture(t, requiredFiles)
	outDir := t.TempDir()
	marker := filepath.Join(outDir, "keep.txt")
	if err := os.WriteFile(marker, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Capture(Options{StateDir: stateDir, OutDir: outDir})
	if err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("Capture error=%v, want overwrite refusal", err)
	}
	if raw, readErr := os.ReadFile(marker); readErr != nil || string(raw) != "keep" {
		t.Fatalf("existing destination changed: data=%q error=%v", raw, readErr)
	}
}

func TestCaptureMissingRequiredFileLeavesNoDestination(t *testing.T) {
	stateDir := writeFixture(t, requiredFiles[:len(requiredFiles)-1])
	outDir := filepath.Join(t.TempDir(), "snapshot")

	_, err := Capture(Options{StateDir: stateDir, OutDir: outDir})
	if err == nil || !strings.Contains(err.Error(), requiredFiles[len(requiredFiles)-1]) {
		t.Fatalf("Capture error=%v, want missing required file", err)
	}
	if _, statErr := os.Stat(outDir); !os.IsNotExist(statErr) {
		t.Fatalf("partial destination should be removed, stat error=%v", statErr)
	}
}

func TestCaptureRejectsOverlappingDirectories(t *testing.T) {
	stateDir := writeFixture(t, requiredFiles)
	outDir := filepath.Join(stateDir, "snapshot")

	_, err := Capture(Options{StateDir: stateDir, OutDir: outDir})
	if err == nil || !strings.Contains(err.Error(), "must be disjoint") {
		t.Fatalf("Capture error=%v, want overlap refusal", err)
	}
}

func writeFixture(t *testing.T, names []string) string {
	t.Helper()
	dir := t.TempDir()
	for i, name := range names {
		data := []byte(name + ":fixture:" + string(rune('a'+i)) + "\n")
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}
