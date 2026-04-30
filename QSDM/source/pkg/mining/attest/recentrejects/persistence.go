package recentrejects

// persistence.go: optional on-disk durability for the §4.6
// rejection ring.
//
// Why this exists:
//
//	The package-level recentrejects.go ring is volatile by
//	design — the package doc lists "Persistence: the ring is
//	volatile; restart wipes it. A future on-disk implementation
//	can plug behind the same RejectionRecorder interface in
//	pkg/mining without changing the handler." This file is
//	exactly that future implementation, plugged in via a
//	per-Store Persister hook so:
//
//	  - Pure unit tests of recentrejects keep the in-memory-only
//	    posture (no filesystem dependency).
//	  - Production binaries get durable forensic records by
//	    setting Config.RecentRejectionsPath in v2wiring.
//	  - Tests can inject a fake persister to drive failure
//	    paths (full disk, corrupt file, etc.).
//
// Why JSONL and not a structured store (sqlite/scylla):
//
//	The ring stores *operator-facing* telemetry, not consensus
//	state. JSONL gives us:
//	  - Trivial human inspection (`tail -f` works).
//	  - Crash-recovery with corruption tolerance (the loader
//	    skips malformed lines instead of refusing to start).
//	  - Append-only durability with no schema evolution
//	    surface — every record is self-describing.
//	  - Zero new dependencies.
//
//	A future SQLite-backed Persister can plug in behind the
//	same interface without touching Store callers.
//
// Bounded growth:
//
//	A naive append-only log grows without limit and a malicious
//	miner spamming forged proofs could fill the disk in days.
//	FilePersister enforces a soft cap by triggering compaction
//	(read-all, keep-last-N, atomic-rename rewrite) every
//	`softCap` appends. The 2x watermark keeps the file bounded
//	at [softCap, 2*softCap-1] records, ≈ [256 KiB, 512 KiB] at
//	the default DefaultMaxRejections.

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
)

// ErrHardCapExceeded is the sentinel returned by
// FilePersister.Append when admitting the record would push
// the JSONL file past the configured hard byte ceiling AND
// a salvage compaction failed to free enough headroom. The
// in-memory ring is UNAFFECTED — Store.Record always appends
// in-memory, so the volatile operator surface (dashboard
// tile, /api/v1/attest/recent-rejections) stays accurate;
// only the durable on-disk record is dropped.
//
// Operators distinguish "transient I/O failure" (any other
// error) from "validator is being actively flooded" (this
// sentinel) via errors.Is, and the
// qsdm_attest_rejection_persist_hardcap_drops_total counter
// fires regardless of which call site sees the error so the
// alert surface is independent of caller-side handling.
var ErrHardCapExceeded = errors.New("recentrejects: persist hard cap exceeded")

// Persister is the on-disk hook for the volatile in-memory
// ring. NewStore takes nil by default (the original posture);
// production wiring (internal/v2wiring.Wire()) installs a
// FilePersister when the operator configures
// RecentRejectionsPath.
//
// The interface is narrow for the same reason
// MetricsRecorder is narrow: tests can inject a fake without
// importing a filesystem helper, and a future
// SQLite-or-whatever backend slots in without changing
// either the Store contract or the Wire() surface.
//
// Implementations MUST be safe for concurrent use — Store
// holds its own write lock during Append, but the persister
// is also read directly by RestoreFromPersister at boot
// before any Append fires.
type Persister interface {
	// Append durably-or-best-effortly records one rejection.
	// Implementations decide their own durability story; the
	// FilePersister below opens the file in append mode for
	// each record (one write+sync per Append) accepting that
	// a hard kill loses at most the in-flight write.
	Append(Rejection) error

	// LoadAll returns all persisted records in append order.
	// Implementations MUST tolerate corrupt lines (typically
	// a partially-written tail after a hard kill) by skipping
	// them rather than refusing to load — the operator's
	// alerting will already have surfaced the original crash.
	LoadAll() ([]Rejection, error)

	// Close releases any handle. Idempotent. Safe to call on
	// a nil receiver via the noopPersister default; production
	// callers (Wire) own the explicit Close lifecycle.
	Close() error
}

// noopPersister is the package-default. Methods are
// structural no-ops so the Store.Record() hot path does not
// pay a syscall when persistence is not wired. This is the
// recommended default for unit tests of recentrejects (see
// recentrejects_test.go which assumes ring-only behaviour).
type noopPersister struct{}

