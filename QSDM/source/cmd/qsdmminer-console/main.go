// Command qsdmminer-console is the miner-friendly console front-end for
// the QSDM / Cell CPU reference miner. It is a sibling of cmd/qsdmminer,
// not a replacement: qsdmminer is intentionally minimal so it can be
// audited line-by-line against MINING_PROTOCOL.md, while this binary
// layers a live stats panel, an interactive first-run wizard, and
// persistent config on top of the same pkg/mining primitives.
//
// Usage overview:
//
//	qsdmminer-console               # runs wizard on first use, then mines
//	qsdmminer-console --setup       # forces the wizard even with a saved
//	                                # config
//	qsdmminer-console --plain       # disables the live panel; emits a
//	                                # plain log line per event (useful in
//	                                # systemd / journalctl / CI)
//	qsdmminer-console --self-test   # in-memory solve-and-verify; exits 0
//	                                # on success, same gate as
//	                                # qsdmminer --self-test
//
// Config is persisted to:
//
//	Linux/macOS: ~/.qsdm/miner.toml
//	Windows:     %USERPROFILE%\.qsdm\miner.toml
//
// Flags override the config file for the current run without rewriting
// it. The file is written 0o600 on POSIX because it contains a reward
// address — not a secret, but still linkable to the operator.
//
// This binary does NOT do anything the protocol would consider new. It
// fetches /api/v1/mining/work, solves with pkg/mining.Solve, and POSTs
// /api/v1/mining/submit — exactly the cmd/qsdmminer flow. The difference
// is ergonomics: a user who runs `qsdmminer-console` with no flags gets
// a setup wizard instead of a cryptic "--address is required" error.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/blackbeardONE/QSDM/pkg/api"
	"github.com/blackbeardONE/QSDM/pkg/branding"
	"github.com/blackbeardONE/QSDM/pkg/buildinfo"
	"github.com/blackbeardONE/QSDM/pkg/mining"
	"golang.org/x/term"
)

// binaryName is the exec name we advertise via --version. See
// pkg/buildinfo.String for the full format. Kept const so a renamed
// binary still identifies itself consistently in bug reports.
const binaryName = "qsdmminer-console"

// -----------------------------------------------------------------------------
// Config
// -----------------------------------------------------------------------------

// Config is persisted to ~/.qsdm/miner.toml. Every field is optional
// for forward-compatibility — an older binary reading a newer file
// must not refuse to start, and a newer binary reading an older file
// must fall back to sensible defaults. Add new fields with a
// `toml:",omitempty"` tag only if their zero value is a valid choice;
// otherwise supply an explicit default in loadConfig.
type Config struct {
	ValidatorURL string `toml:"validator_url"`
	RewardAddr   string `toml:"reward_address"`
	BatchCount   uint32 `toml:"batch_count"`
	PollInterval string `toml:"poll_interval"`
	Plain        bool   `toml:"plain"`
}

func (c Config) pollDuration() time.Duration {
	if c.PollInterval == "" {
		return 2 * time.Second
	}
	d, err := time.ParseDuration(c.PollInterval)
	if err != nil || d <= 0 {
		return 2 * time.Second
	}
	return d
}

func defaultConfigPath() string {
	// UserHomeDir is honored cross-platform by the stdlib. On Windows
	// this resolves to %USERPROFILE%\.qsdm\miner.toml which is where
	// other tooling (nvidia sidecar log dir, etc.) already expects QSDM
	// operator state to live.
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		// Fallback to CWD so the binary still runs on systems with a
		// weird HOME (e.g. locked-down Windows service accounts).
		return ".qsdm-miner.toml"
	}
	return filepath.Join(home, ".qsdm", "miner.toml")
}

func loadConfig(path string) (Config, error) {
	var c Config
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, fmt.Errorf("read %s: %w", path, err)
	}
	if _, err := toml.Decode(string(b), &c); err != nil {
		return c, fmt.Errorf("decode %s: %w", path, err)
	}
	return c, nil
}

func saveConfig(path string, c Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "# %s console miner config — saved %s\n", branding.Name, time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintln(&buf, "# Edit by hand or re-run `qsdmminer-console --setup` to replace.")
	if err := toml.NewEncoder(&buf).Encode(c); err != nil {
		return fmt.Errorf("encode: %w", err)
	}
	// 0o600 on POSIX; Windows ignores the perms but the restrictive
	// mode also documents intent to future readers.
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return nil
}

