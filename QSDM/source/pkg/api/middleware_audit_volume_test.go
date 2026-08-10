package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/internal/logging"
)

func TestIsHighVolumeAuditPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		path string
		want bool
	}{
		{path: "/api/v1/mining/challenge", want: true},
		{path: "/api/v1/mining/work", want: true},
		{path: "/api/v1/mining/submit", want: true},
		{path: "/api/v1/mining/account", want: false},
		{path: "/api/v1/status", want: false},
		{path: "/api/v1/mining/work/extra", want: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.path, func(t *testing.T) {
			t.Parallel()
			if got := isHighVolumeAuditPath(tt.path); got != tt.want {
				t.Fatalf("isHighVolumeAuditPath(%q) = %t, want %t", tt.path, got, tt.want)
			}
		})
	}
}

func TestAuditLogMiddlewareHighVolumeSeverity(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		wantLevel  string
		wantLogged bool
	}{
		{
			name:       "success stays below info",
			statusCode: http.StatusOK,
			wantLogged: false,
		},
		{
			name:       "client failure remains visible",
			statusCode: http.StatusBadRequest,
			wantLevel:  `"level":"WARN"`,
			wantLogged: true,
		},
		{
			name:       "server failure remains visible",
			statusCode: http.StatusInternalServerError,
			wantLevel:  `"level":"ERROR"`,
			wantLogged: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			logOutput := auditLogForRequest(
				t,
				"/api/v1/mining/work",
				tt.statusCode,
			)
			logged := strings.Contains(logOutput, `"msg":"API response"`)
			if logged != tt.wantLogged {
				t.Fatalf("API response logged = %t, want %t; output=%q", logged, tt.wantLogged, logOutput)
			}
			if tt.wantLevel != "" && !strings.Contains(logOutput, tt.wantLevel) {
				t.Fatalf("output does not contain %q: %q", tt.wantLevel, logOutput)
			}
		})
	}
}

func TestAuditLogMiddlewareRegularSuccessRemainsInfo(t *testing.T) {
	logOutput := auditLogForRequest(t, "/api/v1/status", http.StatusOK)
	if !strings.Contains(logOutput, `"level":"INFO"`) {
		t.Fatalf("regular API response was not logged at INFO: %q", logOutput)
	}
	if !strings.Contains(logOutput, `"msg":"API response"`) {
		t.Fatalf("regular API response log is missing: %q", logOutput)
	}
}

func auditLogForRequest(t *testing.T, path string, statusCode int) string {
	t.Helper()

	logPath := filepath.Join(t.TempDir(), "audit.log")
	logger := logging.NewLoggerWithLevel(logPath, true, "INFO")
	handler := AuditLogMiddleware(logger)(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(statusCode)
		},
	))

	request := httptest.NewRequest(http.MethodGet, path, nil)
	handler.ServeHTTP(httptest.NewRecorder(), request)
	if err := logger.Close(); err != nil {
		t.Fatalf("close logger: %v", err)
	}

	output, err := os.ReadFile(logPath)
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	return string(output)
}
