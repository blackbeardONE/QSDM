package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The public task-action list names its source without the server's file path,
// and no query reads the whole log in one response.
func TestQSDMTaskActionsList_NoPathAndBoundedLimit(t *testing.T) {
	dir := t.TempDir()
	actionLog := filepath.Join(dir, "task-actions.jsonl")
	var b strings.Builder
	for i := 0; i < 650; i++ {
		rec := QSDMTaskActionRecord{
			ReceivedAt: "2026-11-14T00:00:00Z",
			Envelope:   QSDMTaskActionEnvelope{ID: fmt.Sprintf("action-%04d", i), Sender: "s", TaskID: "t", Action: "start"},
		}
		line, err := json.Marshal(rec)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(actionLog, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(qsdmTaskActionLogPathEnv, actionLog)
	h := &Handlers{}

	for _, tc := range []struct {
		query string
		want  int
	}{
		{"", 100},
		{"?limit=-1", 100},
		{"?limit=-999999", 100},
		{"?limit=0", 100},
		{"?limit=10", 10},
		{"?limit=5000", 500},
	} {
		rec := httptest.NewRecorder()
		h.QSDMTaskActionsListHandler(rec, httptest.NewRequest(http.MethodGet, "/api/v1/tasks/actions"+tc.query, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%q: status %d", tc.query, rec.Code)
		}
		body := rec.Body.String()
		if strings.Contains(body, dir) || strings.Contains(body, "task-actions.jsonl") {
			t.Fatalf("%q: the response discloses the log's file path", tc.query)
		}
		var resp QSDMTaskActionsListResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		if resp.Source != "operator-action-log" {
			t.Fatalf("%q: source %q, want the label operator-action-log", tc.query, resp.Source)
		}
		if len(resp.Actions) != tc.want {
			t.Fatalf("%q: %d actions, want %d", tc.query, len(resp.Actions), tc.want)
		}
	}
}
