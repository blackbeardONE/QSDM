package main

// Errata E7 tests: the boot step S4r (hl1RepairTipReceipts) completes the
// receipts of a tip block that S5 rule 6 is about to cover with W, on real
// state directories left by a crash after H3 (C5), a failed H5 (C6a/F1 after
// R-FS) or an H5 that wrote a prefix of the lines, and refuses with exit 78
// (W unchanged) when the N-1 generation or the durable N state does not
// support the replay.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/internal/legacymining"
	"github.com/blackbeardONE/QSDM/internal/logging"
	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mining/enrollment"
)

// hl1rTraceFS records the S4r/S5 file-system operations and can fail the
// receipts fsync or the directory fsync.
type hl1rTraceFS struct {
	hl1OSFS
	ops                       []string
	failSyncFile, failSyncDir bool
}

func (f *hl1rTraceFS) SyncFile(name string) error {
	f.ops = append(f.ops, "SyncFile "+filepath.Base(name))
	if f.failSyncFile {
		return errHL1rFault
	}
	return f.hl1OSFS.SyncFile(name)
}

func (f *hl1rTraceFS) SyncDir(dir string) error {
	f.ops = append(f.ops, "SyncDir")
	if f.failSyncDir {
		return errHL1rFault
	}
	return f.hl1OSFS.SyncDir(dir)
}

func (f *hl1rTraceFS) WriteFileDurable(dir, name string, data []byte) error {
	f.ops = append(f.ops, "WriteFileDurable "+name)
	return f.hl1OSFS.WriteFileDurable(dir, name, data)
}

func (f *hl1rTraceFS) index(op string) int {
	for i, o := range f.ops {
		if o == op {
			return i
		}
	}
	return -1
}

// hl1rReceiptsAt parses the complete receipts lines at height h, in file
// order.
func hl1rReceiptsAt(t *testing.T, path string, h uint64) []chain.TxReceipt {
	t.Helper()
	var out []chain.TxReceipt
	lines := bytes.Split(hl1rRead(t, path), []byte("\n"))
	for _, l := range lines[:len(lines)-1] {
		if len(l) == 0 {
			continue
		}
		var r chain.TxReceipt
		if err := json.Unmarshal(l, &r); err != nil {
			t.Fatalf("receipts line %q: %v", l, err)
		}
		if r.BlockHeight == h {
			out = append(out, r)
		}
	}
	return out
}

