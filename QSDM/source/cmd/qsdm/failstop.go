package main

// failstop.go (HL1 WP9): the runtime fail-stop (FAILSTOP.armed ->
// FAILSTOP.json, exit 86), the boot refusal fatalRestore (exit 78), and the
// startup steps S1-S3 that run immediately after the state lock (design rev 4
// §3.2, §5, Appendix A):
//
//	S1  FAILSTOP.json present                                   -> exit 78
//	S2  partial or invalid QSDM_LEGACY_MINING_* environment,
//	    config SHA-256 mismatch, invalid config, canary mode
//	    with a non-empty QSDM_CHAIN_SYNC_URLS                   -> exit 78
//	S3  remove stale D1 temps in both HL1 directories, then D1
//	    FAILSTOP.armed; any failure                             -> exit 78
//
// S1-S3 run only while the process holds qsdm-validator.state.lock (W9, L8),
// so the temp cleanup can never remove a live writer's temp file.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/blackbeardONE/QSDM/internal/legacymining"
	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/producerpolicy"
)

// hl1Exit is os.Exit. Tests replace it to observe exit codes without leaving
// the test process.
var hl1Exit = os.Exit

// hl1Stderrf writes one line to stderr, which journald captures. The standard
// logger is not used: the web log viewer may redirect it to the log file.
func hl1Stderrf(format string, args ...any) {
	fmt.Fprintln(os.Stderr, fmt.Sprintf(format, args...))
}

// fatalRestore refuses the boot with exit 78 (ExitFatalRestore). systemd's
// RestartPreventExitStatus excludes 78 from auto-restart, so the unit stays
// failed until the operator follows the matching runbook and R-START. Like
// log.Fatalf it runs no deferred functions; the OS releases the state lock.
func fatalRestore(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if logger != nil {
		logger.Error("hl1: boot refused", "exit_code", legacymining.ExitFatalRestore, "error_str", msg)
	}
	hl1Stderrf("FATAL (exit %d): %s", legacymining.ExitFatalRestore, msg)
	hl1Exit(legacymining.ExitFatalRestore)
}

// The four formerly exit-1 boot steps that now exit 78 (design rev 4 §2 (e),
// ops-P1-1). Each returns only when fatalRestore's exit returns (tests).

// hl1AcquireStateLock is S0: chain.AcquireStateLock (an exclusive
// non-blocking flock). A busy or unusable lock exits 78 before anything else
// in the state directory is touched.
func hl1AcquireStateLock(path string) *chain.StateLock {
	lock, err := chain.AcquireStateLock(path)
	if err != nil {
		fatalRestore("validator state lock: %v", err)
		return nil
	}
	return lock
}

// hl1LockedTransitionPreflight is the transition journal-prefix check under
// the lock (S4).
func hl1LockedTransitionPreflight(p *producerpolicy.Transition, journalPath string) {
	if err := p.ValidateJournalPrefix(journalPath); err != nil {
		fatalRestore("producer transition locked preflight: %v", err)
	}
}

// hl1OpenChainJournal opens the append-only chain journal (S5 is done).
func hl1OpenChainJournal(path string, tip *chain.Block) *chain.ChainJournal {
	j, err := chain.OpenChainJournal(path, tip)
	if err != nil {
		fatalRestore("chain persistence: open journal %s: %v", path, err)
		return nil
	}
	return j
}

// hl1PersistenceReserve parses QSDM_MIN_PERSISTENCE_FREE_BYTES.
func hl1PersistenceReserve(raw string) uint64 {
	v, err := parsePersistenceReserve(raw)
	if err != nil {
		fatalRestore("chain persistence: invalid disk reserve: %v", err)
		return 0
	}
	return v
}

// hl1FailStopper implements failStop (legacymining.FailStopFunc). It is armed
// at S3 with the state directory and the FAILSTOP.armed content, which is also
// the D1 fallback content of FAILSTOP.json.
type hl1FailStopper struct {
	once sync.Once

	mu     sync.Mutex
	dir    string // state directory; "" until Arm
	marker []byte

	exit func(int)
	now  func() time.Time
	logf func(format string, args ...any)
}

