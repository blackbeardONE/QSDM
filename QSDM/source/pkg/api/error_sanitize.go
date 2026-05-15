package api

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"os"
	"strings"
	"sync/atomic"

	"github.com/blackbeardONE/QSDM/internal/logging"
)

// productionMode is a process-wide flag toggled by SetProductionMode().
// When true, 5xx responses surface a generic "internal server error"
// body with a correlation ID — the raw error stays in the logs only.
//
// We use an atomic.Bool (not a bare bool) so tests can flip the mode at
// runtime under -race without triggering a data-race warning when other
// goroutines call WriteServerError concurrently.
var productionMode atomic.Bool

// init reads QSDM_PRODUCTION_MODE at startup so deployments that boot via
// systemd or a container with the env var set inherit the safe default
// without needing any code change at the call site.
func init() {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("QSDM_PRODUCTION_MODE")))
	if v == "1" || v == "true" || v == "yes" || v == "on" {
		productionMode.Store(true)
	}
}

// SetProductionMode toggles the production error-sanitization behaviour.
// Tests use this to assert both branches; production deployments set
// QSDM_PRODUCTION_MODE=true at boot.
func SetProductionMode(on bool) { productionMode.Store(on) }

// IsProductionMode reports the current setting.
func IsProductionMode() bool { return productionMode.Load() }

// genErrorID returns a short hex correlation ID emitted to both the log
// line and the user-facing response. Operators can grep the logs for the
// ID returned in a customer report to recover the original error.
func genErrorID() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "00000000000c"
	}
	return hex.EncodeToString(b[:])
}

// WriteServerError logs the raw error in full and writes a sanitized 500
// to the client. In production mode the response body is the generic
// "internal server error" string plus the correlation ID; in development
// mode the raw err.Error() is forwarded so devs can read the stack.
//
// Call this in place of writeErrorResponse(w, http.StatusInternalServerError, err.Error())
// whenever the underlying error originated from internal state (DB
// failure, crypto failure, marshalling error, etc.) — leaking those
// strings to attackers reveals storage backend identity, code paths, and
// table names.
//
// Use writeErrorResponse for 4xx errors that are deliberately user-facing
// (validation failures, auth failures, etc.) — those messages are part
// of the API contract.
func WriteServerError(w http.ResponseWriter, logger *logging.Logger, op string, err error) {
	id := genErrorID()
	if logger != nil {
		logger.Error("Internal server error",
			"error_id", id,
			"op", op,
			"error", err,
		)
	}
	msg := "internal server error (id=" + id + ")"
	if !productionMode.Load() && err != nil {
		// Dev-mode: include the raw error so a developer running locally
		// gets immediate feedback. Still prefixed with the id so the
		// log/HTTP correlation is one-step.
		msg = msg + ": " + err.Error()
	}
	writeErrorResponse(w, http.StatusInternalServerError, msg)
}

// SanitizeForLog returns s with control characters (CR, LF, NUL, ANSI
// escape) stripped and the length capped at maxLen. The output is safe to
// drop into a structured log field without enabling log-injection /
// log-forging attacks (CWE-117).
//
// Use this when interpolating ANY user-controlled value into a log line:
// addresses (which we validate, but defense in depth), free-form
// strings (geo tags, error messages echoed from clients), and headers.
func SanitizeForLog(s string) string {
	return SanitizeForLogN(s, MaxStringLength)
}

// SanitizeForLogN is the bounded variant used when a smaller cap is
// appropriate (e.g. a user-agent header capped at 256 chars).
func SanitizeForLogN(s string, maxLen int) string {
	if maxLen <= 0 {
		maxLen = MaxStringLength
	}
	if len(s) > maxLen {
		s = s[:maxLen] + "...(truncated)"
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\n', r == '\r', r == '\t', r == '\v', r == '\f':
			b.WriteByte(' ')
		case r == 0x1b: // ESC — start of ANSI escape sequence
			b.WriteByte(' ')
		case r < 0x20 || r == 0x7f:
			// Other control chars (CWE-93 newline injection, NUL byte logs).
			b.WriteByte(' ')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
