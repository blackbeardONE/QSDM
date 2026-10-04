package hl1tail

// ops.go: the subcommands. Each one checks every precondition before its
// first change (the temp cleanup excepted).

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/blackbeardONE/QSDM/internal/legacymining"
	"github.com/blackbeardONE/QSDM/pkg/chain"
)

// blockRef is the identity of one journal line.
type blockRef struct {
	Height uint64
	Hash   string
	Start  int64
}

// watermark reads W for a W-gated subcommand: absent or invalid W refuses.
func (t *tool) watermark() (legacymining.Watermark, error) {
	w, err := ReadWatermark(t.dir)
	if errors.Is(err, ErrWatermarkMissing) {
		return w, refusef("no served watermark W in %s: nothing gates this trim (R-X)", t.dir)
	}
	if err != nil {
		return w, refusef("%v (S5 rule 2: R-X)", err)
	}
	return w, nil
}

// wCheck records the journal line(s) at W.height.
type wCheck struct {
	w      legacymining.Watermark
	count  int
	hashes []string
}

func (c *wCheck) see(height uint64, hash string) {
	if height == c.w.Height {
		c.count++
		c.hashes = append(c.hashes, hash)
	}
}

// verify requires exactly one journal block at W.height, with W's hash. A W
// the journal does not confirm cannot gate a trim (S5 rule 4 would refuse
// the next boot anyway: R-W).
func (c *wCheck) verify(journal string) error {
	switch {
	case c.count == 0:
		return refusef("%s has no block at the watermark height %d (R-W)", filepath.Base(journal), c.w.Height)
	case c.count > 1:
		return refusef("%s has %d blocks at the watermark height %d (R-X)", filepath.Base(journal), c.count, c.w.Height)
	case c.hashes[0] != c.w.Hash:
		return refusef("%s block %d has hash %s, the watermark has %s (R-W)", filepath.Base(journal), c.w.Height, c.hashes[0], c.w.Hash)
	}
	return nil
}

func notExist(err error, what, path string) error {
	if errors.Is(err, fs.ErrNotExist) {
		return refusef("%s %s does not exist", what, path)
	}
	return err
}

// ----------------------------------------------------------------------------
// trim-fragment (R-C3)
// ----------------------------------------------------------------------------

func (t *tool) trimFragment(file string) error {
	if file == "journal" {
		return t.trimJournalFragment()
	}
	return t.trimReceiptsFragment()
}

// trimJournalFragment removes the bytes after the journal's last '\n' when
// every complete line parses and the last complete height H_last >= W. The
// fragment is then above W and was never served.
func (t *tool) trimJournalFragment() error {
	w, err := t.watermark()
	if err != nil {
		return err
	}
	wc := &wCheck{w: w}
	var last *blockRef
	lay, err := scanLines(t.journal, func(l line) error {
		b, ok, err := parseBlock(t.journal, l)
		if err != nil || !ok {
			return err
		}
		last = &blockRef{Height: b.Height, Hash: b.Hash, Start: l.Start}
		wc.see(b.Height, b.Hash)
		return nil
	})
	if err != nil {
		return notExist(err, "journal", t.journal)
	}
	if lay.Fragment() == 0 {
		return t.noFragment(t.journal, "journal")
	}
	if last == nil {
		return refusef("no complete block precedes the %d-byte fragment of %s; W=%d cannot gate it (R-X)", lay.Fragment(), JournalFile, w.Height)
	}
	if last.Height < w.Height {
		return refusef("the last complete journal line is height %d, below W=%d: the fragment may be a served height (R-X)", last.Height, w.Height)
	}
	if err := wc.verify(t.journal); err != nil {
		return err
	}
	// chain.LoadChainNDJSON would load an unterminated but complete last line
	// as a block. Such a line must be above W too.
	if b, ok := parseFragmentBlock(t.journal, lay); ok && b.Height <= w.Height {
		return refusef("the unterminated last line is a complete block at height %d <= W=%d (R-X)", b.Height, w.Height)
	}
	archive, err := cutFile(t.env, t.journal, lay.LastNL, lay.Size, archiveName(t.journal, "torn", t.env.Now()))
	if err != nil {
		return err
	}
	t.result(map[string]any{
		"file": "journal", "result": "trimmed", "cut_offset": lay.LastNL, "removed_bytes": lay.Fragment(),
		"archive": archive, "last_complete_height": last.Height, "watermark_height": w.Height,
	})
	return nil
}