// hl1FailStop is the process's fail-stopper, armed at S3.
var hl1FailStop = &hl1FailStopper{}

// failStop is the FailStopFunc handed to the block driver, the guard and the
// persistence hook. It never returns in production (L6).
func failStop(code int, cause string) { hl1FailStop.Stop(code, cause) }

// arm records the state directory and marker content that Stop trips.
func (f *hl1FailStopper) arm(dir string, marker []byte) {
	f.mu.Lock()
	f.dir = dir
	f.marker = append([]byte(nil), marker...)
	f.mu.Unlock()
}

// Stop trips FAILSTOP (D2: rename FAILSTOP.armed to FAILSTOP.json, then
// fsync(dir); D1 of FAILSTOP.json if the rename fails), writes
// FAILSTOP.cause.json best effort, and exits with code. If the marker cannot
// be made durable it still exits (§3.2 D2). A sync.Once guards it: a
// concurrent second caller blocks inside the Once until the process exits.
// Stop returns only when the injected exit function returns (tests).
func (f *hl1FailStopper) Stop(code int, cause string) {
	f.once.Do(func() {
		logf := f.logf
		if logf == nil {
			logf = hl1Stderrf
		}
		now := f.now
		if now == nil {
			now = time.Now
		}
		exit := f.exit
		if exit == nil {
			exit = hl1Exit
		}
		f.mu.Lock()
		dir, marker := f.dir, f.marker
		f.mu.Unlock()

		if logger != nil {
			logger.Error("hl1: FAILSTOP", "exit_code", code, "cause", cause)
		}
		logf("hl1: FAILSTOP (exit %d): %s", code, cause)
		if dir == "" {
			logf("hl1: FAILSTOP is not armed; exiting without a marker")
		} else {
			if err := legacymining.TripMarker(dir, legacymining.MarkerFailStop, marker); err != nil {
				logf("hl1: FAILSTOP marker is not durable (exiting anyway): %v", err)
			}
			data, _ := json.Marshal(legacymining.MarkerCause{Cause: cause, AtNS: now().UnixNano()})
			if err := legacymining.WriteFileDurable(dir, legacymining.FailStopCauseFile, append(data, '\n')); err != nil {
				logf("hl1: %s not written (best effort): %v", legacymining.FailStopCauseFile, err)
			}
		}
		exit(code)
	})
}

// hl1BootConfig is the S2 result.
type hl1BootConfig struct {
	Env    legacymining.Env
	Config legacymining.Config // valid only in ModeCanary (ModePublic never passes S2 yet)
}

// Canary reports whether the canary environment is set.
func (b hl1BootConfig) Canary() bool { return b.Env.Mode == legacymining.ModeCanary }

// hl1RefuseFailStop is S1: a tripped FAILSTOP.json refuses every boot until
// R-FS removes it.
func hl1RefuseFailStop(stateDir string) error {
	p := filepath.Join(stateDir, legacymining.FailStopFile)
	_, err := os.Lstat(p)
	switch {
	case err == nil:
		return fmt.Errorf("%s is present: a runtime fail-stop is latched; complete R-FS before starting", p)
	case errors.Is(err, fs.ErrNotExist):
		return nil
	default:
		return fmt.Errorf("stat %s: %w", p, err)
	}
}

// hl1LoadBootConfig is S2. getenv is os.Getenv in production, and syncURLs is
// chainSyncURLsFromEnv(). After the config loads, the mode/version rules
// (CheckModeConfig, ErrConfig) and the HL2 WP-A gate (CheckSupported,
// ErrNotImplemented) run: ModePublic and v2 configs are refused until HL2
// WP-B..E land, so the caller exits 78.
func hl1LoadBootConfig(getenv func(string) string, syncURLs []string) (hl1BootConfig, error) {
	env, err := legacymining.LoadEnv(getenv)
	if err != nil {
		return hl1BootConfig{}, err
	}
	out := hl1BootConfig{Env: env}
	if env.Mode == legacymining.ModeOff {
		return out, nil
	}
	cfg, err := legacymining.LoadConfig(env.ConfigPath, env.ConfigSHA256)
	if err != nil {
		return hl1BootConfig{}, err
	}
	if err := legacymining.CheckModeConfig(env.Mode, cfg); err != nil {
		return hl1BootConfig{}, err
	}
	if err := legacymining.CheckSupported(env.Mode, cfg); err != nil {
		return hl1BootConfig{}, err
	}
	out.Config = cfg
	if err := hl1CheckSyncURLs(env.Mode, syncURLs); err != nil {
		return hl1BootConfig{}, err
	}
	return out, nil
}

