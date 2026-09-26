package main

// Process-level boot tests: the test binary re-executes itself and runs the
// real main(), so the exit codes are the ones systemd would see (design rev 4
// §5 S0-S2, §7 "State lock" and "Fatal conversions", Appendix A).

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/internal/legacymining"
	"github.com/blackbeardONE/QSDM/pkg/chain"
)

const hl1MainChildEnv = "HL1_TEST_RUN_MAIN"

// TestHL1MainChild is the child process: it runs main() and never returns
// normally in these tests (main exits). In a normal test run it is skipped.
func TestHL1MainChild(t *testing.T) {
	if os.Getenv(hl1MainChildEnv) != "1" {
		t.Skip("helper process for the HL1 boot tests")
	}
	os.Args = []string{os.Args[0]}
	main()
	os.Exit(0)
}

// hl1RunMain runs main() in a child process with a scrubbed environment plus
// extra, and returns its exit code and combined output.
func hl1RunMain(t *testing.T, stateDir string, extra ...string) (int, string) {
	t.Helper()
	scratch := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestHL1MainChild$", "-test.count=1")
	cmd.Dir = scratch
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		switch {
		case strings.HasPrefix(k, "QSDM_"), strings.HasPrefix(k, "WEBVIEWER_"),
			k == "CONFIG_FILE", k == "SQLITE_PATH", k == "LOG_FILE", k == "PROPOSAL_FILE":
			continue
		}
		env = append(env, kv)
	}
	env = append(env,
		hl1MainChildEnv+"=1",
		"CONFIG_FILE="+filepath.Join(scratch, "absent.toml"),
		"SQLITE_PATH="+filepath.Join(stateDir, "qsdm.db"),
		"LOG_FILE="+filepath.Join(scratch, "qsdm.log"),
		"PROPOSAL_FILE="+filepath.Join(scratch, "proposals.json"),
		"DISABLE_CLI=1",
	)
	cmd.Env = append(env, extra...)
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("child main did not exit: %v\n%s", ctx.Err(), out)
	}
	var ee *exec.ExitError
	switch {
	case err == nil:
		return 0, string(out)
	case errors.As(err, &ee):
		return ee.ExitCode(), string(out)
	default:
		t.Fatalf("run child: %v", err)
		return -1, ""
	}
}

// hl1TreeHash hashes every path, mode and content below dir.
func hl1TreeHash(t *testing.T, dir string) string {
	t.Helper()
	var lines []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		info, err := d.Info()
		if err != nil {
			return err
		}
		line := rel + " " + info.Mode().String()
		if d.Name() == legacymining.StateLockFile {
			// Windows refuses reads of a byte-range-locked file; its size
			// still shows a truncate or rewrite.
			line += fmt.Sprintf(" size=%d", info.Size())
		} else if !d.IsDir() {
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(data)
			line += " " + hex.EncodeToString(sum[:])
		}
		lines = append(lines, line)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}

