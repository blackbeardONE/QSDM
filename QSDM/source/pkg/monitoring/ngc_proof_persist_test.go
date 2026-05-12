package monitoring

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// resetNGCProofState clears both the in-memory ring and the
// persister so each test starts from a known posture. Defer this
// from every test that touches either state slice.
func resetNGCProofState(t *testing.T) {
	t.Helper()
	ResetNGCProofsForTest()
	ResetNGCProofPersistForTest()
}

func TestNGCProofPersist_EmptyPathIsNoOp(t *testing.T) {
	resetNGCProofState(t)
	defer resetNGCProofState(t)

	if err := SetNGCProofPersistPath("", 0); err != nil {
		t.Fatalf("empty path should not error: %v", err)
	}
	if got := NGCProofPersistPath(); got != "" {
		t.Errorf("expected empty path, got %q", got)
	}
	if err := RecordNGCProofBundle(makeBundle("alpha", time.Now().UTC(), nil)); err != nil {
		t.Fatalf("record bundle: %v", err)
	}
	if got := NGCProofPersistRecordsOnDisk(); got != 0 {
		t.Errorf("records_on_disk = %d; want 0 (persistence disabled)", got)
	}
	if got := NGCProofPersistErrors(); got != 0 {
		t.Errorf("persist errors = %d; want 0 (persistence disabled, no I/O attempted)", got)
	}
}

func TestNGCProofPersist_RoundTrip(t *testing.T) {
	resetNGCProofState(t)
	defer resetNGCProofState(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "ngc_proofs.jsonl")
	if err := SetNGCProofPersistPath(path, 0); err != nil {
		t.Fatalf("set persist path: %v", err)
	}

	// Write three distinct bundles. After this the in-memory ring
	// and the JSONL file must agree on count + content.
	now := time.Now().UTC().Truncate(time.Second)
	bundles := []struct {
		node string
		ts   time.Time
	}{
		{"alpha", now.Add(-3 * time.Minute)},
		{"beta", now.Add(-2 * time.Minute)},
		{"gamma", now.Add(-1 * time.Minute)},
	}
	for _, b := range bundles {
		if err := RecordNGCProofBundle(makeBundle(b.node, b.ts, nil)); err != nil {
			t.Fatalf("record %s: %v", b.node, err)
		}
	}

	if got := NGCProofPersistRecordsOnDisk(); got != 3 {
		t.Errorf("records_on_disk = %d; want 3", got)
	}
	if got := NGCProofPersistErrors(); got != 0 {
		t.Errorf("persist errors = %d; want 0 on a clean tmpdir", got)
	}

	// Wipe the in-memory ring only — keep the file. Restoring from
	// disk must repopulate the ring with the same three bundles.
	ResetNGCProofsForTest()
	n, err := RestoreNGCProofsFromDisk()
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if n != 3 {
		t.Errorf("restored %d records; want 3", n)
	}
	rows := NGCProofDistinctByNodeID()
	if len(rows) != 3 {
		t.Errorf("post-restore distinct rows = %d; want 3", len(rows))
	}
	seen := map[string]bool{}
	for _, r := range rows {
		seen[r.NodeID] = true
	}
	for _, b := range bundles {
		if !seen[b.node] {
			t.Errorf("node %q missing after restore; rows=%+v", b.node, rows)
		}
	}

	// File on disk must be mode 0600 and have exactly 3 lines.
	if runtime.GOOS != "windows" {
		info, statErr := os.Stat(path)
		if statErr != nil {
			t.Fatalf("stat %q: %v", path, statErr)
		}
		if mode := info.Mode().Perm(); mode != 0o600 {
			t.Errorf("file mode = %o; want 0600", mode)
		}
	}
	raw, rerr := os.ReadFile(path)
	if rerr != nil {
		t.Fatalf("read file: %v", rerr)
	}
	lines := 0
	for _, b := range raw {
		if b == '\n' {
			lines++
		}
	}
	if lines != 3 {
		t.Errorf("file has %d newline-terminated lines; want 3 (raw=%q)", lines, raw)
	}
}

