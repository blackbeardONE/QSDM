package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// GET /api/v1/tasks/actions used to report the task-action log's file path on
// the node as "source", and both task-action read routes put the read error,
// which names that path, into their 500 body. Neither may show the path.

func TestTaskActionsListReportsALabelNotTheLogPath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "secret-node-dir")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "task-actions.ndjson")
	if err := os.WriteFile(logPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(qsdmTaskActionLogPathEnv, logPath)
	h := setupTestHandlers()

	rec := httptest.NewRecorder()
	h.QSDMTaskActionsListHandler(rec, httptest.NewRequest(http.MethodGet, "/api/v1/tasks/actions", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "secret-node-dir") {
		t.Fatalf("the answer shows the log's path: %s", rec.Body.String())
	}
	var got QSDMTaskActionsListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("answer is not JSON: %v", err)
	}
	if !got.Configured || got.Source != qsdmTaskActionLogSource || len(got.Actions) != 0 {
		t.Fatalf("answer %+v, want configured, source %q, no actions", got, qsdmTaskActionLogSource)
	}
}

func TestTaskActionReadErrorsDoNotShowTheLogPath(t *testing.T) {
	SetProductionMode(true)
	t.Cleanup(func() { SetProductionMode(false) })
	// A directory where the log file should be: opening works on some
	// systems and reading fails on all of them, so both routes answer 500.
	logPath := filepath.Join(t.TempDir(), "secret-node-dir")
	if err := os.MkdirAll(logPath, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(qsdmTaskActionLogPathEnv, logPath)
	h := setupTestHandlers()

	for _, tc := range []struct {
		name   string
		path   string
		handle func(http.ResponseWriter, *http.Request)
	}{
		{name: "list", path: "/api/v1/tasks/actions", handle: h.QSDMTaskActionsListHandler},
		{name: "one action", path: "/api/v1/tasks/actions/some-action", handle: h.QSDMTaskActionRouteHandler},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			tc.handle(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("status %d, want 500: %s", rec.Code, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), "secret-node-dir") {
				t.Fatalf("the error shows the log's path: %s", rec.Body.String())
			}
		})
	}
}
