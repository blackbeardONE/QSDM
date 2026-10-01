package legacymining

// guard.go (WP4): the canary environment and config loader (§6.1, S2), the
// D1/D2 durable-write primitives (§3.2), and CanaryGuard, the Guard (§6):
// allowlist, caps, pre-armed markers, latched states and the automatic
// triggers of §6.6.

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mining"
	hmacattest "github.com/blackbeardONE/QSDM/pkg/mining/attest/hmac"
)

// -----------------------------------------------------------------------------
// Environment and config (S2, §6.1)
// -----------------------------------------------------------------------------

// Env is the QSDM_LEGACY_MINING_* environment (§8 step 5).
type Env struct {
	Mode         Mode
	DBPath       string     // EnvDB: <stateDir>/LegacyDirName/DBFile
	ConfigPath   string     // EnvCanaryConfig
	ConfigSHA256 ConfigHash // EnvCanaryConfigSHA256
}

// LegacyDir returns the legacy-mining directory, Dir(DBPath). It holds the DB,
// KILL and the legacy-mining markers.
func (e Env) LegacyDir() string { return filepath.Dir(e.DBPath) }

// LoadEnv reads the four QSDM_LEGACY_MINING_* variables through getenv
// (os.Getenv in production). An empty value counts as unset. If all four are
// unset it returns ModeOff (Stage A). Otherwise every failure below is an S2
// refusal, and the caller exits ExitFatalRestore:
//   - a partial set wraps ErrEnvPartial;
//   - a mode other than ModeCanary or ModePublic, a DB path that is not a
//     clean absolute .../LegacyDirName/DBFile path, or a config path that is
//     not clean and absolute wraps ErrConfig;
//   - a pin that is not 64 hex characters wraps ErrConfigHash.
func LoadEnv(getenv func(string) string) (Env, error) {
	names := [...]string{EnvMode, EnvDB, EnvCanaryConfig, EnvCanaryConfigSHA256}
	var vals [len(names)]string
	var set, unset []string
	for i, n := range names {
		vals[i] = getenv(n)
		if vals[i] == "" {
			unset = append(unset, n)
		} else {
			set = append(set, n)
		}
	}
	if len(set) == 0 {
		return Env{Mode: ModeOff}, nil
	}
	if len(unset) != 0 {
		return Env{}, fmt.Errorf("%w: set %s; unset %s", ErrEnvPartial, strings.Join(set, ","), strings.Join(unset, ","))
	}
	e := Env{Mode: Mode(vals[0]), DBPath: vals[1], ConfigPath: vals[2]}
	if e.Mode != ModeCanary && e.Mode != ModePublic {
		return Env{}, fmt.Errorf("%w: %s=%q, want %q or %q", ErrConfig, EnvMode, vals[0], ModeCanary, ModePublic)
	}
	if !cleanAbs(e.DBPath) || filepath.Base(e.DBPath) != DBFile || filepath.Base(e.LegacyDir()) != LegacyDirName {
		return Env{}, fmt.Errorf("%w: %s=%q is not an absolute .../%s/%s path", ErrConfig, EnvDB, e.DBPath, LegacyDirName, DBFile)
	}
	if !cleanAbs(e.ConfigPath) {
		return Env{}, fmt.Errorf("%w: %s=%q is not a clean absolute path", ErrConfig, EnvCanaryConfig, e.ConfigPath)
	}
	if len(vals[3]) != hex.EncodedLen(len(e.ConfigSHA256)) {
		return Env{}, fmt.Errorf("%w: %s is not 64 hex characters", ErrConfigHash, EnvCanaryConfigSHA256)
	}
	if _, err := hex.Decode(e.ConfigSHA256[:], []byte(vals[3])); err != nil {
		return Env{}, fmt.Errorf("%w: %s: %v", ErrConfigHash, EnvCanaryConfigSHA256, err)
	}
	return e, nil
}

func cleanAbs(p string) bool { return filepath.IsAbs(p) && filepath.Clean(p) == p }

// maxConfigBytes bounds the canary config file.
const maxConfigBytes = 64 << 10

// LoadConfig reads the canary config at path and returns ParseConfig of its
// bytes. A read failure or an oversized file wraps ErrConfig.
func LoadConfig(path string, pin ConfigHash) (Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("%w: %v", ErrConfig, err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxConfigBytes+1))
	if err != nil {
		return Config{}, fmt.Errorf("%w: read %s: %v", ErrConfig, path, err)
	}
	if len(data) > maxConfigBytes {
		return Config{}, fmt.Errorf("%w: %s exceeds %d bytes", ErrConfig, path, maxConfigBytes)
	}
	return ParseConfig(data, pin)
}

// configV1Wire is the exact HL1 field set: a v1 file is decoded strictly
// against it, so a v2-only field in a v1 file is an unknown field.
type configV1Wire struct {
	Version         int          `json:"version"`
	Allowed         []AllowEntry `json:"allowed"`
	MaxProofsPerMin int          `json:"max_proofs_per_min"`
	MaxProofsTotal  uint64       `json:"max_proofs_total"`
	MaxPending      int          `json:"max_pending"`
	BudgetCell      uint64       `json:"budget_cell"`
	ExpiresUnix     int64        `json:"expires_unix"`
}