// parseFragmentBlock decodes the bytes after the last '\n' as a block, if
// they form one.
func parseFragmentBlock(path string, lay layout) (*chain.Block, bool) {
	n := lay.Fragment()
	if n <= 0 || n > maxLineBytes {
		return nil, false
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	buf := make([]byte, n)
	if got, _ := f.ReadAt(buf, lay.LastNL); int64(got) != n {
		return nil, false
	}
	b := &chain.Block{}
	if json.Unmarshal(bytes.TrimSuffix(buf, []byte("\r")), b) != nil {
		return nil, false
	}
	return b, true
}

// trimReceiptsFragment removes the bytes after the receipts file's last
// '\n'. It is gated on the journal, not on W: the journal must parse
// completely and end with '\n', every complete receipts line must parse, and
// no complete receipts line may be above the journal tip.
func (t *tool) trimReceiptsFragment() error {
	tip, err := t.completeJournalTip()
	if err != nil {
		return err
	}
	var maxHeight uint64
	var lines int
	lay, err := scanLines(t.receipts, func(l line) error {
		r, ok, err := parseReceipt(t.receipts, l)
		if err != nil || !ok {
			return err
		}
		lines++
		if r.BlockHeight > maxHeight {
			maxHeight = r.BlockHeight
		}
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		t.result(map[string]any{"file": "receipts", "result": "absent"})
		return nil
	}
	if err != nil {
		return err
	}
	if lay.Fragment() == 0 {
		return t.noFragment(t.receipts, "receipts")
	}
	if lines > 0 && maxHeight > tip.Height {
		return refusef("complete receipts lines reach height %d, above the journal tip %d (R-X)", maxHeight, tip.Height)
	}
	archive, err := cutFile(t.env, t.receipts, lay.LastNL, lay.Size, archiveName(t.receipts, "torn", t.env.Now()))
	if err != nil {
		return err
	}
	t.result(map[string]any{
		"file": "receipts", "result": "trimmed", "cut_offset": lay.LastNL, "removed_bytes": lay.Fragment(),
		"archive": archive, "max_receipt_height": maxHeight, "journal_tip": tip.Height,
	})
	return nil
}

// completeJournalTip scans the whole journal. Every line must parse, the
// file must end with '\n', and it must hold at least one block.
func (t *tool) completeJournalTip() (*blockRef, error) {
	var last *blockRef
	lay, err := scanLines(t.journal, func(l line) error {
		b, ok, err := parseBlock(t.journal, l)
		if err != nil || !ok {
			return err
		}
		last = &blockRef{Height: b.Height, Hash: b.Hash, Start: l.Start}
		return nil
	})
	if err != nil {
		return nil, notExist(err, "journal", t.journal)
	}
	if lay.Fragment() != 0 {
		return nil, refusef("the journal has a torn last line (%d bytes after the last newline): run trim-fragment --file journal first", lay.Fragment())
	}
	if last == nil {
		return nil, refusef("the journal %s holds no block", t.journal)
	}
	return last, nil
}

// noFragment completes a possibly interrupted earlier trim: the file already
// ends with '\n' (or is empty), so it and its directory are only fsynced.
func (t *tool) noFragment(path, file string) error {
	if err := syncFile(path); err != nil {
		return fmt.Errorf("fsync %s: %w", path, err)
	}
	if err := t.env.SyncDir(filepath.Dir(path)); err != nil {
		return fmt.Errorf("fsync %s: %w", filepath.Dir(path), err)
	}
	t.result(map[string]any{"file": file, "result": "no-fragment"})
	return nil
}

// ----------------------------------------------------------------------------
// trim --above (R-C4 fallback)
// ----------------------------------------------------------------------------

// trimAbove removes the complete journal lines above h, where h = J-1 and J
// is the journal tip. Preconditions, all checked before any change:
//   - W exists, is valid and the journal confirms it; h >= W.height, so no
//     height <= W is removed (W7);
//   - the journal is complete (no fragment: R-C3 first) and its heights above
//     h form its tail, directly after a block at h;
//   - the tip is at most W.height+1: every HL1 crash state leaves tip <= W+1
//     (S5 rule 5), so a longer tail was sealed without HL1 and may have been
//     served;
//   - the main snapshot pair represents h: for each of accounts and
//     enrollment whose generation link .h<h> exists, the main file is the same
//     inode as the link (the C4 shape). A newer main inode (the C5 shape,
//     accounts at J) is refused: trimming would leave the snapshot ahead of
//     the journal. When no link exists the next boot's root check is the
//     backstop.
func (t *tool) trimAbove(h uint64) error {
	w, err := t.watermark()
	if err != nil {
		return err
	}
	if h < w.Height {
		return refusef("--above %d is below the watermark %d: heights <= W are never removed (W7; J <= W goes to R-X)", h, w.Height)
	}
	wc := &wCheck{w: w}
	var tip, kept *blockRef
	cut := int64(-1)
	removed := 0
	lay, err := scanLines(t.journal, func(l line) error {
		b, ok, err := parseBlock(t.journal, l)
		if err != nil || !ok {
			return err
		}
		wc.see(b.Height, b.Hash)
		ref := &blockRef{Height: b.Height, Hash: b.Hash, Start: l.Start}
		switch {
		case cut < 0 && b.Height <= h:
			kept = ref
		case cut < 0:
			cut, removed = l.Start, 1
		case b.Height <= h:
			return refusef("journal line %d (height %d) follows a height above %d: the tail is not ordered (R-X)", l.No, b.Height, h)
		default:
			removed++
		}
		tip = ref
		return nil
	})
	if err != nil {
		return notExist(err, "journal", t.journal)
	}
	if lay.Fragment() != 0 {
		return refusef("the journal has a torn last line (%d bytes after the last newline): run trim-fragment --file journal (R-C3) first", lay.Fragment())
	}
	if tip == nil {
		return refusef("the journal %s holds no block", t.journal)
	}
	if err := wc.verify(t.journal); err != nil {
		return err
	}
	if tip.Height > w.Height+1 {
		return refusef("the journal tip %d is more than one block above W=%d: those blocks were sealed without HL1 and may have been served (S5 rule 5: R-H/R-X)", tip.Height, w.Height)
	}
	if cut < 0 {
		if tip.Height != h {
			return refusef("the journal tip %d is below --above %d: nothing to trim", tip.Height, h)
		}
		if err := syncFile(t.journal); err != nil {
			return fmt.Errorf("fsync %s: %w", t.journal, err)
		}
		if err := t.env.SyncDir(t.dir); err != nil {
			return fmt.Errorf("fsync %s: %w", t.dir, err)
		}
		t.result(map[string]any{"result": "nothing-above", "above": h, "journal_tip": tip.Height})
		return nil
	}
	if kept == nil || kept.Height != h {
		return refusef("the journal has no block at %d directly below the lines to remove (R-X)", h)
	}
	generation, err := t.checkGeneration(h)
	if err != nil {
		return err
	}
	archive, err := cutFile(t.env, t.journal, cut, lay.Size, archiveName(t.journal, fmt.Sprintf("above-%d", h), t.env.Now()))
	if err != nil {
		return err
	}
	t.result(map[string]any{
		"result": "trimmed", "above": h, "removed_blocks": removed, "removed_from_height": h + 1,
		"removed_through_height": tip.Height, "cut_offset": cut, "removed_bytes": lay.Size - cut,
		"archive": archive, "watermark_height": w.Height, "generation_check": generation,
	})
	return nil
}

// checkGeneration is the .h<h> inode check of trim --above.
func (t *tool) checkGeneration(h uint64) (map[string]string, error) {
	out := map[string]string{}
	for _, main := range []string{t.accounts, t.enrollment} {
		link := fmt.Sprintf(legacymining.GenerationLinkFormat, main, h)
		li, err := os.Lstat(link)
		if errors.Is(err, fs.ErrNotExist) {
			out[filepath.Base(main)] = "no-link"
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("stat %s: %w", link, err)
		}
		if !li.Mode().IsRegular() {
			return nil, refusef("generation link %s is not a regular file (R-X)", link)
		}
		mi, err := os.Lstat(main)
		if errors.Is(err, fs.ErrNotExist) {
			return nil, refusef("%s is absent although its generation link %s exists (R-X)", filepath.Base(main), filepath.Base(link))
		}
		if err != nil {
			return nil, fmt.Errorf("stat %s: %w", main, err)
		}
		if !mi.Mode().IsRegular() || !os.SameFile(mi, li) {
			return nil, refusef("%s is not the same inode as %s: the main snapshot is newer than height %d (C5 shape); trimming would leave it ahead of the journal and the next boot would fail its root check (R-X)", filepath.Base(main), filepath.Base(link), h)
		}
		out[filepath.Base(main)] = "same-inode"
	}
	if out[AccountsFile] == "no-link" && out[EnrollmentFile] == "no-link" {
		fmt.Fprintf(t.env.Stderr, "hl1-tail %s: no .h%d generation links: proceeding; the next boot's state-root check is the backstop (a failure there goes to R-X)\n", t.name, h)
	}
	return out, nil
}

// ----------------------------------------------------------------------------
// watermark seed and retire (R-H, R-B, rollback)
// ----------------------------------------------------------------------------

// seed is R-H step 5. It refuses if W exists, then parses the whole journal
// with LoadChainNDJSON semantics and requires a final '\n'; J is the last
// block. It requires J >= S, F <= J and exactly one journal block at F, whose
// hash is X. Only then does it D1-write W := {J, J.hash, seed, S, F, X}.
func (t *tool) seed(s, f uint64, x string) error {
	wpath := filepath.Join(t.dir, legacymining.WatermarkFile)
	if _, err := os.Lstat(wpath); err == nil {
		return refusef("a watermark already exists (%s): run watermark retire first", wpath)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("stat %s: %w", wpath, err)
	}
	var last *blockRef
	var atF []string
	lay, err := scanLines(t.journal, func(l line) error {
		b, ok, err := parseBlock(t.journal, l)
		if err != nil || !ok {
			return err
		}
		last = &blockRef{Height: b.Height, Hash: b.Hash, Start: l.Start}
		if b.Height == f {
			atF = append(atF, b.Hash)
		}
		return nil
	})
	if err != nil {
		return notExist(err, "journal", t.journal)
	}
	if lay.Fragment() != 0 {
		return refusef("the journal has a torn last line (%d bytes after the last newline); no W exists to gate a trim (R-X)", lay.Fragment())
	}
	if last == nil {
		return refusef("the journal %s holds no block (R-X)", t.journal)
	}
	if last.Height < s {
		return refusef("the journal tip J=%d is below the served tip S=%d: the predecessor served a height it never journaled (R-X)", last.Height, s)
	}
	if f > last.Height {
		return refusef("the follower height F=%d is above the journal tip J=%d (R-X)", f, last.Height)
	}
	if len(atF) != 1 {
		return refusef("the journal has %d blocks at the follower height %d (R-X)", len(atF), f)
	}
	if atF[0] != x {
		return refusef("journal block %d has hash %s, the follower has %s (R-X)", f, atF[0], x)
	}
	if !isLowerHex(last.Hash, 64) {
		return refusef("the journal tip hash %q is not 64 lowercase hexadecimal characters (R-X)", last.Hash)
	}
	st, fh := s, f
	w := legacymining.Watermark{
		Version: legacymining.WatermarkVersion, Height: last.Height, Hash: last.Hash,
		Source: legacymining.WatermarkSourceSeed, WrittenNS: t.env.Now().UnixNano(),
		ServedTip: &st, FollowerHeight: &fh, FollowerHash: x,
	}
	if err := writeWatermark(t.dir, w); err != nil {
		return err
	}
	t.result(map[string]any{"result": "seeded", "watermark": w})
	return nil
}

// retire renames W to hl1-served-watermark.json.retired-<ts> and fsyncs the
// directory. An absent W is already retired: the directory is fsynced (to
// complete an interrupted earlier retire) and the command succeeds. The
// bytes are kept, so an invalid W is retired too.
func (t *tool) retire() error {
	wpath := filepath.Join(t.dir, legacymining.WatermarkFile)
	fi, err := os.Lstat(wpath)
	if errors.Is(err, fs.ErrNotExist) {
		if err := t.env.SyncDir(t.dir); err != nil {
			return fmt.Errorf("fsync %s: %w", t.dir, err)
		}
		t.result(map[string]any{"result": "absent"})
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat %s: %w", wpath, err)
	}
	if !fi.Mode().IsRegular() {
		return refusef("%s is not a regular file (R-X)", wpath)
	}
	w, werr := ReadWatermark(t.dir)
	target := filepath.Join(t.dir, legacymining.WatermarkRetiredPrefix+t.env.Now().UTC().Format("20060102T150405.000000000Z"))
	if _, err := os.Lstat(target); err == nil {
		return refusef("%s already exists", target)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("stat %s: %w", target, err)
	}
	if err := os.Rename(wpath, target); err != nil {
		return fmt.Errorf("retire %s: %w", wpath, err)
	}
	if err := t.env.SyncDir(t.dir); err != nil {
		return fmt.Errorf("fsync %s: %w", t.dir, err)
	}
	out := map[string]any{"result": "retired", "retired_to": target}
	if werr == nil {
		out["watermark"] = w
	} else {
		out["watermark_error"] = werr.Error()
	}
	t.result(out)
	return nil
}

// ----------------------------------------------------------------------------
// Receipts pre-trim (R-C4 step 0, used by qsdm --hl1-tail-replay)
// ----------------------------------------------------------------------------

// PreTrimResult reports a receipts pre-trim.
type PreTrimResult struct {
	Size    int64  // size before
	Cut     int64  // size after
	Archive string // "" when nothing was removed
	// FirstLine is the line number of the first complete line with
	// BlockHeight >= J, or 0 if there is none.
	FirstLine int
}

// PreTrimReceipts is R-C4 step 0 for journal tip j. It finds the first
// complete receipts line with BlockHeight >= j and truncates from its first
// byte, or from the byte after the last '\n' if there is no such line. The
// removed bytes are archived to <receipts>.replay-<ts> first (D1), then the
// file and the directory are fsynced. Replay H5 then appends j's receipts
// once, onto a clean line boundary. An absent file is left alone. A complete
// line before the cut that does not parse is a refusal: the cut point is
// unknown.
func PreTrimReceipts(path string, j uint64, now time.Time) (PreTrimResult, error) {
	fi, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return PreTrimResult{}, nil
	}
	if err != nil {
		return PreTrimResult{}, fmt.Errorf("stat %s: %w", path, err)
	}
	if !fi.Mode().IsRegular() {
		return PreTrimResult{}, refusef("%s is not a regular file", path)
	}
	res := PreTrimResult{Size: fi.Size(), Cut: -1}
	lay, err := scanLines(path, func(l line) error {
		r, ok, err := parseReceipt(path, l)
		if err != nil || !ok {
			return err
		}
		if r.BlockHeight >= j {
			res.Cut, res.FirstLine = l.Start, l.No
			return errStop
		}
		return nil
	})
	if err != nil {
		return PreTrimResult{}, err
	}
	if res.Cut < 0 {
		if lay.Size != res.Size {
			return PreTrimResult{}, fmt.Errorf("%s changed during the scan (%d -> %d bytes)", path, res.Size, lay.Size)
		}
		res.Cut = lay.LastNL
	}
	if res.Cut == res.Size {
		return res, nil
	}
	env := &Env{}
	env.defaults()
	archive, err := cutFile(env, path, res.Cut, res.Size, archiveName(path, "replay", now))
	if err != nil {
		return PreTrimResult{}, err
	}
	res.Archive = archive
	return res, nil
}
