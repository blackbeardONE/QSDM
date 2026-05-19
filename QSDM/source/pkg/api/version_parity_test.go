package api

// Cross-endpoint version-parity regression guard.
//
// Background. The v0.4.3 release-verification surfaced two source<->live
// drift bugs in version-reporting endpoints:
//
//   d753463  /api/v1/health  was hard-coded "version":"1.0.0", predating the
//            project's v0.x.y semver tags. Fixed by routing the field
//            through pkg/buildinfo.
//   9e39439  /api/v1/status  preferred a $QSDM_BUILD_VERSION env var (set
//            by an aging BLR1 systemd version.conf drop-in pinned to
//            v0.4.2) over the build-time -X injection. Fixed by reordering
//            statusVersion() so buildinfo.Version wins when it has been
//            injected.
//
// Both endpoints now read the same three buildinfo globals (.Version,
// .GitSHA, .BuildDate). Without a regression guard, any of the following
// future changes silently re-opens the drift class:
//
//   * a new endpoint that hardcodes a version string,
//   * a revert/refactor of statusVersion() that puts the env var back
//     in front of buildinfo,
//   * a typo that swaps Version for GitSHA (or similar) on one endpoint
//     but not the other,
//   * a future serialiser that omits one of the new fields from one
//     response but not the other.
//
// This test pins the contract:
//   (a) Each endpoint's version/git_sha/build_date triple MUST equal the
//       process-global buildinfo trio.
//   (b) The triples from the two endpoints MUST be byte-equivalent.
//
// Property (b) is implied by (a) but asserted separately because a future
// contributor who breaks (a) on both endpoints in the same way would not
// be caught by (a)-vs-buildinfo alone -- the byte-equivalence check
// catches "both endpoints hardcode the same wrong value" regressions.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/buildinfo"
)

// stubBuildinfo overrides the three buildinfo package-level globals for
// the duration of the test, returning a deferred restore. Tests SHOULD
// always defer the returned closure -- the globals are process-wide,
// and leaving them mutated would taint every subsequent test that reads
// them. Test values are deliberately distinct from the documented
// "dev" / "unknown" / "unknown" sentinels so we can assert the override
// actually takes effect (a no-op override would coincidentally look
// "correct" by reading back the same sentinels).
func stubBuildinfo(t *testing.T, version, gitSHA, buildDate string) func() {
	t.Helper()
	origVersion := buildinfo.Version
	origGitSHA := buildinfo.GitSHA
	origBuildDate := buildinfo.BuildDate
	buildinfo.Version = version
	buildinfo.GitSHA = gitSHA
	buildinfo.BuildDate = buildDate
	return func() {
		buildinfo.Version = origVersion
		buildinfo.GitSHA = origGitSHA
		buildinfo.BuildDate = origBuildDate
	}
}

// versionTriple extracts the (version, git_sha, build_date) fields from
// an arbitrary JSON object. Both /api/v1/health and /api/v1/status flatten
// these at the top level, so a shared extractor works for both.
type versionTriple struct {
	Version   string `json:"version"`
	GitSHA    string `json:"git_sha"`
	BuildDate string `json:"build_date"`
}

func decodeTriple(t *testing.T, body []byte, endpoint string) versionTriple {
	t.Helper()
	var triple versionTriple
	if err := json.Unmarshal(body, &triple); err != nil {
		t.Fatalf("%s: decode triple: %v; body=%s", endpoint, err, string(body))
	}
	return triple
}

