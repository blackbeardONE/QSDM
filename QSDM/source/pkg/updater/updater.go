// Package updater is the consumer-miner self-update client.
//
// Design in one paragraph: the miner polls
// https://qsdm.tech/releases/latest.txt every N hours; when the tag
// differs from the baked-in pkg/buildinfo.Version, the miner fetches
// MANIFEST.json, locates the binary for its (GOOS, GOARCH), streams
// it to disk while computing SHA-256, and verifies the digest against
// the manifest. On match, the new binary is staged at
// <currentExePath>.next. The miner does NOT auto-restart by default —
// the staged file is applied at next startup (the operator's nssm /
// systemd / manual restart). This is a deliberate two-step ritual:
// staging is safe (download + verify), applying mutates the on-disk
// binary and re-execs, which we only want to happen at a moment the
// operator chose.
//
// Security model:
//
//   - HTTPS to qsdm.tech is the transport gate. Without code-signing
//     (which the project hasn't shipped yet — see "What's left" in
//     the consumer-release commit message), HTTPS + SHA-256 from the
//     manifest are the integrity gates.
//   - The SHA-256 in MANIFEST.json is itself only as trustworthy as
//     the file at https://qsdm.tech/releases/<tag>/MANIFEST.json.
//     An attacker who can serve a malicious MANIFEST.json can also
//     serve a matching malicious binary. Therefore the updater is
//     EXACTLY the trust footprint of HTTPS-to-qsdm.tech, no more.
//   - When code-signing lands, the verifier here will additionally
//     check an Ed25519 signature on the manifest produced by an
//     offline release key. That is a strict superset of the current
//     posture; today's stage payload (raw downloaded bytes + matched
//     SHA-256) becomes the input to the future signature check.
//
// Usage from the miner:
//
//	cfg := updater.Config{
//	    BaseURL:        "https://qsdm.tech/releases",
//	    Component:      "qsdmminer",
//	    GOOS:           runtime.GOOS,
//	    GOARCH:         runtime.GOARCH,
//	    CurrentVersion: buildinfo.Version,
//	    HTTPClient:     http.DefaultClient,
//	}
//	u := updater.New(cfg)
//	res, err := u.CheckAndStage(ctx)
//	if res.Staged { log.Printf("update %s staged at %s", res.NewVersion, res.StagedPath) }
//
// Then at startup, before the mining loop:
//
//	if applied, err := updater.ApplyStagedIfPresent(...); applied {
//	    // process re-execs into the new binary; control never
//	    // returns here on the success path.
//	}
package updater

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// utf8BOM is the byte-order mark some Windows tooling (notably
// PowerShell 5.1's `Set-Content -Encoding UTF8`) silently prepends
// to text files. Go's encoding/json rejects it with the cryptic
// "invalid character 'ï'" error; we strip it here so the
// updater is forgiving of release-host tooling drift.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// Default endpoint paths under BaseURL. Operators running their own
// release host can override these — but every QSDM consumer install
// today points at qsdm.tech so the defaults are the correct
// production values.
const (
	DefaultLatestPath = "latest.txt"
	ManifestFileName  = "MANIFEST.json"
)

// Config drives a single Updater instance. Zero values are flagged
// in New() so a caller can't accidentally build an Updater that
// silently fetches from "" and "compares against ''".
type Config struct {
	// BaseURL is the prefix for the release-host URLs. Trailing
	// slash is stripped. Required.
	BaseURL string

	// LatestPath is the path under BaseURL whose body is the
	// current tag, terminated by an optional newline. Empty
	// falls back to DefaultLatestPath.
	LatestPath string

	// Component selects which row of MANIFEST.components is
	// considered. The consumer miner uses "qsdmminer";
	// auxiliary tools could use "qsdmcli" or "qsdm-attester".
	Component string

	// GOOS / GOARCH select the artifact within the chosen
	// component. These mirror runtime.GOOS / runtime.GOARCH;
	// callers usually wire them straight through.
	GOOS, GOARCH string

	// CurrentVersion is what we compare the fetched tag
	// against. Almost always pkg/buildinfo.Version. The
	// "dev" sentinel (un-injected developer build) skips
	// every check via SkipUpdates so a `go run` workflow
	// doesn't randomly start downloading prod releases.
	CurrentVersion string

	// HTTPClient bounds network I/O. Nil falls back to
	// http.DefaultClient with a 30s per-request timeout
	// applied via context — that is the same posture as
	// every other QSDM HTTP client in the tree.
	HTTPClient *http.Client

	// StagePath is the on-disk destination for a fresh
	// download before it replaces the running binary.
	// Empty falls back to <currentExePath>.next so the
	// operator can find it next to their existing exe.
	StagePath string

	// MaxArtifactBytes caps the streamed download so a
	// hostile or accidentally-huge MANIFEST entry can't
	// blow up disk. Default 256 MiB — current artifacts
	// are ~15 MiB, so 256 MiB is ~17x headroom.
	MaxArtifactBytes int64
}

