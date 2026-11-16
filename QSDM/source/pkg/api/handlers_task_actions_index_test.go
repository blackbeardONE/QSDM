package api

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func taskActionRec(id string) QSDMTaskActionRecord {
	return QSDMTaskActionRecord{
		ReceivedAt: "2026-11-16T00:00:00Z",
		Envelope: QSDMTaskActionEnvelope{
			ID: id, Sender: "alice", TaskID: "task-1", Action: "stake", Amount: 1, Nonce: 1,
			Timestamp: "2026-11-16T00:00:00Z",
		},
	}
}

// A submit (preflight + append) must not re-read the whole ledger;
// the ID index is built once and kept current by this process's own appends.
func TestTaskActionLedger_NoFullScanPerSubmit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "task-actions.ndjson")
	qsdmTaskActionLogMu.Lock()
	qsdmTaskActionIdx = qsdmTaskActionIDIndex{}
	qsdmTaskActionIndexScans = 0
	qsdmTaskActionLogMu.Unlock()

	if _, err := appendQSDMTaskAction(path, taskActionRec("seed")); err != nil {
		t.Fatal(err)
	}
	base := qsdmTaskActionIndexScans
	for i := 0; i < 200; i++ {
		id := fmt.Sprintf("a-%d", i)
		if r, err := inspectQSDMTaskActionLog(path, taskActionRec(id).Envelope); err != nil || r.Duplicate {
			t.Fatalf("preflight %s: %+v %v", id, r, err)
		}
		if r, err := appendQSDMTaskAction(path, taskActionRec(id)); err != nil || r.Duplicate {
			t.Fatalf("append %s: %+v %v", id, r, err)
		}
	}
	if got := qsdmTaskActionIndexScans - base; got != 0 {
		t.Fatalf("%d full ledger scans during 200 submits, want 0", got)
	}
	// Duplicates are still found, in the preflight and in the append.
	if r, _ := inspectQSDMTaskActionLog(path, taskActionRec("a-7").Envelope); !r.Duplicate {
		t.Fatal("preflight missed a duplicate")
	}
	if r, _ := appendQSDMTaskAction(path, taskActionRec("seed")); !r.Duplicate {
		t.Fatal("append missed a duplicate")
	}
	if n := countLines(t, path); n != 201 {
		t.Fatalf("%d records, want 201 (no duplicate written)", n)
	}
}

// A ledger changed outside this process (another writer, a restore) is
// re-read: its IDs count as duplicates.
func TestTaskActionLedger_ExternalChangeRebuildsIndex(t *testing.T) {
	path := filepath.Join(t.TempDir(), "task-actions.ndjson")
	if _, err := appendQSDMTaskAction(path, taskActionRec("one")); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(taskActionRec("outside"))
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(append(raw, '\n')); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if r, _ := inspectQSDMTaskActionLog(path, taskActionRec("outside").Envelope); !r.Duplicate {
		t.Fatal("an ID written outside the process was not seen")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if r, _ := inspectQSDMTaskActionLog(path, taskActionRec("one").Envelope); r.Duplicate {
		t.Fatal("a removed ledger still reports old IDs")
	}
}

func countLines(t *testing.T, path string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, c := range b {
		if c == '\n' {
			n++
		}
	}
	return n
}
