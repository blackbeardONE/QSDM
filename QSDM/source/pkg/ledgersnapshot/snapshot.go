// Package ledgersnapshot captures a private, lock-verified copy of the
// validator files required for ledger reconciliation.
package ledgersnapshot

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/chain"
)

const (
	manifestSchemaVersion = 1
	manifestFileName      = "ledger-snapshot.json"
	stateLockFileName     = "qsdm-validator.state.lock"
)

var requiredFiles = []string{
	"qsdm_accounts.json",
	"qsdm_chain.ndjson",
	"qsdm_enrollment.json",
	"qsdm_staking.json",
}

// Options controls one stopped-validator snapshot.
type Options struct {
	StateDir string
	OutDir   string
	Now      time.Time
}

// FileEvidence identifies one exact copied state file without exposing any
// wallet, node, GPU, or key identifiers from its contents.
type FileEvidence struct {
	Name      string `json:"name"`
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256"`
}

// Manifest describes the copied bytes and the stopped-validator guarantee.
type Manifest struct {
	SchemaVersion            int            `json:"schema_version"`
	CreatedAt                string         `json:"created_at"`
	Mode                     string         `json:"mode"`
	Privacy                  string         `json:"privacy"`
	StateLock                string         `json:"state_lock"`
	ValidatorStoppedVerified bool           `json:"validator_stopped_verified"`
	Files                    []FileEvidence `json:"files"`
}

// ManifestFileName returns the fixed manifest name written into every
// snapshot directory.
func ManifestFileName() string { return manifestFileName }

// RequiredFiles returns a copy of the validator file list captured by this
// package.
func RequiredFiles() []string { return append([]string(nil), requiredFiles...) }

// Capture acquires the validator's own state-directory lock, copies the four
// reconciliation files into a new private directory, and records their
// hashes. The destination is removed on any failure and is never overwritten.
func Capture(opts Options) (Manifest, error) {
	stateDir, outDir, err := validatePaths(opts.StateDir, opts.OutDir)
	if err != nil {
		return Manifest{}, err
	}
	if opts.Now.IsZero() {
		opts.Now = time.Now().UTC()
	}

	lockPath := filepath.Join(stateDir, stateLockFileName)
	if info, statErr := os.Lstat(lockPath); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
		return Manifest{}, fmt.Errorf("ledger snapshot: state lock must not be a symbolic link: %s", lockPath)
	} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return Manifest{}, fmt.Errorf("ledger snapshot: inspect state lock: %w", statErr)
	}

	stateLock, err := chain.AcquireStateLock(lockPath)
	if err != nil {
		return Manifest{}, fmt.Errorf("ledger snapshot: validator must be stopped before capture: %w", err)
	}
	lockHeld := true
	defer func() {
		if lockHeld {
			_ = stateLock.Close()
		}
	}()

	if err := os.Mkdir(outDir, 0o700); err != nil {
		if errors.Is(err, os.ErrExist) {
			return Manifest{}, fmt.Errorf("ledger snapshot: refusing to overwrite existing destination %s", outDir)
		}
		return Manifest{}, fmt.Errorf("ledger snapshot: create destination: %w", err)
	}
	succeeded := false
	defer func() {
		if !succeeded {
			cleanupSnapshotDir(outDir)
		}
	}()

	result := Manifest{
		SchemaVersion:            manifestSchemaVersion,
		CreatedAt:                opts.Now.UTC().Format(time.RFC3339Nano),
		Mode:                     "stopped_validator_copy",
		Privacy:                  "aggregate evidence only; source paths and state identifiers are omitted",
		StateLock:                stateLockFileName,
		ValidatorStoppedVerified: true,
		Files:                    make([]FileEvidence, 0, len(requiredFiles)),
	}
	for _, name := range requiredFiles {
		evidence, copyErr := copyVerifiedRegularFile(
			filepath.Join(stateDir, name),
			filepath.Join(outDir, name),
			name,
		)
		if copyErr != nil {
			return Manifest{}, copyErr
		}
		result.Files = append(result.Files, evidence)
	}

	manifestData, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return Manifest{}, fmt.Errorf("ledger snapshot: encode manifest: %w", err)
	}
	manifestData = append(manifestData, '\n')
	if err := writeNewFile(filepath.Join(outDir, manifestFileName), manifestData, 0o600); err != nil {
		return Manifest{}, fmt.Errorf("ledger snapshot: write manifest: %w", err)
	}
	if err := stateLock.Close(); err != nil {
		return Manifest{}, fmt.Errorf("ledger snapshot: release validator state lock: %w", err)
	}
	lockHeld = false
	succeeded = true
	return result, nil
}

