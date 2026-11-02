// Package hl1tail implements the hl1-tail recovery tool (design rev 4 §4.7
// runbooks R-C3, R-C4, R-H, R-B and §8 step 6; Appendix A). cmd/hl1-tail is
// a thin wrapper around Run; cmd/qsdm --hl1-tail-replay uses
// PreTrimReceipts for R-C4 step 0.
//
//	hl1-tail trim-fragment --file journal    R-C3, gated on W
//	hl1-tail trim-fragment --file receipts   R-C3, gated on the journal
//	hl1-tail trim --above <J-1>              R-C4 replay-failure fallback
//	hl1-tail watermark show                  read-only, no lock
//	hl1-tail watermark seed --served-tip S --follower-height F --follower-hash X
//	hl1-tail watermark retire
//
// Every mutating subcommand resolves the state directory exactly as core
// does, Dir(cfg.SQLitePath), and holds <stateDir>/qsdm-validator.state.lock
// (chain.AcquireStateLock, an exclusive non-blocking lock) for its whole run
// (W9, L8). Only while holding it does it remove stale D1 temp files from both
// HL1 directories. Every precondition is checked before the first change, and
// every change is archive -> fsync -> truncate -> fsync file and dir, so a
// refused or interrupted run can simply be repeated. No subcommand ever
// removes a journal height at or below W (W7).
//
// Exit codes (legacymining.TailExit*): 0 success, 1 I/O error (fix and rerun),
// 2 refusal (follow the runbook's refusal branch, usually R-X), 3 the state
// lock is busy (R-STOP first).
package hl1tail

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/blackbeardONE/QSDM/internal/legacymining"
	"github.com/blackbeardONE/QSDM/pkg/buildinfo"
	"github.com/blackbeardONE/QSDM/pkg/chain"
)

// File names in the state directory. They are the names cmd/qsdm main()
// uses; the cmd/qsdm tests pin the match.
const (
	JournalFile    = "qsdm_chain.ndjson"
	AccountsFile   = "qsdm_accounts.json"
	EnrollmentFile = "qsdm_enrollment.json"
	ReceiptsFile   = "qsdm_receipts.ndjson"
)

// Env is the process environment of Run. Zero fields get production defaults
// where one exists.
type Env struct {
	// StateDir resolves the state directory. Production: config.LoadConfig,
	// then filepath.Dir(cfg.SQLitePath), exactly as core (dep:844).
	StateDir func() (string, error)
	// Getenv reads the QSDM_LEGACY_MINING_* environment for the second HL1
	// directory whose stale temps are removed. Default os.Getenv.
	Getenv func(string) string
	Stdout io.Writer // result lines (JSON); default os.Stdout
	Stderr io.Writer // progress, refusals and errors; default os.Stderr
	Now    func() time.Time
	// SyncDir is fsync(dir); default legacymining.SyncDir.
	SyncDir func(dir string) error
}

func (e *Env) defaults() {
	if e.Getenv == nil {
		e.Getenv = os.Getenv
	}
	if e.Stdout == nil {
		e.Stdout = os.Stdout
	}
	if e.Stderr == nil {
		e.Stderr = os.Stderr
	}
	if e.Now == nil {
		e.Now = time.Now
	}
	if e.SyncDir == nil {
		e.SyncDir = legacymining.SyncDir
	}
	if e.StateDir == nil {
		e.StateDir = func() (string, error) { return "", errors.New("no state directory resolver") }
	}
}

// refusal is a failed precondition (exit 2). Nothing has been changed.
type refusal struct{ msg string }

func (r *refusal) Error() string { return r.msg }

func refusef(format string, args ...any) error { return &refusal{msg: fmt.Sprintf(format, args...)} }

// errLockBusy is exit 3.
var errLockBusy = errors.New("the validator state lock is held by core or another tool")