// hl1StateFixture is a producer-shaped state directory: journal, snapshots,
// receipts, W, markers and stale D1 temps in both HL1 directories.
func hl1StateFixture(t *testing.T) (dir string, staleTemps []string) {
	t.Helper()
	dir = t.TempDir()
	blocks := hl1Chain(3)
	journal := filepath.Join(dir, "qsdm_chain.ndjson")
	for _, b := range blocks {
		if err := chain.AppendBlockToFile(journal, b); err != nil {
			t.Fatal(err)
		}
	}
	hl1WriteFile(t, filepath.Join(dir, "qsdm_accounts.json"), "{}")
	hl1WriteFile(t, filepath.Join(dir, "qsdm_enrollment.json"), `{"records":[]}`)
	hl1WriteFile(t, filepath.Join(dir, "qsdm_receipts.ndjson"), "")
	hl1SeedW(t, dir, legacymining.Watermark{Version: 1, Height: 2, Hash: blocks[2].Hash, Source: "seed", WrittenNS: 1})
	hl1WriteFile(t, filepath.Join(dir, legacymining.FailStopArmedFile), `{"release":"old"}`)
	legacy := filepath.Join(dir, legacymining.LegacyDirName)
	if err := os.Mkdir(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	staleTemps = []string{
		filepath.Join(dir, legacymining.TempPrefix+legacymining.WatermarkFile+"-1-0011223344556677"),
		filepath.Join(legacy, legacymining.TempPrefix+legacymining.TrippedFile+"-1-8899aabbccddeeff"),
	}
	for _, p := range staleTemps {
		hl1WriteFile(t, p, "partial")
	}
	return dir, staleTemps
}

func TestHL1BootStateLockBusyExits78WithoutChanges(t *testing.T) {
	if testing.Short() {
		t.Skip("process-level boot test")
	}
	dir, stale := hl1StateFixture(t)
	held, err := chain.AcquireStateLock(filepath.Join(dir, legacymining.StateLockFile))
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	before := hl1TreeHash(t, dir)

	code, out := hl1RunMain(t, dir)
	if code != legacymining.ExitFatalRestore {
		t.Fatalf("exit = %d, want 78\n%s", code, out)
	}
	if !strings.Contains(out, "validator state lock") {
		t.Fatalf("refusal does not name the state lock:\n%s", out)
	}
	if after := hl1TreeHash(t, dir); after != before {
		t.Fatal("the state directory changed although the lock was busy")
	}
	for _, p := range stale {
		if _, err := os.Lstat(p); err != nil {
			t.Fatalf("stale temp %s was removed without the lock: %v", filepath.Base(p), err)
		}
	}
}

func TestHL1BootFailStopTrippedExits78BeforeS3(t *testing.T) {
	if testing.Short() {
		t.Skip("process-level boot test")
	}
	dir, stale := hl1StateFixture(t)
	if err := os.Rename(filepath.Join(dir, legacymining.FailStopArmedFile), filepath.Join(dir, legacymining.FailStopFile)); err != nil {
		t.Fatal(err)
	}
	code, out := hl1RunMain(t, dir)
	if code != legacymining.ExitFatalRestore || !strings.Contains(out, "R-FS") {
		t.Fatalf("exit = %d, want 78 naming R-FS\n%s", code, out)
	}
	if _, err := os.Lstat(filepath.Join(dir, legacymining.FailStopArmedFile)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("S1 refusal still armed a new FAILSTOP")
	}
	for _, p := range stale {
		if _, err := os.Lstat(p); err != nil {
			t.Fatalf("S1 refusal ran the S3 cleanup: %v", err)
		}
	}
}

func TestHL1BootPartialLegacyMiningEnvExits78(t *testing.T) {
	if testing.Short() {
		t.Skip("process-level boot test")
	}
	dir, _ := hl1StateFixture(t)
	code, out := hl1RunMain(t, dir, legacymining.EnvMode+"=canary")
	if code != legacymining.ExitFatalRestore || !strings.Contains(out, "hl1 S2") {
		t.Fatalf("exit = %d, want 78 at S2\n%s", code, out)
	}
}

func TestHL1BootConfigLoadFailureStaysExit1(t *testing.T) {
	if testing.Short() {
		t.Skip("process-level boot test")
	}
	dir, _ := hl1StateFixture(t)
	before := hl1TreeHash(t, dir)
	bad := filepath.Join(t.TempDir(), "bad.toml")
	hl1WriteFile(t, bad, "this is = = not toml [")
	code, out := hl1RunMain(t, dir, "CONFIG_FILE="+bad)
	if code != 1 {
		t.Fatalf("config load failure exit = %d, want 1 (auto-restarted like before)\n%s", code, out)
	}
	if !strings.Contains(out, "Failed to load configuration") {
		t.Fatalf("unexpected failure:\n%s", out)
	}
	if hl1TreeHash(t, dir) != before {
		t.Fatal("a config-load failure changed the state directory")
	}
}
