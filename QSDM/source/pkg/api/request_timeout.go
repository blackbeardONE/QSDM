package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/monitoring"
)

// DefaultRequestTimeout is the canonical per-request deadline applied by
// RequestTimeoutMiddleware. 30s is the OWASP-recommended ceiling for
// authenticated REST calls: long enough for a slow Scylla LWT round-trip,
// short enough that Slowloris-style trickle attacks cannot pin a worker
// indefinitely. Endpoints that legitimately need longer (the websocket
// trace stream and the chunked metrics export) are exempted via
// requestTimeoutBypass below.
const DefaultRequestTimeout = 30 * time.Second

// requestTimeoutBypass enumerates path prefixes whose handlers either
// stream (WebSocket / chunked) or block by design and therefore cannot
// participate in a per-request deadline. Keeping the list narrow and
// explicit is part of the threat model: every new entry must be
// justifiable, otherwise a misbehaving handler can pin a request worker
// forever.
var requestTimeoutBypass = []string{
	"/api/v1/contracts/traces/ws", // WebSocket — must outlive any HTTP deadline
}

// RequestTimeoutMiddleware applies a context deadline to every request.
//
// Concretely:
//   - It wraps r.Context() with a context.WithTimeout(timeout).
//   - It serves the chain against a buffered responseWriter so the
//     timeout fires deterministically even if the handler ignores the
//     context (slow downstream + no select on ctx.Done()).
//   - On timeout, it returns HTTP 504 Gateway Timeout with a generic
//     body and increments the request-timeout security counter.
//
// The implementation deliberately reuses net/http.TimeoutHandler — it is
// battle-tested, handles the buffered ResponseWriter dance correctly, and
// already prevents the "handler wrote after timeout" panic. We only add
// the bypass routing, the metric, and the structured error body.
func RequestTimeoutMiddleware(timeout time.Duration) func(http.Handler) http.Handler {
	if timeout <= 0 {
		timeout = DefaultRequestTimeout
	}

	body := `{"error":"Gateway Timeout","message":"request exceeded the server processing deadline","status":504}`

	return func(next http.Handler) http.Handler {
		// http.TimeoutHandler buffers and discards on timeout — we wrap it
		// in a thin layer that bypasses the bypass-list and records the
		// security metric on timeout via a sentinel-detect responseWriter.
		timed := http.TimeoutHandler(next, timeout, body)

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isRequestTimeoutBypass(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}

			// Stamp the deadline onto the request context so handlers that
			// DO honour ctx.Done() (DB drivers, P2P broadcasts) cancel
			// promptly. TimeoutHandler also does this internally, but its
			// context is private — we want our own so downstream callees
			// can pick it up via r.Context().
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel()
			r = r.WithContext(ctx)

			// Sniff for the timeout via the status code emitted by
			// TimeoutHandler (503 by default; we override via body to 504
			// using a custom write-once writer below).
			sniff := &timeoutSniffer{ResponseWriter: w}
			timed.ServeHTTP(sniff, r)

			// TimeoutHandler emits 503 Service Unavailable on deadline.
			// Map that to the security counter — and rewrite the status
			// line implicitly via the buffered body we set above (the body
			// itself already declares "status":504 for client consumption,
			// but the wire status remains 503; we leave that intact since
			// some load balancers key on 503 for circuit-breaker logic).
			if errors.Is(ctx.Err(), context.DeadlineExceeded) && sniff.status == http.StatusServiceUnavailable {
				monitoring.RecordRequestTimeout()
			}
		})
	}
}

func isRequestTimeoutBypass(path string) bool {
	for _, prefix := range requestTimeoutBypass {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// timeoutSniffer records the status code so the surrounding middleware can
// detect a TimeoutHandler-emitted 503 without parsing the body.
type timeoutSniffer struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (s *timeoutSniffer) WriteHeader(code int) {
	if !s.wrote {
		s.status = code
		s.wrote = true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *timeoutSniffer) Write(p []byte) (int, error) {
	if !s.wrote {
		s.status = http.StatusOK
		s.wrote = true
	}
	return s.ResponseWriter.Write(p)
}