// configV2Wire is the v2 field set.
type configV2Wire struct {
	Version                 int          `json:"version"`
	Allowed                 []AllowEntry `json:"allowed"`
	MaxProofsPerMin         int          `json:"max_proofs_per_min"`
	MaxProofsPerMinPerOwner int          `json:"max_proofs_per_min_per_owner"`
	MaxProofsTotal          uint64       `json:"max_proofs_total"`
	MaxPending              int          `json:"max_pending"`
	MaxPendingPerOwner      int          `json:"max_pending_per_owner"`
	OwnerEpochCapCell       uint64       `json:"owner_epoch_cap_cell"`
	DifficultyBits          int          `json:"difficulty_bits"`
	RequireOperatorSig      bool         `json:"require_operator_sig"`
	RequireFullyBonded      bool         `json:"require_fully_bonded"`
	BondedSlotCap           int          `json:"bonded_slot_cap"`
	BudgetCell              uint64       `json:"budget_cell"`
	ExpiresUnix             int64        `json:"expires_unix"`
}

// decodeStrict decodes exactly one JSON value from data into v, rejecting
// unknown fields and trailing data.
func decodeStrict(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("%w: %v", ErrConfig, err)
	}
	if err := dec.Decode(new(json.RawMessage)); err != io.EOF {
		return fmt.Errorf("%w: trailing data after the config object", ErrConfig)
	}
	return nil
}

// ParseConfig checks sha256(data) against pin (ErrConfigHash), decodes data
// strictly against the field set of its version (unknown fields and trailing
// data are rejected) and validates the result with ValidateConfig. Decode
// and validation failures wrap ErrConfig. The hash is always over the raw
// bytes, before any decoding, so a v1 file's pin is unchanged from HL1.
//
// The version is read first with a lenient decode of the "version" member
// only; the strict decode against configV1Wire or configV2Wire follows. A
// v1 file is therefore accepted or refused exactly as by HL1.
func ParseConfig(data []byte, pin ConfigHash) (Config, error) {
	if sum := sha256.Sum256(data); sum != pin {
		return Config{}, fmt.Errorf("%w: file %x, pinned %x", ErrConfigHash, sum, pin)
	}
	var probe struct {
		Version int `json:"version"`
	}
	if err := json.NewDecoder(bytes.NewReader(data)).Decode(&probe); err != nil {
		return Config{}, fmt.Errorf("%w: %v", ErrConfig, err)
	}
	var c Config
	switch probe.Version {
	case ConfigVersion1:
		var w configV1Wire
		if err := decodeStrict(data, &w); err != nil {
			return Config{}, err
		}
		c = Config{
			Version: w.Version, Allowed: w.Allowed, MaxProofsPerMin: w.MaxProofsPerMin,
			MaxProofsTotal: w.MaxProofsTotal, MaxPending: w.MaxPending, BudgetCell: w.BudgetCell,
			ExpiresUnix: w.ExpiresUnix,
		}
	case ConfigVersion2:
		var w configV2Wire
		if err := decodeStrict(data, &w); err != nil {
			return Config{}, err
		}
		c = Config{
			Version: w.Version, Allowed: w.Allowed, MaxProofsPerMin: w.MaxProofsPerMin,
			MaxProofsTotal: w.MaxProofsTotal, MaxPending: w.MaxPending, BudgetCell: w.BudgetCell,
			ExpiresUnix:             w.ExpiresUnix,
			MaxProofsPerMinPerOwner: w.MaxProofsPerMinPerOwner, MaxPendingPerOwner: w.MaxPendingPerOwner,
			OwnerEpochCapCell: w.OwnerEpochCapCell, DifficultyBits: w.DifficultyBits,
			RequireOperatorSig: w.RequireOperatorSig, RequireFullyBonded: w.RequireFullyBonded,
			BondedSlotCap: w.BondedSlotCap,
		}
	default:
		return Config{}, fmt.Errorf("%w: version %d, want %d or %d", ErrConfig, probe.Version, ConfigVersion1, ConfigVersion2)
	}
	if err := ValidateConfig(c); err != nil {
		return Config{}, err
	}
	return c, nil
}

// ValidateConfig applies the Config rules documented in api.go for
// c.Version. A failure wraps ErrConfig and lists every violated rule. Expiry
// is not checked here: it is a graceful ADMISSION_STOP trigger, not a load
// failure. The mode rules are separate (CheckModeConfig).
func ValidateConfig(c Config) error {
	var bad []string
	switch c.Version {
	case ConfigVersion1:
		bad = validateV1(c)
	case ConfigVersion2:
		bad = validateV2(c)
	default:
		bad = []string{fmt.Sprintf("version %d, want %d or %d", c.Version, ConfigVersion1, ConfigVersion2)}
	}
	if len(bad) != 0 {
		return fmt.Errorf("%w: %s", ErrConfig, strings.Join(bad, "; "))
	}
	return nil
}

// validateV1 is the HL1 rule set, unchanged, plus the rule that a v1 Config
// sets no v2-only field (a v1 file cannot).
func validateV1(c Config) []string {
	var bad []string
	if len(c.Allowed) != 1 {
		bad = append(bad, fmt.Sprintf("%d allowed entries, want exactly 1", len(c.Allowed)))
	} else {
		if !isLowerHex64(c.Allowed[0].MinerAddr) {
			bad = append(bad, "allowed[0].miner_addr is not 64 lowercase hex characters")
		}
		if c.Allowed[0].NodeID == "" {
			bad = append(bad, "allowed[0].node_id is empty")
		}
	}
	if c.MaxProofsPerMin <= 0 {
		bad = append(bad, "max_proofs_per_min must be > 0")
	}
	if c.MaxProofsTotal == 0 {
		bad = append(bad, "max_proofs_total must be > 0")
	}
	if c.MaxPending <= 0 || c.MaxPending > MaxPendingLimit {
		bad = append(bad, fmt.Sprintf("max_pending %d outside 1..%d", c.MaxPending, MaxPendingLimit))
	}
	if c.BudgetCell == 0 {
		bad = append(bad, "budget_cell must be > 0")
	}
	if c.ExpiresUnix <= 0 {
		bad = append(bad, "expires_unix must be > 0")
	}
	if c.MaxProofsPerMinPerOwner != 0 || c.MaxPendingPerOwner != 0 || c.OwnerEpochCapCell != 0 || c.DifficultyBits != 0 ||
		c.RequireOperatorSig || c.RequireFullyBonded || c.BondedSlotCap != 0 {
		bad = append(bad, "a version 1 config sets a version 2 field")
	}
	return bad
}

