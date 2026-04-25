// Package envcompat is a thin environment-variable lookup helper. It used
// to implement a preferred / legacy dual-name deprecation shim during the
// QSDM rebrand; that shim has been removed and the helpers now simply
// trim whitespace around the returned value. The two-argument shape is
// preserved so existing call sites compile unchanged.
package envcompat

import (
	"os"
	"strings"
)

// Lookup returns the value of name, trimmed of surrounding whitespace. If
// it is unset or blank, the empty string is returned.
//
// The second argument is accepted for API compatibility with the pre-rebrand
// shim that used to consult a legacy variable name as a fallback. Both the
// preferred and legacy names are now identical, so the fallback is a no-op.
func Lookup(name string, _ string) string {
	if v, ok := os.LookupEnv(name); ok {
		if t := strings.TrimSpace(v); t != "" {
			return t
		}
	}
	return ""
}

// Truthy reports whether Lookup(name, _) evaluates to a conventionally-true
// string (1, true, yes, on — case-insensitive).
func Truthy(name string, _ string) bool {
	v := strings.ToLower(Lookup(name, ""))
	return v == "1" || v == "true" || v == "yes" || v == "on"
}

// WarnDeprecatedEnv is retained as a no-op for source compatibility.
func WarnDeprecatedEnv(_, _ string) {}

// ResetForTest is retained as a no-op for source compatibility.
func ResetForTest() {}