// Updater is the public type. Constructed once and reused —
// CheckAndStage is safe for concurrent calls but typically only one
// goroutine drives it on a timer.
type Updater struct {
	cfg Config

	mu       sync.Mutex
	lastSeen string // most recent tag we successfully read from latest.txt
}

// Result is the structured outcome of a single CheckAndStage call.
// Tests assert on this rather than on log output.
type Result struct {
	// Checked is true when the updater talked to BaseURL
	// successfully (parsed latest.txt). Distinguishes a
	// transient network failure from "nothing to do".
	Checked bool

	// SkippedReason is non-empty when the updater chose
	// not to do anything (e.g. CurrentVersion=="dev",
	// remote tag matches local). Useful for logs.
	SkippedReason string

	// UpToDate is true when the remote tag equals
	// CurrentVersion — the success path of "we checked,
	// nothing to do".
	UpToDate bool

	// Staged is true when CheckAndStage downloaded a new
	// binary, verified its SHA-256, and wrote it to
	// StagedPath. Staged=true implies NewVersion and
	// StagedPath are populated.
	Staged bool

	// NewVersion is the tag of the staged release.
	NewVersion string

	// StagedPath is the on-disk path of the freshly
	// downloaded binary. Equal to Config.StagePath when
	// the caller supplied one, else <exePath>.next.
	StagedPath string

	// SHA256 is the verified hex digest of StagedPath.
	SHA256 string

	// SizeBytes is the verified size of the staged file.
	SizeBytes int64
}

// Manifest is the on-the-wire shape of MANIFEST.json. The actual
// release script writes a few fields the updater ignores ("builtAt",
// "goVersion", per-component "label"); we list every field we DO
// consume so json.DisallowUnknownFields could be added later if we
// wanted to fail loudly on schema drift.
type Manifest struct {
	Version    string              `json:"version"`
	Components []ManifestComponent `json:"components"`
}