// TestHealthStatusVersionParity asserts the contract documented in the
// file-level comment: both /api/v1/health and /api/v1/status MUST return
// the same (version, git_sha, build_date) triple, and that triple MUST
// equal the process-global buildinfo trio.
//
// Test values are chosen to be byte-distinct from the default sentinels
// (Version="dev", GitSHA="unknown", BuildDate="unknown") so a regression
// that ignored the override would fail loudly rather than silently passing
// by reading back the sentinel for both endpoints.
func TestHealthStatusVersionParity(t *testing.T) {
	const (
		wantVersion   = "v9.9.9-test-parity"
		wantGitSHA    = "deadbeef"
		wantBuildDate = "2026-01-01T00:00:00Z"
	)
	defer stubBuildinfo(t, wantVersion, wantGitSHA, wantBuildDate)()

	h := setupTestHandlers()

	// --- /api/v1/health ---
	healthReq := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	healthRec := httptest.NewRecorder()
	h.HealthCheck(healthRec, healthReq)
	if healthRec.Code != http.StatusOK {
		t.Fatalf("/api/v1/health: status = %d, want 200; body=%s",
			healthRec.Code, healthRec.Body.String())
	}
	healthTriple := decodeTriple(t, healthRec.Body.Bytes(), "/api/v1/health")

	// --- /api/v1/status ---
	statusReq := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	statusRec := httptest.NewRecorder()
	h.StatusHandler(statusRec, statusReq)
	if statusRec.Code != http.StatusOK {
		t.Fatalf("/api/v1/status: status = %d, want 200; body=%s",
			statusRec.Code, statusRec.Body.String())
	}
	statusTriple := decodeTriple(t, statusRec.Body.Bytes(), "/api/v1/status")

	// (a) Each endpoint MUST equal the process-global buildinfo trio.
	if healthTriple.Version != wantVersion {
		t.Errorf("/api/v1/health version = %q, want %q (matches buildinfo.Version override)",
			healthTriple.Version, wantVersion)
	}
	if healthTriple.GitSHA != wantGitSHA {
		t.Errorf("/api/v1/health git_sha = %q, want %q (matches buildinfo.GitSHA override)",
			healthTriple.GitSHA, wantGitSHA)
	}
	if healthTriple.BuildDate != wantBuildDate {
		t.Errorf("/api/v1/health build_date = %q, want %q (matches buildinfo.BuildDate override)",
			healthTriple.BuildDate, wantBuildDate)
	}
	if statusTriple.Version != wantVersion {
		t.Errorf("/api/v1/status version = %q, want %q (matches buildinfo.Version override)",
			statusTriple.Version, wantVersion)
	}
	if statusTriple.GitSHA != wantGitSHA {
		t.Errorf("/api/v1/status git_sha = %q, want %q (matches buildinfo.GitSHA override)",
			statusTriple.GitSHA, wantGitSHA)
	}
	if statusTriple.BuildDate != wantBuildDate {
		t.Errorf("/api/v1/status build_date = %q, want %q (matches buildinfo.BuildDate override)",
			statusTriple.BuildDate, wantBuildDate)
	}

	// (b) Byte-equivalence between the two endpoints. Implied by (a) but
	// asserted separately so a future regression that hardcodes the
	// same wrong value on both endpoints still gets caught here.
	if healthTriple != statusTriple {
		t.Errorf("cross-endpoint drift: /health=%+v vs /status=%+v -- "+
			"both endpoints MUST return the same (version, git_sha, build_date) "+
			"triple sourced from pkg/buildinfo (see file-level comment for the "+
			"d753463/9e39439 drift bugs this test guards against)",
			healthTriple, statusTriple)
	}
}

// TestHealthStatusVersionParity_StatusFallbackChain asserts that
// statusVersion()'s resolution chain works as documented: when
// buildinfo.Version is the "dev" sentinel (i.e. neither -X nor an env var
// was set), the QSDM_BUILD_VERSION env-var fallback still applies. This
// is the operator escape-hatch path preserved for backwards compatibility
// with the pre-buildinfo systemd version.conf drop-in pattern.
//
// We override buildinfo.Version to "dev" (forcing the fallback), set the
// env var, and assert /api/v1/status reports the env-var value while
// /api/v1/health (which always reads buildinfo directly with no fallback)
// reports "dev". The two endpoints DISAGREE in this configuration -- and
// that's correct: the env-var override is a status-handler-specific
// escape hatch documented inline in statusVersion(). The drift is the
// operator's explicit choice in this case, not a bug.
func TestHealthStatusVersionParity_StatusFallbackChain(t *testing.T) {
	defer stubBuildinfo(t, "dev", "unknown", "unknown")()
	t.Setenv("QSDM_BUILD_VERSION", "v9.9.9-env-override")
	// Belt-and-suspenders: ensure the legacy alias is empty so it doesn't
	// shadow our primary env var in the resolution chain.
	t.Setenv("QSDMPLUS_BUILD_VERSION", "")

	h := setupTestHandlers()

	statusReq := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	statusRec := httptest.NewRecorder()
	h.StatusHandler(statusRec, statusReq)
	if statusRec.Code != http.StatusOK {
		t.Fatalf("/api/v1/status: status = %d, want 200", statusRec.Code)
	}
	statusTriple := decodeTriple(t, statusRec.Body.Bytes(), "/api/v1/status")

	if statusTriple.Version != "v9.9.9-env-override" {
		t.Errorf("/api/v1/status version = %q, want %q (env-var fallback "+
			"kicked in when buildinfo.Version=\"dev\")",
			statusTriple.Version, "v9.9.9-env-override")
	}
}