// validateV2 is the HL2 rule set (api.go Config).
func validateV2(c Config) []string {
	var bad []string
	if len(c.Allowed) > MaxAllowedEntries {
		bad = append(bad, fmt.Sprintf("%d allowed entries, want at most %d", len(c.Allowed), MaxAllowedEntries))
	}
	nodes := make(map[string]int, len(c.Allowed))
	for i, e := range c.Allowed {
		if !isLowerHex64(e.MinerAddr) {
			bad = append(bad, fmt.Sprintf("allowed[%d].miner_addr is not 64 lowercase hex characters", i))
		}
		if e.NodeID == "" {
			bad = append(bad, fmt.Sprintf("allowed[%d].node_id is empty", i))
		} else if j, dup := nodes[e.NodeID]; dup {
			bad = append(bad, fmt.Sprintf("allowed[%d].node_id repeats allowed[%d]", i, j))
		} else {
			nodes[e.NodeID] = i
		}
	}
	if c.MaxProofsPerMin <= 0 || c.MaxProofsPerMin > MaxProofsPerMinLimit {
		bad = append(bad, fmt.Sprintf("max_proofs_per_min %d outside 1..%d", c.MaxProofsPerMin, MaxProofsPerMinLimit))
	}
	if c.MaxProofsPerMinPerOwner <= 0 || c.MaxProofsPerMinPerOwner > c.MaxProofsPerMin {
		bad = append(bad, fmt.Sprintf("max_proofs_per_min_per_owner %d outside 1..max_proofs_per_min", c.MaxProofsPerMinPerOwner))
	}
	if c.MaxProofsTotal == 0 {
		bad = append(bad, "max_proofs_total must be > 0")
	}
	if c.MaxPending <= 0 || c.MaxPending > MaxPendingLimit {
		bad = append(bad, fmt.Sprintf("max_pending %d outside 1..%d", c.MaxPending, MaxPendingLimit))
	}
	if c.MaxPendingPerOwner <= 0 || c.MaxPendingPerOwner > c.MaxPending {
		bad = append(bad, fmt.Sprintf("max_pending_per_owner %d outside 1..max_pending", c.MaxPendingPerOwner))
	}
	if c.BudgetCell == 0 {
		bad = append(bad, "budget_cell must be > 0")
	}
	if c.OwnerEpochCapCell == 0 || c.OwnerEpochCapCell > c.BudgetCell {
		bad = append(bad, fmt.Sprintf("owner_epoch_cap_cell %d outside 1..budget_cell", c.OwnerEpochCapCell))
	}
	if c.DifficultyBits < MinDifficultyBits || c.DifficultyBits > MaxDifficultyBits {
		bad = append(bad, fmt.Sprintf("difficulty_bits %d outside %d..%d", c.DifficultyBits, MinDifficultyBits, MaxDifficultyBits))
	}
	if c.BondedSlotCap <= 0 || c.BondedSlotCap > MaxBondedSlotCap {
		bad = append(bad, fmt.Sprintf("bonded_slot_cap %d outside 1..%d", c.BondedSlotCap, MaxBondedSlotCap))
	}
	if c.ExpiresUnix <= 0 {
		bad = append(bad, "expires_unix must be > 0")
	}
	return bad
}

// CheckModeConfig validates c (ValidateConfig) and then the mode/version
// rules. A failure wraps ErrConfig, and the boot exits ExitFatalRestore:
//   - ModeCanary accepts v1 or v2. A v2 canary needs at least one Allowed
//     entry: the canary is an allowlist.
//   - ModePublic requires v2 with RequireOperatorSig and RequireFullyBonded
//     (operator decisions M1 and M3). Allowed is optional (0..K).
//   - every other mode, including ModeOff, has no config and is refused.
//
// Passing these rules does not mean the binary enforces the config: see
// CheckSupported.
func CheckModeConfig(mode Mode, c Config) error {
	if err := ValidateConfig(c); err != nil {
		return err
	}
	switch mode {
	case ModeCanary:
		if c.Version == ConfigVersion2 && len(c.Allowed) == 0 {
			return fmt.Errorf("%w: mode %q with a version 2 config needs at least one allowed entry", ErrConfig, mode)
		}
		return nil
	case ModePublic:
		var bad []string
		if c.Version != ConfigVersion2 {
			bad = append(bad, fmt.Sprintf("version %d, want %d", c.Version, ConfigVersion2))
		} else {
			if !c.RequireOperatorSig {
				bad = append(bad, "require_operator_sig must be true")
			}
			if !c.RequireFullyBonded {
				bad = append(bad, "require_fully_bonded must be true")
			}
		}
		if len(bad) != 0 {
			return fmt.Errorf("%w: mode %q: %s", ErrConfig, mode, strings.Join(bad, "; "))
		}
		return nil
	}
	return fmt.Errorf("%w: mode %q takes no config; want %q or %q", ErrConfig, mode, ModeCanary, ModePublic)
}