// ExitCode maps a subcommand result to the Appendix A exit code.
func ExitCode(err error) int {
	var r *refusal
	switch {
	case err == nil:
		return legacymining.TailExitOK
	case errors.Is(err, errLockBusy):
		return legacymining.TailExitLockBusy
	case errors.As(err, &r):
		return legacymining.TailExitRefused
	default:
		return legacymining.TailExitIO
	}
}

const usageText = `usage:
  hl1-tail trim-fragment --file journal|receipts
  hl1-tail trim --above <height>
  hl1-tail watermark show
  hl1-tail watermark seed --served-tip <S> --follower-height <F> --follower-hash <X>
  hl1-tail watermark retire
  hl1-tail --version

Run only after R-STOP, as qsdm-tech (runuser), with core.env sourced
(design rev 4 section 4.7). Exit codes: 0 ok, 1 I/O error (fix and rerun),
2 refused (follow the runbook; usually R-X), 3 state lock busy (R-STOP first).
`

// Run executes one hl1-tail command and returns its exit code.
func Run(args []string, env Env) int {
	env.defaults()
	if len(args) == 0 {
		fmt.Fprint(env.Stderr, usageText)
		return legacymining.TailExitRefused
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "--version", "-version", "version":
		if len(rest) != 0 {
			return env.usage("version takes no arguments")
		}
		fmt.Fprintln(env.Stdout, buildinfo.String("hl1-tail"))
		return legacymining.TailExitOK
	case "help", "-h", "--help":
		fmt.Fprint(env.Stdout, usageText)
		return legacymining.TailExitOK
	case "trim-fragment":
		fset := env.flags(cmd)
		file := fset.String("file", "", "journal or receipts")
		if !env.parse(fset, rest, "file") {
			return legacymining.TailExitRefused
		}
		if *file != "journal" && *file != "receipts" {
			return env.usage(fmt.Sprintf("--file must be journal or receipts, not %q", *file))
		}
		return env.mutating("trim-fragment --file "+*file, func(t *tool) error { return t.trimFragment(*file) })
	case "trim":
		fset := env.flags(cmd)
		raw := fset.String("above", "", "keep heights <= this value (J-1)")
		if !env.parse(fset, rest, "above") {
			return legacymining.TailExitRefused
		}
		h, err := parseHeight(*raw)
		if err != nil {
			return env.usage("--above: " + err.Error())
		}
		return env.mutating(fmt.Sprintf("trim --above %d", h), func(t *tool) error { return t.trimAbove(h) })
	case "watermark":
		if len(rest) == 0 {
			return env.usage("watermark needs show, seed or retire")
		}
		sub, rest := rest[0], rest[1:]
		switch sub {
		case "show":
			if len(rest) != 0 {
				return env.usage("watermark show takes no arguments")
			}
			return env.show()
		case "retire":
			if len(rest) != 0 {
				return env.usage("watermark retire takes no arguments")
			}
			return env.mutating("watermark retire", func(t *tool) error { return t.retire() })
		case "seed":
			fset := env.flags("watermark seed")
			rawS := fset.String("served-tip", "", "S: the predecessor's last chain_tip")
			rawF := fset.String("follower-height", "", "F: the home follower's chain_tip")
			rawX := fset.String("follower-hash", "", "X: the follower's block hash at F")
			if !env.parse(fset, rest, "served-tip", "follower-height", "follower-hash") {
				return legacymining.TailExitRefused
			}
			s, err := parseHeight(*rawS)
			if err != nil {
				return env.usage("--served-tip: " + err.Error())
			}
			f, err := parseHeight(*rawF)
			if err != nil {
				return env.usage("--follower-height: " + err.Error())
			}
			x := strings.ToLower(*rawX)
			if !isLowerHex(x, 64) {
				return env.usage("--follower-hash must be 64 hexadecimal characters")
			}
			return env.mutating("watermark seed", func(t *tool) error { return t.seed(s, f, x) })
		default:
			return env.usage(fmt.Sprintf("unknown watermark subcommand %q", sub))
		}
	default:
		return env.usage(fmt.Sprintf("unknown command %q", cmd))
	}
}

