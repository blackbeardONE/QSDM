package main

// service.go: consumer-grade ergonomics on top of the existing
// MINING_PROTOCOL.md mining loop.
//
// Three orthogonal features live here, each gated behind a flag so
// the existing operator workflow (raw `qsdmminer-console`, no flags,
// live panel) keeps working byte-for-byte:
//
//   --idle-only
//       Only mine when the GPU is otherwise idle. Backed by
//       pkg/mining/idle which shells out to nvidia-smi every 5s and
//       reports "below ThresholdPct for at least GracePeriod" as the
//       gate signal. When the GPU is busy the loop sleeps in 1-second
//       slices instead of burning Solve attempts that the user's
//       game / video call would otherwise have used.
//
//   --service
//       Background-service mode: no banner, --plain forced on, stderr
//       muted to the log file, exit code 0 on clean shutdown. Suitable
//       for `nssm install` on Windows or a systemd unit on Linux. The
//       log file is rotated by lumberjack so the binary can run for
//       years without filling the disk.
//
//   --log-file <path>
//       Redirect both stdout (panel + plain log lines) and stderr
//       (banner + v2 status) to a rotating file. Implies --plain so
//       the log is human-readable rather than full of ANSI cursor
//       escapes.
//
// Why these three together: the consumer story we promised is
// "install once, walk away, mining happens in the background and
// pauses when you play games." That's --service + --idle-only +
// --log-file, and the polish is making each one work standalone too
// so power users can mix-and-match.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/buildinfo"
	"github.com/blackbeardONE/QSDM/pkg/mining/idle"
	"github.com/blackbeardONE/QSDM/pkg/updater"
	lumberjack "gopkg.in/natefinch/lumberjack.v2"
)

// ConsumerFlags are the user-facing knobs introduced by service.go.
// Bundled in one struct so main() can pass them around without
// growing a 30-arg signature.
type ConsumerFlags struct {
	IdleOnly      bool
	IdleThreshold int
	IdleGrace     time.Duration
	IdlePoll      time.Duration

	Service bool
	LogFile string
	LogSize int // megabytes per file before rotation; 0 = lumberjack default
	LogKeep int // number of rotated files to retain; 0 = lumberjack default

	// Auto-update knobs. When AutoUpdate > 0 the miner runs a
	// background goroutine that polls UpdateURL for a fresh
	// release every AutoUpdate, stages the new binary at
	// <currentExe>.next on a SHA-256 match, and applies any
	// staged update at the next startup (atomic rename + exec).
	// AutoUpdate=0 (default) keeps the legacy behaviour: the
	// miner never reaches out for updates.
	AutoUpdate time.Duration
	UpdateURL  string

	// ApplyStaged is the "do exactly one thing then exit" knob:
	// apply any staged update and exit. Useful for ops scripts
	// that want to drive the swap without setting AutoUpdate.
	// Implies a graceful exit-then-exec; the miner does not
	// proceed to the mining loop on this path.
	ApplyStaged bool
}

