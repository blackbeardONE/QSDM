//go:build hl_crashpoints

package main

// Errata E7, process level (hl_crashpoints build): the real main() in the
// producer role seals a block and is killed after H3 (C5) or after H4 (C6a),
// or fail-stops on an EIO at H5 (F1, then R-FS); the restarted main() runs S4r
// and S5, and is stopped by a crash point at its first seal attempt (C1,
// which leaves the durable state alone). After the restart the receipts cover
// every chain tx exactly once and W is the tip. A SIGKILL inside S4r after
// the append is finished by the next boot.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/internal/blockdriver"
	"github.com/blackbeardONE/QSDM/internal/legacymining"
	"github.com/blackbeardONE/QSDM/pkg/chain"
)

// hl1rFreePorts returns n distinct free TCP ports on 127.0.0.1.
func hl1rFreePorts(t *testing.T, n int) []int {
	t.Helper()
	var out []int
	var ls []net.Listener
	for len(out) < n {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		ls = append(ls, l)
		out = append(out, l.Addr().(*net.TCPAddr).Port)
	}
	for _, l := range ls {
		_ = l.Close()
	}
	return out
}

// hl1rProducerState is a producer state directory main() can boot: blocks
// 0..2 sealed by the HL1 hook (W = 2, source seal) with the block driver's
// funder in the accounts snapshot.
func hl1rProducerState(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	c := hl1rStartCore(t, dir, nil, hl1rSigner(t))
	c.st.accounts.Credit(blockdriver.FunderAddress, 1000)
	for !c.st.producer.HasTip() || c.st.producer.TipHeight() < 2 {
		c.seal()
	}
	c.crash()
	if w := hl1rW(t, dir); w.Height != 2 {
		t.Fatalf("fixture W = %+v", w)
	}
	return dir
}

// hl1rRunProducer runs main() in the producer role on dir with extra
// environment (crash points), on free loopback ports.
func hl1rRunProducer(t *testing.T, dir string, extra ...string) (int, string) {
	t.Helper()
	p := hl1rFreePorts(t, 3)
	env := append([]string{
		"QSDM_SOLO_VALIDATOR_MODE=1",
		fmt.Sprintf("API_PORT=%d", p[0]), fmt.Sprintf("DASHBOARD_PORT=%d", p[1]), fmt.Sprintf("NETWORK_PORT=%d", p[2]),
		"QSDM_API_BIND_ADDRESS=127.0.0.1", "QSDM_DASHBOARD_BIND_ADDRESS=127.0.0.1", "QSDM_NETWORK_BIND_ADDRESS=127.0.0.1",
	}, extra...)
	code, out := hl1RunMain(t, dir, env...)
	if strings.Contains(out, "WARNING: DATA RACE") {
		t.Fatalf("the race detector fired in main():\n%s", out)
	}
	return code, out
}

// hl1rCoverage checks that every tx of every journal block has exactly one
// receipts line at its height with its block hash, and returns the tip.
func hl1rCoverage(t *testing.T, dir string) *chain.Block {
	t.Helper()
	if err := hl1RequireFinalNewline(hl1OSFS{}, filepath.Join(dir, hl1ReceiptsName)); err != nil {
		t.Fatal(err)
	}
	blocks, err := chain.LoadChainNDJSON(filepath.Join(dir, hl1JournalName))
	if err != nil {
		t.Fatal(err)
	}
	type key struct {
		h  uint64
		id string
	}
	lines := map[key]int{}
	hashes := map[key]string{}
	data := hl1rRead(t, filepath.Join(dir, hl1ReceiptsName))
	for _, l := range bytes.Split(bytes.TrimSuffix(data, []byte("\n")), []byte("\n")) {
		var r chain.TxReceipt
		if err := json.Unmarshal(l, &r); err != nil {
			t.Fatalf("receipts line %q: %v", l, err)
		}
		k := key{r.BlockHeight, r.TxID}
		lines[k]++
		hashes[k] = r.BlockHash
	}
	for _, b := range blocks {
		for _, tx := range b.Transactions {
			k := key{b.Height, tx.ID}
			if lines[k] != 1 || hashes[k] != b.Hash {
				t.Fatalf("tx %s of block %d has %d receipts lines (hash %q)", tx.ID, b.Height, lines[k], hashes[k])
			}
		}
	}
	return blocks[len(blocks)-1]
}

func hl1rWantExit(t *testing.T, what string, code int, out string, want int, contains ...string) {
	t.Helper()
	if want >= 0 && code != want {
		t.Fatalf("%s: exit %d, want %d\n%s", what, code, want, out)
	}
	for _, c := range contains {
		if !strings.Contains(out, c) {
			t.Fatalf("%s: output lacks %q (exit %d)\n%s", what, c, code, out)
		}
	}
}

