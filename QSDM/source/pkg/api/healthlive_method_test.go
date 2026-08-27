package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The compose healthchecks probe /api/v1/health/live. wget --spider issues a
// HEAD; HealthLive rejects non-GET. Pin both directions so a probe cannot be
// written against a method this handler refuses.
func TestHealthLive_MethodsThatProbesUse(t *testing.T) {
	h := &Handlers{}
	for method, want := range map[string]int{
		http.MethodGet:  http.StatusOK,
		http.MethodHead: http.StatusMethodNotAllowed,
	} {
		rr := httptest.NewRecorder()
		h.HealthLive(rr, httptest.NewRequest(method, "/api/v1/health/live", nil))
		if rr.Code != want {
			t.Errorf("%s /api/v1/health/live = %d, want %d", method, rr.Code, want)
		}
	}
}
