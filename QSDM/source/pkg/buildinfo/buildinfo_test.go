package buildinfo

import (
	"runtime"
	"strings"
	"testing"
)

// TestString_DefaultsMentionDev verifies the "dev" sentinel is visible
// in --version output when the binary is built without ldflags, which
// is what an operator hitting a local `go run` will see. Visibility of
// the word "dev" is important because bug reports that include it are
// a clear signal the report is against an unreleased build and should
// be reproduced on a tagged artefact before triage.
func TestString_DefaultsMentionDev(t *testing.T) {
	got := String("qsdmminer-console")

	want := []string{
		"qsdmminer-console",
		"dev",
		"unknown",
		runtime.Version(),
		runtime.GOOS,
		runtime.GOARCH,
	}
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("String() missing %q in output: %q", w, got)
		}
	}
}

// TestString_HonoursInjectedValues verifies that release-time ldflags
// injection (mutating the three package vars) is reflected in String()
// output. This is the exact path release-container.yml exercises in
// CI, so regressions here would mean every future release ships with
// a useless --version line.
func TestString_HonoursInjectedValues(t *testing.T) {
	origV, origS, origD := Version, GitSHA, BuildDate
	Version, GitSHA, BuildDate = "v0.1.0", "abc1234", "2026-04-22T10:00:00Z"
	t.Cleanup(func() {
		Version, GitSHA, BuildDate = origV, origS, origD
	})

	got := String("trustcheck")
	for _, want := range []string{"trustcheck", "v0.1.0", "abc1234", "2026-04-22T10:00:00Z"} {
		if !strings.Contains(got, want) {
			t.Errorf("String() missing %q: %q", want, got)
		}
	}
}

// TestShort_OnlyNameAndVersion confirms the terse banner omits the
// runtime fingerprint; callers (e.g. a mining loop's per-round log)
// depend on the line staying short so it doesn't dominate journal
// output.
func TestShort_OnlyNameAndVersion(t *testing.T) {
	origV := Version
	Version = "v1.2.3"
	t.Cleanup(func() { Version = origV })

	got := Short("qsdmminer")
	if got != "qsdmminer v1.2.3" {
		t.Errorf("Short() = %q, want %q", got, "qsdmminer v1.2.3")
	}
	if strings.Contains(got, runtime.GOOS) {
		t.Errorf("Short() should not include GOOS, got %q", got)
	}
}

// TestIsReleaseBuild_SentinelOnlyMeansDev proves the release-gated
// telemetry guard correctly distinguishes "built via release pipeline"
// from "built by a developer". False-positives here would mean a
// dashboard mixes dev traffic into production adoption stats; false-
// negatives would mean a released binary silently fails to report.
func TestIsReleaseBuild_SentinelOnlyMeansDev(t *testing.T) {
	origV, origS, origD := Version, GitSHA, BuildDate
	t.Cleanup(func() { Version, GitSHA, BuildDate = origV, origS, origD })

	Version, GitSHA, BuildDate = "dev", "unknown", "unknown"
	if IsReleaseBuild() {
		t.Error("all-default sentinels should NOT report as a release build")
	}

	Version, GitSHA, BuildDate = "v0.1.0", "abc1234", "2026-04-22T10:00:00Z"
	if !IsReleaseBuild() {
		t.Error("all three non-default should report as a release build")
	}

	// Any remaining sentinel disqualifies the build. A release
	// pipeline that forgets one -X line should not count.
	Version, GitSHA, BuildDate = "v0.1.0", "unknown", "2026-04-22T10:00:00Z"
	if IsReleaseBuild() {
		t.Error("partial injection (missing GitSHA) should NOT report as release")
	}
	Version, GitSHA, BuildDate = "v0.1.0", "abc1234", "unknown"
	if IsReleaseBuild() {
		t.Error("partial injection (missing BuildDate) should NOT report as release")
	}
	Version, GitSHA, BuildDate = "dev", "abc1234", "2026-04-22T10:00:00Z"
	if IsReleaseBuild() {
		t.Error("partial injection (Version=dev) should NOT report as release")
	}
}