func (noopPersister) Append(Rejection) error        { return nil }
func (noopPersister) LoadAll() ([]Rejection, error) { return nil, nil }
func (noopPersister) Close() error                  { return nil }

// IsNoopPersister reports whether p is the package-default
// no-op (or nil). Used by Store.RestoreFromPersister to
// short-circuit a load when no real persister is wired —
// without this check we would incur a pointless interface
// dispatch on every boot.
//
// Exported so v2wiring tests can assert "Wire() did not
// install a real persister when path is empty".
func IsNoopPersister(p Persister) bool {
	if p == nil {
		return true
	}
	_, ok := p.(noopPersister)
	return ok
}

// FilePersister is the production on-disk persister. Records
// are appended as one JSON object per line ("JSONL"), and
// the file is bounded by a soft cap that triggers an in-place
// compaction when crossed. See package doc above for the
// design rationale.
//
// Each Append opens the file in O_APPEND mode for a single
// write+close cycle. This is ~10us of syscall overhead per
// rejection — at a realistic max rejection rate of 100/s
// that's 0.1% CPU, negligible against the rejection-handling
// cost on the verifier hot path.
type FilePersister struct {
	path    string
	mu      sync.Mutex
	softCap int

	// appendsSinceCompact is the watermark counter. Zeroed by
	// the constructor, incremented on every successful
	// Append, reset to 0 after a successful compaction. The
	// counter is a soft signal: if it's wrong (e.g. the
	// process restarted before compaction triggered), the
	// next Append after softCap-many calls will compact.
	appendsSinceCompact int

	// recordsOnDisk is the running count of records in the
	// JSONL file. Seeded by the constructor (a one-shot scan
	// of the existing file), incremented on every successful
	// Append, reset to len(keep) after every successful
	// compaction. Used to back the
	// qsdm_attest_rejection_persist_records_on_disk gauge in
	// pkg/monitoring via notePersistRecordsOnDisk.
	//
	// atomic so RecordsOnDisk() is lock-free; mutations
	// happen under p.mu in Append/compactLocked which
	// serialises the read-modify-write naturally.
	recordsOnDisk atomic.Uint64

	// maxBytes is the OPTIONAL hard ceiling on the JSONL
	// file's on-disk size in bytes. Zero (the default)
	// disables the check entirely — backwards-compatible with
	// every existing caller. When > 0, Append refuses to
	// write a record that would push currentSize+lineSize
	// past the cap AFTER attempting one in-band salvage
	// compaction; the refusal returns ErrHardCapExceeded and
	// fires notePersistHardCapDrop so the
	// qsdm_attest_rejection_persist_hardcap_drops_total
	// counter increments.
	//
	// Why bytes (not records): operators tune this against
	// disk-quota / log-rotate budgets, which are byte-shaped
	// constraints. A record-shaped cap would leak the
	// internal record-size assumption and force a
	// re-tune on every schema change.
	//
	// Read/write under p.mu — atomicity isn't required
	// because the hot-path read happens inside Append's
	// critical section, and the setter is called once at
	// boot before any Append fires (in production wiring).
	maxBytes int64
}

// DefaultPersistSoftCap is the per-file rejection record cap
// used when NewFilePersister is called with softCap <= 0.
// Sized to match the in-memory ring's DefaultMaxRejections
// so a freshly-restarted node restores exactly the same set
// of records it had before the crash.
const DefaultPersistSoftCap = DefaultMaxRejections