// ManifestComponent is one row of Manifest.Components. Component
// + os + arch is the composite key the updater filters by.
type ManifestComponent struct {
	Component string `json:"component"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	File      string `json:"file"`
	SizeBytes int64  `json:"sizeBytes"`
	SHA256    string `json:"sha256"`
}

// Errors. Distinct types so callers can log or branch without
// substring-matching error strings.
var (
	ErrNoStagedUpdate     = errors.New("updater: no staged update file present")
	ErrComponentNotFound  = errors.New("updater: component/os/arch not in manifest")
	ErrSHA256Mismatch     = errors.New("updater: downloaded SHA-256 does not match manifest")
	ErrCurrentVersionDev  = errors.New("updater: current version is dev — refusing to update a non-release build")
	ErrInvalidConfig      = errors.New("updater: invalid configuration")
)

// New constructs an Updater after validating Config. It does NOT
// fetch anything; first network access happens on CheckAndStage.
func New(cfg Config) (*Updater, error) {
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("%w: BaseURL is required", ErrInvalidConfig)
	}
	if cfg.Component == "" {
		return nil, fmt.Errorf("%w: Component is required", ErrInvalidConfig)
	}
	if cfg.GOOS == "" || cfg.GOARCH == "" {
		return nil, fmt.Errorf("%w: GOOS and GOARCH are required", ErrInvalidConfig)
	}
	if cfg.CurrentVersion == "" {
		return nil, fmt.Errorf("%w: CurrentVersion is required", ErrInvalidConfig)
	}
	if cfg.LatestPath == "" {
		cfg.LatestPath = DefaultLatestPath
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = http.DefaultClient
	}
	if cfg.MaxArtifactBytes <= 0 {
		cfg.MaxArtifactBytes = 256 << 20
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	return &Updater{cfg: cfg}, nil
}

// CheckAndStage runs one update cycle: fetch latest tag, compare
// against CurrentVersion, fetch manifest, download artifact, verify
// SHA-256, write to staging path. Returns Result with Checked=true
// on every successful network round-trip (whether or not anything
// was downloaded), and an error only on hard failures (network
// errors, manifest parse errors, sha mismatches).
//
// Idempotency: if the same tag is already staged at StagePath with a
// matching SHA-256, the call returns Staged=false (we don't
// re-download) and UpToDate is set per the local-vs-remote tag
// comparison. The check is by SHA-256, not by mtime, so a
// half-finished download from a previous run gets re-tried.
func (u *Updater) CheckAndStage(ctx context.Context) (Result, error) {
	if u.cfg.CurrentVersion == "dev" {
		return Result{
			SkippedReason: "current build is 'dev' (no buildinfo injection)",
		}, ErrCurrentVersionDev
	}

	tag, err := u.fetchLatestTag(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("fetch latest.txt: %w", err)
	}

	u.mu.Lock()
	u.lastSeen = tag
	u.mu.Unlock()

	if tag == u.cfg.CurrentVersion {
		return Result{Checked: true, UpToDate: true, NewVersion: tag}, nil
	}

	mf, err := u.fetchManifest(ctx, tag)
	if err != nil {
		return Result{Checked: true, NewVersion: tag},
			fmt.Errorf("fetch manifest %s: %w", tag, err)
	}

	c, ok := findComponent(mf, u.cfg.Component, u.cfg.GOOS, u.cfg.GOARCH)
	if !ok {
		return Result{Checked: true, NewVersion: tag},
			fmt.Errorf("%w: %s/%s/%s in tag %s",
				ErrComponentNotFound, u.cfg.Component, u.cfg.GOOS, u.cfg.GOARCH, tag)
	}

	stagePath, err := u.resolveStagePath()
	if err != nil {
		return Result{Checked: true, NewVersion: tag},
			fmt.Errorf("resolve stage path: %w", err)
	}

	// Idempotent skip: if a staged file already exists with the
	// expected hash, just return Staged=false. This covers the
	// case where the operator restarts the miner between the
	// stage step and the apply step — we don't want to
	// re-download a 15 MiB artifact every restart.
	if existing, ok := stagedHashEquals(stagePath, c.SHA256); ok && existing {
		return Result{
			Checked:    true,
			NewVersion: tag,
			Staged:     false,
			StagedPath: stagePath,
			SHA256:     c.SHA256,
			SizeBytes:  c.SizeBytes,
			SkippedReason: "stage file already matches manifest sha256",
		}, nil
	}

	artifactURL := u.cfg.BaseURL + "/" + tag + "/" + c.File
	gotSize, gotSHA, err := u.downloadVerified(ctx, artifactURL, c.SHA256, c.SizeBytes, stagePath)
	if err != nil {
		return Result{Checked: true, NewVersion: tag},
			fmt.Errorf("download %s: %w", c.File, err)
	}

	return Result{
		Checked:    true,
		Staged:     true,
		NewVersion: tag,
		StagedPath: stagePath,
		SHA256:     gotSHA,
		SizeBytes:  gotSize,
	}, nil
}

// LastSeenTag returns the most recent tag the Updater observed from
// latest.txt, or "" if no successful fetch has happened. Used by the
// dashboard / metrics surface so the operator can see "we know about
// vX.Y.Z but haven't applied it yet".
func (u *Updater) LastSeenTag() string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.lastSeen
}

// fetchLatestTag is a one-shot HTTP GET that reads latest.txt and
// returns its trimmed body. Tags are validated against a small
// allow-list character class (alphanum + "+.-_") so a hostile
// response can't poison the stage path with shell metacharacters.
func (u *Updater) fetchLatestTag(ctx context.Context) (string, error) {
	url := u.cfg.BaseURL + "/" + strings.TrimLeft(u.cfg.LatestPath, "/")
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := u.cfg.HTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d for %s", resp.StatusCode, url)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return "", err
	}
	tag := strings.TrimSpace(string(body))
	if tag == "" {
		return "", fmt.Errorf("empty body for %s", url)
	}
	if !looksLikeTag(tag) {
		return "", fmt.Errorf("body does not look like a release tag: %q", tag)
	}
	return tag, nil
}

func looksLikeTag(s string) bool {
	if len(s) == 0 || len(s) > 64 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '+' || r == '.' || r == '-' || r == '_':
		default:
			return false
		}
	}
	return true
}

func (u *Updater) fetchManifest(ctx context.Context, tag string) (*Manifest, error) {
	url := u.cfg.BaseURL + "/" + tag + "/" + ManifestFileName
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := u.cfg.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d for %s", resp.StatusCode, url)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	body = bytes.TrimPrefix(body, utf8BOM)
	var m Manifest
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("decode manifest: %w", err)
	}
	if m.Version == "" {
		return nil, errors.New("manifest version field is empty")
	}
	if m.Version != tag {
		// Sanity-check: latest.txt and the manifest version
		// should agree. If they don't, the publisher is
		// mid-deploy and we'd rather wait one more cycle than
		// race against a half-uploaded set of artefacts.
		return nil, fmt.Errorf("manifest version %q != tag %q", m.Version, tag)
	}
	return &m, nil
}

func findComponent(m *Manifest, name, goos, goarch string) (ManifestComponent, bool) {
	for _, c := range m.Components {
		if c.Component == name && c.OS == goos && c.Arch == goarch {
			return c, true
		}
	}
	return ManifestComponent{}, false
}

// resolveStagePath returns the configured StagePath, falling back to
// <currentExePath>.next. The fallback is computed lazily because the
// process exec path is only available at runtime (and tests like to
// override it via Config.StagePath for hermetic temp-dir runs).
func (u *Updater) resolveStagePath() (string, error) {
	if u.cfg.StagePath != "" {
		return u.cfg.StagePath, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return exe + ".next", nil
}

// downloadVerified streams the artifact at `url` to `stagePath`,
// computing SHA-256 in flight, and renames into place atomically on
// match. Refuses to write more than MaxArtifactBytes. On any error
// (HTTP non-200, hash mismatch, size mismatch, write error) the
// partial file is removed so a subsequent retry starts clean.
func (u *Updater) downloadVerified(ctx context.Context, url, expectedSHA string, expectedSize int64, stagePath string) (int64, string, error) {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, "", err
	}
	resp, err := u.cfg.HTTPClient.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, "", fmt.Errorf("HTTP %d for %s", resp.StatusCode, url)
	}

	tmpPath := stagePath + ".part"
	if err := os.MkdirAll(filepath.Dir(stagePath), 0o755); err != nil {
		return 0, "", fmt.Errorf("mkdir staging: %w", err)
	}
	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return 0, "", fmt.Errorf("open %s: %w", tmpPath, err)
	}

	hasher := sha256.New()
	cap := io.LimitReader(resp.Body, u.cfg.MaxArtifactBytes+1)
	mw := io.MultiWriter(f, hasher)
	n, err := io.Copy(mw, cap)
	if cerr := f.Close(); err == nil && cerr != nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(tmpPath)
		return 0, "", fmt.Errorf("write staged artifact: %w", err)
	}
	if n > u.cfg.MaxArtifactBytes {
		_ = os.Remove(tmpPath)
		return 0, "", fmt.Errorf("artifact exceeded MaxArtifactBytes=%d", u.cfg.MaxArtifactBytes)
	}
	if expectedSize > 0 && n != expectedSize {
		_ = os.Remove(tmpPath)
		return 0, "", fmt.Errorf("size mismatch: got %d, manifest says %d", n, expectedSize)
	}

	got := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(got, expectedSHA) {
		_ = os.Remove(tmpPath)
		return n, got, fmt.Errorf("%w: got %s, want %s", ErrSHA256Mismatch, got, expectedSHA)
	}

	// Atomic move into place. os.Rename is the right primitive
	// on every supported OS: POSIX is atomic by spec, and
	// Windows MoveFileEx-with-replace is atomic when the
	// source + destination are on the same volume (and they
	// always are here — both live next to the exe).
	if err := os.Rename(tmpPath, stagePath); err != nil {
		_ = os.Remove(tmpPath)
		return n, got, fmt.Errorf("rename staged artifact: %w", err)
	}
	return n, got, nil
}

// stagedHashEquals is the no-network idempotency check used in
// CheckAndStage: if a staged file already exists at `path` AND its
// sha256 matches `expected`, return (true, true). Errors (file
// missing / unreadable / bad permissions) all collapse to
// (false, false) so the caller's logic stays simple.
func stagedHashEquals(path, expected string) (matches bool, exists bool) {
	f, err := os.Open(path)
	if err != nil {
		return false, false
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return false, true
	}
	got := hex.EncodeToString(h.Sum(nil))
	return strings.EqualFold(got, expected), true
}

// ApplyStaged performs the on-disk swap and re-execs the new binary.
// Returns ErrNoStagedUpdate when nothing is staged. On the success
// path the caller's process is replaced and control never returns.
//
// The swap sequence is:
//
//   1. Verify <stagePath> exists and is non-empty.
//   2. Move <currentExePath> → <currentExePath>.old (a previous
//      .old is overwritten — we keep at most one rollback copy).
//   3. Move <stagePath> → <currentExePath>.
//   4. Re-exec <currentExePath> with the same args + env, then
//      exit the current process.
//
// On Unix step 4 uses syscall.Exec which replaces the process image
// in place. On Windows there is no exec(); we start the new process
// as a child and exit, relying on the service manager (nssm /
// systemd / manual `Restart-Service`) to reattach to the new pid.
// In practice the typical consumer flow is "operator runs miner
// under nssm; nssm restarts on exit; new binary runs" — same
// outcome on all platforms even though the syscalls differ.
func ApplyStaged(stagePath, currentExePath string, argv []string) error {
	st, err := os.Stat(stagePath)
	if err != nil {
		return ErrNoStagedUpdate
	}
	if st.Size() == 0 {
		_ = os.Remove(stagePath)
		return fmt.Errorf("staged file %s is empty; removed", stagePath)
	}

	oldPath := currentExePath + ".old"
	// Step 2: best-effort move old binary aside. On Windows
	// this works while the binary is running; if the rename
	// fails we still try the rest of the sequence.
	_ = os.Remove(oldPath)
	_ = os.Rename(currentExePath, oldPath)

	// Step 3: promote the staged binary into place.
	if err := os.Rename(stagePath, currentExePath); err != nil {
		// Rollback if we moved the original aside.
		_ = os.Rename(oldPath, currentExePath)
		return fmt.Errorf("apply: rename staged into place: %w", err)
	}

	// Step 4: spawn the new binary.
	args := argv
	if len(args) == 0 {
		args = []string{currentExePath}
	}
	cmd := exec.Command(currentExePath, args[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("apply: start new binary: %w", err)
	}
	// Don't Wait — the parent must exit so service managers
	// reattach; nssm/systemd see a non-zero/zero exit and
	// supervise the new pid (or don't, if running standalone).
	os.Exit(0)
	return nil // unreachable
}

// ApplyStagedIfPresent is a convenience wrapper: looks for a
// .next file next to the running exe, applies if present, returns
// (false, nil) if absent. Designed for unconditional invocation at
// startup behind a `--auto-apply` flag.
func ApplyStagedIfPresent(currentExePath string, argv []string) (bool, error) {
	stagePath := currentExePath + ".next"
	if _, err := os.Stat(stagePath); err != nil {
		return false, nil
	}
	return true, ApplyStaged(stagePath, currentExePath, argv)
}