// CheckSupported is the HL2 WP-A fail-closed gate. It returns an error
// wrapping ErrNotImplemented for every mode/config pair that the contract
// defines but this binary does not enforce yet, and the boot exits
// ExitFatalRestore:
//   - ModePublic (needs WP-B..E: per-owner admission, operator keys, the
//     per-owner ledger and the configurable difficulty);
//   - a v2 config in any mode, because the Guard, Ledger and miningsvc
//     still enforce only the v1 fields. Booting one would silently ignore
//     its per-owner caps, difficulty_bits, require_operator_sig and
//     require_fully_bonded.
//
// Only ModeCanary with a v1 config passes, exactly as in HL1. Each HL2 work
// package narrows this gate when its enforcement lands.
func CheckSupported(mode Mode, c Config) error {
	switch {
	case mode == ModePublic:
		return fmt.Errorf("%w: %s=%q needs HL2 work packages WP-B..E; this binary refuses to boot it", ErrNotImplemented, EnvMode, mode)
	case c.Version != ConfigVersion1:
		return fmt.Errorf("%w: config version %d is not enforced by this binary (HL2 WP-B..E); use a version %d config", ErrNotImplemented, c.Version, ConfigVersion1)
	}
	return nil
}

func isLowerHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// -----------------------------------------------------------------------------
// D1/D2 durable-write primitives (§3.2)
// -----------------------------------------------------------------------------

// durableFS is the file-system surface of D1 and D2. Guard tests replace it to
// simulate a full or failing file system.
type durableFS interface {
	OpenFile(name string, flag int, perm fs.FileMode) (durableFile, error)
	Rename(oldpath, newpath string) error
	Remove(name string) error
	Lstat(name string) (fs.FileInfo, error)
	ReadDir(name string) ([]fs.DirEntry, error)
	SyncDir(dir string) error
}

type durableFile interface {
	Write(p []byte) (int, error)
	Sync() error
	Close() error
}

type osDurableFS struct{}

func (osDurableFS) OpenFile(name string, flag int, perm fs.FileMode) (durableFile, error) {
	f, err := os.OpenFile(name, flag, perm)
	if err != nil {
		return nil, err
	}
	return f, nil
}

func (osDurableFS) Rename(oldpath, newpath string) error       { return os.Rename(oldpath, newpath) }
func (osDurableFS) Remove(name string) error                   { return os.Remove(name) }
func (osDurableFS) Lstat(name string) (fs.FileInfo, error)     { return os.Lstat(name) }
func (osDurableFS) ReadDir(name string) ([]fs.DirEntry, error) { return os.ReadDir(name) }

func (osDurableFS) SyncDir(dir string) error {
	if runtime.GOOS == "windows" {
		// Windows has no directory fsync; production is Linux.
		return nil
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	serr := d.Sync()
	if cerr := d.Close(); serr == nil {
		serr = cerr
	}
	return serr
}

// SyncDir is fsync(dir) (§3.2): open(dir, O_RDONLY), then fsync. It is a no-op
// on Windows, which has no directory fsync.
func SyncDir(dir string) error { return osDurableFS{}.SyncDir(dir) }

// WriteFileDurable is D1 (§3.2). It creates the temp file
// TempPrefix+<name>-<pid>-<16 hex random> in dir with
// O_CREAT|O_EXCL|O_WRONLY and mode 0600, writes data, fsyncs and closes it,
// renames it to dir/name and fsyncs dir. The temp name is unique, so a stale
// temp left by a SIGKILL never blocks a later write. On failure before the
// rename the temp is removed; a failure of the final fsync(dir) leaves name in
// place but not known to be durable.
func WriteFileDurable(dir, name string, data []byte) error {
	return d1Write(osDurableFS{}, dir, name, data)
}

func d1Write(fsys durableFS, dir, name string, data []byte) error {
	if name == "" || name == "." || name == ".." || name != filepath.Base(name) {
		return fmt.Errorf("legacymining: D1: invalid file name %q", name)
	}
	var rnd [8]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return fmt.Errorf("legacymining: D1 %s: random temp name: %w", name, err)
	}
	tmp := filepath.Join(dir, fmt.Sprintf("%s%s-%d-%s", TempPrefix, name, os.Getpid(), hex.EncodeToString(rnd[:])))
	f, err := fsys.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("legacymining: D1 %s: create temp: %w", name, err)
	}
	err = d1Fill(f, data)
	if err == nil {
		if rerr := fsys.Rename(tmp, filepath.Join(dir, name)); rerr != nil {
			err = fmt.Errorf("rename: %w", rerr)
		}
	}
	if err != nil {
		_ = fsys.Remove(tmp)
		return fmt.Errorf("legacymining: D1 %s: %w", name, err)
	}
	if err := fsys.SyncDir(dir); err != nil {
		return fmt.Errorf("legacymining: D1 %s: fsync dir: %w", name, err)
	}
	return nil
}