// NewFilePersister opens or creates the JSONL file at path.
// softCap controls the on-disk record cap before compaction
// fires (clamped to DefaultPersistSoftCap when <= 0).
//
// The returned persister can be installed on a Store via
// Store.SetPersister; Store.RestoreFromPersister then
// replays the file's contents into the in-memory ring.
//
// Returns an error only when the path is unreachable
// (permission denied, directory missing) — a missing file
// is not an error and is created lazily on first Append.
func NewFilePersister(path string, softCap int) (*FilePersister, error) {
	if path == "" {
		return nil, errors.New("recentrejects: FilePersister path is empty")
	}
	if softCap <= 0 {
		softCap = DefaultPersistSoftCap
	}
	// Touch the file with restrictive permissions so a
	// subsequent Append's O_APPEND open does not race a
	// permissive default. Done once at construction so the
	// hot path skips the permission-set syscall.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("recentrejects: open %q: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("recentrejects: close %q: %w", path, err)
	}
	p := &FilePersister{path: path, softCap: softCap}

	// Count existing records (best-effort) so the on-disk
	// records gauge starts with an accurate value at boot,
	// not a flat zero until the first Append. This is a
	// one-shot read of <= softCap*recordSize bytes; for the
	// 1024-record default that's well under 1 MiB and
	// completes in a few ms even on a cold disk.
	//
	// Errors are swallowed: a corrupt file means we boot
	// with a stale gauge but keep operating; the next
	// Append will tick the count from whatever value we
	// seeded, drifting only by the size of the corruption
	// we couldn't parse.
	if recs, err := p.loadAllLocked(); err == nil {
		n := uint64(len(recs))
		p.recordsOnDisk.Store(n)
		notePersistRecordsOnDisk(n)
	}

	return p, nil
}

// RecordsOnDisk returns the FilePersister's running count of
// records in the JSONL file. Atomic / lock-free; the value
// is approximate during a concurrent Append (the increment
// races the read by ≤ 1) but stabilises within microseconds.
//
// Exported for tests and for in-process consumers that want
// the gauge value without going through the metrics adapter
// (the dashboard tile reads the metrics adapter; the
// persistence test reads this directly).
func (p *FilePersister) RecordsOnDisk() uint64 {
	if p == nil {
		return 0
	}
	return p.recordsOnDisk.Load()
}

// Path returns the underlying file path. Used by tests and
// by the v2wiring smoke test to verify the persister was
// installed at the expected location.
func (p *FilePersister) Path() string {
	if p == nil {
		return ""
	}
	return p.path
}

// SoftCap returns the configured soft cap (records before
// compaction fires).
func (p *FilePersister) SoftCap() int {
	if p == nil {
		return 0
	}
	return p.softCap
}

// SetMaxBytes installs the hard byte ceiling for the JSONL
// file. n <= 0 disables the check (the construction default).
//
// Intended to be called ONCE at boot from the wiring layer
// (internal/v2wiring.Wire when cfg.RecentRejectionsMaxBytes
// is set). Calling at runtime is safe — the next Append's
// cap evaluation will pick up the new value — but that is
// an unusual posture; operators typically rotate the value
// via config-reload + restart, not hot-update.
//
// Why a setter (rather than a constructor parameter): the
// existing NewFilePersister signature is consumed by tests
// that have no opinion on the cap; threading a third
// parameter through every test would expand the diff
// without serving the test. The setter keeps construction
// minimal AND lets v2wiring opt in explicitly with one
// extra line.
func (p *FilePersister) SetMaxBytes(n int64) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if n < 0 {
		n = 0
	}
	p.maxBytes = n
}