// -----------------------------------------------------------------------------
// Events — the mining loop emits one Event per state change; the
// renderer consumes them and updates either the live panel (console
// mode) or a plain log line (--plain mode or non-TTY stdout).
// Decoupling the loop from the output lets us test each side in
// isolation.
// -----------------------------------------------------------------------------

type EventKind int

const (
	EvConnecting EventKind = iota
	EvConnected
	EvEpochChanged
	EvDAGReady
	EvProofAccepted
	EvProofRejected
	EvError
	EvInfo
	EvShutdown
)

type Event struct {
	Kind    EventKind
	At      time.Time
	Message string

	// Populated for EvEpochChanged / EvDAGReady.
	Epoch   uint64
	DAGSize uint32

	// Populated for EvProofAccepted / EvProofRejected.
	Height   uint64
	Attempts uint64
	ProofID  string
	Reason   string // rejection reason or HTTP error detail
}

// -----------------------------------------------------------------------------
// Dashboard state — the source of truth that the renderer paints from.
// applyEvent is the only mutation path and is covered by tests.
// -----------------------------------------------------------------------------

type Dashboard struct {
	StartedAt    time.Time
	Validator    string
	Address      string
	Status       string // connecting, connected, error
	StatusDetail string
	Epoch        uint64
	DAGReady     bool
	DAGSize      uint32
	Accepted     uint64
	Rejected     uint64
	LastEvent    string
	LastEventAt  time.Time
}

func (d *Dashboard) applyEvent(e Event) {
	d.LastEvent = e.Message
	d.LastEventAt = e.At
	switch e.Kind {
	case EvConnecting:
		d.Status = "connecting"
		d.StatusDetail = e.Message
	case EvConnected:
		d.Status = "connected"
		d.StatusDetail = ""
	case EvEpochChanged:
		d.Epoch = e.Epoch
		d.DAGSize = e.DAGSize
		d.DAGReady = false
	case EvDAGReady:
		d.DAGReady = true
	case EvProofAccepted:
		d.Accepted++
	case EvProofRejected:
		d.Rejected++
	case EvError:
		d.Status = "error"
		d.StatusDetail = e.Message
	}
}

// -----------------------------------------------------------------------------
// Formatting helpers — kept pure so they're trivially unit-testable.
// -----------------------------------------------------------------------------

// formatHashrate picks a human-friendly unit. A reference CPU miner
// rarely exceeds 10–20 H/s, so we only ladder up to KH/s for safety
// on unusually fast hardware; anything higher is already out of
// scope for a CPU reference implementation.
func formatHashrate(hps float64) string {
	switch {
	case hps >= 1_000_000:
		return fmt.Sprintf("%.2f MH/s", hps/1_000_000)
	case hps >= 1_000:
		return fmt.Sprintf("%.2f KH/s", hps/1_000)
	default:
		return fmt.Sprintf("%.2f  H/s", hps)
	}
}

// formatDuration renders uptime as HH:MM:SS; >= 1 day adds a "Nd"
// prefix. Keep this monotonic so the rightmost digits never widen
// the panel mid-session (important because the renderer does not
// repaint the full frame — it overwrites lines in place).
func formatDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	days := int(d / (24 * time.Hour))
	d -= time.Duration(days) * 24 * time.Hour
	h := int(d / time.Hour)
	d -= time.Duration(h) * time.Hour
	m := int(d / time.Minute)
	d -= time.Duration(m) * time.Minute
	s := int(d / time.Second)
	if days > 0 {
		return fmt.Sprintf("%dd %02d:%02d:%02d", days, h, m, s)
	}
	return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
}

// truncateAddr keeps the wallet prefix and last 4 chars visible,
// collapsing the middle with a single-char ellipsis. Miners glance at
// this field to confirm they're crediting the right address; an
// untruncated 50-char string would wrap the panel on narrow terminals.
func truncateAddr(a string) string {
	const keepHead = 8
	const keepTail = 4
	if len(a) <= keepHead+keepTail+1 {
		return a
	}
	return a[:keepHead] + "\u2026" + a[len(a)-keepTail:]
}

// -----------------------------------------------------------------------------
// Renderer — console (TTY) and plain (log) modes share the same Event
// stream. The console renderer uses ANSI escapes and rewrites the last
// N lines in place; this is deliberately simpler and more portable
// than full ncurses / tcell and degrades gracefully under `tee` /
// `journalctl`.
// -----------------------------------------------------------------------------

