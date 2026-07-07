package monitoring

// ngc_proof_persist.go: optional on-disk durability for the
// in-memory NGC attestation ring buffer (see ngc_proofs.go).
//
// Why this exists.
//
//	The `ngcProofs` slice in ngc_proofs.go is volatile: every
//	qsdm.service restart wipes it. That had a visible
//	consequence on 2026-05-11/12 — after the v0.3.2 deploy
//	(session 87) and again after the host-key-persist deploy
//	(session 89), `/api/v1/trust/attestations/summary.attested`
//	momentarily dropped to 0 because pre-restart bundles posted
//	to /api/v1/monitoring/ngc-proofs never survived the binary
//	swap. The blip cleared on the next sidecar tick (≤10 min
//	per sidecar, 2 sidecars) but the trust transparency
//	external probe enforces min_attested >= 2 and turned red
//	for ~8 min after the v0.3.2 deploy.
//
//	This file persists the ring to a JSONL log under the
//	configured state directory so the next boot can replay it.
//	Pre-restart bundles whose `timestamp_utc` is still inside
//	the freshness window (default 15 min) immediately
//	re-populate the summary, and the post-restart blip
//	disappears.
//
// Why JSONL (and not a structured store).
//
//	Same reasoning as pkg/mining/attest/recentrejects/persistence.go:
//	  - Trivial human inspection (`tail -f` works).
//	  - Crash recovery with corruption tolerance (the loader
//	    skips malformed lines instead of refusing to start).
//	  - Append-only durability with no schema-evolution
//	    surface — every record is self-describing.
//	  - Zero new dependencies.
//
// On-disk record shape (one JSON object per line).
//
//	{"received_at":"<RFC3339Nano>","raw":<the original bundle JSON>}
//
//	`raw` is preserved byte-equivalent to what the sidecar
//	posted (after JSON re-marshal, which is canonical). That
//	matters because dashboards / NGCProofDistinctByNodeID
//	parse the bundle fields directly; preserving the bundle
//	structure means restored entries look identical to live
//	entries.
//
// Bounded growth.
//
//	The in-memory ring caps at maxNGCProofEntries (32). The
//	JSONL persister applies the same soft cap: every N
//	appends trigger a compaction that keeps only the last N
//	records and atomic-renames the result onto the path.
//	Default cap matches the in-memory ring (32 records) so
//	a freshly-restarted node restores exactly the same set
//	of bundles it had pre-restart.
//
// Tests.
//
//	ngc_proof_persist_test.go covers: round-trip save +
//	restore preserves NGCProofDistinctByNodeID rows; corrupt
//	tail line is skipped on load; compaction caps the file at
//	softCap records; empty path is a no-op; nil/empty file
//	returns 0 records with no error.

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ngcPersistSoftCapDefault matches maxNGCProofEntries so the JSONL
// file's record count converges with the in-memory ring size after
// every compaction. Exported via SetNGCProofPersistPath's softCap
// argument for tests that want to drive compaction without writing
// 32 bundles.
const ngcPersistSoftCapDefault = maxNGCProofEntries

// ngcPersistedLine is the on-disk JSONL record shape. ReceivedAt is
// rendered as a string so a `cat` of the file is human-readable
// without a hex/binary decoder; Raw is kept as raw JSON so the
// bundle structure is preserved byte-equivalent to the original
// POST body.
type ngcPersistedLine struct {
	ReceivedAt string          `json:"received_at"`
	Raw        json.RawMessage `json:"raw"`
}

var (
	ngcPersistMu      sync.Mutex
	ngcPersistPath    string
	ngcPersistSoftCap int
	ngcPersistAppends int          // monotonic; reset by compactLocked
	ngcPersistOnDisk  atomic.Int64 // running record count for the dashboard gauge
	ngcPersistErrors  atomic.Uint64
)