// RegisterConsumerFlags binds the consumer flags to the default
// flag set. Called from main() exactly once before flag.Parse().
//
// Defaults are picked to "do the right thing" for a desktop user
// who hasn't read the docs:
//
//   --idle-only=false           (must opt in; some users want full speed)
//   --idle-threshold=10         (10% utilization is "idle" — accounts
//                                for desktop compositor noise)
//   --idle-grace=60s            (1 minute of idle before resume; long
//                                enough that closing a game and
//                                immediately opening another doesn't
//                                trigger a hashrate spike during the
//                                load screen)
//   --idle-poll=5s              (5s nvidia-smi cadence is invisible
//                                to the user but resumes mining
//                                quickly enough that the operator
//                                doesn't notice idle gaps)
//   --service=false             (default off; --service implies log-
//                                file at <homedir>/.qsdm/miner.log)
//   --log-file=""               (empty = stdout/stderr unchanged)
//   --log-size-mb=10            (10 MiB per rotated file)
//   --log-keep=5                (keep 5 generations = ~50 MiB ceiling)
func RegisterConsumerFlags(fs *flag.FlagSet, out *ConsumerFlags) {
	fs.BoolVar(&out.IdleOnly, "idle-only", false,
		"only mine when the GPU is otherwise idle (probes nvidia-smi); pauses mining while you game / video-call")
	fs.IntVar(&out.IdleThreshold, "idle-threshold", idle.DefaultThresholdPct,
		"GPU utilization percentage (0-100) below which the GPU is considered idle")
	fs.DurationVar(&out.IdleGrace, "idle-grace", idle.DefaultGracePeriod,
		"how long the GPU must stay below --idle-threshold before mining resumes")
	fs.DurationVar(&out.IdlePoll, "idle-poll", idle.DefaultInterval,
		"how often --idle-only probes the GPU (lower = faster resume, more nvidia-smi calls)")

	fs.BoolVar(&out.Service, "service", false,
		"background-service mode: no banner, plain log mode, exit 0 on clean shutdown; pairs with nssm/systemd")
	fs.StringVar(&out.LogFile, "log-file", "",
		"write all output to this file (rotated by size); empty keeps stdout/stderr")
	fs.IntVar(&out.LogSize, "log-size-mb", 10,
		"max megabytes per log file before rotation")
	fs.IntVar(&out.LogKeep, "log-keep", 5,
		"number of rotated log files to retain")

	fs.DurationVar(&out.AutoUpdate, "auto-update", 0,
		"poll <update-url> every N for a fresh release; download + sha256-verify + stage as <exe>.next; auto-apply at next startup. Set to 0 to disable.")
	fs.StringVar(&out.UpdateURL, "update-url", "https://qsdm.tech/releases",
		"base URL for the release manifest + binaries (no trailing slash)")
	fs.BoolVar(&out.ApplyStaged, "apply-staged-update", false,
		"apply any staged update (<exe>.next) by atomic rename + re-exec, then exit")
}

// applyServiceMode redirects stdout+stderr to a rotating log file
// when the operator passed --service or --log-file. The returned
// closer flushes the lumberjack writer on exit; callers pass it to
// defer in main().
//
// Side effects when --service or --log-file is set:
//
//   - cfg.Plain is forced to true so the panel renderer's ANSI
//     escape codes don't pollute the log file
//   - stdout and stderr are reassigned to the rotating writer; the
//     raw OS handles are NOT closed, so a hosting service manager
//     can still capture early-startup stderr
//   - the deprecation banner is suppressed (would add ~10 lines of
//     box-drawing noise per restart in a long-running log)
//
// Returns (effectivePlain, closer, error). closer is non-nil even
// on the no-op path — callers can defer it unconditionally.
func applyServiceMode(cf *ConsumerFlags, cfg *Config) (bool, io.Closer, error) {
	plain := cfg.Plain || cf.Service

	if !cf.Service && cf.LogFile == "" {
		return plain, noopCloser{}, nil
	}

	logPath := cf.LogFile
	if logPath == "" {
		logPath = defaultServiceLogPath()
	}

	// Make sure the parent directory exists; lumberjack creates the
	// file but not its parents. 0o700 matches the rest of ~/.qsdm.
	if dir := filepath.Dir(logPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return plain, noopCloser{}, fmt.Errorf("service log dir %s: %w", dir, err)
		}
	}

	lj := &lumberjack.Logger{
		Filename:   logPath,
		MaxSize:    cf.LogSize,
		MaxBackups: cf.LogKeep,
		MaxAge:     0,
		Compress:   false,
		LocalTime:  false,
	}

	// stdout and stderr both go to the same writer. We write a
	// boot-line so a fresh log is obviously not just an empty file.
	fmt.Fprintf(lj, "qsdmminer service starting at %s\n", time.Now().UTC().Format(time.RFC3339))
	os.Stdout = openWriterAsFile(lj)
	os.Stderr = openWriterAsFile(lj)

	return true, lj, nil
}

// noopCloser is the closer returned when --service / --log-file
// were not requested. Lets the caller defer-close unconditionally.
type noopCloser struct{}

func (noopCloser) Close() error { return nil }

// openWriterAsFile wraps an io.Writer in *os.File via a pipe so the
// stdlib functions that take *os.File (json/log internals) keep
// working when stdout/stderr are reassigned. The reader half of the
// pipe is drained into the writer in a goroutine.
//
// Why a pipe vs replacing fmt.Fprintln calls: existing code (and any
// dependency we don't control) writes to os.Stdout / os.Stderr with
// the assumption that they are *os.File. Replacing them with a
// non-File would compile but break os.Stdout.Sync() and any
// "is-a-tty" check; a pipe-backed *os.File preserves both.
func openWriterAsFile(w io.Writer) *os.File {
	r, pw, err := os.Pipe()
	if err != nil {
		// Fall back to the old fd; better than crashing a mining
		// loop on a broken /dev/fd or Windows handle exhaustion.
		return os.Stderr
	}
	go func() {
		defer r.Close()
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				_, _ = w.Write(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	return pw
}

// defaultServiceLogPath is <userhome>/.qsdm/miner.log. The miner
// already keeps its config + HMAC key under the same dir, so adding
// a log file there is consistent for ops who back up the directory.
func defaultServiceLogPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ".qsdm-miner.log"
	}
	return filepath.Join(home, ".qsdm", "miner.log")
}