type renderer interface {
	Render(d *Dashboard, hps float64)
	Event(e Event)
	Close()
}

// plainRenderer just prints a timestamped line per Event. Suitable
// for --plain, CI, and systemd journal redirection.
type plainRenderer struct{ w io.Writer }

func (p *plainRenderer) Render(_ *Dashboard, _ float64) {}
func (p *plainRenderer) Event(e Event) {
	fmt.Fprintf(p.w, "%s %s %s\n",
		e.At.UTC().Format("15:04:05"), kindLabel(e.Kind), e.Message)
}
func (p *plainRenderer) Close() {}

func kindLabel(k EventKind) string {
	switch k {
	case EvConnecting:
		return "[conn]"
	case EvConnected:
		return "[ok]  "
	case EvEpochChanged:
		return "[epoch]"
	case EvDAGReady:
		return "[dag] "
	case EvProofAccepted:
		return "[PASS]"
	case EvProofRejected:
		return "[FAIL]"
	case EvError:
		return "[err] "
	case EvShutdown:
		return "[bye] "
	default:
		return "[info]"
	}
}

// consoleRenderer maintains a 14-line panel at the bottom of the
// terminal. Each Render call rewrites those 14 lines using
// "\x1b[14A\r" (cursor up 14, carriage return) and "\x1b[K" (clear to
// end of line) per row. The first render prints 14 blank lines to
// reserve the space, so the cursor-up is always well-defined.
type consoleRenderer struct {
	w           io.Writer
	firstRender bool
	lines       int // number of lines the panel occupies
}

func newConsoleRenderer(w io.Writer) *consoleRenderer {
	return &consoleRenderer{w: w, firstRender: true, lines: 14}
}

const (
	ansiReset  = "\x1b[0m"
	ansiBold   = "\x1b[1m"
	ansiDim    = "\x1b[2m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
	ansiRed    = "\x1b[31m"
	ansiCyan   = "\x1b[36m"
	ansiClrEol = "\x1b[K"
)

func (c *consoleRenderer) Render(d *Dashboard, hps float64) {
	var buf bytes.Buffer
	if c.firstRender {
		// Reserve the vertical real estate for the panel.
		for i := 0; i < c.lines; i++ {
			buf.WriteByte('\n')
		}
		c.firstRender = false
	}
	// Move cursor up to the top of the panel.
	fmt.Fprintf(&buf, "\x1b[%dA\r", c.lines)

	writeLine := func(s string) {
		buf.WriteString(s)
		buf.WriteString(ansiClrEol)
		buf.WriteByte('\n')
	}

	statusColor := ansiYellow
	switch d.Status {
	case "connected":
		statusColor = ansiGreen
	case "error":
		statusColor = ansiRed
	}

	uptime := formatDuration(time.Since(d.StartedAt))
	dagLabel := "building…"
	if d.DAGReady {
		dagLabel = fmt.Sprintf("ready · N=%d", d.DAGSize)
	}

	writeLine(ansiBold + "  " + branding.Name + " miner console " + ansiReset + ansiDim + "· protocol v" + itoa(mining.ProtocolVersion) + ansiReset)
	writeLine(ansiDim + "  \u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500" + ansiReset)
	writeLine(fmt.Sprintf("  %-16s %s", "Reward address", truncateAddr(d.Address)))
	writeLine(fmt.Sprintf("  %-16s %s  %s%s%s", "Validator", d.Validator, statusColor, "["+d.Status+"]", ansiReset))
	if d.StatusDetail != "" {
		writeLine(fmt.Sprintf("  %-16s %s%s%s", "", ansiDim, d.StatusDetail, ansiReset))
	} else {
		writeLine("")
	}
	writeLine(fmt.Sprintf("  %-16s %d  (DAG %s)", "Epoch", d.Epoch, dagLabel))
	writeLine("")
	writeLine(fmt.Sprintf("  %-16s %s", "Hashrate", formatHashrate(hps)))
	writeLine(fmt.Sprintf("  %-16s %s%d%s accepted, %s%d%s rejected",
		"Proofs",
		ansiGreen, d.Accepted, ansiReset,
		ansiYellow, d.Rejected, ansiReset))
	writeLine(fmt.Sprintf("  %-16s %s", "Uptime", uptime))
	writeLine("")
	lastEvtAge := ""
	if !d.LastEventAt.IsZero() {
		lastEvtAge = fmt.Sprintf(" (%s ago)", shortAge(time.Since(d.LastEventAt)))
	}
	writeLine(fmt.Sprintf("  %-16s %s%s", "Last event", truncateForLine(d.LastEvent, 80), lastEvtAge))
	writeLine("")
	writeLine(ansiDim + "  Ctrl-C to stop. Config: " + ansiReset + ansiCyan + os.Getenv("QSDM_MINER_CONFIG_DISPLAY") + ansiReset)
	_, _ = c.w.Write(buf.Bytes())
}