func (e *Env) usage(msg string) int {
	fmt.Fprintf(e.Stderr, "hl1-tail: %s\n%s", msg, usageText)
	return legacymining.TailExitRefused
}

func (e *Env) flags(name string) *flag.FlagSet {
	fset := flag.NewFlagSet(name, flag.ContinueOnError)
	fset.SetOutput(e.Stderr)
	return fset
}

// parse parses args into fset and requires every named flag and no
// positional arguments.
func (e *Env) parse(fset *flag.FlagSet, args []string, required ...string) bool {
	if err := fset.Parse(args); err != nil {
		return false
	}
	if fset.NArg() != 0 {
		e.usage(fmt.Sprintf("%s: unexpected argument %q", fset.Name(), fset.Arg(0)))
		return false
	}
	set := map[string]bool{}
	fset.Visit(func(f *flag.Flag) { set[f.Name] = true })
	for _, name := range required {
		if !set[name] {
			e.usage(fmt.Sprintf("%s: --%s is required", fset.Name(), name))
			return false
		}
	}
	return true
}

// parseHeight accepts only a plain decimal uint64.
func parseHeight(s string) (uint64, error) {
	if s == "" || strings.TrimSpace(s) != s || strings.HasPrefix(s, "+") {
		return 0, fmt.Errorf("%q is not a decimal height", s)
	}
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%q is not a decimal height", s)
	}
	return v, nil
}

// tool is one run against a resolved state directory.
type tool struct {
	env        *Env
	name       string
	dir        string
	journal    string
	accounts   string
	enrollment string
	receipts   string
}

// resolve finds the state directory. It never creates it.
func (e *Env) resolve(name string) (*tool, error) {
	raw, err := e.StateDir()
	if err != nil {
		return nil, fmt.Errorf("resolve the state directory (Dir(cfg.SQLitePath)): %w", err)
	}
	dir, err := filepath.Abs(raw)
	if err != nil {
		return nil, fmt.Errorf("state directory %q: %w", raw, err)
	}
	fi, err := os.Stat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, refusef("state directory %s does not exist", dir)
	}
	if err != nil {
		return nil, fmt.Errorf("stat state directory %s: %w", dir, err)
	}
	if !fi.IsDir() {
		return nil, refusef("state directory %s is not a directory", dir)
	}
	return &tool{
		env: e, name: name, dir: dir,
		journal:    filepath.Join(dir, JournalFile),
		accounts:   filepath.Join(dir, AccountsFile),
		enrollment: filepath.Join(dir, EnrollmentFile),
		receipts:   filepath.Join(dir, ReceiptsFile),
	}, nil
}

// mutating runs fn under the state lock, after the stale-temp cleanup.
func (e *Env) mutating(name string, fn func(*tool) error) int {
	t, err := e.resolve(name)
	if err != nil {
		return e.finish(name, err)
	}
	lockPath := filepath.Join(t.dir, legacymining.StateLockFile)
	lock, err := chain.AcquireStateLock(lockPath)
	if err != nil {
		if strings.Contains(err.Error(), "already in use") {
			err = fmt.Errorf("%w (%s): %v", errLockBusy, lockPath, err)
		}
		return e.finish(name, err)
	}
	defer lock.Close()
	fmt.Fprintf(e.Stderr, "hl1-tail %s: state directory %s; state lock held\n", name, t.dir)
	if err := t.cleanTemps(); err != nil {
		return e.finish(name, err)
	}
	return e.finish(name, fn(t))
}