// hl1CheckSyncURLs refuses HTTP chain sync whenever legacy mining is on
// (canary or public; §2 (d), S2). The production core.env sets
// QSDM_CHAIN_SYNC_URLS empty.
func hl1CheckSyncURLs(mode legacymining.Mode, syncURLs []string) error {
	if mode != legacymining.ModeOff && len(syncURLs) > 0 {
		return fmt.Errorf("legacy mining %s refuses QSDM_CHAIN_SYNC_URLS (%d source(s) configured); clear it", mode, len(syncURLs))
	}
	return nil
}

// hl1LegacyDirs returns the legacy-mining directories whose stale temps S3
// removes: <stateDir>/legacy-mining and, when legacy mining is on,
// Env.LegacyDir().
func hl1LegacyDirs(stateDir string, env legacymining.Env) []string {
	dirs := []string{filepath.Join(stateDir, legacymining.LegacyDirName)}
	if env.Mode != legacymining.ModeOff && filepath.Clean(env.LegacyDir()) != filepath.Clean(dirs[0]) {
		dirs = append(dirs, env.LegacyDir())
	}
	return dirs
}

// hl1ArmFailStop is S3. It removes stale .hl1-tmp-* files from the state
// directory and every existing legacy-mining directory (a symlinked or
// non-directory path is left alone; the guard refuses it later), then
// D1-creates FAILSTOP.armed unless FAILSTOP.json exists, and arms stopper.
// Any failure is a boot refusal.
func hl1ArmFailStop(stopper *hl1FailStopper, stateDir string, legacyDirs []string, release string, now time.Time) error {
	for _, dir := range append([]string{stateDir}, legacyDirs...) {
		if dir != stateDir {
			fi, err := os.Lstat(dir)
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				return fmt.Errorf("stale temp cleanup: stat %s: %w", dir, err)
			}
			if !fi.IsDir() || fi.Mode()&fs.ModeSymlink != 0 {
				continue
			}
		}
		n, err := legacymining.RemoveStaleTemps(dir)
		if err != nil {
			return fmt.Errorf("stale temp cleanup: %w", err)
		}
		if n > 0 {
			hl1Logger().Info("hl1: removed stale D1 temp files", "dir", dir, "count", n, "prefix", legacymining.TempPrefix)
		}
	}
	marker, err := json.Marshal(legacymining.ArmedMarker{Release: release, BootNS: now.UnixNano(), PID: os.Getpid()})
	if err != nil {
		return fmt.Errorf("encode %s: %w", legacymining.FailStopArmedFile, err)
	}
	marker = append(marker, '\n')
	if _, err := legacymining.ArmMarker(stateDir, legacymining.MarkerFailStop, marker); err != nil {
		return fmt.Errorf("arm FAILSTOP: %w", err)
	}
	stopper.arm(stateDir, marker)
	return nil
}

// hl1BootS1S3 runs S1-S3 for the core boot and returns the S2 result. Every
// failure is fatalRestore (exit 78).
func hl1BootS1S3(stateDir, release string) hl1BootConfig {
	if err := hl1RefuseFailStop(stateDir); err != nil {
		fatalRestore("hl1 S1: %v", err)
		return hl1BootConfig{}
	}
	boot, err := hl1LoadBootConfig(os.Getenv, chainSyncURLsFromEnv())
	if err != nil {
		fatalRestore("hl1 S2: %v", err)
		return hl1BootConfig{}
	}
	if err := hl1ArmFailStop(hl1FailStop, stateDir, hl1LegacyDirs(stateDir, boot.Env), release, time.Now()); err != nil {
		fatalRestore("hl1 S3: %v", err)
		return hl1BootConfig{}
	}
	return boot
}