func (c *consoleRenderer) Event(_ Event) {
	// Event stream is applied into Dashboard state by the owner and
	// then Render is called; nothing extra to print here.
}

func (c *consoleRenderer) Close() {
	// Drop a newline so the shell prompt lands cleanly below the panel.
	fmt.Fprintln(c.w)
}

// Short uint-to-string helper so the renderer can splice
// mining.ProtocolVersion (a uint32) into the panel banner without
// pulling strconv.
func itoa(n uint32) string { return fmt.Sprintf("%d", n) }

// shortAge renders "3s", "12m", "4h" etc. so "Last event" line stays
// narrow enough to fit on the right side of the panel.
func shortAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d/time.Second))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	default:
		return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
	}
}

func truncateForLine(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max < 4 {
		return s[:max]
	}
	return s[:max-1] + "\u2026"
}

// -----------------------------------------------------------------------------
// Setup wizard
// -----------------------------------------------------------------------------

// runSetup is the interactive first-run flow. It asks for the reward
// address and validator URL, pre-filling from any existing config so a
// repeated --setup just bumps the values. The wizard is stdin-only —
// no TUI framework, no readline — so it works equally well on a
// headless VPS, a Windows console host, and a CI stdin pipe.
func runSetup(path string, cur Config) (Config, error) {
	fmt.Printf("%s — setting up %s console miner\n", branding.Name, branding.CoinName)
	fmt.Println("Answers are saved to", path)
	fmt.Println("Press Enter to accept the [default] shown in brackets.")
	fmt.Println()

	validator := prompt("Validator URL",
		orDefault(cur.ValidatorURL, "https://testnet.qsdm.tech"))
	validator = strings.TrimRight(strings.TrimSpace(validator), "/")

	addr := prompt("Reward address ("+branding.CoinSymbol+")",
		cur.RewardAddr)
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return cur, errors.New("reward address must not be empty")
	}

	batch := promptUint("Batch count per proof", uint32OrDefault(cur.BatchCount, 1))
	poll := prompt("Poll interval (e.g. 2s, 500ms)",
		orDefault(cur.PollInterval, "2s"))

	newCfg := Config{
		ValidatorURL: validator,
		RewardAddr:   addr,
		BatchCount:   batch,
		PollInterval: poll,
		Plain:        cur.Plain,
	}

	if err := saveConfig(path, newCfg); err != nil {
		return cur, err
	}
	fmt.Printf("\nSaved %s\n", path)
	return newCfg, nil
}

func prompt(label, def string) string {
	if def != "" {
		fmt.Printf("  %s [%s]: ", label, def)
	} else {
		fmt.Printf("  %s: ", label)
	}
	var line string
	if _, err := fmt.Scanln(&line); err != nil {
		// Scanln returns io.EOF on an empty line — treat as "accept
		// default". Any other error (e.g. pipe closed) is also
		// treated as an empty answer so the wizard doesn't crash on
		// a detached stdin.
		line = ""
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return def
	}
	return line
}

