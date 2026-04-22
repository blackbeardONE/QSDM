package monitoring

// ngcFieldString reads a string field from an NGC proof bundle, trying the
// preferred (post-rebrand) name first and falling back to the legacy
// (pre-rebrand) name for backwards compatibility with already-deployed
// sidecars. Missing or non-string values yield the empty string.
//
// Rationale: the Cell-coin rebrand (Major Update §7.1) renames bundle
// fields from qsdmplus_* to qsdm_*. Existing sidecars in the field emit
// qsdmplus_* until they are upgraded; we must not break them during the
// deprecation window, but new code should read the preferred name.
func ngcFieldString(m map[string]interface{}, preferred, legacy string) string {
	if v, ok := m[preferred].(string); ok && v != "" {
		return v
	}
	if v, ok := m[legacy].(string); ok {
		return v
	}
	return ""
}
