package hl1tail

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/internal/legacymining"
	"github.com/blackbeardONE/QSDM/pkg/chain"
)

// -----------------------------------------------------------------------------
// Fixtures
// -----------------------------------------------------------------------------

var testNow = time.Date(2026, 9, 26, 12, 0, 0, 123456789, time.UTC)

func testChain(n int) []*chain.Block {
	var out []*chain.Block
	prev := ""
	for i := 0; i < n; i++ {
		b := &chain.Block{
			Height: uint64(i), PrevHash: prev, StateRoot: fmt.Sprintf("root-%d", i),
			Timestamp: time.Date(2026, 7, 20, 0, 0, i, 0, time.UTC), ProducerID: "hl1tail-test",
		}
		b.Hash = chain.ComputeBlockHash(b)
		out = append(out, b)
		prev = b.Hash
	}
	return out
}

func blockLine(t *testing.T, b *chain.Block) []byte {
	t.Helper()
	data, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	return append(data, '\n')
}

func receiptLine(t *testing.T, id string, h uint64) []byte {
	t.Helper()
	data, err := json.Marshal(&chain.TxReceipt{TxID: id, BlockHeight: h, Status: chain.ReceiptSuccess, Timestamp: time.Unix(int64(h), 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	return append(data, '\n')
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// writeJournal writes blocks as NDJSON followed by fragment.
func writeJournal(t *testing.T, dir string, blocks []*chain.Block, fragment string) []byte {
	t.Helper()
	var buf bytes.Buffer
	for _, b := range blocks {
		buf.Write(blockLine(t, b))
	}
	buf.WriteString(fragment)
	writeFile(t, filepath.Join(dir, JournalFile), buf.Bytes())
	return buf.Bytes()
}

func wmark(h uint64, hash string) legacymining.Watermark {
	return legacymining.Watermark{Version: 1, Height: h, Hash: hash, Source: legacymining.WatermarkSourceSeal, WrittenNS: 1}
}

func writeW(t *testing.T, dir string, w legacymining.Watermark) []byte {
	t.Helper()
	data, err := json.Marshal(w)
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	writeFile(t, filepath.Join(dir, legacymining.WatermarkFile), data)
	return data
}

// staleTemps plants a stale D1 temp in the state directory and in
// legacy-mining.
func staleTemps(t *testing.T, dir string) []string {
	t.Helper()
	legacy := filepath.Join(dir, legacymining.LegacyDirName)
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	paths := []string{
		filepath.Join(dir, legacymining.TempPrefix+legacymining.WatermarkFile+"-1-0011223344556677"),
		filepath.Join(legacy, legacymining.TempPrefix+legacymining.TrippedFile+"-1-8899aabbccddeeff"),
	}
	for _, p := range paths {
		writeFile(t, p, []byte("partial"))
	}
	return paths
}

// treeHash hashes every path, mode and content below dir, except the state
// lock file: every taker creates it and writes its PID (and Windows refuses
// reads of a locked byte range). lockSize reports that file separately.
func treeHash(t *testing.T, dir string) string {
	t.Helper()
	var lines []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Name() == legacymining.StateLockFile {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		info, err := d.Info()
		if err != nil {
			return err
		}
		line := rel + " " + info.Mode().String()
		if !d.IsDir() {
			sum := sha256.Sum256(readFile(t, p))
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

// lockSize is the state lock file's size, or -1 when it does not exist.
func lockSize(dir string) int64 {
	fi, err := os.Lstat(filepath.Join(dir, legacymining.StateLockFile))
	if err != nil {
		return -1
	}
	return fi.Size()
}

type runResult struct {
	code           int
	stdout, stderr string
}

func (r runResult) String() string {
	return fmt.Sprintf("exit %d\nstdout: %s\nstderr: %s", r.code, r.stdout, r.stderr)
}

// syncCounter counts fsync(dir) calls per directory and can fail them.
type syncCounter struct {
	calls map[string]int
	fail  error
}

func (s *syncCounter) sync(dir string) error {
	if s.calls == nil {
		s.calls = map[string]int{}
	}
	s.calls[filepath.Clean(dir)]++
	if s.fail != nil {
		return s.fail
	}
	return legacymining.SyncDir(dir)
}

func runTool(t *testing.T, dir string, sc *syncCounter, args ...string) runResult {
	t.Helper()
	return runToolAt(t, dir, sc, testNow, args...)
}

func runToolAt(t *testing.T, dir string, sc *syncCounter, now time.Time, args ...string) runResult {
	t.Helper()
	var out, errb bytes.Buffer
	env := Env{
		StateDir: func() (string, error) { return dir, nil },
		Getenv:   func(string) string { return "" },
		Stdout:   &out, Stderr: &errb,
		Now: func() time.Time { return now },
	}
	if sc != nil {
		env.SyncDir = sc.sync
	}
	code := Run(args, env)
	return runResult{code: code, stdout: out.String(), stderr: errb.String()}
}

func lastResult(t *testing.T, r runResult) map[string]any {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(r.stdout), "\n")
	var m map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &m); err != nil {
		t.Fatalf("result is not JSON: %v\n%s", err, r)
	}
	return m
}

func holdLock(t *testing.T, dir string) *chain.StateLock {
	t.Helper()
	l, err := chain.AcquireStateLock(filepath.Join(dir, legacymining.StateLockFile))
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

// -----------------------------------------------------------------------------
// State lock: every mutating subcommand refuses with exit 3 and changes
// nothing; with the lock free each one succeeds; show is lock-free.
// -----------------------------------------------------------------------------

type mutatingCase struct {
	name  string
	args  []string
	setup func(t *testing.T, dir string)
}

func mutatingCases() []mutatingCase {
	blocks := testChain(5)
	return []mutatingCase{
		{"trim-fragment journal", []string{"trim-fragment", "--file", "journal"}, func(t *testing.T, dir string) {
			writeJournal(t, dir, blocks, `{"height":5,"prev`)
			writeW(t, dir, wmark(4, blocks[4].Hash))
		}},
		{"trim-fragment receipts", []string{"trim-fragment", "--file", "receipts"}, func(t *testing.T, dir string) {
			writeJournal(t, dir, blocks, "")
			writeFile(t, filepath.Join(dir, ReceiptsFile), append(receiptLine(t, "a", 4), []byte(`{"tx_id":"b","blo`)...))
		}},
		{"trim --above", []string{"trim", "--above", "3"}, func(t *testing.T, dir string) {
			writeJournal(t, dir, blocks, "")
			writeW(t, dir, wmark(3, blocks[3].Hash))
		}},
		{"watermark seed", []string{"watermark", "seed", "--served-tip", "4", "--follower-height", "2", "--follower-hash", blocks[2].Hash}, func(t *testing.T, dir string) {
			writeJournal(t, dir, blocks, "")
		}},
		{"watermark retire", []string{"watermark", "retire"}, func(t *testing.T, dir string) {
			writeJournal(t, dir, blocks, "")
			writeW(t, dir, wmark(4, blocks[4].Hash))
		}},
	}
}

func TestMutatingSubcommandsRefuseWhileLockHeld(t *testing.T) {
	for _, tc := range mutatingCases() {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			tc.setup(t, dir)
			stale := staleTemps(t, dir)
			lock := holdLock(t, dir)
			before, beforeLock := treeHash(t, dir), lockSize(dir)
			r := runTool(t, dir, nil, tc.args...)
			if r.code != legacymining.TailExitLockBusy {
				lock.Close()
				t.Fatalf("lock held: %s", r)
			}
			if !strings.Contains(r.stderr, "R-STOP") {
				t.Errorf("lock-busy message does not point to R-STOP:\n%s", r.stderr)
			}
			if treeHash(t, dir) != before || lockSize(dir) != beforeLock {
				t.Error("the state directory changed although the lock was busy")
			}
			for _, p := range stale {
				if !exists(p) {
					t.Errorf("stale temp %s removed without the lock", filepath.Base(p))
				}
			}
			if err := lock.Close(); err != nil {
				t.Fatal(err)
			}

			// With the lock free the same command succeeds and cleans up.
			r = runTool(t, dir, nil, tc.args...)
			if r.code != legacymining.TailExitOK {
				t.Fatalf("lock free: %s", r)
			}
			for _, p := range stale {
				if exists(p) {
					t.Errorf("stale temp %s survived a locked run", filepath.Base(p))
				}
			}
		})
	}
}

func TestWatermarkShowIsLockFreeAndReadOnly(t *testing.T) {
	dir := t.TempDir()
	blocks := testChain(3)
	writeJournal(t, dir, blocks, "")
	writeW(t, dir, wmark(2, blocks[2].Hash))
	writeFile(t, filepath.Join(dir, legacymining.WatermarkRetiredPrefix+"20260101T000000.000000000Z"), []byte("old"))
	stale := staleTemps(t, dir)
	lock := holdLock(t, dir)
	defer lock.Close()
	before := treeHash(t, dir)

	r := runTool(t, dir, nil, "watermark", "show")
	if r.code != legacymining.TailExitOK {
		t.Fatalf("show with the lock held: %s", r)
	}
	m := lastResult(t, r)
	if m["status"] != "present" {
		t.Fatalf("status = %v", m["status"])
	}
	w := m["watermark"].(map[string]any)
	if w["height"].(float64) != 2 || w["hash"] != blocks[2].Hash {
		t.Fatalf("watermark = %v", w)
	}
	if ret := m["retired"].([]any); len(ret) != 1 {
		t.Fatalf("retired = %v", ret)
	}
	if treeHash(t, dir) != before {
		t.Fatal("show changed the state directory")
	}
	for _, p := range stale {
		if !exists(p) {
			t.Fatal("show removed a stale temp: it must not clean up")
		}
	}

	// Absent and invalid W: exit 2, still read-only.
	if err := os.Remove(filepath.Join(dir, legacymining.WatermarkFile)); err != nil {
		t.Fatal(err)
	}
	if r := runTool(t, dir, nil, "watermark", "show"); r.code != legacymining.TailExitRefused || lastResult(t, r)["status"] != "absent" {
		t.Fatalf("absent W: %s", r)
	}
	writeFile(t, filepath.Join(dir, legacymining.WatermarkFile), []byte(`{"version":2}`))
	if r := runTool(t, dir, nil, "watermark", "show"); r.code != legacymining.TailExitRefused || lastResult(t, r)["status"] != "invalid" {
		t.Fatalf("invalid W: %s", r)
	}
}

// -----------------------------------------------------------------------------
// trim-fragment --file journal (R-C3, W-gated)
// -----------------------------------------------------------------------------

func TestTrimFragmentJournal(t *testing.T) {
	blocks := testChain(5) // complete lines 0..4
	fullLine5 := strings.TrimSuffix(string(blockLine(t, testChain(6)[5])), "\n")
	for _, tc := range []struct {
		name     string
		blocks   []*chain.Block
		fragment string
		w        *legacymining.Watermark
		rawW     string
		prefix   string // replaces the first line
		wantCode int
		wantMsg  string
	}{
		{name: "fragment above W=tip", blocks: blocks, fragment: `{"height":5,"prev_hash":"`, w: ptrW(wmark(4, blocks[4].Hash)), wantCode: 0},
		{name: "fragment above W<tip", blocks: blocks, fragment: `{"hei`, w: ptrW(wmark(2, blocks[2].Hash)), wantCode: 0},
		{name: "complete block N without newline", blocks: blocks, fragment: fullLine5, w: ptrW(wmark(4, blocks[4].Hash)), wantCode: 0},
		{name: "H_last below W", blocks: blocks[:4], fragment: `{"height":4`, w: ptrW(wmark(4, blocks[4].Hash)), wantCode: 2, wantMsg: "below W"},
		{name: "W absent", blocks: blocks, fragment: `{"x`, wantCode: 2, wantMsg: "no served watermark"},
		{name: "W invalid", blocks: blocks, fragment: `{"x`, rawW: `{"version":1,"height":4}`, wantCode: 2, wantMsg: "watermark invalid"},
		{name: "W hash not in journal", blocks: blocks, fragment: `{"x`, w: ptrW(wmark(3, blocks[2].Hash)), wantCode: 2, wantMsg: "R-W"},
		{name: "earlier line does not parse", blocks: blocks, fragment: `{"x`, w: ptrW(wmark(4, blocks[4].Hash)), prefix: "not json\n", wantCode: 2, wantMsg: "does not parse"},
		{name: "only a fragment", blocks: nil, fragment: `{"height":0`, w: ptrW(wmark(0, blocks[0].Hash)), wantCode: 2, wantMsg: "no complete block"},
		{name: "unterminated complete block at or below W", blocks: blocks, fragment: strings.TrimSuffix(string(blockLine(t, blocks[3])), "\n"), w: ptrW(wmark(4, blocks[4].Hash)), wantCode: 2, wantMsg: "<= W"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			var buf bytes.Buffer
			buf.WriteString(tc.prefix)
			for _, b := range tc.blocks {
				buf.Write(blockLine(t, b))
			}
			complete := buf.Len()
			buf.WriteString(tc.fragment)
			journal := filepath.Join(dir, JournalFile)
			writeFile(t, journal, buf.Bytes())
			switch {
			case tc.w != nil:
				writeW(t, dir, *tc.w)
			case tc.rawW != "":
				writeFile(t, filepath.Join(dir, legacymining.WatermarkFile), []byte(tc.rawW))
			}
			before := treeHash(t, dir)
			r := runTool(t, dir, nil, "trim-fragment", "--file", "journal")
			if r.code != tc.wantCode || !strings.Contains(r.stderr, tc.wantMsg) {
				t.Fatalf("want exit %d %q: %s", tc.wantCode, tc.wantMsg, r)
			}
			if tc.wantCode != 0 {
				if treeHash(t, dir) != before {
					t.Fatal("a refused trim changed the state directory")
				}
				return
			}
			if got := readFile(t, journal); !bytes.Equal(got, buf.Bytes()[:complete]) {
				t.Fatalf("journal after trim is %d bytes, want the %d complete bytes", len(got), complete)
			}
			m := lastResult(t, r)
			if got := readFile(t, m["archive"].(string)); string(got) != tc.fragment {
				t.Fatalf("archive = %q, want the fragment %q", got, tc.fragment)
			}
			if !strings.HasPrefix(filepath.Base(m["archive"].(string)), JournalFile+".torn-") {
				t.Fatalf("archive name %s", m["archive"])
			}
			// Rerun: nothing after the last newline; exit 0, no change.
			after := treeHash(t, dir)
			if r := runTool(t, dir, nil, "trim-fragment", "--file", "journal"); r.code != 0 || lastResult(t, r)["result"] != "no-fragment" {
				t.Fatalf("rerun: %s", r)
			}
			if treeHash(t, dir) != after {
				t.Fatal("a no-fragment rerun changed the state directory")
			}
		})
	}
}

func ptrW(w legacymining.Watermark) *legacymining.Watermark { return &w }

// An I/O failure (fsync of the archive directory) exits 1 before the
// truncation; the rerun completes the trim.
func TestTrimFragmentIOErrorThenRerun(t *testing.T) {
	dir := t.TempDir()
	blocks := testChain(3)
	full := writeJournal(t, dir, blocks, `{"height":3`)
	writeW(t, dir, wmark(2, blocks[2].Hash))
	sc := &syncCounter{fail: errors.New("injected EIO")}
	r := runTool(t, dir, sc, "trim-fragment", "--file", "journal")
	if r.code != legacymining.TailExitIO || !strings.Contains(r.stderr, "rerun") {
		t.Fatalf("injected fsync failure: %s", r)
	}
	if got := readFile(t, filepath.Join(dir, JournalFile)); !bytes.Equal(got, full) {
		t.Fatal("the journal was truncated although the archive was not durable")
	}
	if r := runToolAt(t, dir, nil, testNow.Add(time.Second), "trim-fragment", "--file", "journal"); r.code != 0 {
		t.Fatalf("rerun: %s", r)
	}
	if got := readFile(t, filepath.Join(dir, JournalFile)); !bytes.HasSuffix(got, []byte("\n")) || len(got) != len(full)-len(`{"height":3`) {
		t.Fatalf("rerun left %d bytes", len(got))
	}
}

// -----------------------------------------------------------------------------
// trim-fragment --file receipts (R-C3, gated on the journal)
// -----------------------------------------------------------------------------

func TestTrimFragmentReceipts(t *testing.T) {
	blocks := testChain(5) // tip 4
	for _, tc := range []struct {
		name            string
		journalFragment string
		receipts        []byte
		wantCode        int
		wantMsg         string
		wantKeep        int // bytes kept on success
	}{
		{name: "complete lines at the tip plus a fragment", receipts: cat(receiptLine(t, "a", 3), receiptLine(t, "b", 4), []byte(`{"tx_id":"c","block_hei`)), wantKeep: len(receiptLine(t, "a", 3)) + len(receiptLine(t, "b", 4))},
		{name: "fragment only", receipts: []byte(`{"tx_id":"c"`), wantKeep: 0},
		{name: "complete but unterminated last receipt", receipts: cat(receiptLine(t, "a", 3), bytes.TrimSuffix(receiptLine(t, "b", 4), []byte("\n"))), wantKeep: len(receiptLine(t, "a", 3))},
		{name: "torn journal", journalFragment: `{"height":5`, receipts: cat(receiptLine(t, "a", 3), []byte(`{"x`)), wantCode: 2, wantMsg: "journal has a torn last line"},
		{name: "receipt above the journal tip", receipts: cat(receiptLine(t, "a", 5), []byte(`{"x`)), wantCode: 2, wantMsg: "above the journal tip"},
		{name: "earlier receipt does not parse", receipts: cat([]byte("garbage\n"), []byte(`{"x`)), wantCode: 2, wantMsg: "does not parse"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir() // no W: receipts are not gated on W
			writeJournal(t, dir, blocks, tc.journalFragment)
			path := filepath.Join(dir, ReceiptsFile)
			writeFile(t, path, tc.receipts)
			before := treeHash(t, dir)
			r := runTool(t, dir, nil, "trim-fragment", "--file", "receipts")
			if r.code != tc.wantCode || !strings.Contains(r.stderr, tc.wantMsg) {
				t.Fatalf("want exit %d %q: %s", tc.wantCode, tc.wantMsg, r)
			}
			if tc.wantCode != 0 {
				if treeHash(t, dir) != before {
					t.Fatal("a refused trim changed the state directory")
				}
				return
			}
			if got := readFile(t, path); !bytes.Equal(got, tc.receipts[:tc.wantKeep]) {
				t.Fatalf("receipts after trim = %q", got)
			}
			m := lastResult(t, r)
			if got := readFile(t, m["archive"].(string)); !bytes.Equal(got, tc.receipts[tc.wantKeep:]) {
				t.Fatalf("archive = %q", got)
			}
		})
	}
	t.Run("absent receipts file", func(t *testing.T) {
		dir := t.TempDir()
		writeJournal(t, dir, blocks, "")
		if r := runTool(t, dir, nil, "trim-fragment", "--file", "receipts"); r.code != 0 || lastResult(t, r)["result"] != "absent" {
			t.Fatalf("%s", r)
		}
	})
}

func cat(parts ...[]byte) []byte { return bytes.Join(parts, nil) }

// -----------------------------------------------------------------------------
// trim --above (R-C4 fallback): inode check, W gate
// -----------------------------------------------------------------------------

// genFixture is a journal 0..J with W = J-1 and snapshot files. c5 makes
// the main accounts file a newer inode than its .h<J-1> link.
func genFixture(t *testing.T, j int, links, c5 bool) (dir string, blocks []*chain.Block, full []byte) {
	t.Helper()
	dir = t.TempDir()
	blocks = testChain(j + 1)
	full = writeJournal(t, dir, blocks, "")
	writeW(t, dir, wmark(uint64(j-1), blocks[j-1].Hash))
	acc := filepath.Join(dir, AccountsFile)
	enr := filepath.Join(dir, EnrollmentFile)
	writeFile(t, acc, []byte(`[{"height":"J-1"}]`))
	writeFile(t, enr, []byte(`{"records":[]}`))
	if links {
		for _, p := range []string{acc, enr} {
			if err := os.Link(p, fmt.Sprintf("%s.h%d", p, j-1)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if c5 {
		// H3 of J: temp + rename gives the main file a new inode.
		tmp := acc + ".pending"
		writeFile(t, tmp, []byte(`[{"height":"J"}]`))
		if err := os.Rename(tmp, acc); err != nil {
			t.Fatal(err)
		}
	}
	return dir, blocks, full
}

func TestTrimAboveInodeCheck(t *testing.T) {
	const j = 6
	t.Run("C4 shape: same inodes, allowed", func(t *testing.T) {
		dir, blocks, full := genFixture(t, j, true, false)
		r := runTool(t, dir, nil, "trim", "--above", fmt.Sprint(j-1))
		if r.code != 0 {
			t.Fatalf("%s", r)
		}
		keep := len(full) - len(blockLine(t, blocks[j]))
		if got := readFile(t, filepath.Join(dir, JournalFile)); !bytes.Equal(got, full[:keep]) {
			t.Fatal("journal is not the J-1 prefix")
		}
		m := lastResult(t, r)
		if got := readFile(t, m["archive"].(string)); !bytes.Equal(got, blockLine(t, blocks[j])) {
			t.Fatal("archive is not line J")
		}
		gen := m["generation_check"].(map[string]any)
		if gen[AccountsFile] != "same-inode" || gen[EnrollmentFile] != "same-inode" {
			t.Fatalf("generation check = %v", gen)
		}
	})
	t.Run("C5 shape: newer accounts inode, refused", func(t *testing.T) {
		dir, _, full := genFixture(t, j, true, true)
		before := treeHash(t, dir)
		r := runTool(t, dir, nil, "trim", "--above", fmt.Sprint(j-1))
		if r.code != legacymining.TailExitRefused || !strings.Contains(r.stderr, "C5 shape") {
			t.Fatalf("%s", r)
		}
		if treeHash(t, dir) != before || !bytes.Equal(readFile(t, filepath.Join(dir, JournalFile)), full) {
			t.Fatal("a refused trim changed the state directory")
		}
	})
	t.Run("no links: allowed, root check is the backstop", func(t *testing.T) {
		dir, _, _ := genFixture(t, j, false, false)
		r := runTool(t, dir, nil, "trim", "--above", fmt.Sprint(j-1))
		if r.code != 0 || !strings.Contains(r.stderr, "backstop") {
			t.Fatalf("%s", r)
		}
	})
	t.Run("link present, main absent: refused", func(t *testing.T) {
		dir, _, _ := genFixture(t, j, true, false)
		if err := os.Remove(filepath.Join(dir, EnrollmentFile)); err != nil {
			t.Fatal(err)
		}
		if r := runTool(t, dir, nil, "trim", "--above", fmt.Sprint(j-1)); r.code != legacymining.TailExitRefused {
			t.Fatalf("%s", r)
		}
	})
}

func TestTrimAboveRefusals(t *testing.T) {
	const j = 5
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, dir string, blocks []*chain.Block)
		above uint64
		msg   string
	}{
		{"W absent", func(t *testing.T, dir string, _ []*chain.Block) {
			os.Remove(filepath.Join(dir, legacymining.WatermarkFile))
		}, j - 1, "no served watermark"},
		{"above below W", func(t *testing.T, dir string, b []*chain.Block) { writeW(t, dir, wmark(j, b[j].Hash)) }, j - 1, "below the watermark"},
		{"tip more than W+1", func(t *testing.T, dir string, b []*chain.Block) { writeW(t, dir, wmark(j-2, b[j-2].Hash)) }, j - 1, "S5 rule 5"},
		{"torn journal", func(t *testing.T, dir string, b []*chain.Block) { writeJournal(t, dir, b, `{"height":6`) }, j - 1, "R-C3"},
		{"W hash mismatch", func(t *testing.T, dir string, b []*chain.Block) { writeW(t, dir, wmark(j-1, b[j-2].Hash)) }, j - 1, "R-W"},
		{"above the tip", func(t *testing.T, dir string, b []*chain.Block) {}, j + 3, "nothing to trim"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, blocks, _ := genFixture(t, j, false, false)
			tc.setup(t, dir, blocks)
			before := treeHash(t, dir)
			r := runTool(t, dir, nil, "trim", "--above", fmt.Sprint(tc.above))
			if r.code != legacymining.TailExitRefused || !strings.Contains(r.stderr, tc.msg) {
				t.Fatalf("want refusal %q: %s", tc.msg, r)
			}
			if treeHash(t, dir) != before {
				t.Fatal("a refused trim changed the state directory")
			}
		})
	}
	t.Run("nothing above: no-op", func(t *testing.T) {
		dir, _, full := genFixture(t, j, false, false)
		r := runTool(t, dir, nil, "trim", "--above", fmt.Sprint(j))
		if r.code != 0 || lastResult(t, r)["result"] != "nothing-above" || !bytes.Equal(readFile(t, filepath.Join(dir, JournalFile)), full) {
			t.Fatalf("%s", r)
		}
	})
}

// No subcommand removes a complete journal line at or below W, for every W
// and every --above.
func TestNoJournalRemovalAtOrBelowW(t *testing.T) {
	const j = 4
	blocks := testChain(j + 1)
	for wh := 0; wh <= j; wh++ {
		for above := 0; above <= j+1; above++ {
			dir := t.TempDir()
			writeJournal(t, dir, blocks, "")
			writeW(t, dir, wmark(uint64(wh), blocks[wh].Hash))
			r := runTool(t, dir, nil, "trim", "--above", fmt.Sprint(above))
			got := readFile(t, filepath.Join(dir, JournalFile))
			var want bytes.Buffer
			for _, b := range blocks[:wh+1] {
				want.Write(blockLine(t, b))
			}
			if !bytes.HasPrefix(got, want.Bytes()) {
				t.Fatalf("W=%d --above %d removed a height <= W: %s", wh, above, r)
			}
			removedAny := len(got) < len(writeJournalBytes(t, blocks))
			if removedAny && !(wh == j-1 && above == j-1) {
				t.Fatalf("W=%d --above %d removed lines; only W=J-1, --above J-1 may: %s", wh, above, r)
			}
		}
	}
	// trim-fragment never removes a complete line either.
	for wh := 0; wh <= j; wh++ {
		dir := t.TempDir()
		full := writeJournal(t, dir, blocks, `{"height":5`)
		writeW(t, dir, wmark(uint64(wh), blocks[wh].Hash))
		runTool(t, dir, nil, "trim-fragment", "--file", "journal")
		if got := readFile(t, filepath.Join(dir, JournalFile)); !bytes.HasPrefix(full, got) || len(got) < len(full)-len(`{"height":5`) {
			t.Fatalf("W=%d: trim-fragment removed a complete line", wh)
		}
	}
}

func writeJournalBytes(t *testing.T, blocks []*chain.Block) []byte {
	var buf bytes.Buffer
	for _, b := range blocks {
		buf.Write(blockLine(t, b))
	}
	return buf.Bytes()
}

// -----------------------------------------------------------------------------
// watermark seed and retire
// -----------------------------------------------------------------------------

func TestWatermarkSeedMatrix(t *testing.T) {
	blocks := testChain(8) // J = 7
	seed := func(s, f uint64, x string) []string {
		return []string{"watermark", "seed", "--served-tip", fmt.Sprint(s), "--follower-height", fmt.Sprint(f), "--follower-hash", x}
	}
	for _, tc := range []struct {
		name     string
		setup    func(t *testing.T, dir string)
		args     []string
		wantCode int
		msg      string
	}{
		{"success J > S", nil, seed(6, 5, blocks[5].Hash), 0, ""},
		{"success J = S = F, uppercase X", nil, seed(7, 7, strings.ToUpper(blocks[7].Hash)), 0, ""},
		{"W exists", func(t *testing.T, dir string) { writeW(t, dir, wmark(7, blocks[7].Hash)) }, seed(7, 7, blocks[7].Hash), 2, "already exists"},
		{"torn journal", func(t *testing.T, dir string) { writeJournal(t, dir, blocks, `{"height":8`) }, seed(7, 7, blocks[7].Hash), 2, "torn last line"},
		{"J < S", nil, seed(8, 7, blocks[7].Hash), 2, "never journaled"},
		{"F > J", nil, seed(7, 8, blocks[7].Hash), 2, "above the journal tip"},
		{"hash mismatch at F", nil, seed(7, 6, blocks[5].Hash), 2, "the follower has"},
		{"empty journal", func(t *testing.T, dir string) { writeFile(t, filepath.Join(dir, JournalFile), nil) }, seed(0, 0, blocks[0].Hash), 2, "holds no block"},
		{"no journal", func(t *testing.T, dir string) { os.Remove(filepath.Join(dir, JournalFile)) }, seed(0, 0, blocks[0].Hash), 2, "does not exist"},
		{"two blocks at F", func(t *testing.T, dir string) {
			writeJournal(t, dir, append(append([]*chain.Block{}, blocks...), blocks[3]), "")
		}, seed(3, 3, blocks[3].Hash), 2, "2 blocks"},
		{"bad follower hash", nil, seed(7, 7, "xyz"), 2, "64 hexadecimal"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeJournal(t, dir, blocks, "")
			// A retired W does not count as an existing W.
			writeFile(t, filepath.Join(dir, legacymining.WatermarkRetiredPrefix+"x"), []byte("old"))
			if tc.setup != nil {
				tc.setup(t, dir)
			}
			before := treeHash(t, dir)
			r := runTool(t, dir, nil, tc.args...)
			if r.code != tc.wantCode || !strings.Contains(r.stderr, tc.msg) {
				t.Fatalf("want exit %d %q: %s", tc.wantCode, tc.msg, r)
			}
			if tc.wantCode != 0 {
				if treeHash(t, dir) != before {
					t.Fatal("a refused seed changed the state directory")
				}
				return
			}
			w, err := ReadWatermark(dir)
			if err != nil {
				t.Fatal(err)
			}
			s, f := *mustU(t, tc.args[3]), *mustU(t, tc.args[5])
			if w.Height != 7 || w.Hash != blocks[7].Hash || w.Source != legacymining.WatermarkSourceSeed ||
				w.WrittenNS != testNow.UnixNano() || w.ServedTip == nil || *w.ServedTip != s ||
				w.FollowerHeight == nil || *w.FollowerHeight != f || w.FollowerHash != blocks[f].Hash {
				t.Fatalf("seeded W = %+v", w)
			}
		})
	}
}

func mustU(t *testing.T, s string) *uint64 {
	t.Helper()
	v, err := parseHeight(s)
	if err != nil {
		t.Fatal(err)
	}
	return &v
}

func TestWatermarkRetire(t *testing.T) {
	blocks := testChain(2)
	t.Run("renames and fsyncs", func(t *testing.T) {
		dir := t.TempDir()
		data := writeW(t, dir, wmark(1, blocks[1].Hash))
		sc := &syncCounter{}
		r := runTool(t, dir, sc, "watermark", "retire")
		if r.code != 0 {
			t.Fatalf("%s", r)
		}
		if exists(filepath.Join(dir, legacymining.WatermarkFile)) {
			t.Fatal("W still present")
		}
		retired := filepath.Join(dir, legacymining.WatermarkRetiredPrefix+"20260926T120000.123456789Z")
		if got := readFile(t, retired); !bytes.Equal(got, data) {
			t.Fatalf("retired bytes = %q", got)
		}
		abs, _ := filepath.Abs(dir)
		if sc.calls[filepath.Clean(abs)] < 1 {
			t.Fatalf("no fsync of the state directory after the rename: %v", sc.calls)
		}
		// Rerun: already retired.
		if r := runTool(t, dir, nil, "watermark", "retire"); r.code != 0 || lastResult(t, r)["result"] != "absent" {
			t.Fatalf("rerun: %s", r)
		}
	})
	t.Run("fsync failure exits 1; the rerun completes", func(t *testing.T) {
		dir := t.TempDir()
		writeW(t, dir, wmark(1, blocks[1].Hash))
		sc := &syncCounter{fail: errors.New("injected EIO")}
		if r := runTool(t, dir, sc, "watermark", "retire"); r.code != legacymining.TailExitIO {
			t.Fatalf("%s", r)
		}
		if r := runTool(t, dir, nil, "watermark", "retire"); r.code != 0 {
			t.Fatalf("rerun: %s", r)
		}
	})
	t.Run("invalid W is retired with its bytes", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, legacymining.WatermarkFile), []byte("garbage"))
		r := runTool(t, dir, nil, "watermark", "retire")
		if r.code != 0 || lastResult(t, r)["watermark_error"] == nil {
			t.Fatalf("%s", r)
		}
	})
	t.Run("W that is not a regular file is refused", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, legacymining.WatermarkFile), 0o700); err != nil {
			t.Fatal(err)
		}
		if r := runTool(t, dir, nil, "watermark", "retire"); r.code != legacymining.TailExitRefused {
			t.Fatalf("%s", r)
		}
	})
}

