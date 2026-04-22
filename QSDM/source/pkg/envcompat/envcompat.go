// Package envcompat provides the environment-variable deprecation shim used
// during the Major Update rebrand (QSDM+ \u2192 QSDM, Cell coin introduction).
// See QSDM/docs/docs/REBRAND_NOTES.md for the full deprecation table and
// timeline.
//
// Usage contract every caller should rely on:
//
//   - The preferred (post-rebrand) name is QSDM_*.
//   - The legacy (pre-rebrand) name is QSDMPLUS_*.
//   - When both are set, the preferred name wins (the operator has already
//     migrated; their legacy value is a stale backup that must NOT silently
//     override the new one).
//   - When only the legacy name is set, its value is adopted AND a single
//     WARN-level deprecation notice is logged to the default Go logger,
//     once per process, per legacy variable.
//
// This package has no dependencies outside the standard library on purpose:
// it is imported by pkg/config (loaded at startup, before logging/monitoring
// subsystems initialise), by pkg/api, by pkg/wasm, by pkg/monitoring and by
// cmd/qsdmplus main. It must remain cheap and cycle-free.
package envcompat

import (
	"log"
	"os"
	"strings"
	"sync"
)

var deprecatedEnvOnce sync.Map // map[string]struct{}; key = legacy env name

// Lookup returns the value of the preferred env var if set, otherwise the
// value of the legacy env var (logging a one-shot deprecation warning when it
// falls back). Returns empty string when neither is set or both are empty.
func Lookup(preferred, legacy string) string {
	if v, ok := os.LookupEnv(preferred); ok && strings.TrimSpace(v) != "" {
		return v
	}
	if v, ok := os.LookupEnv(legacy); ok && strings.TrimSpace(v) != "" {
		WarnDeprecatedEnv(legacy, preferred)
		return v
	}
	return ""
}

// Truthy is a convenience that reports whether Lookup(preferred, legacy)
// evaluates to a conventionally-true string (1, true, yes).
func Truthy(preferred, legacy string) bool {
	v := strings.TrimSpace(strings.ToLower(Lookup(preferred, legacy)))
	return v == "1" || v == "true" || v == "yes"
}

// WarnDeprecatedEnv logs a WARN-level message the first time a legacy env
// variable is observed in this process. Subsequent observations are silent
// so dashboards and log shippers are not overwhelmed in long-running
// processes. Exported so callers that need to consume legacy names outside
// the Lookup/Truthy helpers (for example when the legacy name has no direct
// post-rebrand rename, only a semantic deprecation) can still emit a
// consistent warning.
func WarnDeprecatedEnv(legacy, preferred string) {
	if _, loaded := deprecatedEnvOnce.LoadOrStore(legacy, struct{}{}); loaded {
		return
	}
	if preferred == "" {
		log.Printf("WARN: deprecated environment variable %s is set; it has no direct "+
			"replacement after the QSDM+ \u2192 QSDM rebrand. See "+
			"QSDM/docs/docs/REBRAND_NOTES.md for guidance.", legacy)
		return
	}
	log.Printf("WARN: deprecated environment variable %s is set; rename it to %s "+
		"(QSDM+ \u2192 QSDM rebrand \u2014 see QSDM/docs/docs/REBRAND_NOTES.md). "+
		"The legacy name will be removed in a future release.",
		legacy, preferred)
}

// ResetForTest clears the once-per-process deprecation cache. Tests that
// verify the deprecation log path must call this to avoid cross-test leakage.
func ResetForTest() {
	deprecatedEnvOnce = sync.Map{}
}