func promptUint(label string, def uint32) uint32 {
	s := prompt(label, fmt.Sprintf("%d", def))
	var v uint32
	_, err := fmt.Sscanf(s, "%d", &v)
	if err != nil || v == 0 {
		return def
	}
	return v
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func uint32OrDefault(v, def uint32) uint32 {
	if v == 0 {
		return def
	}
	return v
}

// printNvidiaLockDeprecationBanner emits a one-time notice to the
// given writer warning operators that the CPU reference miner will
// be retired when the NVIDIA-locked v2 protocol activates. The
// design is documented in nvidia_locked_qsdmplus_blockchain_architecture.md
// at the repo root.
//
// Writes go to stderr (not stdout) so piping the miner's stdout to
// a log-aggregator stays clean. The banner is deliberately framed
// as informational, not as a fatal precondition — the binary still
// works end-to-end against pre-v2 validators, which is what the
// testnet currently runs.
func printNvidiaLockDeprecationBanner(w io.Writer) {
	fmt.Fprintln(w, "┌─────────────────────────────────────────────────────────────────────┐")
	fmt.Fprintln(w, "│  qsdmminer-console: NVIDIA-lock pivot in progress                   │")
	fmt.Fprintln(w, "│                                                                     │")
	fmt.Fprintln(w, "│  QSDM is moving to a GPU-only protocol (see                         │")
	fmt.Fprintln(w, "│  nvidia_locked_qsdmplus_blockchain_architecture.md). Once the       │")
	fmt.Fprintln(w, "│  v2 hard fork activates, CPU proofs will NOT be accepted on         │")
	fmt.Fprintln(w, "│  mainnet. This binary is kept for testnet replay + reference.       │")
	fmt.Fprintln(w, "│  Plan your deployment around an NVIDIA CUDA GPU.                    │")
	fmt.Fprintln(w, "└─────────────────────────────────────────────────────────────────────┘")
}

// -----------------------------------------------------------------------------
// main
// -----------------------------------------------------------------------------

func main() {
	var (
		configPath   = flag.String("config", defaultConfigPath(), "path to the miner config file")
		validatorURL = flag.String("validator", "", "override config: validator base URL")
		rewardAddr   = flag.String("address", "", "override config: reward address")
		setup        = flag.Bool("setup", false, "force the interactive setup wizard then exit (or continue mining after)")
		plain        = flag.Bool("plain", false, "disable the live console panel; log one line per event instead")
		selfTest     = flag.Bool("self-test", false, "run an in-memory solve-and-verify and exit 0 on success")
		batchCount   = flag.Uint("batch-count", 0, "override config: batches claimed per proof (0 = use config)")
		pollInterval = flag.Duration("poll", 0, "override config: work-poll interval (0 = use config)")
		httpTimeout  = flag.Duration("http-timeout", 30*time.Second, "per-request HTTP timeout")
		showVersion  = flag.Bool("version", false, "print build metadata (release tag, git SHA, build date, runtime) and exit")
	)
	flag.Usage = func() {
		out := flag.CommandLine.Output()
		fmt.Fprintf(out, "%s — friendly console miner (MINING_PROTOCOL.md v%d)\n\n", branding.FullTitle(), mining.ProtocolVersion)
		fmt.Fprintf(out, "Run with no flags to use the saved config. First run opens a setup wizard.\n\n")
		fmt.Fprintf(out, "Usage: %s [flags]\n\nFlags:\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()

	// --version is handled before config load / wizard / any side
	// effect, so it's usable on a fresh host that doesn't yet have a
	// miner.toml. Same contract as cmd/qsdmminer and cmd/trustcheck.
	if *showVersion {
		fmt.Println(buildinfo.String(binaryName))
		return
	}

	if *selfTest {
		if err := runSelfTest(); err != nil {
			fmt.Fprintf(os.Stderr, "self-test FAILED: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("self-test OK: proof solved and verified end-to-end via pkg/mining")
		return
	}

	// Deprecation banner — printed on every real mining run (but NOT
	// on --version / --self-test, which must stay machine-parseable
	// for CI and `docker inspect`-style checks). The project is
	// pivoting to the NVIDIA-locked v2 protocol described in
	// nvidia_locked_qsdmplus_blockchain_architecture.md; once v2
	// activates, CPU-only miners can no longer produce proofs that
	// mainnet validators accept. This binary stays in-tree for
	// testnet replay and algorithmic reference, and shipping it
	// without a banner would be quietly misleading to any operator
	// who expects mainnet rewards. Keep the text short — operators
	// running systemd / journalctl will see it on every restart.
	printNvidiaLockDeprecationBanner(os.Stderr)

	cfg, err := loadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}

	// The setup wizard runs when: the user explicitly asked; OR the
	// config lacks the two minimum fields AND no CLI override has
	// supplied them. This avoids pushing a wizard at someone who is
	// scripting the miner via flags.
	needSetup := *setup || (cfg.RewardAddr == "" && *rewardAddr == "")
	if needSetup {
		newCfg, err := runSetup(*configPath, cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "setup: %v\n", err)
			os.Exit(1)
		}
		cfg = newCfg
		if *setup && !cliWantsContinue() {
			return
		}
	}

	// CLI flags win over config so an operator can temporarily point
	// at a different validator without editing miner.toml.
	if *validatorURL != "" {
		cfg.ValidatorURL = strings.TrimRight(*validatorURL, "/")
	}
	if *rewardAddr != "" {
		cfg.RewardAddr = *rewardAddr
	}
	if *batchCount > 0 {
		cfg.BatchCount = uint32(*batchCount)
	}
	if *pollInterval > 0 {
		cfg.PollInterval = pollInterval.String()
	}
	if cfg.BatchCount == 0 {
		cfg.BatchCount = 1
	}

	if cfg.ValidatorURL == "" {
		fmt.Fprintln(os.Stderr, "no validator_url set — run `qsdmminer-console --setup` first")
		os.Exit(2)
	}
	if cfg.RewardAddr == "" {
		fmt.Fprintln(os.Stderr, "no reward_address set — run `qsdmminer-console --setup` first")
		os.Exit(2)
	}

	// Renderer choice: --plain forces plain; otherwise TTY autodetect.
	// Piping to a file should never emit ANSI escapes.
	usePanel := !*plain && !cfg.Plain && term.IsTerminal(int(os.Stdout.Fd()))

	// Stash the config path so the panel footer can display it.
	// Using an env var keeps the renderer free of config-path
	// plumbing; tests that don't set the env see an empty footer.
	_ = os.Setenv("QSDM_MINER_CONFIG_DISPLAY", *configPath)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	var rend renderer
	if usePanel {
		rend = newConsoleRenderer(os.Stdout)
	} else {
		rend = &plainRenderer{w: os.Stdout}
	}
	defer rend.Close()

	dash := &Dashboard{
		StartedAt: time.Now(),
		Validator: cfg.ValidatorURL,
		Address:   cfg.RewardAddr,
		Status:    "connecting",
	}
	events := make(chan Event, 32)
	var attempts uint64

	// Renderer goroutine: draw at 2 Hz in panel mode, passive in plain
	// mode. Keeping this out of the mining loop means a stuck HTTP
	// request doesn't freeze the panel.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		// Rolling-window hashrate: keep last-known cumulative and
		// timestamp, derive rate as Δcount/Δt. 10-second window is
		// the same cadence qsdmminer uses; it prevents the displayed
		// rate from jittering between 0 and peak every redraw.
		const window = 10 * time.Second
		type sample struct {
			at    time.Time
			count uint64
		}
		var samples []sample
		currentHPS := 0.0
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-events:
				if !ok {
					return
				}
				dash.applyEvent(ev)
				rend.Event(ev)
			case now := <-ticker.C:
				cur := atomic.LoadUint64(&attempts)
				samples = append(samples, sample{at: now, count: cur})
				cutoff := now.Add(-window)
				for len(samples) > 1 && samples[0].at.Before(cutoff) {
					samples = samples[1:]
				}
				if len(samples) >= 2 {
					first := samples[0]
					last := samples[len(samples)-1]
					dt := last.at.Sub(first.at).Seconds()
					if dt > 0 {
						currentHPS = float64(last.count-first.count) / dt
					}
				}
				rend.Render(dash, currentHPS)
			}
		}
	}()

	client := &http.Client{Timeout: *httpTimeout}
	runLoop(ctx, client, cfg, events, &attempts)

	events <- Event{Kind: EvShutdown, At: time.Now(), Message: "shutting down"}
	close(events)
	wg.Wait()
}