// A seeded W round-trips through the strict parser; unknown fields, a bad
// version, hash or source are refused.
func TestParseWatermark(t *testing.T) {
	h := strings.Repeat("ab", 32)
	s, f := uint64(5), uint64(4)
	good := legacymining.Watermark{Version: 1, Height: 5, Hash: h, Source: "seed", WrittenNS: 9, ServedTip: &s, FollowerHeight: &f, FollowerHash: h}
	data, _ := json.Marshal(good)
	if w, err := ParseWatermark(append(data, '\n')); err != nil || *w.ServedTip != 5 {
		t.Fatalf("good W: %+v %v", w, err)
	}
	for _, bad := range []string{
		`{"version":1,"height":5,"hash":"` + h + `","source":"seal","written_ns":1,"x":1}`,
		`{"version":2,"height":5,"hash":"` + h + `","source":"seal","written_ns":1}`,
		`{"version":1,"height":5,"hash":"` + strings.ToUpper(h) + `","source":"seal","written_ns":1}`,
		`{"version":1,"height":5,"hash":"` + h + `","source":"guess","written_ns":1}`,
		`{"version":1,"height":5,"hash":"` + h + `","source":"seal","written_ns":1} {}`,
		`not json`,
	} {
		if _, err := ParseWatermark([]byte(bad)); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}

// -----------------------------------------------------------------------------
// Receipts pre-trim (R-C4 step 0)
// -----------------------------------------------------------------------------

func TestPreTrimReceipts(t *testing.T) {
	const j = 5
	l3, l4a, l4b, l5a, l5b := receiptLine(t, "a", 3), receiptLine(t, "b", 4), receiptLine(t, "c", 4), receiptLine(t, "d", 5), receiptLine(t, "e", 5)
	for _, tc := range []struct {
		name    string
		data    []byte
		keep    int
		archive bool
		err     string
	}{
		{name: "J lines and a fragment", data: cat(l3, l4a, l4b, l5a, []byte(`{"tx_id":"e","blo`)), keep: len(l3) + len(l4a) + len(l4b), archive: true},
		{name: "complete J lines", data: cat(l3, l4a, l5a, l5b), keep: len(l3) + len(l4a), archive: true},
		{name: "fragment only above J-1", data: cat(l3, l4a, []byte(`{"tx`)), keep: len(l3) + len(l4a), archive: true},
		{name: "clean J-1 file", data: cat(l3, l4a), keep: len(l3) + len(l4a)},
		{name: "empty file", data: nil, keep: 0},
		{name: "unparseable line before the cut", data: cat(l3, []byte("junk\n"), l5a), err: "does not parse"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, ReceiptsFile)
			writeFile(t, path, tc.data)
			res, err := PreTrimReceipts(path, j, testNow)
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("err = %v, want %q", err, tc.err)
				}
				if !bytes.Equal(readFile(t, path), tc.data) {
					t.Fatal("a refused pre-trim changed the file")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := readFile(t, path); !bytes.Equal(got, tc.data[:tc.keep]) {
				t.Fatalf("kept %q", got)
			}
			if (res.Archive != "") != tc.archive {
				t.Fatalf("archive = %q", res.Archive)
			}
			if tc.archive {
				if got := readFile(t, res.Archive); !bytes.Equal(got, tc.data[tc.keep:]) {
					t.Fatalf("archive = %q", got)
				}
				if !strings.HasPrefix(filepath.Base(res.Archive), ReceiptsFile+".replay-") {
					t.Fatalf("archive name %s", res.Archive)
				}
			}
		})
	}
	if res, err := PreTrimReceipts(filepath.Join(t.TempDir(), ReceiptsFile), j, testNow); err != nil || res.Archive != "" {
		t.Fatalf("absent file: %+v %v", res, err)
	}
}

