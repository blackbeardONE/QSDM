package hl1tail

// scan.go: streaming NDJSON line scanning and the durable cut primitive
// (archive -> fsync -> truncate -> fsync file and dir) shared by every trim.

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/blackbeardONE/QSDM/internal/legacymining"
	"github.com/blackbeardONE/QSDM/pkg/chain"
)

// maxLineBytes mirrors the 4 MiB bufio.Scanner ceiling of
// chain.LoadChainNDJSON and chain.ReceiptStore.LoadNDJSON: a complete line
// (including its '\n') longer than this does not load.
const maxLineBytes = 4 * 1024 * 1024

// line is one complete, newline-terminated line of an NDJSON file.
type line struct {
	No    int   // 1-based line number
	Start int64 // offset of the first byte
	End   int64 // offset just past the '\n'
	// Data is the line without its '\n' and one trailing '\r' (bufio.ScanLines
	// semantics). It is valid only during the callback.
	Data []byte
}

// layout is the line structure of a scanned file.
type layout struct {
	Size   int64 // bytes read
	LastNL int64 // offset just past the last '\n'; 0 if there is none
}

// Fragment is the number of bytes after the last '\n': a torn last line.
func (l layout) Fragment() int64 { return l.Size - l.LastNL }

// errStop ends a scan early without an error.
var errStop = errors.New("stop")

// scanLines calls fn for every complete line of path, in order. A callback
// may return errStop to end the scan early (the layout then covers only the
// lines read so far). A complete line longer than maxLineBytes is a refusal,
// as it is for the core loaders; the bytes after the last '\n' (the fragment)
// are counted but never passed to fn.
func scanLines(path string, fn func(l line) error) (layout, error) {
	f, err := os.Open(path)
	if err != nil {
		return layout{}, err
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 1<<20)
	var (
		out       layout
		acc       []byte
		tooLong   bool
		lineStart int64
		no        int
	)
	for {
		chunk, rerr := r.ReadSlice('\n')
		if len(chunk) > 0 {
			out.Size += int64(len(chunk))
			if !tooLong {
				if len(acc)+len(chunk) > maxLineBytes {
					tooLong, acc = true, acc[:0]
				} else {
					acc = append(acc, chunk...)
				}
			}
		}
		switch {
		case rerr == nil:
			no++
			if tooLong {
				return out, refusef("%s line %d (byte %d) exceeds the %d-byte loader limit", filepath.Base(path), no, lineStart, maxLineBytes)
			}
			data := acc[:len(acc)-1]
			if n := len(data); n > 0 && data[n-1] == '\r' {
				data = data[:n-1]
			}
			if err := fn(line{No: no, Start: lineStart, End: out.Size, Data: data}); err != nil {
				if errors.Is(err, errStop) {
					out.LastNL = out.Size
					return out, nil
				}
				return out, err
			}
			acc, tooLong = acc[:0], false
			lineStart, out.LastNL = out.Size, out.Size
		case errors.Is(rerr, bufio.ErrBufferFull):
			continue
		case errors.Is(rerr, io.EOF):
			return out, nil
		default:
			return out, fmt.Errorf("read %s: %w", path, rerr)
		}
	}
}

// parseBlock decodes one journal line the way chain.LoadChainNDJSON does.
// ok is false for an empty line, which the loader skips.
func parseBlock(path string, l line) (*chain.Block, bool, error) {
	if len(l.Data) == 0 {
		return nil, false, nil
	}
	blk := &chain.Block{}
	if err := json.Unmarshal(l.Data, blk); err != nil {
		return nil, false, refusef("%s line %d (byte %d) does not parse as a block: %v", filepath.Base(path), l.No, l.Start, err)
	}
	return blk, true, nil
}

// parseReceipt decodes one receipts line the way
// chain.ReceiptStore.LoadNDJSON does. ok is false for an empty line.
func parseReceipt(path string, l line) (*chain.TxReceipt, bool, error) {
	if len(l.Data) == 0 {
		return nil, false, nil
	}
	r := &chain.TxReceipt{}
	if err := json.Unmarshal(l.Data, r); err != nil {
		return nil, false, refusef("%s line %d (byte %d) does not parse as a receipt: %v", filepath.Base(path), l.No, l.Start, err)
	}
	return r, true, nil
}

// cutFile makes path end at byte cut. Each step is durable before the next:
//  1. the removed bytes [cut, size) are archived to dir/archiveName with D1
//     (unique temp, write, fsync, rename, fsync(dir)); an existing archive
//     name is never overwritten;
//  2. path is truncated to cut and fsynced;
//  3. dir is fsynced.
//
// size is the size the scan observed; if the file changed since, nothing is
// cut. A failure at any step leaves a state that the same command can simply
// be rerun on.
func cutFile(env *Env, path string, cut, size int64, archiveName string) (string, error) {
	if cut < 0 || cut > size {
		return "", fmt.Errorf("cut %s: offset %d outside 0..%d", path, cut, size)
	}
	dir := filepath.Dir(path)
	archive := filepath.Join(dir, archiveName)
	if err := archiveRange(env, path, cut, size, dir, archiveName); err != nil {
		return "", err
	}
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return archive, fmt.Errorf("open %s for truncation: %w", path, err)
	}
	fi, err := f.Stat()
	if err == nil && fi.Size() != size {
		err = fmt.Errorf("%s changed during the run (%d -> %d bytes)", path, size, fi.Size())
	}
	if err == nil {
		err = f.Truncate(cut)
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return archive, fmt.Errorf("truncate %s to %d: %w", path, cut, err)
	}
	if err := env.SyncDir(dir); err != nil {
		return archive, fmt.Errorf("fsync %s: %w", dir, err)
	}
	return archive, nil
}

// archiveRange copies src[from:to) to dir/name with D1 semantics
// (legacymining.WriteFileDurable), streaming instead of buffering. The temp
// name starts with legacymining.TempPrefix, so S3 and every mutating
// subcommand clean it up if the process dies mid-copy.
func archiveRange(env *Env, src string, from, to int64, dir, name string) error {
	final := filepath.Join(dir, name)
	if _, err := os.Lstat(final); err == nil {
		return fmt.Errorf("archive %s already exists", final)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat archive %s: %w", final, err)
	}
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer in.Close()
	var rnd [8]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return fmt.Errorf("archive %s: random temp name: %w", name, err)
	}
	tmp := filepath.Join(dir, fmt.Sprintf("%s%s-%d-%s", legacymining.TempPrefix, name, os.Getpid(), hex.EncodeToString(rnd[:])))
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("archive %s: create temp: %w", name, err)
	}
	n, err := io.Copy(out, io.NewSectionReader(in, from, to-from))
	if err == nil && n != to-from {
		err = io.ErrUnexpectedEOF
	}
	if err == nil {
		err = out.Sync()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, final)
	}
	if err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("archive %s: %w", name, err)
	}
	if err := env.SyncDir(dir); err != nil {
		return fmt.Errorf("archive %s: fsync %s: %w", name, dir, err)
	}
	return nil
}

// syncFile opens name write-only (no create), fsyncs and closes it.
func syncFile(name string) error {
	f, err := os.OpenFile(name, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	serr := f.Sync()
	if cerr := f.Close(); serr == nil {
		serr = cerr
	}
	return serr
}