func d1Fill(f durableFile, data []byte) error {
	n, err := f.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	if err != nil {
		_ = f.Close()
		return fmt.Errorf("write: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("fsync: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close: %w", err)
	}
	return nil
}

// ArmMarker pre-arms marker base in dir: D1 of base+ArmedSuffix with content,
// unless the tripped base+TrippedSuffix already exists. It reports whether it
// armed. It is S3 for FAILSTOP (in the state directory) and S14 for TRIPPED and
// ADMISSION_STOPPED (Guard.PreArm).
func ArmMarker(dir, base string, content []byte) (bool, error) {
	return armMarker(osDurableFS{}, dir, base, content)
}

func armMarker(fsys durableFS, dir, base string, content []byte) (bool, error) {
	if _, err := fsys.Lstat(filepath.Join(dir, base+TrippedSuffix)); err == nil {
		return false, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return false, fmt.Errorf("legacymining: arm %s: %w", base, err)
	}
	if err := d1Write(fsys, dir, base+ArmedSuffix, content); err != nil {
		return false, err
	}
	return true, nil
}

// TripMarker is D2 (§3.2) for marker base in dir: rename(base+ArmedSuffix,
// base+TrippedSuffix), then fsync(dir). The rename allocates no data blocks,
// so a pre-armed marker trips on a full file system. If the rename or the
// fsync fails (for example, the marker was never armed), TripMarker falls back
// to D1 of base+TrippedSuffix with content. It returns nil once the tripped
// marker is durable or already existed, and an error only when both paths
// failed; the caller then fail-stops (FAILSTOP: exit ExitFailStop anyway;
// TRIPPED and ADMISSION_STOPPED: FailStopFunc(ExitFailStop, CauseMarkerIO)).
func TripMarker(dir, base string, content []byte) error {
	return tripMarker(osDurableFS{}, dir, base, content)
}

func tripMarker(fsys durableFS, dir, base string, content []byte) error {
	tripped := filepath.Join(dir, base+TrippedSuffix)
	if _, err := fsys.Lstat(tripped); err == nil {
		return nil
	}
	err := fsys.Rename(filepath.Join(dir, base+ArmedSuffix), tripped)
	if err == nil {
		if err = fsys.SyncDir(dir); err == nil {
			return nil
		}
		err = fmt.Errorf("fsync dir: %w", err)
	}
	if derr := d1Write(fsys, dir, base+TrippedSuffix, content); derr != nil {
		return fmt.Errorf("legacymining: trip %s: rename: %v; D1 fallback: %w", base, err, derr)
	}
	return nil
}

// RemoveStaleTemps removes every non-directory entry of dir whose name starts
// with TempPrefix, then fsyncs dir (§3.2). Call it only while holding the
// state lock, so it can never remove a live writer's temp (S3 and the mutating
// hl1-tail subcommands). A missing dir has nothing to clean and returns 0, nil.
func RemoveStaleTemps(dir string) (int, error) {
	return removeStaleTemps(osDurableFS{}, dir)
}

func removeStaleTemps(fsys durableFS, dir string) (int, error) {
	entries, err := fsys.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("legacymining: temp cleanup %s: %w", dir, err)
	}
	removed := 0
	var errs []error
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), TempPrefix) || e.IsDir() {
			continue
		}
		if err := fsys.Remove(filepath.Join(dir, e.Name())); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
			continue
		}
		removed++
	}
	if err := fsys.SyncDir(dir); err != nil {
		errs = append(errs, fmt.Errorf("fsync dir: %w", err))
	}
	if len(errs) != 0 {
		return removed, fmt.Errorf("legacymining: temp cleanup %s: %w", dir, errors.Join(errs...))
	}
	return removed, nil
}

// -----------------------------------------------------------------------------
// CanaryGuard (§6)
// -----------------------------------------------------------------------------

// GuardOptions configures NewGuard.
type GuardOptions struct {
	// Dir is the legacy-mining directory, Env.LegacyDir(). It must be an
	// existing absolute directory, not a symlink and, on Unix, mode 0700.
	// Otherwise NewGuard fails with ErrUnsafePath and mining stays closed.
	// Store.Open checks the owner.
	Dir string
	// Config and ConfigHash come from LoadConfig and Env.ConfigSHA256.
	Config     Config
	ConfigHash ConfigHash
	// Release is the binary's version string, written into armed markers.
	Release string
	// Store receives one events row per trip, best effort. It may be nil or
	// not yet open: the Guard is built before Store.Open (S7).
	Store Store
	// FailStop is failStop (cmd/qsdm). Required. It is called only when a
	// marker cannot be made durable (CauseMarkerIO).
	FailStop FailStopFunc
	// EnrollmentActive reports whether nodeID is an active enrollment owned
	// by owner (S16). Required. Admission stays closed while it is false.
	EnrollmentActive func(nodeID, owner string) bool
	// Now is the clock. nil means time.Now.
	Now func() time.Time
	// Logf logs trips and latches. nil means log.Printf.
	Logf func(format string, args ...any)
}

// CanaryGuard is the Guard. It holds the in-memory latches; the marker files
// make them survive restarts (§6.5). Admission-side counters are in memory and
// restart at zero on each boot.
//
// Locks: guard.mu is taken only to trip or pre-arm (L5); it may call the Store
// (events) and FailStop, never the Ledger. cmu is a leaf lock for the trigger
// counters and is never held across a trip or any call out of the Guard. All
// state reads are atomic.
type CanaryGuard struct {
	dir      string
	cfg      Config
	hash     ConfigHash
	store    Store
	failStop FailStopFunc
	enrolled func(nodeID, owner string) bool
	now      func() time.Time
	logf     func(format string, args ...any)
	fs       durableFS
	base     time.Time // clock origin; offsets below are monotonic ns since base
	marker   []byte    // ArmedMarker JSON: armed-marker and D1-fallback content

	// Latches.
	killed  atomic.Bool // KILL observed; sticky for the process
	frozen  atomic.Bool // TRIPPED
	stopped atomic.Bool // ADMISSION_STOPPED

	// S16 and NoSealTimeout.
	activateOnce atomic.Bool
	activated    atomic.Bool
	clean        atomic.Bool
	activatedAt  atomic.Int64
	lastSeal     atomic.Int64 // last local durable seal, or activation
	seals        atomic.Uint64

	mu           sync.Mutex // guard.mu
	frozenCause  string
	stoppedCause string

	cmu        sync.Mutex // leaf
	rateMinute int64      // TakeRate
	rateTaken  int
	subMinute  int64 // Admit, for the rate-burst trigger
	subCount   uint64
	burstLast  int64
	burstRun   int
	notAllowed triggerWindow
	duplicates triggerWindow
}