// hl1rNoTS is a receipt's NDJSON line with the timestamp zeroed.
func hl1rNoTS(t *testing.T, r *chain.TxReceipt) string {
	t.Helper()
	cp := *r
	cp.Timestamp = time.Time{}
	data, err := json.Marshal(&cp)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// hl1rReference is a reference follower: copies of the .h<n-1> pair, the
// journal up to n-1 restored as replay does, then TryAppendExternalBlock(n).
// It returns n's receipts by tx ID.
func hl1rReference(t *testing.T, f *hl1rFixture, n uint64) map[string]*chain.TxReceipt {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{hl1AccountsName, hl1EnrollmentName} {
		hl1WriteFile(t, filepath.Join(dir, name), string(hl1rRead(t, hl1GenerationLink(f.path(name), n-1))))
	}
	blocks, err := chain.LoadChainNDJSON(f.path(hl1JournalName))
	if err != nil {
		t.Fatal(err)
	}
	if last := blocks[len(blocks)-1]; last.Height != n {
		t.Fatalf("journal tip %d, want %d", last.Height, n)
	}
	st, err := hl1NewReplayStack(dir, nil, nil, f.policy, logging.NewSilentLogger())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.producer.ValidateProducerTransitionChain(blocks); err != nil {
		t.Fatal(err)
	}
	prefix := append([]*chain.Block(nil), blocks[:len(blocks)-1]...)
	if err := st.restore(prefix, filepath.Join(dir, hl1AccountsName), filepath.Join(dir, hl1EnrollmentName), filepath.Join(dir, hl1ReceiptsName)); err != nil {
		t.Fatalf("reference follower restore: %v", err)
	}
	if err := st.producer.TryAppendExternalBlock(blocks[len(blocks)-1]); err != nil {
		t.Fatalf("reference follower append: %v", err)
	}
	out := map[string]*chain.TxReceipt{}
	for _, r := range st.receipts.GetByBlock(n) {
		out[r.TxID] = r
	}
	return out
}

// hl1rKept records the bytes and inodes of the journal, the main snapshots
// and the .h<n-1> generation, which S4r must leave alone.
type hl1rKept struct {
	paths []string
	data  map[string][]byte
	info  map[string]os.FileInfo
}

func hl1rKeep(t *testing.T, f *hl1rFixture, n uint64) *hl1rKept {
	t.Helper()
	k := &hl1rKept{data: map[string][]byte{}, info: map[string]os.FileInfo{}}
	for _, name := range []string{hl1JournalName, hl1AccountsName, hl1EnrollmentName} {
		k.paths = append(k.paths, f.path(name))
	}
	k.paths = append(k.paths, hl1GenerationLink(f.path(hl1AccountsName), n-1), hl1GenerationLink(f.path(hl1EnrollmentName), n-1))
	for _, p := range k.paths {
		k.data[p], k.info[p] = hl1rRead(t, p), hl1rStat(t, p)
	}
	return k
}

func (k *hl1rKept) check(t *testing.T) {
	t.Helper()
	for _, p := range k.paths {
		if !bytes.Equal(hl1rRead(t, p), k.data[p]) || !os.SameFile(hl1rStat(t, p), k.info[p]) {
			t.Fatalf("S4r changed %s", filepath.Base(p))
		}
	}
}

// -----------------------------------------------------------------------------
// C5, C6a/F1 and a partial H5: the boot completes N's receipts, then W := N
// -----------------------------------------------------------------------------

func TestHL1S4rCompletesTipReceipts(t *testing.T) {
	if testing.Short() {
		t.Skip("state-directory recovery test")
	}
	const n = 5
	shapes := map[string]string{"H4": "C5 (killed after H3)", "H5": "C6a/F1 (H5 wrote nothing)", "H5p": "H5 wrote one line, then EIO"}
	for _, transition := range []bool{false, true} {
		for _, fault := range []string{"H4", "H5", "H5p"} {
			t.Run(fmt.Sprintf("%s transition=%v", shapes[fault], transition), func(t *testing.T) {
				f := hl1rCrash(t, transition, n, fault)
				if w := hl1rW(t, f.dir); w.Height != n-1 || w.Source != legacymining.WatermarkSourceSeal {
					t.Fatalf("crash state W = %+v, want N-1 (seal)", w)
				}
				receipts := f.path(hl1ReceiptsName)
				before := hl1rRead(t, receipts)
				wantPresent := 0
				if fault == "H5p" {
					wantPresent = 1
				}
				if got := len(hl1rReceiptsAt(t, receipts, n)); got != wantPresent {
					t.Fatalf("crash state has %d receipts lines at N, want %d", got, wantPresent)
				}
				txs := len(f.blkJ.Transactions)
				kept := hl1rKeep(t, f, n)
				ref := hl1rReference(t, f, n)
				core := map[string]*chain.TxReceipt{}
				for _, r := range f.core.st.receipts.GetByBlock(n) {
					core[r.TxID] = r
				}
				if len(ref) != txs || len(core) != txs {
					t.Fatalf("reference %d, crashed core %d receipts; block has %d txs", len(ref), len(core), txs)
				}
				replays := hl1S4rScratchReplays.Load()

				fsys := &hl1rTraceFS{}
				st, s4r, s5, err := hl1rBootWith(t, f.dir, f.policy, fsys, nil)
				if err != nil {
					t.Fatalf("boot: %v", err)
				}
				if !s4r.Gate || s4r.Height != n || len(s4r.Regenerated) != txs-wantPresent || s4r.Written != txs-wantPresent {
					t.Fatalf("S4r = %+v", s4r)
				}
				if got := hl1S4rScratchReplays.Load() - replays; got != 1 {
					t.Fatalf("%d scratch replays, want 1", got)
				}
				if !s5.Wrote || s5.DurableTip != n {
					t.Fatalf("S5 = %+v", s5)
				}
				if w := hl1rW(t, f.dir); w.Height != n || w.Hash != f.blkJ.Hash || w.Source != legacymining.WatermarkSourceBoot {
					t.Fatalf("W = %+v, want N (boot)", w)
				}

				// Only appended: the existing bytes are kept, and every tx of N
				// has exactly one line, equal (timestamps aside) to the
				// reference follower's and to the crashed core's receipt.
				after := hl1rRead(t, receipts)
				if !bytes.HasPrefix(after, before) || after[len(after)-1] != '\n' {
					t.Fatal("S4r did not append after the existing receipts bytes")
				}
				lines := hl1rReceiptsAt(t, receipts, n)
				if len(lines) != txs {
					t.Fatalf("%d receipts lines at N, want %d", len(lines), txs)
				}
				seen := map[string]bool{}
				for i := range lines {
					r := &lines[i]
					if seen[r.TxID] {
						t.Fatalf("tx %s has two receipts lines at N", r.TxID)
					}
					seen[r.TxID] = true
					g, c := ref[r.TxID], core[r.TxID]
					if g == nil || c == nil {
						t.Fatalf("receipt for tx %s, which neither the reference nor the core has", r.TxID)
					}
					if got := hl1rNoTS(t, r); got != hl1rNoTS(t, g) || got != hl1rNoTS(t, c) {
						t.Fatalf("receipt of %s:\n  disk      %s\n  reference %s\n  core      %s", r.TxID, got, hl1rNoTS(t, g), hl1rNoTS(t, c))
					}
					if i >= wantPresent && !r.Timestamp.Equal(f.blkJ.Timestamp) {
						t.Fatalf("regenerated receipt of %s has timestamp %v, want the block's %v", r.TxID, r.Timestamp, f.blkJ.Timestamp)
					}
				}
				if got := len(st.receipts.GetByBlock(n)); got != txs {
					t.Fatalf("the in-memory store has %d receipts at N, want %d", got, txs)
				}
				kept.check(t)

				// The receipts file and the directory are fsynced before W.
				iF, iD, iW := fsys.index("SyncFile "+hl1ReceiptsName), fsys.index("SyncDir"), fsys.index("WriteFileDurable "+legacymining.WatermarkFile)
				if iF < 0 || iD < iF || iW < iD {
					t.Fatalf("file-system order %q: want the receipts fsync, then the directory fsync, then W", fsys.ops)
				}

				// The next boot has tip = W; the chain continues normally.
				replays = hl1S4rScratchReplays.Load()
				next := hl1rStartCore(t, f.dir, f.policy, f.signer).seal()
				if next.Height != n+1 || hl1S4rScratchReplays.Load() != replays {
					t.Fatalf("after S4r the next seal is %d", next.Height)
				}
				heights, terminated := hl1rReceiptHeights(t, receipts)
				if !terminated || heights[n] != txs || heights[n+1] != len(next.Transactions) {
					t.Fatalf("receipts per height = %v", heights)
				}
			})
		}
	}
}

// Complete receipts are never touched: H5 ran before a failed H6 (tip = W+1),
// and a boot at tip = W.
func TestHL1S4rLeavesCompleteReceiptsAlone(t *testing.T) {
	if testing.Short() {
		t.Skip("state-directory recovery test")
	}
	const n = 4
	f := hl1rCrash(t, false, n, "H6")
	receipts := f.path(hl1ReceiptsName)
	before := hl1rRead(t, receipts)
	replays := hl1S4rScratchReplays.Load()
	_, s4r, s5, err := hl1rBootWith(t, f.dir, nil, hl1OSFS{}, nil)
	if err != nil || !s4r.Gate || len(s4r.Regenerated) != 0 || !s5.Wrote || s5.DurableTip != n {
		t.Fatalf("boot with complete receipts at N: %+v %+v %v", s4r, s5, err)
	}
	if !bytes.Equal(hl1rRead(t, receipts), before) || hl1S4rScratchReplays.Load() != replays {
		t.Fatal("S4r changed complete receipts or ran a scratch replay")
	}
	_, s4r, s5, err = hl1rBootWith(t, f.dir, nil, hl1OSFS{}, nil)
	if err != nil || s4r.Gate || s5.Wrote || s5.DurableTip != n {
		t.Fatalf("boot at tip = W: %+v %+v %v", s4r, s5, err)
	}
	if !bytes.Equal(hl1rRead(t, receipts), before) || hl1S4rScratchReplays.Load() != replays {
		t.Fatal("S4r ran at tip = W")
	}
}

// A local failed-drop receipt at N (status 0, index past the block) is
// allowed and kept; the missing receipts are still regenerated.
func TestHL1S4rKeepsLocalFailedDrop(t *testing.T) {
	if testing.Short() {
		t.Skip("state-directory recovery test")
	}
	const n = 4
	f := hl1rCrash(t, false, n, "H5")
	drop := chain.TxReceipt{TxID: "hl1r-dropped", BlockHeight: n, BlockHash: f.blkJ.Hash, Status: chain.ReceiptFailed,
		Error: "nonce mismatch", IndexInBlock: len(f.blkJ.Transactions), Timestamp: hl1rNow}
	hl1rAppendLine(t, f.path(hl1ReceiptsName), &drop)
	if _, _, err := hl1rBoot(t, f.dir, nil); err != nil {
		t.Fatalf("boot: %v", err)
	}
	lines := hl1rReceiptsAt(t, f.path(hl1ReceiptsName), n)
	if len(lines) != len(f.blkJ.Transactions)+1 || lines[0].TxID != drop.TxID {
		t.Fatalf("receipts at N = %+v", lines)
	}
}

func hl1rAppendLine(t *testing.T, path string, r *chain.TxReceipt) {
	t.Helper()
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	fh, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer fh.Close()
	if _, err := fh.Write(append(data, '\n')); err != nil {
		t.Fatal(err)
	}
}

// hl1rRewriteReceipts rewrites the receipts file keeping only the lines keep
// accepts, and applies edit to each kept line.
func hl1rRewriteReceipts(t *testing.T, path string, keep func(chain.TxReceipt) bool, edit func([]byte) []byte) {
	t.Helper()
	var out bytes.Buffer
	lines := bytes.Split(hl1rRead(t, path), []byte("\n"))
	for _, l := range lines[:len(lines)-1] {
		var r chain.TxReceipt
		if err := json.Unmarshal(l, &r); err != nil {
			t.Fatal(err)
		}
		if keep != nil && !keep(r) {
			continue
		}
		if edit != nil {
			l = edit(l)
		}
		out.Write(append(l, '\n'))
	}
	hl1WriteFile(t, path, out.String())
}

// -----------------------------------------------------------------------------
// Refusals: exit 78 with the state directory (W included) unchanged
// -----------------------------------------------------------------------------

func TestHL1S4rRefusals(t *testing.T) {
	if testing.Short() {
		t.Skip("state-directory recovery test")
	}
	const n = 4
	for _, tc := range []struct {
		name  string
		fault string
		setup func(t *testing.T, f *hl1rFixture)
		mod   func(*hl1RepairOptions)
		want  []string
	}{
		{"accounts generation link missing", "H5", func(t *testing.T, f *hl1rFixture) {
			if err := os.Remove(hl1GenerationLink(f.path(hl1AccountsName), n-1)); err != nil {
				t.Fatal(err)
			}
		}, nil, []string{"generation link", "is missing", hl1S4rRouteC6RX}},
		{"enrollment generation link missing", "H5", func(t *testing.T, f *hl1rFixture) {
			if err := os.Remove(hl1GenerationLink(f.path(hl1EnrollmentName), n-1)); err != nil {
				t.Fatal(err)
			}
		}, nil, []string{"generation link", "is missing", hl1S4rRouteC6RX}},
		{"tampered accounts generation: pair root mismatch", "H5", func(t *testing.T, f *hl1rFixture) {
			link := hl1GenerationLink(f.path(hl1AccountsName), n-1)
			var accs []chain.Account
			if err := json.Unmarshal(hl1rRead(t, link), &accs); err != nil || len(accs) == 0 {
				t.Fatalf("accounts generation: %v", err)
			}
			accs[0].Balance++
			data, _ := json.MarshalIndent(accs, "", "  ")
			hl1WriteFile(t, link, string(data)) // in place: the .h inode, not the main file
		}, nil, []string{"does not reproduce", hl1S4rRouteC6RX}},
		{"replay of N fails (producer not authorized)", "H5", nil, func(o *hl1RepairOptions) {
			o.Authorized = []string{"hl1r-some-other-producer"}
		}, []string{"replay onto the .h3 pair failed", hl1S4rRouteC6RX}},
		{"accounts post-state mismatch", "H5", nil, func(o *hl1RepairOptions) {
			o.mutatePost = func(pre *chain.EnrollmentAwareApplier) { pre.Accounts().Credit("hl1r-phantom", 1) }
		}, []string{"accounts replayed", hl1S4rRouteC6C4}},
		{"enrollment post-state mismatch", "H5", nil, func(o *hl1RepairOptions) {
			o.mutatePost = func(pre *chain.EnrollmentAwareApplier) {
				st, err := hl1ApplierEnrollment(pre)
				if err != nil {
					panic(err)
				}
				st.MarkEvidenceSeen([32]byte{7})
			}
		}, []string{"enrollment state replayed", hl1S4rRouteC6C4}},
		{"present receipt with a changed amount", "H5p", func(t *testing.T, f *hl1rFixture) {
			hl1rRewriteReceipts(t, f.path(hl1ReceiptsName), nil, func(l []byte) []byte {
				if bytes.Contains(l, []byte(`"block_height":4,`)) {
					if !bytes.Contains(l, []byte(`"amount":0,`)) {
						t.Fatalf("receipts line without a zero amount: %s", l)
					}
					return bytes.Replace(l, []byte(`"amount":0,`), []byte(`"amount":5,`), 1)
				}
				return l
			})
		}, nil, []string{"present receipt", hl1S4rRouteC6C4}},
		{"block W lacks receipts and W was sealed", "H5", func(t *testing.T, f *hl1rFixture) {
			hl1rRewriteReceipts(t, f.path(hl1ReceiptsName), func(r chain.TxReceipt) bool { return r.BlockHeight != n-1 }, nil)
		}, nil, []string{"more than one block lacks receipts", "follow " + hl1S4rRouteRX}},
		{"receipt above the tip", "H5", func(t *testing.T, f *hl1rFixture) {
			hl1rAppendLine(t, f.path(hl1ReceiptsName), &chain.TxReceipt{TxID: "hl1r-future", BlockHeight: n + 1, BlockHash: f.blkJ.Hash, Status: 1})
		}, nil, []string{"above the restored tip", "follow " + hl1S4rRouteRX}},
		{"receipt at N with a foreign hash", "H5", func(t *testing.T, f *hl1rFixture) {
			hl1rAppendLine(t, f.path(hl1ReceiptsName), &chain.TxReceipt{TxID: f.blkJ.Transactions[0].ID, BlockHeight: n, BlockHash: strings.Repeat("ab", 32), Status: 1})
		}, nil, []string{"carries block hash", "follow " + hl1S4rRouteRX}},
		{"unexpected receipt at N", "H5", func(t *testing.T, f *hl1rFixture) {
			hl1rAppendLine(t, f.path(hl1ReceiptsName), &chain.TxReceipt{TxID: "hl1r-stranger", BlockHeight: n, BlockHash: f.blkJ.Hash, Status: 1})
		}, nil, []string{"not a local failed-drop", "follow " + hl1S4rRouteRX}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := hl1rCrash(t, false, n, tc.fault)
			if tc.setup != nil {
				tc.setup(t, f)
			}
			before := hl1rTreeHash(t, f.dir)
			_, _, _, err := hl1rBootWith(t, f.dir, nil, hl1OSFS{}, tc.mod)
			if err == nil {
				t.Fatal("S4r did not refuse")
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Fatalf("err = %v, want %q", err, w)
				}
			}
			if hl1rTreeHash(t, f.dir) != before {
				t.Fatal("a refused S4r changed the state directory")
			}
			if w := hl1rW(t, f.dir); w.Height != n-1 {
				t.Fatalf("W = %+v after a refusal", w)
			}
		})
	}
}