// buildIdleProbe returns a configured *idle.Probe when --idle-only
// is set, or nil when idle-gating is disabled. The probe is run in
// its own goroutine by the caller; the returned cancel function
// must be invoked on shutdown to stop the sampler.
//
// Returns nil when --idle-only is off so call sites can use
// the result as a single nil-check sentinel for "should I gate?".
func buildIdleProbe(cf *ConsumerFlags) *idle.Probe {
	if !cf.IdleOnly {
		return nil
	}
	return &idle.Probe{
		ThresholdPct: cf.IdleThreshold,
		GracePeriod:  cf.IdleGrace,
		Interval:     cf.IdlePoll,
	}
}

// idleGate is the runLoop-facing decision primitive: "should this
// iteration pause, and if so, why?". Wraps *idle.Probe so the loop
// is decoupled from the specific probe implementation — tests stub
// idleGate directly without spinning up the nvidia-smi sampler.
//
// The "no probe" path returns "don't pause" — callers construct an
// idleGate from buildIdleGate(probe) which handles the nil-probe
// case. The conservative posture matters: a host that fails to
// produce readings (probe.IsIdle ok=false) will keep mining rather
// than starve, because "no signal" is more often "nvidia-smi not
// installed" than "GPU is genuinely under load".
type idleGate struct {
	probe *idle.Probe
}

// shouldPause returns (true, reason) when the runLoop should sit
// out the next iteration. Reason is human-readable so the dashboard
// can surface it directly. (false, "") means proceed with mining.
func (g *idleGate) shouldPause() (bool, string) {
	if g == nil || g.probe == nil {
		return false, ""
	}
	idleNow, ok := g.probe.IsIdle(time.Now())
	if !ok {
		// Probe hasn't produced a successful reading yet, OR
		// the most recent reading errored. Fail open: keep
		// mining. The probe's FailureReason() is surfaced in
		// the dashboard footer so the operator can debug.
		return false, ""
	}
	if idleNow {
		return false, ""
	}
	last := g.probe.Last()
	if last.GPUPct < 0 {
		return true, "GPU busy (utilization unknown), waiting"
	}
	return true, fmt.Sprintf("GPU busy at %d%%, waiting for %s of idle",
		last.GPUPct, g.probe.GracePeriod)
}

// buildIdleGate is the constructor main() uses. Returns nil when
// the probe is nil, so the runLoop's `if gate != nil` check stays
// simple.
func buildIdleGate(probe *idle.Probe) *idleGate {
	if probe == nil {
		return nil
	}
	return &idleGate{probe: probe}
}

// applyStagedUpdateAtStartup is the "did the operator restart us
// because they wanted to apply a staged update?" hook. Called from
// main() BEFORE applyServiceMode reassigns stdout/stderr — applying
// a staged update reexecs into a different binary, and we want the
// new process to make its own logging choices rather than inherit
// stale pipes.
//
// Behaviour:
//
//   - If --apply-staged-update was passed, ALWAYS apply (or report
//     "no staged update" + exit 1).
//   - Else if --auto-update is on AND a staged update exists next
//     to the running exe, apply silently. This makes the consumer
//     story (`qsdmminer --auto-update=24h`) self-applying: the
//     background poller stages, the next service-manager restart
//     atomically rolls forward.
//   - Else: do nothing.
//
// Returns (applied=true) only on the success path of an exec swap.
// The current process is gone after that, so callers will only see
// (false, ...) or never see anything (process replaced).
func applyStagedUpdateAtStartup(cf *ConsumerFlags) (applied bool, err error) {
	if !cf.ApplyStaged && cf.AutoUpdate <= 0 {
		return false, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return false, fmt.Errorf("locate self: %w", err)
	}
	stagePath := exe + ".next"
	if _, err := os.Stat(stagePath); errors.Is(err, os.ErrNotExist) {
		if cf.ApplyStaged {
			fmt.Fprintln(os.Stderr,
				"qsdmminer: --apply-staged-update set but no staged update found at "+stagePath)
			os.Exit(1)
		}
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("stat staged update: %w", err)
	}
	fmt.Fprintf(os.Stderr,
		"qsdmminer: applying staged update from %s (atomic rename + re-exec)\n",
		stagePath)
	// ApplyStaged either exits the process via os.Exit(0) on
	// success OR returns an error. Returning err==nil here
	// would be a bug — but we wrap defensively.
	err = updater.ApplyStaged(stagePath, exe, os.Args)
	return false, err
}