// The S4r fault points fail the boot with the I/O route and leave W alone;
// the next boot completes N without duplicates.
func TestHL1S4rInjectedFaults(t *testing.T) {
	if testing.Short() {
		t.Skip("state-directory recovery test")
	}
	const n = 4
	for _, point := range []string{"boot:S4r:append", "boot:S4r:fsync"} {
		t.Run(point, func(t *testing.T) {
			f := hl1rCrash(t, false, n, "H5")
			wBefore := hl1rRead(t, f.path(legacymining.WatermarkFile))
			t.Setenv("QSDM_HL1_FAULT", point+":EIO")
			t.Setenv("QSDM_HL1_CRASH", "")
			hl1ResetCrashpoints()
			_, _, _, err := hl1rBootWith(t, f.dir, nil, hl1OSFS{}, nil)
			if err == nil || !strings.Contains(err.Error(), hl1S4rRouteC6IO) || !strings.Contains(err.Error(), point) {
				t.Fatalf("err = %v", err)
			}
			if !bytes.Equal(hl1rRead(t, f.path(legacymining.WatermarkFile)), wBefore) {
				t.Fatal("W changed after an injected S4r fault")
			}
			t.Setenv("QSDM_HL1_FAULT", "")
			hl1ResetCrashpoints()
			if _, s5, err := hl1rBoot(t, f.dir, nil); err != nil || !s5.Wrote {
				t.Fatalf("the next boot: %+v %v", s5, err)
			}
			if got := len(hl1rReceiptsAt(t, f.path(hl1ReceiptsName), n)); got != len(f.blkJ.Transactions) {
				t.Fatalf("%d receipts at N, want %d", got, len(f.blkJ.Transactions))
			}
		})
	}
	hl1ResetCrashpoints()
}

// hl1rKilled is the exit code of a crash point: SIGKILL on Unix (-1),
// TerminateProcess(1) on Windows. Checked through the crash point's log line.
const hl1rKilled = -2

func TestHL1S4rProcessCrashRecovery(t *testing.T) {
	if testing.Short() {
		t.Skip("process-level crash test")
	}
	const stop = "QSDM_HL1_CRASH=produce:seal-guard" // C1 at the first seal after the boot
	for _, tc := range []struct {
		name  string
		crash []string // environment of the run that seals block N
		point string   // its crash point log, or "" for the F1 exit 86
	}{
		{"C5: SIGKILL after H3", []string{"QSDM_HL1_CRASH=persist:after-H3"}, "persist:after-H3"},
		{"C6a: SIGKILL after H4", []string{"QSDM_HL1_CRASH=persist:after-H4"}, "persist:after-H4"},
		{"F1: EIO at H5, then R-FS", []string{"QSDM_HL1_FAULT=persist:H5:EIO", stop + "@2"}, ""},
		{"SIGKILL inside S4r after the append", []string{"QSDM_HL1_CRASH=persist:after-H4"}, "persist:after-H4"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := hl1rProducerState(t)
			code, out := hl1rRunProducer(t, dir, tc.crash...)
			if tc.point != "" {
				hl1rWantExit(t, "crash run", code, out, hl1rKilled, "SIGKILL at "+tc.point)
			} else {
				hl1rWantExit(t, "F1 run", code, out, legacymining.ExitFailStop, "persist:H5")
				code, out = hl1rRunProducer(t, dir, stop)
				hl1rWantExit(t, "start with FAILSTOP.json", code, out, legacymining.ExitFatalRestore, "R-FS")
				// R-FS: the cause (an injected EIO) is gone; remove the latch.
				if err := os.Rename(filepath.Join(dir, legacymining.FailStopFile), filepath.Join(dir, "FAILSTOP.json.resolved")); err != nil {
					t.Fatal(err)
				}
			}
			// The crash state: journal N, W = N-1, no receipts at N.
			blocks, err := chain.LoadChainNDJSON(filepath.Join(dir, hl1JournalName))
			if err != nil {
				t.Fatal(err)
			}
			blkN := blocks[len(blocks)-1]
			if w := hl1rW(t, dir); w.Height != blkN.Height-1 {
				t.Fatalf("crash state: W = %d, journal tip %d", w.Height, blkN.Height)
			}
			if n := len(hl1rReceiptsAt(t, filepath.Join(dir, hl1ReceiptsName), blkN.Height)); n != 0 {
				t.Fatalf("crash state already has %d receipts at N", n)
			}

			if strings.HasPrefix(tc.name, "SIGKILL inside S4r") {
				code, out = hl1rRunProducer(t, dir, "QSDM_HL1_CRASH=boot:S4r:after-append")
				hl1rWantExit(t, "S4r crash run", code, out, hl1rKilled, "SIGKILL at boot:S4r:after-append")
				if w := hl1rW(t, dir); w.Height != blkN.Height-1 {
					t.Fatalf("W = %d after a crash inside S4r", w.Height)
				}
			}

			code, out = hl1rRunProducer(t, dir, stop)
			hl1rWantExit(t, "restart", code, out, hl1rKilled, "SIGKILL at produce:seal-guard", "served watermark verified")
			tip := hl1rCoverage(t, dir)
			wantLog := "hl1 S4r: receipts regenerated"
			if strings.HasPrefix(tc.name, "SIGKILL inside S4r") {
				wantLog = "has all its receipts" // the killed S4r had appended every line
			}
			if !strings.Contains(out, wantLog) {
				t.Fatalf("the restart did not log %q:\n%s", wantLog, out)
			}
			if tip.Hash != blkN.Hash {
				t.Fatalf("journal tip %d after the restart, want %d", tip.Height, blkN.Height)
			}
			if w := hl1rW(t, dir); w.Height != blkN.Height || w.Hash != blkN.Hash || w.Source != legacymining.WatermarkSourceBoot {
				t.Fatalf("W after the restart = %+v, want N (boot)", w)
			}
			if _, err := os.Lstat(filepath.Join(dir, legacymining.FailStopFile)); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("FAILSTOP.json after the restart: %v", err)
			}
		})
	}
}