// SetNGCProofPersistPath enables JSONL persistence of accepted NGC
// proof bundles to the given path. softCap controls how many
// records the on-disk file may grow to before a compaction trims
// it back; pass 0 for the default (matches the in-memory ring size,
// so restart restores the exact in-memory state).
//
// An empty path DISABLES persistence (legacy in-memory-only
// behaviour). Calling SetNGCProofPersistPath("") at runtime stops
// further Appends but leaves the existing file on disk untouched.
//
// Returns an error only when the path is unreachable (parent
// directory missing or unwritable). A missing file is not an error
// — it is created lazily on first Append. The caller is expected
// to invoke RestoreNGCProofsFromDisk() exactly once after this
// function returns, before any RecordNGCProof* calls fire (i.e.
// before the API server binds), so the in-memory ring is populated
// from disk first and the persisted file is not cleared by an
// early compaction over an empty ring.
func SetNGCProofPersistPath(path string, softCap int) error {
	ngcPersistMu.Lock()
	defer ngcPersistMu.Unlock()

	path = strings.TrimSpace(path)
	if path == "" {
		ngcPersistPath = ""
		ngcPersistSoftCap = 0
		ngcPersistAppends = 0
		ngcPersistOnDisk.Store(0)
		return nil
	}

	parent := filepath.Dir(path)
	if pinfo, perr := os.Stat(parent); perr != nil {
		return fmt.Errorf("ngc proof persist: parent directory %q does not exist (create it before starting the node): %w", parent, perr)
	} else if !pinfo.IsDir() {
		return fmt.Errorf("ngc proof persist: parent %q is not a directory", parent)
	}

	// Touch the file with restrictive permissions so a subsequent
	// O_APPEND open does not race a permissive default. Done once
	// at configuration time so the hot path skips the chmod
	// syscall.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600) // #nosec G304 -- path is internal persistence configuration.
	if err != nil {
		return fmt.Errorf("ngc proof persist: open %q: %w", path, err)
	}
	if cerr := f.Close(); cerr != nil {
		return fmt.Errorf("ngc proof persist: close %q: %w", path, cerr)
	}

	if softCap <= 0 {
		softCap = ngcPersistSoftCapDefault
	}
	ngcPersistPath = path
	ngcPersistSoftCap = softCap
	ngcPersistAppends = 0

	// Seed the on-disk gauge so it reflects the file's pre-existing
	// state at boot rather than zero until the first ingest.
	if recs, lerr := loadNGCProofsFromDiskLocked(); lerr == nil {
		ngcPersistOnDisk.Store(int64(len(recs)))
	}
	return nil
}

// NGCProofPersistPath returns the currently-configured path or ""
// when persistence is disabled.
func NGCProofPersistPath() string {
	ngcPersistMu.Lock()
	defer ngcPersistMu.Unlock()
	return ngcPersistPath
}

// NGCProofPersistRecordsOnDisk returns the running count of
// records in the JSONL file. Exported for dashboards and for
// tests that want to assert compaction kept the file bounded.
func NGCProofPersistRecordsOnDisk() int64 {
	return ngcPersistOnDisk.Load()
}

// NGCProofPersistErrors returns a running total of filesystem
// errors observed while persisting ingest bundles. Exported for
// dashboards and tests; bumped by appendNGCProofToDisk on any
// open / write / close failure.
func NGCProofPersistErrors() uint64 {
	return ngcPersistErrors.Load()
}

// ResetNGCProofPersistForTest clears the persister state. Used
// alongside ResetNGCProofsForTest by package tests that exercise
// the disk path in isolation.
func ResetNGCProofPersistForTest() {
	ngcPersistMu.Lock()
	defer ngcPersistMu.Unlock()
	ngcPersistPath = ""
	ngcPersistSoftCap = 0
	ngcPersistAppends = 0
	ngcPersistOnDisk.Store(0)
	ngcPersistErrors.Store(0)
}