// Block W without receipts: refused when W was sealed by HL1 (above), but
// only a warning when W was seeded (R0 history) or advanced at boot.
func TestHL1S4rSeededWBlockWithoutReceipts(t *testing.T) {
	if testing.Short() {
		t.Skip("state-directory recovery test")
	}
	const n = 4
	for _, source := range []string{legacymining.WatermarkSourceSeed, legacymining.WatermarkSourceBoot} {
		t.Run(source, func(t *testing.T) {
			f := hl1rCrash(t, false, n, "H5")
			receipts := f.path(hl1ReceiptsName)
			hl1rRewriteReceipts(t, receipts, func(r chain.TxReceipt) bool { return r.BlockHeight != n-1 }, nil)
			w := hl1rW(t, f.dir)
			w.Source = source
			hl1SeedW(t, f.dir, w)
			_, s4r, s5, err := hl1rBootWith(t, f.dir, nil, hl1OSFS{}, nil)
			if err != nil || len(s4r.Regenerated) != len(f.blkJ.Transactions) || !s5.Wrote {
				t.Fatalf("boot: %+v %+v %v", s4r, s5, err)
			}
			heights, _ := hl1rReceiptHeights(t, receipts)
			if heights[n] != len(f.blkJ.Transactions) || heights[n-1] != 0 {
				t.Fatalf("receipts per height = %v", heights)
			}
		})
	}
}