// cliWantsContinue returns true if the user asked --setup but *also*
// supplied enough flags to keep mining after the wizard exits.
// Today we pick the explicit behaviour: --setup by itself exits;
// --setup together with --address or --validator keeps mining. This
// avoids surprising scripts that re-run the wizard in a cron job.
func cliWantsContinue() bool {
	var seen bool
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "address" || f.Name == "validator" {
			seen = true
		}
	})
	return seen
}

// -----------------------------------------------------------------------------
// Mining loop — same flow as cmd/qsdmminer, adapted to emit Events
// instead of printing directly. Keeping the two loops independent
// preserves the cmd/qsdmminer invariant that it remains a single
// readable file mappable 1-to-1 against MINING_PROTOCOL.md, while
// this binary can freely evolve its UX.
// -----------------------------------------------------------------------------

func runLoop(ctx context.Context, client *http.Client, cfg Config, events chan<- Event, attempts *uint64) {
	var (
		currentEpoch uint64 = ^uint64(0)
		currentDAG   mining.DAG
	)
	poll := cfg.pollDuration()
	send := func(e Event) {
		e.At = time.Now()
		select {
		case events <- e:
		case <-ctx.Done():
		}
	}

	send(Event{Kind: EvConnecting, Message: "contacting " + cfg.ValidatorURL})

	for {
		if ctx.Err() != nil {
			return
		}
		work, err := fetchWork(ctx, client, cfg.ValidatorURL)
		if err != nil {
			send(Event{Kind: EvError, Message: "fetch work: " + err.Error()})
			sleepOrCancel(ctx, poll)
			continue
		}
		send(Event{Kind: EvConnected, Message: fmt.Sprintf("work received: height=%d", work.Height)})

		batchCount := cfg.BatchCount
		if work.BatchCountMaximum > 0 && batchCount > work.BatchCountMaximum {
			send(Event{Kind: EvInfo, Message: fmt.Sprintf("clamping batch_count %d → %d (server max)", batchCount, work.BatchCountMaximum)})
			batchCount = work.BatchCountMaximum
		}
		ws, hdr, diff, err := api.WorkToMiningCore(work)
		if err != nil {
			send(Event{Kind: EvError, Message: "decode work: " + err.Error()})
			sleepOrCancel(ctx, poll)
			continue
		}
		ws.Canonicalize()
		batchRoot, err := ws.PrefixRoot(batchCount)
		if err != nil {
			send(Event{Kind: EvError, Message: "prefix root: " + err.Error()})
			sleepOrCancel(ctx, poll)
			continue
		}
		target, err := mining.TargetFromDifficulty(diff)
		if err != nil {
			send(Event{Kind: EvError, Message: "target: " + err.Error()})
			sleepOrCancel(ctx, poll)
			continue
		}
		if work.Epoch != currentEpoch {
			send(Event{Kind: EvEpochChanged, Epoch: work.Epoch, DAGSize: work.DAGSize,
				Message: fmt.Sprintf("new mining epoch %d (N=%d)", work.Epoch, work.DAGSize)})
			start := time.Now()
			dag, err := mining.NewInMemoryDAG(work.Epoch, ws.Root(), work.DAGSize)
			if err != nil {
				send(Event{Kind: EvError, Message: "build DAG: " + err.Error()})
				sleepOrCancel(ctx, poll)
				continue
			}
			send(Event{Kind: EvDAGReady, Epoch: work.Epoch, DAGSize: work.DAGSize,
				Message: fmt.Sprintf("DAG built in %s", time.Since(start).Round(time.Millisecond))})
			currentDAG = dag
			currentEpoch = work.Epoch
		}

		sctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
		res, err := mining.Solve(sctx, mining.SolverParams{
			Epoch:      work.Epoch,
			Height:     work.Height,
			HeaderHash: hdr,
			MinerAddr:  cfg.RewardAddr,
			BatchRoot:  batchRoot,
			BatchCount: batchCount,
			Target:     target,
			DAG:        currentDAG,
		}, nil, attempts)
		cancel()
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				continue
			}
			send(Event{Kind: EvError, Message: "solve: " + err.Error()})
			sleepOrCancel(ctx, poll)
			continue
		}
		raw, err := res.Proof.CanonicalJSON()
		if err != nil {
			send(Event{Kind: EvError, Message: "encode proof: " + err.Error()})
			continue
		}
		resp, err := submitProof(ctx, client, cfg.ValidatorURL, raw)
		if err != nil {
			send(Event{Kind: EvError, Message: "submit: " + err.Error()})
			sleepOrCancel(ctx, poll)
			continue
		}
		if resp.Accepted {
			send(Event{
				Kind:     EvProofAccepted,
				Height:   work.Height,
				Epoch:    work.Epoch,
				Attempts: res.Attempts,
				ProofID:  resp.ProofID,
				Message: fmt.Sprintf("proof ACCEPTED height=%d attempts=%d id=%s",
					work.Height, res.Attempts, resp.ProofID),
			})
		} else {
			send(Event{
				Kind:   EvProofRejected,
				Height: work.Height,
				Reason: resp.RejectReason,
				Message: fmt.Sprintf("proof rejected reason=%s detail=%q",
					resp.RejectReason, resp.Detail),
			})
		}
	}
}