// -----------------------------------------------------------------------------
// Usage and state directory
// -----------------------------------------------------------------------------

func TestUsageRefusals(t *testing.T) {
	dir := t.TempDir()
	writeJournal(t, dir, testChain(2), "")
	for _, args := range [][]string{
		nil,
		{"frobnicate"},
		{"trim-fragment"},
		{"trim-fragment", "--file", "accounts"},
		{"trim-fragment", "--file", "journal", "extra"},
		{"trim"},
		{"trim", "--above", "-1"},
		{"trim", "--above", "+3"},
		{"trim", "--above", "x"},
		{"watermark"},
		{"watermark", "burn"},
		{"watermark", "show", "x"},
		{"watermark", "retire", "--force"},
		{"watermark", "seed", "--served-tip", "1"},
	} {
		before := treeHash(t, dir)
		if r := runTool(t, dir, nil, args...); r.code != legacymining.TailExitRefused {
			t.Errorf("%q: %s", args, r)
		}
		if treeHash(t, dir) != before || exists(filepath.Join(dir, legacymining.StateLockFile)) {
			t.Errorf("%q: a usage error touched the state directory", args)
		}
	}
	if r := runTool(t, dir, nil, "--version"); r.code != 0 || !strings.HasPrefix(r.stdout, "hl1-tail ") {
		t.Fatalf("--version: %s", r)
	}
}

