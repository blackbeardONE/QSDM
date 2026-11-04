package ledgersnapshot

// bootstrap.go: the read side of a snapshot used for the verified snapshot
// bootstrap of a new follower (QSDM/docs/docs/SNAPSHOT_BOOTSTRAP.md).
// The snapshot format is the one Capture writes (the four validator files and
// ledger-snapshot.json); export adds an advisory snapshot-checkpoint.json.
// Nothing here decides whether the content is valid chain state: that is the
// node's job (cmd/qsdm --snapshot-import), which only ever reads the staged
// copy this file produces, never the source directory.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
)

const checkpointFileName = "snapshot-checkpoint.json"

// maxManifestBytes bounds the manifest and checkpoint reads.
const maxManifestBytes = 64 << 10

// CheckpointFileName returns the advisory checkpoint file name an export
// writes beside the manifest.
func CheckpointFileName() string { return checkpointFileName }

// Checkpoint is the tip of an exported snapshot. Export writes it for the
// operator; an importer never trusts it (the trusted checkpoint is pinned in
// the importing node's configuration) and only refuses when it disagrees.
type Checkpoint struct {
	Height       uint64 `json:"height"`
	BlockHash    string `json:"block_hash"`
	StateRoot    string `json:"state_root"`
	ProducerID   string `json:"producer_id"`
	TipSigned    bool   `json:"tip_signed"`
	Blocks       int    `json:"blocks"`
	JournalBytes int64  `json:"journal_bytes"`
}

var sha256HexPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ReadManifest strictly decodes dir/ledger-snapshot.json and checks that it
// describes exactly the files Capture writes, in order.
func ReadManifest(dir string) (Manifest, error) {
	data, err := readSmallRegularFile(filepath.Join(dir, manifestFileName))
	if err != nil {
		return Manifest{}, fmt.Errorf("ledger snapshot: manifest: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		return Manifest{}, fmt.Errorf("ledger snapshot: manifest invalid: %w", err)
	}
	if err := dec.Decode(new(json.RawMessage)); err != io.EOF {
		return Manifest{}, errors.New("ledger snapshot: manifest has trailing data")
	}
	if m.SchemaVersion != manifestSchemaVersion {
		return Manifest{}, fmt.Errorf("ledger snapshot: manifest schema_version %d, want %d", m.SchemaVersion, manifestSchemaVersion)
	}
	if !m.ValidatorStoppedVerified || m.StateLock != stateLockFileName {
		return Manifest{}, errors.New("ledger snapshot: manifest does not record a lock-verified stopped-validator copy")
	}
	if len(m.Files) != len(requiredFiles) {
		return Manifest{}, fmt.Errorf("ledger snapshot: manifest lists %d files, want %d", len(m.Files), len(requiredFiles))
	}
	for i, f := range m.Files {
		if f.Name != requiredFiles[i] {
			return Manifest{}, fmt.Errorf("ledger snapshot: manifest file %d is %q, want %q", i, f.Name, requiredFiles[i])
		}
		if f.SizeBytes < 0 || !sha256HexPattern.MatchString(f.SHA256) {
			return Manifest{}, fmt.Errorf("ledger snapshot: manifest entry %s is malformed", f.Name)
		}
	}
	return m, nil
}

// StageVerified copies the snapshot in snapshotDir into stageDir (created
// here, mode 0700, never overwritten), hashing every byte it writes, and
// returns the manifest only if every staged file has exactly the manifest's
// size and SHA-256. The caller verifies and installs the staged copy, so a
// source changed after this check cannot reach the node. stageDir is removed
// on any failure.
func StageVerified(snapshotDir, stageDir string) (Manifest, error) {
	m, err := ReadManifest(snapshotDir)
	if err != nil {
		return Manifest{}, err
	}
	if err := os.Mkdir(stageDir, 0o700); err != nil {
		if errors.Is(err, os.ErrExist) {
			return Manifest{}, fmt.Errorf("ledger snapshot: refusing to overwrite existing staging directory %s", stageDir)
		}
		return Manifest{}, fmt.Errorf("ledger snapshot: create staging directory: %w", err)
	}
	ok := false
	defer func() {
		if !ok {
			cleanupSnapshotDir(stageDir)
		}
	}()
	for _, want := range m.Files {
		got, err := copyVerifiedRegularFile(filepath.Join(snapshotDir, want.Name), filepath.Join(stageDir, want.Name), want.Name)
		if err != nil {
			return Manifest{}, err
		}
		if got.SizeBytes != want.SizeBytes || got.SHA256 != want.SHA256 {
			return Manifest{}, fmt.Errorf("ledger snapshot: %s does not match the manifest (size %d sha256 %s, manifest size %d sha256 %s)",
				want.Name, got.SizeBytes, got.SHA256, want.SizeBytes, want.SHA256)
		}
	}
	ok = true
	return m, nil
}

// WriteCheckpoint writes dir/snapshot-checkpoint.json (never overwritten).
func WriteCheckpoint(dir string, cp Checkpoint) error {
	data, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return fmt.Errorf("ledger snapshot: encode checkpoint: %w", err)
	}
	return writeNewFile(filepath.Join(dir, checkpointFileName), append(data, '\n'), 0o600)
}

// ReadCheckpoint reads dir/snapshot-checkpoint.json. A missing file returns
// ok == false and no error.
func ReadCheckpoint(dir string) (cp Checkpoint, ok bool, err error) {
	data, err := readSmallRegularFile(filepath.Join(dir, checkpointFileName))
	if errors.Is(err, os.ErrNotExist) {
		return Checkpoint{}, false, nil
	}
	if err != nil {
		return Checkpoint{}, false, fmt.Errorf("ledger snapshot: checkpoint: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cp); err != nil {
		return Checkpoint{}, false, fmt.Errorf("ledger snapshot: checkpoint invalid: %w", err)
	}
	return cp, true, nil
}

// RemoveSnapshot removes a snapshot directory written by Capture (and an
// export's checkpoint file). Unknown files are left, so the directory itself
// survives if anything else was put in it.
func RemoveSnapshot(dir string) {
	_ = os.Remove(filepath.Join(dir, checkpointFileName))
	cleanupSnapshotDir(dir)
}

// RemoveStage removes a staging directory made by StageVerified, including
// anything the caller left in it.
func RemoveStage(stageDir string) error { return os.RemoveAll(stageDir) }

// FileSHA256 hashes one regular file (used to record the installed bytes).
func FileSHA256(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

func readSmallRegularFile(path string) ([]byte, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	if fi.Size() > maxManifestBytes {
		return nil, fmt.Errorf("%s is %d bytes (limit %d)", path, fi.Size(), maxManifestBytes)
	}
	return os.ReadFile(path)
}
