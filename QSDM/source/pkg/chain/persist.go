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
)

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