// I/O errors in the append, the receipts fsync or the directory fsync exit
// 78 with W unchanged; the next boot completes N without duplicates.
func TestHL1S4rIOErrors(t *testing.T) {
	if testing.Short() {
		t.Skip("state-directory recovery test")
	}
	const n = 4
	for _, tc := range []struct {
		name string
		fsys *hl1rTraceFS
		mod  func(t *testing.T) func(*hl1RepairOptions)
	}{
		{"EIO in the append after one line", &hl1rTraceFS{}, func(t *testing.T) func(*hl1RepairOptions) {
			return func(o *hl1RepairOptions) {
				o.Append = func(rs *chain.ReceiptStore, path string, h uint64) (int, error) {
					return hl1rAppendPrefix(t, rs, path, h, 1)
				}
			}
		}},
		{"EIO in the receipts fsync", &hl1rTraceFS{failSyncFile: true}, nil},
		{"EIO in the directory fsync", &hl1rTraceFS{failSyncDir: true}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := hl1rCrash(t, false, n, "H5")
			wBefore := hl1rRead(t, f.path(legacymining.WatermarkFile))
			var mod func(*hl1RepairOptions)
			if tc.mod != nil {
				mod = tc.mod(t)
			}
			_, _, _, err := hl1rBootWith(t, f.dir, nil, tc.fsys, mod)
			if err == nil || !strings.Contains(err.Error(), hl1S4rRouteC6IO) {
				t.Fatalf("err = %v, want the I/O route", err)
			}
			if !bytes.Equal(hl1rRead(t, f.path(legacymining.WatermarkFile)), wBefore) {
				t.Fatal("W changed after an S4r I/O error")
			}
			if tc.fsys.index("WriteFileDurable "+legacymining.WatermarkFile) >= 0 {
				t.Fatal("W was written after an S4r I/O error")
			}
			_, s4r, s5, err := hl1rBootWith(t, f.dir, nil, hl1OSFS{}, nil)
			if err != nil || !s5.Wrote || s5.DurableTip != n {
				t.Fatalf("the next boot: %+v %+v %v", s4r, s5, err)
			}
			lines := hl1rReceiptsAt(t, f.path(hl1ReceiptsName), n)
			seen := map[string]bool{}
			for _, r := range lines {
				if seen[r.TxID] {
					t.Fatalf("duplicate receipt of %s at N", r.TxID)
				}
				seen[r.TxID] = true
			}
			if len(lines) != len(f.blkJ.Transactions) {
				t.Fatalf("%d receipts at N after the next boot, want %d", len(lines), len(f.blkJ.Transactions))
			}
		})
	}
}