func validatePaths(stateDir, outDir string) (string, string, error) {
	if strings.TrimSpace(stateDir) == "" || strings.TrimSpace(outDir) == "" {
		return "", "", errors.New("ledger snapshot: state directory and output directory are required")
	}
	stateAbs, err := filepath.Abs(stateDir)
	if err != nil {
		return "", "", fmt.Errorf("ledger snapshot: resolve state directory: %w", err)
	}
	outAbs, err := filepath.Abs(outDir)
	if err != nil {
		return "", "", fmt.Errorf("ledger snapshot: resolve output directory: %w", err)
	}
	if pathsOverlap(stateAbs, outAbs) {
		return "", "", errors.New("ledger snapshot: state and output directories must be disjoint")
	}
	info, err := os.Stat(stateAbs)
	if err != nil {
		return "", "", fmt.Errorf("ledger snapshot: inspect state directory: %w", err)
	}
	if !info.IsDir() {
		return "", "", fmt.Errorf("ledger snapshot: state path is not a directory: %s", stateAbs)
	}
	stateResolved, err := filepath.EvalSymlinks(stateAbs)
	if err != nil {
		return "", "", fmt.Errorf("ledger snapshot: resolve state directory links: %w", err)
	}
	if _, err := os.Lstat(outAbs); err == nil {
		return "", "", fmt.Errorf("ledger snapshot: refusing to overwrite existing destination %s", outAbs)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", "", fmt.Errorf("ledger snapshot: inspect destination: %w", err)
	}
	outParent := filepath.Dir(outAbs)
	if err := os.MkdirAll(outParent, 0o700); err != nil {
		return "", "", fmt.Errorf("ledger snapshot: create destination parent: %w", err)
	}
	parentResolved, err := filepath.EvalSymlinks(outParent)
	if err != nil {
		return "", "", fmt.Errorf("ledger snapshot: resolve destination parent links: %w", err)
	}
	outResolved := filepath.Join(parentResolved, filepath.Base(outAbs))
	if pathsOverlap(stateResolved, outResolved) {
		return "", "", errors.New("ledger snapshot: resolved state and output directories must be disjoint")
	}
	return stateResolved, outResolved, nil
}

func pathsOverlap(a, b string) bool {
	return pathWithin(a, b) || pathWithin(b, a)
}

func pathWithin(parent, candidate string) bool {
	rel, err := filepath.Rel(parent, candidate)
	if err != nil || filepath.IsAbs(rel) {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)))
}

func copyVerifiedRegularFile(sourcePath, destinationPath, name string) (FileEvidence, error) {
	before, err := os.Lstat(sourcePath)
	if err != nil {
		return FileEvidence{}, fmt.Errorf("ledger snapshot: inspect required file %s: %w", name, err)
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return FileEvidence{}, fmt.Errorf("ledger snapshot: required file %s must be a regular file", name)
	}

	source, err := os.Open(sourcePath)
	if err != nil {
		return FileEvidence{}, fmt.Errorf("ledger snapshot: open required file %s: %w", name, err)
	}
	defer source.Close()
	opened, err := source.Stat()
	if err != nil {
		return FileEvidence{}, fmt.Errorf("ledger snapshot: stat opened file %s: %w", name, err)
	}
	if !os.SameFile(before, opened) {
		return FileEvidence{}, fmt.Errorf("ledger snapshot: required file %s changed while it was opened", name)
	}

	destination, err := os.OpenFile(destinationPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return FileEvidence{}, fmt.Errorf("ledger snapshot: create copy %s: %w", name, err)
	}
	keep := false
	defer func() {
		_ = destination.Close()
		if !keep {
			_ = os.Remove(destinationPath)
		}
	}()

	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(destination, hash), source)
	if err != nil {
		return FileEvidence{}, fmt.Errorf("ledger snapshot: copy %s: %w", name, err)
	}
	if err := destination.Sync(); err != nil {
		return FileEvidence{}, fmt.Errorf("ledger snapshot: sync copy %s: %w", name, err)
	}
	if err := destination.Close(); err != nil {
		return FileEvidence{}, fmt.Errorf("ledger snapshot: close copy %s: %w", name, err)
	}
	after, err := os.Lstat(sourcePath)
	if err != nil {
		return FileEvidence{}, fmt.Errorf("ledger snapshot: recheck required file %s: %w", name, err)
	}
	openedAfter, err := source.Stat()
	if err != nil {
		return FileEvidence{}, fmt.Errorf("ledger snapshot: restat opened file %s: %w", name, err)
	}
	if !os.SameFile(opened, after) || openedAfter.Size() != opened.Size() || !openedAfter.ModTime().Equal(opened.ModTime()) {
		return FileEvidence{}, fmt.Errorf("ledger snapshot: required file %s changed during capture", name)
	}
	if written != opened.Size() {
		return FileEvidence{}, fmt.Errorf("ledger snapshot: copied size for %s is %d bytes, expected %d", name, written, opened.Size())
	}
	keep = true
	return FileEvidence{
		Name:      name,
		SizeBytes: written,
		SHA256:    hex.EncodeToString(hash.Sum(nil)),
	}, nil
}

func writeNewFile(path string, data []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if !closed {
			_ = f.Close()
		}
	}()
	written, err := f.Write(data)
	if err != nil {
		return err
	}
	if written != len(data) {
		return io.ErrShortWrite
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	closed = true
	return nil
}

func cleanupSnapshotDir(path string) {
	for _, name := range requiredFiles {
		_ = os.Remove(filepath.Join(path, name))
	}
	_ = os.Remove(filepath.Join(path, manifestFileName))
	_ = os.Remove(path)
}