// RestoreNGCProofsFromDisk replays the JSONL file at the configured
// path into the in-memory ring. Returns the number of records
// successfully restored. Lines that fail to parse as JSON are
// skipped (the corruption-tolerant default — typically a partially-
// written tail after a hard kill).
//
// Safe to call when persistence is disabled (returns (0, nil)).
// Safe to call when the file does not yet exist (returns (0, nil)).
func RestoreNGCProofsFromDisk() (int, error) {
	ngcPersistMu.Lock()
	defer ngcPersistMu.Unlock()
	if ngcPersistPath == "" {
		return 0, nil
	}

	recs, err := loadNGCProofsFromDiskLocked()
	if err != nil {
		return 0, err
	}
	if len(recs) == 0 {
		return 0, nil
	}

	ngcMu.Lock()
	defer ngcMu.Unlock()
	for _, r := range recs {
		var receivedAt time.Time
		if r.ReceivedAt != "" {
			if t, perr := time.Parse(time.RFC3339Nano, r.ReceivedAt); perr == nil {
				receivedAt = t.UTC()
			}
		}
		if receivedAt.IsZero() {
			receivedAt = time.Now().UTC()
		}
		// Trim or skip if the existing ring would overflow. The
		// JSONL file might have more than maxNGCProofEntries
		// records temporarily (between compaction triggers); we
		// keep only the trailing maxNGCProofEntries so the in-
		// memory state matches the pre-restart steady-state.
		ngcProofs = append(ngcProofs, ngcStoredProof{ReceivedAt: receivedAt, Raw: r.Raw})
	}
	if len(ngcProofs) > maxNGCProofEntries {
		ngcProofs = ngcProofs[len(ngcProofs)-maxNGCProofEntries:]
	}
	return len(recs), nil
}

// loadNGCProofsFromDiskLocked reads every JSONL record from the
// configured path. Caller must hold ngcPersistMu. Lines that fail
// to parse are skipped (corruption-tolerant).
//
// Returns an empty slice (not an error) when the file does not
// exist or persistence is disabled.
func loadNGCProofsFromDiskLocked() ([]ngcPersistedLine, error) {
	if ngcPersistPath == "" {
		return nil, nil
	}
	f, err := os.Open(ngcPersistPath) // #nosec G304 -- process-private persistence path set during trusted startup.
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("ngc proof persist: open %q for read: %w", ngcPersistPath, err)
	}
	defer f.Close()

	var out []ngcPersistedLine
	sc := bufio.NewScanner(f)
	// 1 MiB max line — well above the 512 KiB max bundle size in
	// ngc_proofs.go::maxNGCProofBytes, and large enough that the
	// scanner won't truncate a real record but small enough that
	// a corrupt huge line gets skipped via the JSON-unmarshal
	// error rather than via a Scanner buffer overflow that
	// aborts the whole load.
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for sc.Scan() {
		raw := sc.Bytes()
		if len(raw) == 0 {
			continue
		}
		var line ngcPersistedLine
		if err := json.Unmarshal(raw, &line); err != nil {
			// Skip the malformed line and keep loading.
			continue
		}
		if len(line.Raw) == 0 {
			continue
		}
		out = append(out, line)
	}
	if err := sc.Err(); err != nil {
		return out, fmt.Errorf("ngc proof persist: scan %q: %w", ngcPersistPath, err)
	}
	return out, nil
}