// runAutoUpdater starts the background updater goroutine when
// --auto-update is set. The goroutine ticks at the configured
// interval, attempts one CheckAndStage per tick, and logs the
// outcome. It honours ctx cancellation so a Ctrl-C / SIGTERM tears
// the goroutine down promptly.
//
// Returns silently when AutoUpdate <= 0; callers can invoke this
// unconditionally.
//
// We deliberately do NOT auto-apply mid-run. Auto-apply would
// require killing the live mining loop and re-exec'ing the binary,
// which is a sharp edge: in-flight Solve calls would lose their
// proof, the live console panel would flicker, and any service
// manager that's monitoring the pid would see a fork. Staging at
// runtime + applying at restart is the same outcome with none of
// those edge cases.
func runAutoUpdater(ctx context.Context, cf *ConsumerFlags) {
	if cf.AutoUpdate <= 0 {
		return
	}
	cur := buildinfo.Version
	if cur == "dev" {
		fmt.Fprintln(os.Stderr,
			"qsdmminer: --auto-update enabled but build is 'dev' (no buildinfo injection); skipping")
		return
	}
	cli := &http.Client{Timeout: 30 * time.Second}
	u, err := updater.New(updater.Config{
		BaseURL:        cf.UpdateURL,
		Component:      "qsdmminer",
		GOOS:           runtime.GOOS,
		GOARCH:         runtime.GOARCH,
		CurrentVersion: cur,
		HTTPClient:     cli,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "qsdmminer: auto-updater disabled: %v\n", err)
		return
	}

	interval := cf.AutoUpdate
	if interval < 5*time.Minute {
		// 5 min floor. Anything below that is a stress test
		// against qsdm.tech, not a real update cadence.
		// Operators who want fast iteration can run
		// --apply-staged-update by hand.
		interval = 5 * time.Minute
	}
	fmt.Fprintf(os.Stderr,
		"qsdmminer: auto-update enabled, polling %s every %s (current=%s)\n",
		cf.UpdateURL, interval, cur)

	go func() {
		// Initial check after a small jitter so a host that
		// boots a fleet of miners doesn't hammer the release
		// host in lockstep. 30s is small enough for a fresh
		// install to see "update staged" on the panel within
		// a minute, and large enough to avoid the thundering
		// herd.
		select {
		case <-ctx.Done():
			return
		case <-time.After(30 * time.Second):
		}
		runOneUpdateCheck(ctx, u)
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				runOneUpdateCheck(ctx, u)
			}
		}
	}()
}

// runOneUpdateCheck wraps Updater.CheckAndStage with consistent
// logging. Errors land on stderr but never crash the miner — the
// updater is best-effort, and a flapping release host should not
// take down a healthy mining loop.
func runOneUpdateCheck(ctx context.Context, u *updater.Updater) {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	res, err := u.CheckAndStage(cctx)
	switch {
	case errors.Is(err, updater.ErrCurrentVersionDev):
		// Already logged during runAutoUpdater bootstrap.
		return
	case err != nil:
		fmt.Fprintf(os.Stderr, "qsdmminer: update check failed: %v\n", err)
		return
	case res.Staged:
		fmt.Fprintf(os.Stderr,
			"qsdmminer: staged update %s at %s (sha256=%s, %d bytes); restart to apply\n",
			res.NewVersion, res.StagedPath, res.SHA256, res.SizeBytes)
	case res.UpToDate:
		fmt.Fprintf(os.Stderr,
			"qsdmminer: update check OK, already on %s\n", res.NewVersion)
	default:
		fmt.Fprintf(os.Stderr,
			"qsdmminer: update check: tag=%s skipped (%s)\n",
			res.NewVersion, res.SkippedReason)
	}
}