// finish reports err and returns its exit code.
func (e *Env) finish(name string, err error) int {
	code := ExitCode(err)
	switch code {
	case legacymining.TailExitOK:
		fmt.Fprintf(e.Stderr, "hl1-tail %s: done\n", name)
	case legacymining.TailExitLockBusy:
		fmt.Fprintf(e.Stderr, "hl1-tail %s: STATE LOCK BUSY (exit %d): %v\nNothing was changed. Run R-STOP (the unit must be inactive or failed, MainPID=0), then rerun.\n", name, code, err)
	case legacymining.TailExitRefused:
		fmt.Fprintf(e.Stderr, "hl1-tail %s: REFUSED (exit %d): %v\nNo journal, receipts, snapshot or watermark file was changed (only stale .hl1-tmp-* files may have been removed). Follow the runbook's refusal branch (usually R-X: preserve everything and escalate).\n", name, code, err)
	default:
		fmt.Fprintf(e.Stderr, "hl1-tail %s: I/O ERROR (exit %d): %v\nFix the cause and rerun the same command; every step is archive, fsync, truncate, fsync.\n", name, code, err)
	}
	return code
}

// hl1Dirs are the HL1 directories whose stale D1 temps are removed: the
// state directory, <stateDir>/legacy-mining and, when the canary environment
// names another one (canary or public), its legacy-mining directory. A missing, symlinked or
// non-directory legacy-mining path is left alone (as S3 does).
func (t *tool) hl1Dirs() []string {
	dirs := []string{t.dir, filepath.Join(t.dir, legacymining.LegacyDirName)}
	if env, err := legacymining.LoadEnv(t.env.Getenv); err == nil && env.Mode != legacymining.ModeOff {
		if d := env.LegacyDir(); filepath.Clean(d) != filepath.Clean(dirs[1]) {
			dirs = append(dirs, d)
		}
	}
	return dirs
}

// cleanTemps removes stale .hl1-tmp-* files. It runs only under the lock, so
// it can never remove a live writer's temp.
func (t *tool) cleanTemps() error {
	for i, dir := range t.hl1Dirs() {
		if i > 0 {
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
			fmt.Fprintf(t.env.Stderr, "hl1-tail %s: removed %d stale %s* file(s) in %s\n", t.name, n, legacymining.TempPrefix, dir)
		}
	}
	return nil
}

// result prints one JSON result line on stdout.
func (t *tool) result(v map[string]any) {
	v["command"] = t.name
	v["state_dir"] = t.dir
	data, _ := json.Marshal(v)
	fmt.Fprintln(t.env.Stdout, string(data))
}

// archiveName is <base>.<kind>-<UTC timestamp with nanoseconds>.
func archiveName(path, kind string, now time.Time) string {
	return fmt.Sprintf("%s.%s-%s", filepath.Base(path), kind, now.UTC().Format("20060102T150405.000000000Z"))
}

// show is `watermark show`: read-only, no lock, no cleanup. It exits 0 when a
// valid W is present and 2 when W is absent or invalid.
func (e *Env) show() int {
	const name = "watermark show"
	t, err := e.resolve(name)
	if err != nil {
		return e.finish(name, err)
	}
	out := map[string]any{"watermark_file": filepath.Join(t.dir, legacymining.WatermarkFile)}
	entries, err := os.ReadDir(t.dir)
	if err != nil {
		return e.finish(name, fmt.Errorf("list %s: %w", t.dir, err))
	}
	retired := []string{}
	for _, de := range entries {
		if strings.HasPrefix(de.Name(), legacymining.WatermarkRetiredPrefix) {
			retired = append(retired, de.Name())
		}
	}
	sort.Strings(retired)
	out["retired"] = retired
	w, werr := ReadWatermark(t.dir)
	switch {
	case werr == nil:
		out["status"] = "present"
		out["watermark"] = w
	case errors.Is(werr, ErrWatermarkMissing):
		out["status"] = "absent"
		werr = refusef("no watermark in %s (R-H seeds one; R-B and rollback retire it)", t.dir)
	default:
		out["status"] = "invalid"
		out["error"] = werr.Error()
		werr = refusef("%v (S5 rule 2: R-X)", werr)
	}
	t.result(out)
	if werr != nil {
		code := ExitCode(werr)
		fmt.Fprintf(e.Stderr, "hl1-tail %s: %v\n", name, werr)
		return code
	}
	return legacymining.TailExitOK
}