// appendNGCProofToDisk durably-or-best-effortly persists one
// bundle. Caller must hold ngcMu (this function is called from
// appendNGCProofRawLocked). Failures are recorded in the
// ngcPersistErrors counter but do NOT propagate to the caller —
// the in-memory ring update has already succeeded, and we never
// want a filesystem hiccup to block ingest.
//
// Compaction runs in-band: after every softCap successful Appends
// we read the file back, keep the last softCap records, and
// atomic-rename a fresh copy onto the path.
func appendNGCProofToDisk(entry ngcStoredProof) {
	ngcPersistMu.Lock()
	defer ngcPersistMu.Unlock()
	if ngcPersistPath == "" {
		return
	}

	line := ngcPersistedLine{
		ReceivedAt: entry.ReceivedAt.UTC().Format(time.RFC3339Nano),
		Raw:        entry.Raw,
	}
	buf, err := json.Marshal(line)
	if err != nil {
		ngcPersistErrors.Add(1)
		return
	}

	f, err := os.OpenFile(ngcPersistPath, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0o600) // #nosec G304 -- process-private persistence path set during trusted startup.
	if err != nil {
		ngcPersistErrors.Add(1)
		return
	}

	prefixNewline, perr := ngcPersistNeedsNewlinePrefix(f)
	if perr != nil {
		_ = f.Close()
		ngcPersistErrors.Add(1)
		return
	}
	out := make([]byte, 0, len(buf)+2)
	if prefixNewline {
		out = append(out, '\n')
	}
	out = append(out, buf...)
	out = append(out, '\n')

	if _, werr := f.Write(out); werr != nil {
		_ = f.Close()
		ngcPersistErrors.Add(1)
		return
	}
	if cerr := f.Close(); cerr != nil {
		ngcPersistErrors.Add(1)
		return
	}

	ngcPersistAppends++
	ngcPersistOnDisk.Add(1)

	if ngcPersistAppends >= ngcPersistSoftCap {
		if cerr := compactNGCProofsOnDiskLocked(); cerr == nil {
			ngcPersistAppends = 0
		} else {
			ngcPersistErrors.Add(1)
		}
	}
}

// ngcPersistNeedsNewlinePrefix mirrors the recentrejects
// partial-write defence: if the previous Append crashed mid-write,
// the file's tail byte is not a newline. Prepending one before
// this record stops the corrupt fragment from running together
// with the new record on the same line — the bufio.Scanner in
// loadNGCProofsFromDiskLocked then drops the corrupt half-line
// in isolation, preserving every valid record before AND after
// the crash boundary.
func ngcPersistNeedsNewlinePrefix(f *os.File) (bool, error) {
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

// compactNGCProofsOnDiskLocked rewrites the file to keep only the
// last ngcPersistSoftCap records. Caller must hold ngcPersistMu.
// The compaction is atomic: write to <path>.tmp, fsync-on-close,
// rename onto <path>. A failure at any step leaves the original
// file untouched.
func compactNGCProofsOnDiskLocked() error {
	recs, err := loadNGCProofsFromDiskLocked()
	if err != nil {
		return err
	}
	if len(recs) <= ngcPersistSoftCap {
		return nil
	}
	keep := recs[len(recs)-ngcPersistSoftCap:]

	tmp := ngcPersistPath + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600) // #nosec G304 -- tmp derives only from the trusted persistence path.
	if err != nil {
		return fmt.Errorf("ngc proof persist: create tmp %q: %w", tmp, err)
	}
	bw := bufio.NewWriter(f)
	for _, r := range keep {
		buf, merr := json.Marshal(r)
		if merr != nil {
			_ = f.Close()
			_ = os.Remove(tmp)
			return fmt.Errorf("ngc proof persist: marshal during compact: %w", merr)
		}
		if _, werr := bw.Write(buf); werr != nil {
			_ = f.Close()
			_ = os.Remove(tmp)
			return fmt.Errorf("ngc proof persist: write tmp: %w", werr)
		}
		if werr := bw.WriteByte('\n'); werr != nil {
			_ = f.Close()
			_ = os.Remove(tmp)
			return fmt.Errorf("ngc proof persist: newline tmp: %w", werr)
		}
	}
	if ferr := bw.Flush(); ferr != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("ngc proof persist: flush tmp: %w", ferr)
	}
	if cerr := f.Close(); cerr != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("ngc proof persist: close tmp: %w", cerr)
	}
	if rerr := os.Rename(tmp, ngcPersistPath); rerr != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("ngc proof persist: rename %q -> %q: %w", tmp, ngcPersistPath, rerr)
	}
	ngcPersistOnDisk.Store(int64(len(keep)))
	return nil
}