var _ Guard = (*CanaryGuard)(nil)

// NewGuard validates o and loads the TRIPPED.json and ADMISSION_STOPPED.json
// latches left by earlier boots. KILL is read by State on every call.
func NewGuard(o GuardOptions) (*CanaryGuard, error) {
	if err := ValidateConfig(o.Config); err != nil {
		return nil, err
	}
	// The CanaryGuard enforces the v1 (single allowlisted pair) rules only
	// (HL2 WP-A; WP-B lifts this).
	if err := CheckSupported(ModeCanary, o.Config); err != nil {
		return nil, err
	}
	if o.FailStop == nil || o.EnrollmentActive == nil {
		return nil, errors.New("legacymining: NewGuard: FailStop and EnrollmentActive are required")
	}
	if err := checkLegacyDir(o.Dir); err != nil {
		return nil, err
	}
	g := &CanaryGuard{
		dir:      o.Dir,
		cfg:      copyConfig(o.Config),
		hash:     o.ConfigHash,
		store:    o.Store,
		failStop: o.FailStop,
		enrolled: o.EnrollmentActive,
		now:      o.Now,
		logf:     o.Logf,
		fs:       osDurableFS{},
	}
	if g.now == nil {
		g.now = time.Now
	}
	if g.logf == nil {
		g.logf = log.Printf
	}
	g.base = g.now()
	g.marker, _ = json.Marshal(ArmedMarker{Release: o.Release, BootNS: g.base.UnixNano(), PID: os.Getpid()})
	g.marker = append(g.marker, '\n')
	for _, l := range []struct {
		file  string
		flag  *atomic.Bool
		cause *string
	}{
		{TrippedFile, &g.frozen, &g.frozenCause},
		{AdmissionStoppedFile, &g.stopped, &g.stoppedCause},
	} {
		_, err := g.fs.Lstat(filepath.Join(g.dir, l.file))
		switch {
		case err == nil:
			l.flag.Store(true)
			*l.cause = "latched"
			g.logf("legacymining: %s present: latched from an earlier boot", l.file)
		case !errors.Is(err, fs.ErrNotExist):
			return nil, fmt.Errorf("%w: %s: %v", ErrUnsafePath, l.file, err)
		}
	}
	return g, nil
}

func checkLegacyDir(dir string) error {
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("%w: %q is not absolute", ErrUnsafePath, dir)
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnsafePath, err)
	}
	if fi.Mode()&fs.ModeSymlink != 0 || !fi.IsDir() {
		return fmt.Errorf("%w: %s is not a directory", ErrUnsafePath, dir)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o700 {
		return fmt.Errorf("%w: %s has mode %#o, want 0700", ErrUnsafePath, dir, fi.Mode().Perm())
	}
	return nil
}

func copyConfig(c Config) Config {
	c.Allowed = append([]AllowEntry(nil), c.Allowed...)
	return c
}

// Config implements Guard.
func (g *CanaryGuard) Config() Config { return copyConfig(g.cfg) }

// ConfigHash implements Guard.
func (g *CanaryGuard) ConfigHash() ConfigHash { return g.hash }

func (g *CanaryGuard) mono(t time.Time) int64   { return int64(t.Sub(g.base)) }
func (g *CanaryGuard) minute(t time.Time) int64 { return g.mono(t) / int64(time.Minute) }

// State implements Guard. KILL is sticky once observed, like the other
// latches: clearing any state takes a restart (§6.5). An Lstat error other
// than not-exist counts as KILL.
func (g *CanaryGuard) State() State {
	g.checkTimers(g.now())
	return g.state()
}

func (g *CanaryGuard) state() State {
	switch {
	case g.isKilled():
		return StateKilled
	case g.frozen.Load():
		return StateFrozen
	case g.stopped.Load():
		return StateAdmissionStopped
	}
	return StateOpen
}

func (g *CanaryGuard) isKilled() bool {
	if g.killed.Load() {
		return true
	}
	_, err := g.fs.Lstat(filepath.Join(g.dir, KillFile))
	if errors.Is(err, fs.ErrNotExist) {
		return false
	}
	if g.killed.CompareAndSwap(false, true) {
		if err != nil {
			g.logf("legacymining: stat %s failed: %v; treating as KILL", KillFile, err)
		} else {
			g.logf("legacymining: %s present: KILLED", KillFile)
		}
	}
	return true
}

// checkTimers evaluates the time-based triggers lazily: expiry
// (ADMISSION_STOP) and NoSealTimeout after Activate (FREEZE).
func (g *CanaryGuard) checkTimers(now time.Time) {
	if !g.stopped.Load() && now.Unix() >= g.cfg.ExpiresUnix {
		g.StopAdmission(fmt.Sprintf("%s:expires_unix=%d", CauseExpired, g.cfg.ExpiresUnix))
	}
	if g.activated.Load() && !g.frozen.Load() {
		if idle := g.mono(now) - g.lastSeal.Load(); idle >= int64(NoSealTimeout) {
			g.Freeze(fmt.Sprintf("%s:%s", CauseNoSeal, time.Duration(idle).Round(time.Second)))
		}
	}
}

// AdmissionOpen implements Guard.
func (g *CanaryGuard) AdmissionOpen() bool {
	now := g.now()
	g.checkTimers(now)
	return g.closedReason(now) == ""
}

