// Package chain — persistence helpers.
//
// File format: NDJSON, one block per line. Choose append-only
// over a single-document JSON array because:
//
//   - Append on each seal is O(1) (one fsync of one block),
//     vs. O(n) rewrite of the whole array.
//   - A crash mid-write loses at most one block, never the
//     whole chain. Truncation recovery is the operator
//     trimming the last (incomplete) line.
//   - Trivially streamable on load: the loader processes
//     blocks one at a time and bails on the first parse error,
//     which surfaces the exact bad line for diagnosis.
//
// This file deliberately uses encoding/json directly rather
// than a versioned envelope: pkg/chain.Block is the canonical
// on-the-wire shape (every BFT propose-body validates against
// it via computeBlockHash), so the persisted form is the
// already-canonical form. Schema bumps would require a
// network upgrade anyway, and a tagged envelope would invite
// drift between "persistence shape" and "consensus shape".
//
// Atomicity: AppendBlockToFile uses an O_APPEND open so two
// concurrent writers can't interleave bytes within a single
// JSON line. We do NOT flush+fsync after every append — the
// testnet posture treats persistence as best-effort durable;
// hardening to fsync-per-block is a follow-up tracked by the
// chain-persistence runbook.
package chain

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// ChainJournal owns one append handle for the lifetime of a validator. Keeping
// the handle open prevents transient readers from making a later Windows open
// fail, while the continuity checks prevent a gap or fork from reaching disk.
type ChainJournal struct {
	mu         sync.Mutex
	path       string
	file       *os.File
	hasTip     bool
	lastHeight uint64
	lastHash   string
	closed     bool
}