// MaxBytes returns the configured hard byte ceiling, or 0
// when the cap is disabled. Read under p.mu so a concurrent
// SetMaxBytes cannot tear the value across word boundaries
// on 32-bit platforms.
func (p *FilePersister) MaxBytes() int64 {
	if p == nil {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.maxBytes
}

// Append serialises rec to JSON and appends it to the file
// as one line. Triggers compaction when the post-append
// counter reaches softCap.
//
// Crash-recovery framing: BEFORE writing the record's
// `<json>\n` payload, Append checks the file's last byte.
// If the file is non-empty AND the previous byte is NOT a
// newline (the signature of a partial-write tail from a
// prior crash), Append prepends an extra `\n` so the corrupt
// fragment cannot run together with this record on the same
// line. The bufio.Scanner in LoadAll then skips the corrupt
// line in isolation, preserving every valid record before
// AND after the crash boundary.
//
// Returns an error on any filesystem failure; callers are
// expected to surface this via their preferred logging
// channel (Wire() wraps it in cfg.LogRecentRejectionsError).
// A persistence failure does NOT prevent the in-memory ring
// from receiving the record — Store.Record always appends
// in-memory regardless of the persister's success.
func (p *FilePersister) Append(rec Rejection) error {
	if p == nil {
		return errors.New("recentrejects: nil FilePersister")
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	line, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("recentrejects: marshal: %w", err)
	}

	// Hard-cap enforcement (skipped when maxBytes <= 0).
	// Worst-case admit cost is len(line) + 2 bytes (one
	// optional partial-write framing newline + one record
	// terminator newline). Use the worst case for the cap
	// check so a marginal record can't sneak past with
	// sub-byte precision games.
	if p.maxBytes > 0 {
		admitCost := int64(len(line)) + 2
		size, sizeErr := p.currentSizeLocked()
		if sizeErr != nil {
			return fmt.Errorf("recentrejects: stat for hard-cap: %w", sizeErr)
		}
		if size+admitCost > p.maxBytes {
			// Try one in-band salvage compaction. If the
			// soft-cap loop has been keeping pace this is
			// almost certainly redundant; if a flood is
			// actively outrunning the soft cap (which is
			// THE scenario the hard cap exists for) the
			// compaction will trim the head and free
			// enough headroom to admit the next record.
			if cerr := p.compactLocked(); cerr == nil {
				p.appendsSinceCompact = 0
				size, sizeErr = p.currentSizeLocked()
				if sizeErr != nil {
					return fmt.Errorf("recentrejects: stat after salvage: %w", sizeErr)
				}
			}
			if size+admitCost > p.maxBytes {
				// Even after a salvage compaction we cannot
				// admit this record without breaching the
				// cap. Drop it and fire telemetry so the
				// alert pipeline catches the flood.
				notePersistHardCapDrop(int(admitCost))
				return ErrHardCapExceeded
			}
		}
	}

	// Open O_RDWR (not O_WRONLY) so we can read the last
	// byte for the partial-write defence. O_APPEND ensures
	// our Write lands at end-of-file regardless of seek
	// position (POSIX-atomic up to PIPE_BUF on Linux; on
	// Windows the behaviour is similarly end-relative).
	f, err := os.OpenFile(p.path, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("recentrejects: open %q for append: %w", p.path, err)
	}

	prefixNewline, err := needsNewlinePrefix(f)
	if err != nil {
		_ = f.Close()
		return fmt.Errorf("recentrejects: tail check %q: %w", p.path, err)
	}

	// Compose the write in a single buffer so the kernel
	// sees one contiguous append (cheaper than two writes,
	// and the partial-write window for our own record is
	// reduced to a single Write call).
	out := make([]byte, 0, len(line)+2)
	if prefixNewline {
		out = append(out, '\n')
	}
	out = append(out, line...)
	out = append(out, '\n')

	if _, werr := f.Write(out); werr != nil {
		_ = f.Close()
		return fmt.Errorf("recentrejects: write %q: %w", p.path, werr)
	}
	if cerr := f.Close(); cerr != nil {
		return fmt.Errorf("recentrejects: close %q: %w", p.path, cerr)
	}

	// Successful append: bump both the watermark counter
	// (drives compaction) and the records-on-disk gauge
	// (drives the dashboard / Prometheus surfaces). The
	// gauge update goes through the metrics adapter chain,
	// which is one atomic.Load + one type-assertion +
	// one atomic.Store — well under a microsecond.
	p.appendsSinceCompact++
	newCount := p.recordsOnDisk.Add(1)
	notePersistRecordsOnDisk(newCount)

	if p.appendsSinceCompact >= p.softCap {
		if err := p.compactLocked(); err != nil {
			// Compaction failure is non-fatal for the next
			// Append (we've already written this record); the
			// next call after another softCap-1 appends will
			// retry. Surface to the caller so an alerting
			// pipeline can notice repeated failures.
			return fmt.Errorf("recentrejects: compact: %w", err)
		}
		p.appendsSinceCompact = 0
	}
	return nil
}

// needsNewlinePrefix reports whether the next Append should
// insert a leading '\n' to separate itself from a possibly-
// partial previous write. Returns false on an empty file
// (no prior content to separate from).
func needsNewlinePrefix(f *os.File) (bool, error) {
	info, err := f.Stat()
	if err != nil {
		return false, err
	}
	if info.Size() == 0 {
		return false, nil
	}
	last := make([]byte, 1)
	if _, err := f.ReadAt(last, info.Size()-1); err != nil {
		return false, err
	}
	return last[0] != '\n', nil
}

// LoadAll reads every record in the file in append order.
// Lines that fail to parse as JSON are skipped (the
// corruption-tolerant default — typical cause is a partially-
// written tail after a hard kill).
//
// Returns an empty slice (not an error) when the file does
// not exist; the constructor creates it eagerly so this
// path triggers only when the file is removed under us.
func (p *FilePersister) LoadAll() ([]Rejection, error) {
	if p == nil {
		return nil, errors.New("recentrejects: nil FilePersister")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.loadAllLocked()
}

// currentSizeLocked stat-reports the JSONL file's size in
// bytes. ENOENT (file not yet created — a fresh persister
// before the first Append) returns 0 with no error so the
// cap check on a brand-new file always passes the
// "size + admitCost > cap" branch correctly. Caller must
// hold p.mu.
func (p *FilePersister) currentSizeLocked() (int64, error) {
	info, err := os.Stat(p.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	return info.Size(), nil
}

func (p *FilePersister) loadAllLocked() ([]Rejection, error) {
	f, err := os.Open(p.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("recentrejects: open %q for read: %w", p.path, err)
	}
	defer f.Close()

	var out []Rejection
	sc := bufio.NewScanner(f)
	// 64 KiB max line: the in-memory truncation caps GPUName
	// + CertSubject at 256 runes each and Detail at 200 — a
	// well-formed record is well under 2 KiB. The 64 KiB
	// ceiling defends against a corrupt huge-line scenario
	// where the file is missing newlines (and the malformed
	// "line" gets skipped via the JSON-unmarshal error).
	sc.Buffer(make([]byte, 0, 4096), 64*1024)

	for sc.Scan() {
		raw := sc.Bytes()
		if len(raw) == 0 {
			continue
		}
		var r Rejection
		if err := json.Unmarshal(raw, &r); err != nil {
			// Defensive: skip the malformed line and keep
			// loading. A partially-written record at the
			// tail of a crashed log is the most common case.
			continue
		}
		out = append(out, r)
	}
	if err := sc.Err(); err != nil {
		return out, fmt.Errorf("recentrejects: scan %q: %w", p.path, err)
	}
	return out, nil
}

// Close is a no-op — FilePersister opens the file fresh on
// each Append and closes it after every write, so there is
// no persistent handle to release. The method exists to
// satisfy the Persister interface and to give v2wiring a
// stable shutdown surface in case a future implementation
// (e.g. a buffered writer with periodic flush) needs it.
func (p *FilePersister) Close() error { return nil }

// compactLocked rewrites the file, retaining only the most
// recent softCap records. Called from Append while holding
// p.mu so the rewrite race with concurrent Appends is
// impossible by construction.
//
// Strategy:
//
//  1. Read all records via loadAllLocked.
//  2. If <= softCap, do nothing (file already bounded).
//  3. Slice to keep last softCap.
//  4. Write to <path>.tmp.
//  5. Atomic rename onto <path>.
//
// On failure at any step, the original file is unchanged;
// the next Append re-attempts compaction after another
// softCap appends.
func (p *FilePersister) compactLocked() error {
	recs, err := p.loadAllLocked()
	if err != nil {
		return err
	}
	if len(recs) <= p.softCap {
		return nil
	}
	keep := recs[len(recs)-p.softCap:]

	tmp := p.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("recentrejects: create tmp %q: %w", tmp, err)
	}
	bw := bufio.NewWriter(f)
	for _, r := range keep {
		line, err := json.Marshal(r)
		if err != nil {
			_ = f.Close()
			_ = os.Remove(tmp)
			return fmt.Errorf("recentrejects: marshal during compact: %w", err)
		}
		if _, err := bw.Write(line); err != nil {
			_ = f.Close()
			_ = os.Remove(tmp)
			return fmt.Errorf("recentrejects: write tmp: %w", err)
		}
		if err := bw.WriteByte('\n'); err != nil {
			_ = f.Close()
			_ = os.Remove(tmp)
			return fmt.Errorf("recentrejects: newline tmp: %w", err)
		}
	}
	if err := bw.Flush(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("recentrejects: flush tmp: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("recentrejects: close tmp: %w", err)
	}
	if err := os.Rename(tmp, p.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("recentrejects: rename %q -> %q: %w", tmp, p.path, err)
	}

	// Compaction succeeded: file now has exactly len(keep)
	// records. Update the running gauge and emit two
	// observations to the metrics adapter — one for the
	// compactions counter (rate-of-compactions alert), one
	// for the records-on-disk gauge so the dashboard tile
	// reflects the post-compaction size on the next scrape.
	post := uint64(len(keep))
	p.recordsOnDisk.Store(post)
	notePersistCompaction(len(keep))
	notePersistRecordsOnDisk(post)
	return nil
}
