// Package buildinfo exposes build-time metadata (semver tag, git commit
// SHA, build timestamp) that release artefacts can embed via
// `-ldflags -X`. Every cmd/ main that ships as a signed release
// artefact reads these vars through a --version flag so bug reports
// carry a precise commit + release identifier.
//
// Default values ("dev" / "unknown") mean the binary was built outside
// the release pipeline (`go run`, `go build` without flags). That is
// valid and expected for local development; CI gates never rely on
// these strings, only human operators do.
//
// Release-time injection from .github/workflows/release-container.yml:
//
//	go build \
//	  -trimpath \
//	  -ldflags "-s -w \
//	    -X github.com/blackbeardONE/QSDM/pkg/buildinfo.Version=v0.1.0 \
//	    -X github.com/blackbeardONE/QSDM/pkg/buildinfo.GitSHA=abc1234 \
//	    -X github.com/blackbeardONE/QSDM/pkg/buildinfo.BuildDate=2026-04-22T10:00:00Z" \
//	  ./cmd/qsdmminer-console
//
// With that, `qsdmminer-console --version` prints:
//
//	qsdmminer-console v0.1.0 (abc1234, 2026-04-22T10:00:00Z, go1.25.9, linux/amd64)
//
// Operators can map the semver to the GitHub Release page and the SHA
// to a commit on main, which is the minimum metadata needed to triage
// an incident against a specific artefact.
package buildinfo

import (
	"fmt"
	"runtime"
	"strings"
)

// Version is the semver tag embedded at release time. "dev" is the
// explicit "built outside the release pipeline" sentinel.
var Version = "dev"

// GitSHA is the (short) commit hash the binary was built from.
// "unknown" is the explicit "not injected" sentinel.
var GitSHA = "unknown"

// BuildDate is the UTC RFC 3339 timestamp at which the binary was
// built. "unknown" is the explicit "not injected" sentinel.
var BuildDate = "unknown"

// String returns the canonical one-liner used by every --version flag
// across QSDM binaries. Callers supply the binary name because one
// package is reused by several cmd/ mains (qsdmminer, qsdmminer-console,
// trustcheck, genesis-ceremony) and we want each binary to print its
// own name without having to duplicate the formatting logic.
//
// Example:
//
//	qsdmminer-console v0.1.0 (abc1234, 2026-04-22T10:00:00Z, go1.25.9, linux/amd64)
func String(binaryName string) string {
	return fmt.Sprintf("%s %s (%s, %s, %s, %s/%s)",
		strings.TrimSpace(binaryName),
		Version,
		GitSHA,
		BuildDate,
		runtime.Version(),
		runtime.GOOS,
		runtime.GOARCH,
	)
}

// Short returns just "<binary> <semver>", suitable for terse log
// banners where the full runtime fingerprint would be noise (e.g. a
// startup line emitted before every mining round).
func Short(binaryName string) string {
	return fmt.Sprintf("%s %s", strings.TrimSpace(binaryName), Version)
}

// IsReleaseBuild reports whether all three ldflags-injected vars look
// like they came from the release pipeline rather than the defaults.
// Useful for gating "phone home" telemetry or Prometheus labels to
// released builds only — we never want a dev build to pollute a
// release-adoption dashboard with a "dev" version row.
func IsReleaseBuild() bool {
	return Version != "dev" &&
		GitSHA != "unknown" &&
		BuildDate != "unknown"
}
