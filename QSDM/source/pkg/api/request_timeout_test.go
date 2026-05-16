package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/monitoring"
)

func TestRequestTimeout_FastHandlerPasses(t *testing.T) {
	h := RequestTimeoutMiddleware(50 * time.Millisecond)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("fast handler expected 200, got %d", w.Code)
	}
}

func TestRequestTimeout_SlowHandlerCancelled(t *testing.T) {
	monitoring.ResetSecurityMetricsForTest()

	// 20ms deadline vs 200ms sleep is a 10× margin. Previously this
	// test was ~40% flaky in isolation because the middleware gated
	// the metric increment on errors.Is(ctx.Err(), context.DeadlineExceeded),
	// which reflects the context's cancel-state — not whether the
	// deadline passed. TimeoutHandler emitted a 503 promptly on its
	// own derived child context, but our outer ctx's async cancel
	// had a microsecond window where ctx.Err() still returned nil.
	// The fix in request_timeout.go uses time.Now().After(deadline)
	// instead, which is race-free; this test pins the fix.
	h := RequestTimeoutMiddleware(20 * time.Millisecond)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Block until the per-request deadline fires. We don't read
		// ctx.Done() here because the middleware uses TimeoutHandler,
		// which buffers the response and unblocks the client on its own.
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("slow handler expected 503 from TimeoutHandler, got %d", w.Code)
	}
	if got := monitoring.RequestTimeoutCount(); got < 1 {
		t.Fatalf("expected request_timeout_total >= 1, got %d", got)
	}
}

func TestRequestTimeout_BypassPaths(t *testing.T) {
	called := false
	h := RequestTimeoutMiddleware(10 * time.Millisecond)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond) // would normally trip the deadline
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/contracts/traces/ws", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if !called {
		t.Fatal("bypass path was not invoked by the inner handler")
	}
	if w.Code != http.StatusOK {
		t.Fatalf("bypass path expected 200, got %d", w.Code)
	}
}