func TestNGCProofPersist_CorruptTailIsSkipped(t *testing.T) {
	resetNGCProofState(t)
	defer resetNGCProofState(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "ngc_proofs.jsonl")
	if err := SetNGCProofPersistPath(path, 0); err != nil {
		t.Fatalf("set persist path: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if err := RecordNGCProofBundle(makeBundle("alpha", now.Add(-2*time.Minute), nil)); err != nil {
		t.Fatalf("record alpha: %v", err)
	}
	if err := RecordNGCProofBundle(makeBundle("beta", now.Add(-1*time.Minute), nil)); err != nil {
		t.Fatalf("record beta: %v", err)
	}

	// Simulate a hard-kill mid-write: append a partial JSON line
	// without a trailing newline. The corruption-tolerant loader
	// must skip this line and still return the 2 good records.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open append: %v", err)
	}
	if _, err := f.WriteString(`{"received_at":"2026-05-12T05:00:00`); err != nil {
		t.Fatalf("write corrupt tail: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	ResetNGCProofsForTest()
	n, err := RestoreNGCProofsFromDisk()
	if err != nil {
		t.Fatalf("restore over corrupt tail: %v", err)
	}
	if n != 2 {
		t.Errorf("restored %d records; want 2 (the corrupt tail must be skipped)", n)
	}
}

func TestNGCProofPersist_CompactionKeepsBounded(t *testing.T) {
	resetNGCProofState(t)
	defer resetNGCProofState(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "ngc_proofs.jsonl")
	// Drive compaction with a small softCap so we don't need 32+
	// records in the test.
	const softCap = 4
	if err := SetNGCProofPersistPath(path, softCap); err != nil {
		t.Fatalf("set persist path: %v", err)
	}

	// Write 2 * softCap bundles. After the second softCap-mark a
	// compaction must fire and bring the file back to softCap
	// records.
	now := time.Now().UTC().Truncate(time.Second)
	for i := 0; i < 2*softCap; i++ {
		if err := RecordNGCProofBundle(makeBundle(fmt.Sprintf("node-%02d", i), now.Add(time.Duration(i)*time.Second), nil)); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}

	if got := NGCProofPersistRecordsOnDisk(); got != int64(softCap) {
		t.Errorf("records_on_disk = %d after compaction; want %d", got, softCap)
	}

	// Verify the file content matches by counting newline-terminated
	// records on disk.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	lines := 0
	for _, b := range raw {
		if b == '\n' {
			lines++
		}
	}
	if lines != softCap {
		t.Errorf("file has %d lines after compaction; want %d (raw=%q)", lines, softCap, raw)
	}
}

func TestNGCProofPersist_RestoreOverEmptyFileIsNoOp(t *testing.T) {
	resetNGCProofState(t)
	defer resetNGCProofState(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "ngc_proofs.jsonl")
	if err := SetNGCProofPersistPath(path, 0); err != nil {
		t.Fatalf("set persist path: %v", err)
	}
	n, err := RestoreNGCProofsFromDisk()
	if err != nil {
		t.Fatalf("restore empty file: %v", err)
	}
	if n != 0 {
		t.Errorf("restored %d from empty file; want 0", n)
	}
}

func TestNGCProofPersist_MissingParentIsActionableError(t *testing.T) {
	resetNGCProofState(t)
	defer resetNGCProofState(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "definitely-not-a-real-subdir", "ngc_proofs.jsonl")
	err := SetNGCProofPersistPath(path, 0)
	if err == nil {
		t.Fatalf("expected error for missing parent dir, got nil")
	}
	if !strings.Contains(err.Error(), "parent") {
		t.Errorf("error %q should mention parent directory", err.Error())
	}
}