func TestStateDirectoryIsNeverCreated(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent")
	var out, errb bytes.Buffer
	code := Run([]string{"watermark", "retire"}, Env{StateDir: func() (string, error) { return missing, nil }, Stdout: &out, Stderr: &errb})
	if code != legacymining.TailExitRefused || exists(missing) {
		t.Fatalf("exit %d, created=%v: %s", code, exists(missing), errb.String())
	}
	code = Run([]string{"watermark", "retire"}, Env{StateDir: func() (string, error) { return "", errors.New("config") }, Stdout: &out, Stderr: &errb})
	if code != legacymining.TailExitIO {
		t.Fatalf("config failure exit %d", code)
	}
}

// The second HL1 directory named by the canary environment is cleaned too.
func TestCleanupCoversCanaryLegacyDir(t *testing.T) {
	dir := t.TempDir()
	blocks := testChain(2)
	writeJournal(t, dir, blocks, "")
	writeW(t, dir, wmark(1, blocks[1].Hash))
	other := filepath.Join(t.TempDir(), legacymining.LegacyDirName)
	if err := os.Mkdir(other, 0o700); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(other, legacymining.TempPrefix+"x-1-00")
	writeFile(t, stale, []byte("p"))
	env := map[string]string{
		legacymining.EnvMode:               "canary",
		legacymining.EnvDB:                 filepath.Join(other, legacymining.DBFile),
		legacymining.EnvCanaryConfig:       filepath.Join(other, "c.json"),
		legacymining.EnvCanaryConfigSHA256: strings.Repeat("0", 64),
	}
	var out, errb bytes.Buffer
	code := Run([]string{"watermark", "retire"}, Env{
		StateDir: func() (string, error) { return dir, nil }, Getenv: func(k string) string { return env[k] },
		Stdout: &out, Stderr: &errb, Now: func() time.Time { return testNow },
	})
	if code != 0 || exists(stale) {
		t.Fatalf("exit %d, stale survived=%v: %s", code, exists(stale), errb.String())
	}
}