// closedReason returns "" when admission is open, and otherwise a short
// reason that becomes the 503 detail.
func (g *CanaryGuard) closedReason(now time.Time) string {
	if s := g.state(); s != StateOpen {
		return s.String()
	}
	switch {
	case !g.activated.Load():
		return "not-activated"
	case !g.clean.Load():
		return "reconcile-not-clean"
	case g.mono(now)-g.activatedAt.Load() < int64(QuietPeriod) || g.seals.Load() < QuietSeals:
		return "quiet-period"
	case now.Unix() >= g.cfg.ExpiresUnix:
		return "expired"
	}
	if e := g.cfg.Allowed[0]; !g.enrolled(e.NodeID, e.MinerAddr) {
		return "enrollment-inactive"
	}
	return ""
}

// PreArm implements Guard (S14).
func (g *CanaryGuard) PreArm() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	var errs []error
	for _, m := range [...]string{MarkerTripped, MarkerAdmissionStopped} {
		if _, err := armMarker(g.fs, g.dir, m, g.marker); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Activate implements Guard (S16). Only the first call has an effect.
func (g *CanaryGuard) Activate(reconcileClean bool) {
	if !g.activateOnce.CompareAndSwap(false, true) {
		g.logf("legacymining: Activate called again; ignored")
		return
	}
	now := g.mono(g.now())
	g.activatedAt.Store(now)
	g.lastSeal.Store(now)
	g.clean.Store(reconcileClean)
	g.activated.Store(true)
	if !reconcileClean {
		g.logf("legacymining: reconciliation not clean; admission stays closed until restart")
	}
}

// Admit implements Guard (§4.1 step 3).
func (g *CanaryGuard) Admit() error {
	now := g.now()
	g.recordSubmission(now)
	g.checkTimers(now)
	if r := g.closedReason(now); r != "" {
		return &Rejection{Kind: KindAdmissionClosed, Detail: r}
	}
	return nil
}

// recordSubmission feeds the rate-burst trigger: more than
// RateBurstFactor*MaxProofsPerMin submissions in each of RateBurstMinutes
// consecutive minutes.
func (g *CanaryGuard) recordSubmission(now time.Time) {
	m := g.minute(now)
	limit := uint64(g.cfg.MaxProofsPerMin) * RateBurstFactor
	g.cmu.Lock()
	if m != g.subMinute {
		g.subMinute, g.subCount = m, 0
	}
	g.subCount++
	trip := false
	if g.subCount == limit+1 {
		if g.burstRun > 0 && g.burstLast == m-1 {
			g.burstRun++
		} else {
			g.burstRun = 1
		}
		g.burstLast = m
		trip = g.burstRun >= RateBurstMinutes
	}
	g.cmu.Unlock()
	if trip {
		g.StopAdmission(fmt.Sprintf("%s:>%d/min for %d consecutive minutes", CauseRateBurst, limit, RateBurstMinutes))
	}
}

// Precheck implements Guard (§4.1 step 4).
func (g *CanaryGuard) Precheck(raw []byte) (Candidate, error) {
	p, err := mining.ParseProof(raw)
	if err != nil {
		return Candidate{}, &Rejection{Kind: KindMalformed, Detail: err.Error()}
	}
	allow := g.cfg.Allowed[0]
	if p.MinerAddr != allow.MinerAddr {
		g.countNotAllowlisted()
		return Candidate{}, &Rejection{Kind: KindMinerNotAllowed}
	}
	if p.Attestation.Type != mining.AttestationTypeHMAC {
		return Candidate{}, &Rejection{Kind: KindAttestationType, Detail: fmt.Sprintf("want %s", mining.AttestationTypeHMAC)}
	}
	b, err := hmacattest.ParseBundle(p.Attestation.BundleBase64)
	if err != nil {
		return Candidate{}, &Rejection{Kind: KindMalformed, Detail: "bundle: " + err.Error()}
	}
	if b.NodeID != allow.NodeID {
		g.countNotAllowlisted()
		return Candidate{}, &Rejection{Kind: KindNodeNotAllowed}
	}
	c := Candidate{Proof: p, NodeID: b.NodeID}
	if len(b.Nonce) != hex.EncodedLen(len(c.AttNonce)) {
		return Candidate{}, &Rejection{Kind: KindMalformed, Detail: "bundle: nonce is not 64 hex characters"}
	}
	if _, err := hex.Decode(c.AttNonce[:], []byte(b.Nonce)); err != nil {
		return Candidate{}, &Rejection{Kind: KindMalformed, Detail: "bundle: nonce: " + err.Error()}
	}
	return c, nil
}

func (g *CanaryGuard) countNotAllowlisted() {
	now := g.mono(g.now())
	g.cmu.Lock()
	n := g.notAllowed.add(now, NotAllowlistedLimit)
	g.cmu.Unlock()
	if n >= NotAllowlistedLimit {
		g.StopAdmission(fmt.Sprintf("%s:%d in %s", CauseNotAllowlisted, n, TriggerWindow))
	}
}

// TakeRate implements Guard (§4.1 step 5).
func (g *CanaryGuard) TakeRate() error {
	m := g.minute(g.now())
	g.cmu.Lock()
	if m != g.rateMinute {
		g.rateMinute, g.rateTaken = m, 0
	}
	ok := g.rateTaken < g.cfg.MaxProofsPerMin
	if ok {
		g.rateTaken++
	}
	g.cmu.Unlock()
	if !ok {
		return &Rejection{Kind: KindRateLimited, Detail: fmt.Sprintf("max %d proofs per minute", g.cfg.MaxProofsPerMin)}
	}
	return nil
}

// hmacNonceReplay starts the HMAC verifier's nonce-replay rejection
// (pkg/mining/attest/hmac VerifyAttestation step 6c). mining.Verifier
// flattens attestation errors into a RejectError detail, so the sentinel
// does not survive Verify; guard_test.go pins this text against the verifier.
const hmacNonceReplay = "hmac: nonce already used by node "

// countsAsDuplicate reports whether err counts toward the §6.6
// duplicate/nonce-conflict trigger (see Guard.ObserveRejection).
func countsAsDuplicate(err error) bool {
	if err == nil {
		return false
	}
	switch RejectKindOf(err) {
	case KindDuplicate, KindNonceConflict:
		return true
	case 0:
	default:
		return false
	}
	var re *mining.RejectError
	if errors.As(err, &re) {
		return re.Reason == mining.ReasonDuplicate ||
			(re.Reason == mining.ReasonAttestation && strings.Contains(re.Detail, hmacNonceReplay))
	}
	return errors.Is(err, mining.ErrAttestationNonceMismatch) && strings.Contains(err.Error(), hmacNonceReplay)
}

// ObserveRejection implements Guard. It never trips FREEZE: a DB UNIQUE hit is
// a 400 that only counts toward ADMISSION_STOP.
func (g *CanaryGuard) ObserveRejection(err error) {
	if !countsAsDuplicate(err) {
		return
	}
	now := g.mono(g.now())
	g.cmu.Lock()
	n := g.duplicates.add(now, DuplicateLimit+1)
	g.cmu.Unlock()
	if n > DuplicateLimit {
		g.StopAdmission(fmt.Sprintf("%s:%d in %s", CauseDuplicates, n, TriggerWindow))
	}
}

// ObserveSeal implements Guard. Only local seals after Activate count toward
// QuietSeals and restart the NoSealTimeout clock.
func (g *CanaryGuard) ObserveSeal(height uint64, local bool) {
	if !local || !g.activated.Load() {
		return
	}
	g.lastSeal.Store(g.mono(g.now()))
	g.seals.Add(1)
}

// ObserveTotals implements Guard. Totals of another config window, or a
// non-finite or negative amount, also stop admission (fail closed).
func (g *CanaryGuard) ObserveTotals(t Totals, rewardCell float64) {
	if t.ConfigSHA256 != g.hash {
		g.StopAdmission(fmt.Sprintf("%s:totals for config %x, active %x", CauseBudget, t.ConfigSHA256[:8], g.hash[:8]))
		return
	}
	if t.Proofs >= g.cfg.MaxProofsTotal {
		g.StopAdmission(fmt.Sprintf("%s:%d>=%d", CauseProofsTotal, t.Proofs, g.cfg.MaxProofsTotal))
	}
	limit := float64(g.cfg.BudgetCell) - BudgetStopMarginCells*rewardCell
	if !finite(t.Emitted) || t.Emitted < 0 || !finite(rewardCell) || rewardCell <= 0 || t.Emitted >= limit {
		g.StopAdmission(fmt.Sprintf("%s:emitted=%v,limit=%v,rewardCell=%v", CauseBudget, t.Emitted, limit, rewardCell))
	}
}

func finite(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }

// Freeze implements Guard.
func (g *CanaryGuard) Freeze(cause string) {
	g.trip(&g.frozen, &g.frozenCause, MarkerTripped, "freeze", cause)
}

// StopAdmission implements Guard.
func (g *CanaryGuard) StopAdmission(cause string) {
	g.trip(&g.stopped, &g.stoppedCause, MarkerAdmissionStopped, "admission-stop", cause)
}

// trip latches in memory first, so every later state read fails closed, then
// makes the marker durable (D2, falling back to D1). If both fail it calls
// FailStop(ExitFailStop, CauseMarkerIO). The cause file and the events row are
// best effort.
func (g *CanaryGuard) trip(latched *atomic.Bool, first *string, marker, kind, cause string) {
	if latched.Load() {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if latched.Load() {
		return
	}
	latched.Store(true)
	*first = cause
	at := g.now().UnixNano()
	g.logf("legacymining: %s (%s): %s", kind, marker, cause)
	if err := tripMarker(g.fs, g.dir, marker, g.marker); err != nil {
		g.logf("legacymining: %s: %v; fail-stop %s", kind, err, CauseMarkerIO)
		g.failStop(ExitFailStop, CauseMarkerIO)
	}
	b, _ := json.Marshal(MarkerCause{Cause: cause, AtNS: at})
	if err := d1Write(g.fs, g.dir, marker+CauseSuffix, append(b, '\n')); err != nil {
		g.logf("legacymining: %s: cause file (best effort): %v", kind, err)
	}
	if g.store != nil {
		if err := g.store.Event(Event{AtNS: at, Kind: kind, Detail: cause}); err != nil {
			g.logf("legacymining: %s: events row (best effort): %v", kind, err)
		}
	}
}

// triggerWindow counts events in the trailing TriggerWindow (§6.6). add keeps
// at most keep timestamps, which is enough to decide a limit of keep.
type triggerWindow struct{ at []int64 }

func (w *triggerWindow) add(now int64, keep int) int {
	cut := now - int64(TriggerWindow)
	i := 0
	for i < len(w.at) && w.at[i] <= cut {
		i++
	}
	w.at = append(w.at[:0], w.at[i:]...)
	w.at = append(w.at, now)
	if len(w.at) > keep {
		w.at = append(w.at[:0], w.at[len(w.at)-keep:]...)
	}
	return len(w.at)
}