func TestHL1S4rFollowerRoleDoesNothing(t *testing.T) {
	if testing.Short() {
		t.Skip("state-directory recovery test")
	}
	const n = 4
	f := hl1rCrash(t, false, n, "H5")
	st, blocks, err := hl1rRestore(t, f.dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	before, replays := hl1rTreeHash(t, f.dir), hl1S4rScratchReplays.Load()
	o := hl1rRepairOptions(f.dir, nil, st, blocks, hl1OSFS{})
	o.ProducerRole = false
	res, err := hl1RepairTipReceipts(o)
	if err != nil || res.Gate || len(res.Regenerated) != 0 {
		t.Fatalf("follower-role S4r: %+v %v", res, err)
	}
	if hl1rTreeHash(t, f.dir) != before || hl1S4rScratchReplays.Load() != replays {
		t.Fatal("S4r ran in the follower role")
	}
}

// -----------------------------------------------------------------------------
// Units: the S5 decision, the receipts tail, canonical enrollment equality
// -----------------------------------------------------------------------------

func TestHL1S5Decide(t *testing.T) {
	blocks := hl1Chain(6)
	at := hl1BlockAtFn(blocks)
	w := func(h uint64, hash string) legacymining.Watermark {
		return legacymining.Watermark{Version: 1, Height: h, Hash: hash, Source: "seal"}
	}
	for _, tc := range []struct {
		name    string
		w       legacymining.Watermark
		tip     *chain.Block
		advance bool
		err     string
	}{
		{"empty chain", w(0, blocks[0].Hash), nil, false, "empty chain"},
		{"W above tip", w(5, blocks[4].Hash), blocks[4], false, "R-W"},
		{"hash mismatch", w(4, blocks[3].Hash), blocks[5], false, "R-W"},
		{"tip above W+1", w(3, blocks[3].Hash), blocks[5], false, "R-H"},
		{"tip = W+1", w(4, blocks[4].Hash), blocks[5], true, ""},
		{"tip = W", w(5, blocks[5].Hash), blocks[5], false, ""},
	} {
		adv, err := hl1S5Decide(tc.w, tc.tip, at)
		if adv != tc.advance || (err == nil) != (tc.err == "") || (err != nil && !strings.Contains(err.Error(), tc.err)) {
			t.Errorf("%s: advance=%v err=%v", tc.name, adv, err)
		}
	}
	if got, ok := hl1BlocksAt(blocks[2:])(4); !ok || got != blocks[4] {
		t.Fatal("hl1BlocksAt on a chain that does not start at 0")
	}
	if _, ok := hl1BlocksAt(blocks)(6); ok {
		t.Fatal("hl1BlocksAt above the tip")
	}
}

func TestHL1LastReceiptHeight(t *testing.T) {
	dir := t.TempDir()
	long := strings.Repeat("x", 70<<10)
	for _, tc := range []struct {
		name, data string
		absent     bool
		h          uint64
		ok         bool
		err        string
	}{
		{name: "absent", absent: true},
		{name: "empty", data: ""},
		{name: "one line", data: `{"tx_id":"a","block_height":7}` + "\n", h: 7, ok: true},
		{name: "last of two", data: `{"block_height":7}` + "\n" + `{"block_height":9}` + "\n", h: 9, ok: true},
		{name: "blank lines at the end", data: `{"block_height":7}` + "\n\n\n", h: 7, ok: true},
		{name: "long last line", data: `{"block_height":3}` + "\n" + `{"memo":"` + long + `","block_height":11}` + "\n", h: 11, ok: true},
		{name: "torn", data: `{"block_height":7}`, err: "R-C3"},
		{name: "garbage", data: "not json\n", err: "does not parse"},
	} {
		p := filepath.Join(dir, strings.ReplaceAll(tc.name, " ", "_"))
		if !tc.absent {
			hl1WriteFile(t, p, tc.data)
		}
		h, ok, err := hl1LastReceiptHeight(hl1OSFS{}, p)
		if h != tc.h || ok != tc.ok || (err == nil) != (tc.err == "") || (err != nil && !strings.Contains(err.Error(), tc.err)) {
			t.Errorf("%s: h=%d ok=%v err=%v", tc.name, h, ok, err)
		}
	}
}

func TestHL1EnrollmentEqualIsCanonical(t *testing.T) {
	dir := t.TempDir()
	rec := func(id string) string {
		return `{"node_id":"` + id + `","owner":"o-` + id + `","gpu_uuid":"g-` + id + `","hmac_key":"AAEC","stake_dust":5,"enrolled_at_height":1}`
	}
	ev1, ev2 := strings.Repeat("11", 32), strings.Repeat("22", 32)
	load := func(name, data string) *enrollment.InMemoryState {
		p := filepath.Join(dir, name)
		hl1WriteFile(t, p, data)
		s := enrollment.NewInMemoryState()
		if _, err := s.Load(p); err != nil {
			t.Fatal(err)
		}
		return s
	}
	a := load("a.json", `{"records":[`+rec("n1")+`,`+rec("n2")+`],"seen_evidence_hex":["`+ev1+`","`+ev2+`"]}`)
	b := load("b.json", `{"records":[`+rec("n2")+`,`+rec("n1")+`],"seen_evidence_hex":["`+ev2+`","`+ev1+`"]}`)
	c := load("c.json", `{"records":[`+rec("n2")+`,`+rec("n1")+`],"seen_evidence_hex":["`+ev2+`"]}`)
	d := load("d.json", `{"records":[`+rec("n1")+`],"seen_evidence_hex":["`+ev2+`","`+ev1+`"]}`)
	for _, tc := range []struct {
		name string
		x, y *enrollment.InMemoryState
		want bool
	}{
		{"same state, other order", a, b, true},
		{"evidence differs", a, c, false},
		{"records differ", a, d, false},
	} {
		got, err := hl1EnrollmentEqual(tc.x, tc.y)
		if err != nil || got != tc.want {
			t.Errorf("%s: %v %v", tc.name, got, err)
		}
	}
}

// -----------------------------------------------------------------------------
// Source pins: placement in main(), no v2wiring/api, S4r never writes W
// -----------------------------------------------------------------------------

func TestHL1S4rPlacementAndPins(t *testing.T) {
	s := hl1ParseMain(t)
	s4r := s.one(t, "hl1RepairTipReceipts")
	// After the whole S4 restore block, including every receipts load and
	// the migration, and the "restored from disk" log.
	for _, name := range []string{"adminReceipts.LoadNDJSON", "adminReceipts.Load", "adminReceipts.AppendBlockNDJSON",
		"adminProducer.RestoreChain", "v2Wired.EnrollmentState.Load", "v2Wired.StateApplier.StateRoot"} {
		ps := s.calls(s.main, name)
		if len(ps) == 0 {
			t.Fatalf("main() has no %s", name)
		}
		for _, p := range ps {
			if p >= s4r {
				t.Fatalf("%s (line %d) runs after S4r (line %d)", name, s.line(p), s.line(s4r))
			}
		}
	}
	var restoredLog token.Pos
	ast.Inspect(s.main, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok && hl1CallName(c) == "logger.Info" && len(c.Args) > 0 {
			if lit, ok := c.Args[0].(*ast.BasicLit); ok && strings.Contains(lit.Value, "restored from disk") {
				restoredLog = c.Pos()
			}
		}
		return true
	})
	if restoredLog == 0 || restoredLog >= s4r {
		t.Fatal("S4r must follow the \"restored from disk\" log")
	}
	// Before S5, OpenChainJournal, the hook installation, the driver and the
	// API.
	for _, later := range []string{"hl1StartupWatermark", "hl1OpenChainJournal", "blockDriver.Start", "api.NewServer"} {
		s.before(t, "hl1RepairTipReceipts", later)
	}
	var hookInstalled token.Pos
	ast.Inspect(s.main, func(n ast.Node) bool {
		if as, ok := n.(*ast.AssignStmt); ok && len(as.Lhs) == 1 {
			if sel, ok := as.Lhs[0].(*ast.SelectorExpr); ok && sel.Sel.Name == "OnSealedBlock" && hl1ExprName(sel.X) == "adminProducer" {
				hookInstalled = as.Pos()
			}
		}
		return true
	})
	if hookInstalled == 0 || hookInstalled <= s4r {
		t.Fatal("S4r must run before the persistence hook is installed")
	}
	// Its options and its refusal.
	var opts map[string]string
	ast.Inspect(s.main, func(n ast.Node) bool {
		if cl, ok := n.(*ast.CompositeLit); ok && hl1ExprName(cl.Type) == "hl1RepairOptions" {
			opts = map[string]string{}
			for _, e := range cl.Elts {
				kv := e.(*ast.KeyValueExpr)
				opts[hl1ExprName(kv.Key)] = hl1ExprName(kv.Value)
			}
		}
		return true
	})
	for k, v := range map[string]string{"ProducerRole": "localBlockProduction", "Blocks": "hl1RestoredBlocks", "Live": "hl1LiveApplier",
		"Receipts": "adminReceipts", "ReceiptsPath": "receiptsNDJSONPath", "AccountsPath": "accountsStatePath",
		"EnrollmentPath": "enrollmentStatePath", "StateDir": "stateDir", "Transition": "cfg.ProducerTransition",
		"Authorized": "cfg.AuthorizedBlockProducers"} {
		if opts[k] != v {
			t.Fatalf("hl1RepairOptions.%s = %q, want %q", k, opts[k], v)
		}
	}
	var refused bool
	for _, p := range s.calls(s.main, "fatalRestore") {
		ast.Inspect(s.main, func(n ast.Node) bool {
			if c, ok := n.(*ast.CallExpr); ok && c.Pos() == p {
				if lit, ok := c.Args[0].(*ast.BasicLit); ok && lit.Value == `"hl1 S4r: %v"` && p > s4r {
					refused = true
				}
			}
			return true
		})
	}
	if !refused {
		t.Fatalf("an S4r error must exit 78 through fatalRestore(%q)", "hl1 S4r: %v")
	}

	// The new file: no v2wiring (its Wire sets api, monitoring and mining
	// globals), no api, and nothing that writes W.
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "hl1_receipts_regen.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, imp := range f.Imports {
		p := strings.Trim(imp.Path.Value, `"`)
		if strings.HasSuffix(p, "/internal/v2wiring") || strings.HasSuffix(p, "/pkg/api") || strings.HasSuffix(p, "/pkg/monitoring") {
			t.Fatalf("hl1_receipts_regen.go imports %s", p)
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CallExpr:
			switch name := hl1CallName(x); {
			case name == "hl1WriteWatermark", name == "hl1StartupWatermark", strings.HasSuffix(name, "WriteFileDurable"),
				strings.HasSuffix(name, ".Save"), name == "os.WriteFile", name == "os.Rename", name == "os.Remove":
				t.Fatalf("S4r calls %s", name)
			}
		case *ast.SelectorExpr:
			if x.Sel.Name == "WatermarkFile" || x.Sel.Name == "OnSealedBlock" {
				t.Fatalf("S4r references %s", x.Sel.Name)
			}
		}
		return true
	})
}
