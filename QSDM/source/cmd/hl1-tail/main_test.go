package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/internal/legacymining"
	"github.com/blackbeardONE/QSDM/pkg/chain"
)

const childEnv = "HL1_TAIL_TEST_RUN_MAIN"

// TestMainChild runs main() with the arguments in HL1_TAIL_TEST_ARGS.
func TestMainChild(t *testing.T) {
	if os.Getenv(childEnv) != "1" {
		t.Skip("helper process")
	}
	os.Args = append([]string{"hl1-tail"}, strings.Split(os.Getenv("HL1_TAIL_TEST_ARGS"), "\x1f")...)
	main()
	os.Exit(97) // main always exits
}

// runMain runs the hl1-tail main() in a child process with the core
// configuration resolving the state directory to stateDir.
func runMain(t *testing.T, stateDir string, args ...string) (int, string) {
	t.Helper()
	scratch := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestMainChild$", "-test.count=1")
	cmd.Dir = scratch
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, "QSDM_") || strings.HasPrefix(k, "HL1_") || k == "CONFIG_FILE" || k == "SQLITE_PATH" {
			continue
		}
		env = append(env, kv)
	}
	cmd.Env = append(env, childEnv+"=1", "HL1_TAIL_TEST_ARGS="+strings.Join(args, "\x1f"),
		"CONFIG_FILE="+filepath.Join(scratch, "absent.toml"),
		"SQLITE_PATH="+filepath.Join(stateDir, "qsdm.db"),
		"LOG_FILE="+filepath.Join(scratch, "qsdm.log"),
		"PROPOSAL_FILE="+filepath.Join(scratch, "proposals.json"))
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("child did not exit: %v\n%s", ctx.Err(), out)
	}
	var ee *exec.ExitError
	switch {
	case err == nil:
		return 0, string(out)
	case errors.As(err, &ee):
		return ee.ExitCode(), string(out)
	}
	t.Fatalf("run child: %v", err)
	return -1, ""
}

// The binary resolves the state directory as Dir(cfg.SQLitePath) and exits
// with the Appendix A codes: show works while core holds the lock, retire
// exits 3 then, and 0 once the lock is free.
func TestBinaryStateDirAndExitCodes(t *testing.T) {
	if testing.Short() {
		t.Skip("process-level test")
	}
	dir := t.TempDir()
	w, _ := json.Marshal(legacymining.Watermark{Version: 1, Height: 7, Hash: strings.Repeat("ab", 32), Source: "seal", WrittenNS: 1})
	if err := os.WriteFile(filepath.Join(dir, legacymining.WatermarkFile), append(w, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	held, err := chain.AcquireStateLock(filepath.Join(dir, legacymining.StateLockFile))
	if err != nil {
		t.Fatal(err)
	}
	code, out := runMain(t, dir, "watermark", "show")
	if code != 0 || !strings.Contains(out, `"status":"present"`) || !strings.Contains(out, `"height":7`) {
		t.Fatalf("show under the lock: exit %d\n%s", code, out)
	}
	if code, out := runMain(t, dir, "watermark", "retire"); code != legacymining.TailExitLockBusy {
		t.Fatalf("retire under the lock: exit %d\n%s", code, out)
	}
	if err := held.Close(); err != nil {
		t.Fatal(err)
	}
	if code, out := runMain(t, dir, "watermark", "retire"); code != 0 {
		t.Fatalf("retire: exit %d\n%s", code, out)
	}
	if code, out := runMain(t, dir, "watermark", "show"); code != legacymining.TailExitRefused || !strings.Contains(out, `"status":"absent"`) {
		t.Fatalf("show after retire: exit %d\n%s", code, out)
	}
	if code, out := runMain(t, dir, "--version"); code != 0 || !strings.HasPrefix(out, "hl1-tail ") {
		t.Fatalf("--version: exit %d\n%s", code, out)
	}
}