// -----------------------------------------------------------------------------
// HTTP helpers — straight port of cmd/qsdmminer's fetch/submit. Kept
// local so this binary has no test-time dependency on cmd/qsdmminer,
// which is a main package and can't be imported anyway.
// -----------------------------------------------------------------------------

func fetchWork(ctx context.Context, client *http.Client, baseURL string) (*api.MiningWork, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/v1/mining/work", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, truncateForLine(string(body), 200))
	}
	var work api.MiningWork
	if err := json.Unmarshal(body, &work); err != nil {
		return nil, fmt.Errorf("decode work: %w", err)
	}
	return &work, nil
}

func submitProof(ctx context.Context, client *http.Client, baseURL string, raw []byte) (*api.MiningSubmitResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/v1/mining/submit", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var out api.MiningSubmitResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decode submit (status %d): %w", resp.StatusCode, err)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusBadRequest {
		return &out, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	return &out, nil
}

func sleepOrCancel(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// -----------------------------------------------------------------------------
// Self-test — identical semantics to cmd/qsdmminer --self-test. The
// implementation is a straight port rather than an import because
// cmd/qsdmminer is a main package. Keeping a working self-test here
// means CI can gate this binary against MINING_PROTOCOL.md the same
// way it gates qsdmminer.
// -----------------------------------------------------------------------------

func runSelfTest() error {
	ws := syntheticWorkSet(4)
	const dagN = 128
	epoch := uint64(0)
	dag, err := mining.NewInMemoryDAG(epoch, ws.Root(), dagN)
	if err != nil {
		return fmt.Errorf("dag: %w", err)
	}
	difficulty := big.NewInt(2)
	target, err := mining.TargetFromDifficulty(difficulty)
	if err != nil {
		return err
	}
	headerHash := [32]byte{0x5E, 0x1F, 0x7E, 0x57}
	batchRoot, err := ws.PrefixRoot(1)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	solveStart := time.Now()
	res, err := mining.Solve(ctx, mining.SolverParams{
		Epoch:      epoch,
		HeaderHash: headerHash,
		MinerAddr:  "qsdm1selftest",
		BatchRoot:  batchRoot,
		BatchCount: 1,
		Target:     target,
		DAG:        dag,
	}, nil, nil)
	if err != nil {
		return fmt.Errorf("solve: %w", err)
	}
	solveDur := time.Since(solveStart)
	verifier, err := mining.NewVerifier(mining.VerifierConfig{
		EpochParams:      mining.NewEpochParams(),
		DifficultyParams: mining.NewDifficultyAdjusterParams(),
		Chain:            &selftestChain{tip: 0, header: headerHash},
		Addresses:        selftestAddr{},
		Batches:          selftestBatch{},
		Dedup:            mining.NewProofIDSet(1024),
		Quarantine:       mining.NewQuarantineSet(),
		DAGProvider:      func(_ uint64) (mining.DAG, error) { return dag, nil },
		WorkSetProvider:  func(_ uint64) (mining.WorkSet, error) { return ws, nil },
		DifficultyAt:     func(_ uint64) (*big.Int, error) { return difficulty, nil },
	})
	if err != nil {
		return fmt.Errorf("verifier: %w", err)
	}
	raw, err := res.Proof.CanonicalJSON()
	if err != nil {
		return err
	}
	if _, err := verifier.Verify(raw, 0); err != nil {
		return fmt.Errorf("verify: %w", err)
	}
	fmt.Printf("self-test: solved in %d attempts in %s\n", res.Attempts, solveDur.Round(time.Millisecond))
	return nil
}

func syntheticWorkSet(n int) mining.WorkSet {
	ws := mining.WorkSet{Batches: make([]mining.Batch, n)}
	for i := 0; i < n; i++ {
		cells := make([]mining.ParentCellRef, 3)
		for j := 0; j < 3; j++ {
			var ch [32]byte
			ch[0] = byte(i)
			ch[1] = byte(j)
			cells[j] = mining.ParentCellRef{
				ID:          []byte{byte(i), byte(j), 0xAB},
				ContentHash: ch,
			}
		}
		ws.Batches[i] = mining.Batch{Cells: cells}
	}
	ws.Canonicalize()
	return ws
}

type selftestChain struct {
	tip    uint64
	header [32]byte
}

func (c *selftestChain) TipHeight() uint64 { return c.tip }
func (c *selftestChain) HeaderHashAt(h uint64) ([32]byte, bool) {
	if h == c.tip {
		return c.header, true
	}
	return [32]byte{}, false
}

type selftestAddr struct{}

func (selftestAddr) ValidateAddress(a string) error {
	if a == "" {
		return errors.New("empty address")
	}
	return nil
}

type selftestBatch struct{}

func (selftestBatch) ValidateBatch(_ mining.Batch) error { return nil }
