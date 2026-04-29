// Regression tests guarding against the "search-and-replace flattens
// the deprecation surface" failure mode that broke main on 2026-04-26.
//
// Background: the rebrand commit (db9b590) ran a `qsdmplus -> qsdm`
// pass over the whole tree, including the branding constants below.
// The pass collapsed every `Legacy = "...qsdmplus..."` literal to the
// canonical `qsdm` value, which:
//
//   - made every Preferred/Legacy pair identical (no fallback at all),
//   - silently rejected pre-rebrand sidecars that still send
//     `X-QSDMPLUS-NGC-Secret` headers or `qsdmplus_node_id` proof
//     fields (handlers.go and nvidia_hmac.go fall back to the Legacy
//     constant, which now equals Preferred and so has no effect),
//   - left LegacyName equal to Name, hiding the rebrand from the API
//     status response.
//
// These tests fail loudly if anyone re-runs that style of pass.

package branding

import "testing"

func TestLegacyNameDistinctFromName(t *testing.T) {
	if LegacyName == Name {
		t.Fatalf("LegacyName must differ from Name; both = %q. "+
			"This is the canary for the rebrand search-and-replace "+
			"flattening bug — see comment above.", Name)
	}
	if LegacyName == "" {
		t.Fatalf("LegacyName is empty; api/handlers_status.go publishes "+
			"this in /api/v1/status and clients depend on a non-empty "+
			"deprecation hint")
	}
}

// pairedConstants enumerates every Preferred/Legacy pair that ships
// to the network on the wire (HTTP headers + proof JSON fields). Add
// a row here when introducing a new pair; the loop below asserts the
// "Legacy != Preferred AND Legacy contains the retired prefix" shape
// uniformly.
func pairedConstants() []struct {
	name      string
	preferred string
	legacy    string
} {
	return []struct {
		name      string
		preferred string
		legacy    string
	}{
		{"NGCSecretHeader", NGCSecretHeaderPreferred, NGCSecretHeaderLegacy},
		{"MetricsScrapeSecretHeader", MetricsScrapeSecretHeaderPreferred, MetricsScrapeSecretHeaderLegacy},
		{"ProofNodeIDField", ProofNodeIDFieldPreferred, ProofNodeIDFieldLegacy},
		{"ProofHMACField", ProofHMACFieldPreferred, ProofHMACFieldLegacy},
		{"ProofIngestNonceField", ProofIngestNonceFieldPreferred, ProofIngestNonceFieldLegacy},
	}
}

func TestPreferredAndLegacyDiffer(t *testing.T) {
	for _, p := range pairedConstants() {
		if p.preferred == p.legacy {
			t.Errorf("%s: Preferred = Legacy = %q. "+
				"This collapses the deprecation fallback to a no-op; "+
				"pre-rebrand clients will be silently rejected.",
				p.name, p.preferred)
		}
	}
}

func TestLegacyConstantsCarryRetiredPrefix(t *testing.T) {
	// The retired prefix is "qsdmplus" / "QSDMPLUS" depending on
	// case. Header constants are X-QSDMPLUS-*, JSON fields are
	// qsdmplus_*.
	for _, p := range pairedConstants() {
		if !containsAnyFold(p.legacy, "qsdmplus") {
			t.Errorf("%s.Legacy = %q does not contain the retired "+
				"prefix \"qsdmplus\" (case-insensitive). The rebrand "+
				"search-and-replace likely overwrote this; revert to "+
				"the pre-rebrand value (see git show db9b590~1 -- "+
				"QSDM/source/pkg/branding/branding.go).",
				p.name, p.legacy)
		}
	}
}

func TestPreferredConstantsCarryCanonicalPrefixOnly(t *testing.T) {
	// The canonical prefix is "qsdm" / "QSDM" and MUST NOT contain
	// the retired "qsdmplus" anywhere — that would mean the rebrand
	// missed a substitution on the new-name side.
	for _, p := range pairedConstants() {
		if containsAnyFold(p.preferred, "qsdmplus") {
			t.Errorf("%s.Preferred = %q contains \"qsdmplus\"; "+
				"the rebrand left a stale legacy substring on the "+
				"canonical side.", p.name, p.preferred)
		}
		if !containsAnyFold(p.preferred, "qsdm") {
			t.Errorf("%s.Preferred = %q is missing the canonical "+
				"\"qsdm\" prefix entirely.", p.name, p.preferred)
		}
	}
}

// containsAnyFold is a tiny case-insensitive substring helper; we
// don't pull in strings.EqualFold-via-ToLower to keep this file
// alloc-free.
func containsAnyFold(s, sub string) bool {
	if len(sub) == 0 || len(s) < len(sub) {
		return len(sub) == 0
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		match := true
		for j := 0; j < len(sub); j++ {
			a := s[i+j]
			b := sub[j]
			if a >= 'A' && a <= 'Z' {
				a += 'a' - 'A'
			}
			if b >= 'A' && b <= 'Z' {
				b += 'a' - 'A'
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