// OpenChainJournal opens path for append and seeds its continuity guard from
// the canonical in-memory tip. A nil tip represents a fresh chain.
func OpenChainJournal(path string, tip *Block) (*ChainJournal, error) {
	if path == "" {
		return nil, errors.New("chain.OpenChainJournal: empty path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("chain.OpenChainJournal: create parent: %w", err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("chain.OpenChainJournal: open %s: %w", path, err)
	}
	j := &ChainJournal{path: path, file: f}
	if tip != nil {
		j.hasTip = true
		j.lastHeight = tip.Height
		j.lastHash = tip.Hash
	}
	return j, nil
}

// Append writes and fsyncs one block only when it extends the guarded tip.
func (j *ChainJournal) Append(blk *Block) error {
	if j == nil {
		return errors.New("chain.ChainJournal.Append: nil journal")
	}
	if blk == nil {
		return errors.New("chain.ChainJournal.Append: nil block")
	}
	if want := computeBlockHash(blk); blk.Hash != want {
		return fmt.Errorf("chain.ChainJournal.Append: invalid block hash at height %d", blk.Height)
	}

	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed || j.file == nil {
		return errors.New("chain.ChainJournal.Append: journal is closed")
	}
	if j.hasTip {
		if blk.Height != j.lastHeight+1 || blk.PrevHash != j.lastHash {
			return fmt.Errorf("chain.ChainJournal.Append: block height %d does not extend persisted tip height %d", blk.Height, j.lastHeight)
		}
	} else if blk.Height != 0 || blk.PrevHash != "" {
		return fmt.Errorf("chain.ChainJournal.Append: first block must be genesis, got height %d", blk.Height)
	}

	data, err := json.Marshal(blk)
	if err != nil {
		return fmt.Errorf("chain.ChainJournal.Append: marshal: %w", err)
	}
	data = append(data, '\n')
	for written := 0; written < len(data); {
		n, writeErr := j.file.Write(data[written:])
		if writeErr != nil {
			return fmt.Errorf("chain.ChainJournal.Append: write %s: %w", j.path, writeErr)
		}
		if n == 0 {
			return fmt.Errorf("chain.ChainJournal.Append: short write to %s", j.path)
		}
		written += n
	}
	if err := j.file.Sync(); err != nil {
		return fmt.Errorf("chain.ChainJournal.Append: sync %s: %w", j.path, err)
	}
	j.hasTip = true
	j.lastHeight = blk.Height
	j.lastHash = blk.Hash
	return nil
}

// Close flushes and closes the journal.
func (j *ChainJournal) Close() error {
	if j == nil {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return nil
	}
	j.closed = true
	if j.file == nil {
		return nil
	}
	err := j.file.Close()
	j.file = nil
	return err
}

// AppendBlockToFile appends one block as a single NDJSON line
// to path, creating the file (mode 0o644) if missing. The line
// is exactly:
//
//	{...JSON-encoded *Block...}\n
//
// No trailing whitespace, no leading marker. A reader that
// sees zero bytes between newlines should skip them — this is
// the recovery posture for a truncated tail (the last block
// was being written when the process crashed).
func AppendBlockToFile(path string, blk *Block) error {
	if path == "" {
		return errors.New("chain.AppendBlockToFile: empty path")
	}
	if blk == nil {
		return errors.New("chain.AppendBlockToFile: nil block")
	}
	data, err := json.Marshal(blk)
	if err != nil {
		return fmt.Errorf("chain.AppendBlockToFile: marshal: %w", err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("chain.AppendBlockToFile: open %s: %w", path, err)
	}
	defer f.Close()
	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("chain.AppendBlockToFile: write %s: %w", path, err)
	}
	return nil
}

// LoadChainNDJSON reads path line-by-line, decoding each as a
// *Block, and returns the resulting slice. Returns
// (nil, nil) iff path does not exist — the caller treats that
// as "fresh chain, run genesis seal".
//
// On a truncated tail (the last line is partial because the
// process crashed mid-write), the truncated line is skipped
// rather than treated as a fatal parse error. The threshold
// for "this line is parseable" is "json.Unmarshal succeeds";
// a partial line will fail and surface a warning via the
// returned error chain. Operators recover by deleting the
// incomplete trailing line.
//
// We intentionally do NOT validate block hashes or the
// height-contiguity invariant here — RestoreChain enforces the
// latter and a state-root mismatch on the next ApplyTx surfaces
// the former. Keeping the loader pure I/O makes it cheap to
// unit-test against synthetic NDJSON fixtures.
func LoadChainNDJSON(path string) ([]*Block, error) {
	if path == "" {
		return nil, errors.New("chain.LoadChainNDJSON: empty path")
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("chain.LoadChainNDJSON: open %s: %w", path, err)
	}
	defer f.Close()

	var out []*Block
	scanner := bufio.NewScanner(f)
	// Allow blocks up to 4 MiB. The default 64 KiB ceiling
	// is too small once a block carries even a few hundred
	// txs with payload_b64. 4 MiB is well above any
	// well-formed block on this testnet and still catches
	// runaway lines as a parse failure rather than an OOM.
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	lineno := 0
	for scanner.Scan() {
		lineno++
		raw := scanner.Bytes()
		if len(raw) == 0 {
			continue
		}
		blk := &Block{}
		if err := json.Unmarshal(raw, blk); err != nil {
			return out, fmt.Errorf("chain.LoadChainNDJSON: parse line %d of %s: %w (loaded %d blocks before failure; trim the bad line to recover)",
				lineno, path, err, len(out))
		}
		out = append(out, blk)
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return out, fmt.Errorf("chain.LoadChainNDJSON: scan %s: %w", path, err)
	}
	return out, nil
}

// ReplaceChainFile rewrites path with one validated canonical branch and moves
// the previous journal to backupPath. It is intended for one-time recovery of
// legacy journals that contain interleaved forks from duplicate processes.
func ReplaceChainFile(path, backupPath string, blocks []*Block) error {
	if path == "" || backupPath == "" {
		return errors.New("chain.ReplaceChainFile: path and backup path are required")
	}
	if path == backupPath {
		return errors.New("chain.ReplaceChainFile: backup path must differ from source")
	}
	for i, blk := range blocks {
		if blk == nil {
			return fmt.Errorf("chain.ReplaceChainFile: nil block at index %d", i)
		}
		if want := computeBlockHash(blk); blk.Hash != want {
			return fmt.Errorf("chain.ReplaceChainFile: invalid hash at index %d height %d", i, blk.Height)
		}
		if i > 0 {
			prev := blocks[i-1]
			if blk.Height != prev.Height+1 || blk.PrevHash != prev.Hash {
				return fmt.Errorf("chain.ReplaceChainFile: broken continuity at index %d height %d", i, blk.Height)
			}
		}
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("chain.ReplaceChainFile: create parent: %w", err)
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".canonical-*.tmp")
	if err != nil {
		return fmt.Errorf("chain.ReplaceChainFile: create temp: %w", err)
	}
	tmpPath := tmp.Name()
	cleanupTemp := true
	defer func() {
		_ = tmp.Close()
		if cleanupTemp {
			_ = os.Remove(tmpPath)
		}
	}()
	writer := bufio.NewWriterSize(tmp, 256*1024)
	for i, blk := range blocks {
		data, marshalErr := json.Marshal(blk)
		if marshalErr != nil {
			return fmt.Errorf("chain.ReplaceChainFile: marshal index %d: %w", i, marshalErr)
		}
		if _, writeErr := writer.Write(append(data, '\n')); writeErr != nil {
			return fmt.Errorf("chain.ReplaceChainFile: write index %d: %w", i, writeErr)
		}
	}
	if err := writer.Flush(); err != nil {
		return fmt.Errorf("chain.ReplaceChainFile: flush: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("chain.ReplaceChainFile: sync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("chain.ReplaceChainFile: close: %w", err)
	}
	if err := os.Rename(path, backupPath); err != nil {
		return fmt.Errorf("chain.ReplaceChainFile: archive original: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Rename(backupPath, path)
		return fmt.Errorf("chain.ReplaceChainFile: install canonical journal: %w", err)
	}
	cleanupTemp = false
	return nil
}
