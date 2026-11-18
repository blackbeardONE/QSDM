package api

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
)

// 2026-11-18 report: /api/v1/audit/items showed absolute server paths (/opt/qsdm/host_key, /etc/systemd/..., ...).
// No served byte of the items list or the summary may carry one now.
var servedHostPathRE = regexp.MustCompile(`(^|[\s"'(\[=:,` + "`" + `])/(opt|root|var|etc|home|srv|usr|tmp|run|mnt|data|proc|sys|boot|lib|media)/`)

func TestAuditEndpoints_ServeNoHostPath(t *testing.T) {
	resetAuditChecklistForTest()
	h := setupTestHandlers()
	for _, tc := range []struct {
		path string
		fn   func(http.ResponseWriter, *http.Request)
	}{
		{"/api/v1/audit/items", h.AuditItemsHandler},
		{"/api/v1/audit/summary", h.AuditSummaryHandler},
	} {
		w := httptest.NewRecorder()
		tc.fn(w, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d", tc.path, w.Code)
		}
		body := w.Body.String()
		if loc := servedHostPathRE.FindStringIndex(body); loc != nil {
			end := loc[1] + 40
			if end > len(body) {
				end = len(body)
			}
			t.Fatalf("%s serves a host path: %q", tc.path, body[loc[0]:end])
		}
	}
}
