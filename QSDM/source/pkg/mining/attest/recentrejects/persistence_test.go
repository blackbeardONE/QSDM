package recentrejects

// persistence_test.go: unit coverage for the on-disk
// FilePersister + Store.SetPersister / RestoreFromPersister
// integration. Test design mirrors metrics_test.go:
//
//   - Each test owns its own tempdir + Store (no globals).
//   - Tests reset the package-level metrics recorder on
//     Cleanup so a stray PersistErrorRecorder hookup from
//     pkg/monitoring's init() does not leak into a parallel
//     test's expectations.
//   - Concurrency tests use t.Parallel + a worker pool to
//     stress the mu lock; no race detector trickery beyond
//     `go test -race`.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fixedNow returns a deterministic time so RecordedAt is
// stable across test runs (the JSON round-trip is sensitive
// to monotonic-clock stripping otherwise).
func fixedNow() func() time.Time {
	t := time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)
	return func() time.Time { return t }
}

// newFilePersisterT is a tiny tempdir helper: creates a fresh
// directory, returns the persister + path. Cleanup removes
// the dir.
func newFilePersisterT(t *testing.T, softCap int) (*FilePersister, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "recentrejects.jsonl")
	fp, err := NewFilePersister(path, softCap)
	if err != nil {
		t.Fatalf("NewFilePersister: %v", err)
	}
	t.Cleanup(func() { _ = fp.Close() })
	return fp, path
}

// TestFilePersister_AppendThenLoadRoundTrip — the basic
// happy path: append three records, LoadAll returns them in
// append order with full field fidelity.
func TestFilePersister_AppendThenLoadRoundTrip(t *testing.T) {
	t.Parallel()
	fp, _ := newFilePersisterT(t, 0)

	want := []Rejection{
		{Seq: 1, RecordedAt: time.Date(2026, 4, 29, 9, 0, 0, 0, time.UTC), Kind: KindArchSpoofUnknown, Reason: "unknown_arch", Arch: "rubin", Height: 100, MinerAddr: "qsdm1abc"},
		{Seq: 2, RecordedAt: time.Date(2026, 4, 29, 9, 1, 0, 0, time.UTC), Kind: KindArchSpoofGPUNameMismatch, Reason: "gpu_name_mismatch", Arch: "hopper", Height: 101, MinerAddr: "qsdm1def", GPUName: "NVIDIA H100 80GB HBM3"},
		{Seq: 3, RecordedAt: time.Date(2026, 4, 29, 9, 2, 0, 0, time.UTC), Kind: KindHashrateOutOfBand, Arch: "blackwell", Height: 102, Detail: "claimed=2e15 band=[1e14,1e15]"},
	}
	for _, r := range want {
		if err := fp.Append(r); err != nil {
			t.Fatalf("Append %d: %v", r.Seq, err)
		}
	}
	got, err := fp.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("LoadAll len: got %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Seq != want[i].Seq || got[i].Kind != want[i].Kind || got[i].Arch != want[i].Arch {
			t.Errorf("record %d mismatch:\n got %+v\nwant %+v", i, got[i], want[i])
		}
	}
}

// TestFilePersister_LoadAll_NonExistentFileIsEmpty —
// removing the file under us must yield (nil, nil), not an
// error. Common case: the operator manually rotates / wipes
// the log and we should boot cleanly with an empty ring.
func TestFilePersister_LoadAll_NonExistentFileIsEmpty(t *testing.T) {
	t.Parallel()
	fp, path := newFilePersisterT(t, 0)
	if err := os.Remove(path); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	got, err := fp.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll on missing file: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 records, got %d", len(got))
	}
}

// TestFilePersister_LoadAll_SkipsCorruptLines mirrors the
// real-world scenario: the validator was hard-killed
// mid-write. The tail of the file has a partially-written
// record (no closing brace). LoadAll must tolerate this.
func TestFilePersister_LoadAll_SkipsCorruptLines(t *testing.T) {
	t.Parallel()
	fp, path := newFilePersisterT(t, 0)
	if err := fp.Append(Rejection{Seq: 1, Kind: KindArchSpoofUnknown}); err != nil {
		t.Fatalf("Append1: %v", err)
	}
	// Inject corruption between the valid line and the next.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := f.WriteString("{\"this is not valid json"); err != nil {
		_ = f.Close()
		t.Fatalf("write garbage: %v", err)
	}
	// IMPORTANT: NO trailing newline — the corruption is the
	// last "line" of the file, exactly the partial-write
	// shape a hard kill would produce.
	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := fp.Append(Rejection{Seq: 2, Kind: KindHashrateOutOfBand}); err != nil {
		t.Fatalf("Append2: %v", err)
	}

	got, err := fp.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 valid records, got %d: %+v", len(got), got)
	}
	if got[0].Seq != 1 || got[1].Seq != 2 {
		t.Errorf("Seq order wrong: got %v / %v", got[0].Seq, got[1].Seq)
	}
}

// TestFilePersister_CompactionTriggersAtSoftCap drives the
// soft-cap compaction path: append softCap+extra records,
// verify the on-disk file has exactly softCap records and
// they are the most recent softCap (not the oldest).
func TestFilePersister_CompactionTriggersAtSoftCap(t *testing.T) {
	t.Parallel()
	fp, path := newFilePersisterT(t, 5) // tight cap for the test

	// 8 appends → after the 5th, compaction fires (we set
	// softCap=5, so appendsSinceCompact reaches 5 at the
	// 5th call). The first 5 records remain, then 3 more
	// appends bring the on-disk count to 8 again — but the
	// next compaction will only fire at appendsSinceCompact
	// >= 5, i.e. after 10 cumulative appends. So on disk
	// we expect 5 records (post-first-compaction) + 3
	// appended after = 8 records.
	for i := uint64(1); i <= 8; i++ {
		if err := fp.Append(Rejection{Seq: i, Kind: KindArchSpoofUnknown, Reason: fmt.Sprintf("r%d", i)}); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}
	got, err := fp.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	// We expect compaction to have fired exactly once (at
	// append #5), trimming 0 records (file had 5 == softCap,
	// no excess to drop). Then 3 more appends. Total = 8 on
	// disk, all retained.
	//
	// To force a real trim, we'd need to load a pre-existing
	// file with > softCap records and then Append; do that
	// in TestFilePersister_CompactionTrimsOldestRecords
	// below. Here we just lock that compaction is invoked
	// and the file is intact.
	if len(got) != 8 {
		t.Fatalf("expected 8 records on disk, got %d", len(got))
	}
	// File should be unchanged in record-order despite
	// compaction having executed once.
	for i, r := range got {
		want := uint64(i + 1)
		if r.Seq != want {
			t.Errorf("record %d Seq: got %d, want %d", i, r.Seq, want)
		}
	}

	// Smoke: file size is bounded — sanity-check that the
	// file isn't accumulating ghosts.
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if st.Size() <= 0 {
		t.Errorf("file empty after appends?")
	}
}

// TestFilePersister_CompactionTrimsOldestRecords forces the
// compaction trim path by pre-seeding the file with > softCap
// records and then appending one more (which crosses the
// watermark and trims the head).
func TestFilePersister_CompactionTrimsOldestRecords(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "recentrejects.jsonl")

	// Pre-seed the file with 12 records by hand.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	for i := uint64(1); i <= 12; i++ {
		line, err := json.Marshal(Rejection{Seq: i, Kind: KindArchSpoofUnknown})
		if err != nil {
			t.Fatalf("marshal %d: %v", i, err)
		}
		line = append(line, '\n')
		if _, err := f.Write(line); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Open the persister with softCap=5; the constructor
	// does NOT compact. Compaction fires only on Append, so
	// trigger 5 more appends to drive appendsSinceCompact
	// past the watermark.
	fp, err := NewFilePersister(path, 5)
	if err != nil {
		t.Fatalf("NewFilePersister: %v", err)
	}
	t.Cleanup(func() { _ = fp.Close() })

	for i := uint64(13); i <= 17; i++ {
		if err := fp.Append(Rejection{Seq: i, Kind: KindArchSpoofUnknown}); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}
	// On Append #17 the watermark hits softCap=5, compaction
	// runs: file now has 17 records (12 seeded + 5 appended).
	// compactLocked keeps the last 5 → Seqs {13, 14, 15, 16, 17}.
	got, err := fp.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("post-compaction len: got %d, want 5", len(got))
	}
	want := []uint64{13, 14, 15, 16, 17}
	for i, r := range got {
		if r.Seq != want[i] {
			t.Errorf("record %d Seq: got %d, want %d", i, r.Seq, want[i])
		}
	}
}

// TestStore_RestoreFromPersister_PopulatesRing locks the
// boot-time replay path: a Store with a persister containing
// 3 records calls RestoreFromPersister and the ring is
// populated.
func TestStore_RestoreFromPersister_PopulatesRing(t *testing.T) {
	t.Parallel()
	fp, _ := newFilePersisterT(t, 0)
	for i := uint64(1); i <= 3; i++ {
		if err := fp.Append(Rejection{Seq: i, Kind: KindArchSpoofUnknown, Reason: fmt.Sprintf("r%d", i)}); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}

	s := NewStore(0, fixedNow())
	s.SetPersister(fp)

	n, err := s.RestoreFromPersister()
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if n != 3 {
		t.Fatalf("Restore count: got %d, want 3", n)
	}
	if got := s.Len(); got != 3 {
		t.Errorf("Len after restore: got %d, want 3", got)
	}
	page := s.List(ListOptions{})
	if len(page.Records) != 3 {
		t.Fatalf("List len: got %d, want 3", len(page.Records))
	}
	for i, r := range page.Records {
		want := uint64(i + 1)
		if r.Seq != want {
			t.Errorf("record %d Seq: got %d, want %d", i, r.Seq, want)
		}
	}
}

// TestStore_RestoreFromPersister_RespectsCap covers the
// "file has more records than the in-memory ring caps" case:
// Restore must trim to the most recent Cap() records, not
// load the lot and OOM.
func TestStore_RestoreFromPersister_RespectsCap(t *testing.T) {
	t.Parallel()
	fp, _ := newFilePersisterT(t, 0)
	// 10 records on disk; ring capped at 4.
	for i := uint64(1); i <= 10; i++ {
		if err := fp.Append(Rejection{Seq: i, Kind: KindArchSpoofUnknown}); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}
	s := NewStore(4, fixedNow())
	s.SetPersister(fp)

	n, err := s.RestoreFromPersister()
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if n != 4 {
		t.Fatalf("Restore count: got %d, want 4", n)
	}
	page := s.List(ListOptions{})
	if len(page.Records) != 4 {
		t.Fatalf("List len: got %d, want 4", len(page.Records))
	}
	// Most-recent-4 means Seqs {7, 8, 9, 10}.
	want := []uint64{7, 8, 9, 10}
	for i, r := range page.Records {
		if r.Seq != want[i] {
			t.Errorf("record %d Seq: got %d, want %d", i, r.Seq, want[i])
		}
	}
}

// TestStore_RestoreFromPersister_ReseedsSeqCounter verifies
// that post-restore Record() calls assign Seqs strictly
// above the highest persisted Seq, not collisions starting
// from 1 again.
func TestStore_RestoreFromPersister_ReseedsSeqCounter(t *testing.T) {
	t.Parallel()
	fp, _ := newFilePersisterT(t, 0)
	for i := uint64(1); i <= 3; i++ {
		if err := fp.Append(Rejection{Seq: i, Kind: KindArchSpoofUnknown}); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}
	s := NewStore(0, fixedNow())
	s.SetPersister(fp)
	if _, err := s.RestoreFromPersister(); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	gotSeq := s.Record(Rejection{Kind: KindArchSpoofUnknown, Reason: "post-restore"})
	if gotSeq != 4 {
		t.Errorf("post-restore Record Seq: got %d, want 4", gotSeq)
	}
}

// TestStore_RestoreFromPersister_DoubleCallFails locks the
// "exactly once" contract: a second Restore call must error
// rather than silently no-op (which would mask a wiring bug
// where two boot paths both call Restore).
func TestStore_RestoreFromPersister_DoubleCallFails(t *testing.T) {
	t.Parallel()
	fp, _ := newFilePersisterT(t, 0)
	s := NewStore(0, fixedNow())
	s.SetPersister(fp)

	if _, err := s.RestoreFromPersister(); err != nil {
		t.Fatalf("first Restore: %v", err)
	}
	_, err := s.RestoreFromPersister()
	if err == nil {
		t.Fatal("second Restore: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "already called") {
		t.Errorf("error: got %q, want to contain 'already called'", err)
	}
}

// TestStore_NoopPersister_NoFilesystemAccess locks the default
// posture: a Store with no SetPersister call performs zero
// filesystem operations on Record / Restore.
func TestStore_NoopPersister_NoFilesystemAccess(t *testing.T) {
	t.Parallel()
	s := NewStore(0, fixedNow())
	// No SetPersister; default = noopPersister.
	if !IsNoopPersister(s.Persister()) {
		t.Fatal("default persister must be noop")
	}
	// Restore on noop is a no-op (n=0, err=nil).
	n, err := s.RestoreFromPersister()
	if err != nil {
		t.Errorf("Restore on noop: %v", err)
	}
	if n != 0 {
		t.Errorf("Restore on noop count: got %d, want 0", n)
	}
	// Record fires the in-memory append only.
	gotSeq := s.Record(Rejection{Kind: KindArchSpoofUnknown})
	if gotSeq != 1 {
		t.Errorf("Record Seq: got %d, want 1", gotSeq)
	}
	if c := s.PersistErrorCount(); c != 0 {
		t.Errorf("noop persister produced PersistErrorCount=%d", c)
	}
}

// TestStore_RecordCallsAppend covers the integration path:
// every Record() call also fires Persister.Append on the
// installed persister.
func TestStore_RecordCallsAppend(t *testing.T) {
	t.Parallel()
	fp, _ := newFilePersisterT(t, 0)
	s := NewStore(0, fixedNow())
	s.SetPersister(fp)

	const n = 5
	for i := 0; i < n; i++ {
		s.Record(Rejection{Kind: KindArchSpoofUnknown, Reason: fmt.Sprintf("r%d", i)})
	}
	got, err := fp.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(got) != n {
		t.Errorf("on-disk count: got %d, want %d", len(got), n)
	}
}

// failingPersister always returns the same error on Append.
// Used to drive the PersistErrorCount path.
type failingPersister struct {
	err error
}

func (p failingPersister) Append(Rejection) error        { return p.err }
func (p failingPersister) LoadAll() ([]Rejection, error) { return nil, nil }
func (p failingPersister) Close() error                  { return nil }

// TestStore_PersistErrorIncrementsCounter locks the
// best-effort persistence contract: Append failures bump the
// counter but do NOT block the in-memory ring or panic.
func TestStore_PersistErrorIncrementsCounter(t *testing.T) {
	t.Parallel()
	s := NewStore(0, fixedNow())
	s.SetPersister(failingPersister{err: errors.New("disk full simulated")})

	const n = 4
	for i := 0; i < n; i++ {
		got := s.Record(Rejection{Kind: KindArchSpoofUnknown})
		if got == 0 {
			t.Errorf("Record %d: returned Seq=0 (expected non-zero — in-memory ring must accept)", i)
		}
	}
	if got := s.PersistErrorCount(); got != n {
		t.Errorf("PersistErrorCount: got %d, want %d", got, n)
	}
	if got := s.Len(); got != n {
		t.Errorf("in-memory ring depth: got %d, want %d (in-memory must accept regardless of persist failure)", got, n)
	}
}

// TestFilePersister_AppendConcurrent stress-tests the
// per-Append open/close mutation under contention. Run with
// -race.
func TestFilePersister_AppendConcurrent(t *testing.T) {
	t.Parallel()
	fp, _ := newFilePersisterT(t, 0)
	const workers = 8
	const perWorker = 50
	var wg sync.WaitGroup
	errCh := make(chan error, workers*perWorker)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				if err := fp.Append(Rejection{
					Seq:    uint64(id*1000 + i),
					Kind:   KindArchSpoofUnknown,
					Reason: fmt.Sprintf("w%d-i%d", id, i),
				}); err != nil {
					errCh <- err
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Errorf("concurrent Append: %v", err)
	}
	got, err := fp.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	want := workers * perWorker
	if len(got) != want {
		t.Errorf("concurrent count: got %d, want %d", len(got), want)
	}
}

// TestNewFilePersister_EmptyPathRejected — a nil path is a
// programmer error and must surface immediately, not silently
// no-op.
func TestNewFilePersister_EmptyPathRejected(t *testing.T) {
	t.Parallel()
	_, err := NewFilePersister("", 0)
	if err == nil {
		t.Fatal("expected error for empty path")
	}
}

// TestNewFilePersister_DefaultSoftCap sanity-checks the
// constructor's softCap defaulting: 0 → DefaultPersistSoftCap;
// negative → DefaultPersistSoftCap.
func TestNewFilePersister_DefaultSoftCap(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, in := range []int{0, -1, -100} {
		fp, err := NewFilePersister(filepath.Join(dir, fmt.Sprintf("rj-%d.jsonl", -in)), in)
		if err != nil {
			t.Fatalf("NewFilePersister(%d): %v", in, err)
		}
		if got := fp.SoftCap(); got != DefaultPersistSoftCap {
			t.Errorf("softCap(%d): got %d, want %d", in, got, DefaultPersistSoftCap)
		}
	}
}

// TestStore_SetPersister_NilRevertsToNoop locks the
// detach path: SetPersister(nil) reverts to the no-op
// default so a test cleanup or a runtime detach does not
// leave a nil pointer in the hot path.
func TestStore_SetPersister_NilRevertsToNoop(t *testing.T) {
	t.Parallel()
	fp, _ := newFilePersisterT(t, 0)
	s := NewStore(0, fixedNow())
	s.SetPersister(fp)
	if IsNoopPersister(s.Persister()) {
		t.Fatal("expected real persister installed")
	}
	s.SetPersister(nil)
	if !IsNoopPersister(s.Persister()) {
		t.Fatal("SetPersister(nil) did not revert to noop")
	}
	// Subsequent Record must not bump PersistErrorCount.
	s.Record(Rejection{Kind: KindArchSpoofUnknown})
	if got := s.PersistErrorCount(); got != 0 {
		t.Errorf("noop after detach produced PersistErrorCount=%d", got)
	}
}

// ----- Compaction-observability suite ---------------------------
//
// Three behaviours under test:
//
//   - FilePersister.RecordsOnDisk increments on every successful
//     Append and resets to len(keep) after compaction.
//   - The constructor seeds RecordsOnDisk from any pre-existing
//     records (so a v2wiring boot reflects a non-zero gauge
//     before the first Append fires).
//   - The metrics-recorder hooks
//     (PersistCompactionRecorder + PersistRecordsRecorder) fire
//     on the same events.
//
// All tests use the package's existing withCaptureRecorder
// helper (see metrics_test.go); the captureRecorder satisfies
// all four interfaces, and t.Cleanup inside withCaptureRecorder
// restores the no-op default so a sibling test sharing the
// process-wide recorder does not see leftover hooks.

// TestFilePersister_RecordsOnDisk_TracksAppendAndCompact pins
// the basic atomic-counter contract: each successful Append
// increments by one; each successful compaction resets to the
// post-compaction count.
func TestFilePersister_RecordsOnDisk_TracksAppendAndCompact(t *testing.T) {
	t.Parallel()
	fp, _ := newFilePersisterT(t, 5)

	if got := fp.RecordsOnDisk(); got != 0 {
		t.Fatalf("baseline RecordsOnDisk: got %d, want 0", got)
	}

	for i := uint64(1); i <= 4; i++ {
		if err := fp.Append(Rejection{Seq: i, Kind: KindArchSpoofUnknown}); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}
	if got := fp.RecordsOnDisk(); got != 4 {
		t.Errorf("after 4 appends: got %d, want 4", got)
	}

	// 5th append crosses the watermark; compactLocked
	// fires but the file already has exactly softCap=5
	// records → loadAllLocked returns 5, no trim, gauge
	// holds at 5.
	if err := fp.Append(Rejection{Seq: 5, Kind: KindArchSpoofUnknown}); err != nil {
		t.Fatalf("Append 5: %v", err)
	}
	if got := fp.RecordsOnDisk(); got != 5 {
		t.Errorf("after 5th append (compaction is a no-op): got %d, want 5", got)
	}

	// 6th-9th appends drive the count to 9; the next
	// compaction at #10 will trim to 5.
	for i := uint64(6); i <= 9; i++ {
		if err := fp.Append(Rejection{Seq: i, Kind: KindArchSpoofUnknown}); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}
	if got := fp.RecordsOnDisk(); got != 9 {
		t.Errorf("pre-trim count: got %d, want 9", got)
	}
	if err := fp.Append(Rejection{Seq: 10, Kind: KindArchSpoofUnknown}); err != nil {
		t.Fatalf("Append 10: %v", err)
	}
	// Append 10 → count goes 9→10, then compaction fires
	// (10 records vs softCap=5 → trim to last 5).
	if got := fp.RecordsOnDisk(); got != 5 {
		t.Errorf("post-trim count: got %d, want 5", got)
	}
}

// TestNewFilePersister_SeedsRecordsOnDiskFromExistingFile —
// the boot-time scan must report a non-zero gauge before the
// first Append. Otherwise dashboards would lie about disk
// state in the seconds between v2wiring.Wire and the first
// rejection.
func TestNewFilePersister_SeedsRecordsOnDiskFromExistingFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "recentrejects.jsonl")

	// Hand-write 7 records.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	for i := uint64(1); i <= 7; i++ {
		line, err := json.Marshal(Rejection{Seq: i, Kind: KindArchSpoofUnknown})
		if err != nil {
			t.Fatalf("marshal %d: %v", i, err)
		}
		line = append(line, '\n')
		if _, err := f.Write(line); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	fp, err := NewFilePersister(path, 10)
	if err != nil {
		t.Fatalf("NewFilePersister: %v", err)
	}
	t.Cleanup(func() { _ = fp.Close() })
	if got := fp.RecordsOnDisk(); got != 7 {
		t.Errorf("constructor seed: got %d, want 7", got)
	}
}

// TestFilePersister_CompactionHook_FiresOnTrim — the
// PersistCompactionRecorder hook fires on every successful
// compaction, with the correct post-compaction count. A
// no-op compaction (file already at softCap) still fires
// the hook because the metrics adapter currently wants the
// rate signal regardless of the trim outcome.
func TestFilePersister_CompactionHook_FiresOnTrim(t *testing.T) {
	rec := withCaptureRecorder(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "recentrejects.jsonl")

	// Hand-write 8 records to force the first Append-driven
	// compaction to actually trim (softCap=3 → keep last 3).
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	for i := uint64(1); i <= 8; i++ {
		line, err := json.Marshal(Rejection{Seq: i, Kind: KindArchSpoofUnknown})
		if err != nil {
			t.Fatalf("marshal %d: %v", i, err)
		}
		line = append(line, '\n')
		if _, err := f.Write(line); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	fp, err := NewFilePersister(path, 3)
	if err != nil {
		t.Fatalf("NewFilePersister: %v", err)
	}
	t.Cleanup(func() { _ = fp.Close() })

	// 3 more appends — the watermark hits softCap=3 on
	// append #3, compaction fires, file has 11 records →
	// trims to last 3.
	for i := uint64(9); i <= 11; i++ {
		if err := fp.Append(Rejection{Seq: i, Kind: KindArchSpoofUnknown}); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}

	compactions, recs := rec.persistSnapshot()
	if len(compactions) != 1 {
		t.Fatalf("compaction hook fired %d times, want exactly 1", len(compactions))
	}
	if compactions[0] != 3 {
		t.Errorf("compaction recordsAfter: got %d, want 3 (softCap)", compactions[0])
	}
	// recordsOnDisk hook must have been invoked at least
	// for: constructor seed (8), 3× Append, 1× compaction
	// post-trim (3). We don't assert exact ordering of
	// the multi-event sequence — only that the trim event
	// is in the trail.
	sawTrim := false
	for _, n := range recs {
		if n == 3 {
			sawTrim = true
			break
		}
	}
	if !sawTrim {
		t.Errorf("expected SetPersistRecordsOnDisk(3) after trim, got values %v", recs)
	}
}

// TestFilePersister_CompactionHook_NoTrim_DoesNotFire pins the
// edge case where appendsSinceCompact crosses the watermark
// but the file is already at softCap (so loadAllLocked
// returns exactly softCap records, no trim is needed). The
// hook MUST NOT fire on this path because the file did not
// actually rewrite — only successful os.Rename calls count
// as compactions for alerting purposes. If we counted the
// no-trim early-return as a compaction, operators would
// misread the steady-state compaction rate of a healthy
// validator at exactly the soft-cap boundary as 1/softCap-
// appends-per-rejection.
func TestFilePersister_CompactionHook_NoTrim_DoesNotFire(t *testing.T) {
	rec := withCaptureRecorder(t)

	fp, _ := newFilePersisterT(t, 3)
	for i := uint64(1); i <= 3; i++ {
		if err := fp.Append(Rejection{Seq: i, Kind: KindArchSpoofUnknown}); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}
	// Append #3 hits the watermark — compactLocked fires
	// but loadAllLocked returns 3 records (== softCap),
	// the early-return path keeps the file unchanged.
	compactions, _ := rec.persistSnapshot()
	if len(compactions) != 0 {
		t.Errorf("compaction hook should not fire on no-trim path; got %d calls with values %v",
			len(compactions), compactions)
	}
	// Sanity: file is at expected size, gauge reflects it.
	if got := fp.RecordsOnDisk(); got != 3 {
		t.Errorf("RecordsOnDisk: got %d, want 3", got)
	}
}

// -----------------------------------------------------------------------------
// Hard-cap defence (2026-04-30)
//
// The soft-cap loop bounds the file at [softCap, 2*softCap-1)
// records under realistic traffic — but a flood that outruns
// the rewrite cycle could in principle grow the file past any
// budget the operator wanted to keep. The hard cap is a
// belt-and-braces second bound: byte-shaped (operator tunes
// against disk-quota / log-rotate budgets), enforced AFTER a
// salvage compaction attempt, and surfaces the drop via
// telemetry so alerting catches the flood independently of
// the compactions-rate alert.
//
// The four tests below pin:
//
//   - SetMaxBytes/MaxBytes round-trip + negative clamping.
//   - Disabled (maxBytes==0): no drops regardless of size.
//   - Salvage compaction admits the next record when there's
//     enough trim headroom (the common case under flood).
//   - Hard refusal when even compaction can't free enough
//     space: ErrHardCapExceeded + telemetry hook fires.
// -----------------------------------------------------------------------------

// TestFilePersister_MaxBytes_Accessor_RoundTrip — basic
// setter/getter pair with the negative-input clamp.
func TestFilePersister_MaxBytes_Accessor_RoundTrip(t *testing.T) {
	t.Parallel()
	fp, _ := newFilePersisterT(t, 0)

	// Default: disabled.
	if got := fp.MaxBytes(); got != 0 {
		t.Errorf("default MaxBytes = %d, want 0 (disabled)", got)
	}

	fp.SetMaxBytes(8 * 1024 * 1024)
	if got := fp.MaxBytes(); got != 8*1024*1024 {
		t.Errorf("after SetMaxBytes(8MiB) = %d, want %d", got, 8*1024*1024)
	}

	// Negative clamps to 0 (= disabled). A panic-on-negative
	// would be hostile to test fixtures that compute the cap
	// from arithmetic; clamp is the same posture as
	// softCap's <=0 → DefaultPersistSoftCap.
	fp.SetMaxBytes(-1)
	if got := fp.MaxBytes(); got != 0 {
		t.Errorf("after SetMaxBytes(-1) = %d, want 0 (clamped)", got)
	}

	// Nil-receiver is a no-op (programmer-error guard, same
	// posture as RecordsOnDisk on nil).
	var nilP *FilePersister
	nilP.SetMaxBytes(999) // must not panic
	if got := nilP.MaxBytes(); got != 0 {
		t.Errorf("nil-receiver MaxBytes() = %d, want 0", got)
	}
}

// TestFilePersister_MaxBytes_Disabled_NoDropsRegardlessOfSize
// pins the "feature off by default" contract: with maxBytes=0
// (or unset), no Append ever returns ErrHardCapExceeded and
// no drop hook fires, even when the file far exceeds any
// realistic ceiling.
func TestFilePersister_MaxBytes_Disabled_NoDropsRegardlessOfSize(t *testing.T) {
	rec := withCaptureRecorder(t)

	// softCap=1000 keeps the soft-cap loop quiet for the
	// 50-record write below.
	fp, _ := newFilePersisterT(t, 1000)
	if got := fp.MaxBytes(); got != 0 {
		t.Fatalf("precondition: expected disabled hard cap, got %d", got)
	}

	for i := uint64(1); i <= 50; i++ {
		if err := fp.Append(Rejection{
			Seq: i, Kind: KindArchSpoofUnknown,
			// Padding to make each record several
			// hundred bytes so a hard cap of (say) 4 KiB
			// would have fired — but with the cap
			// disabled this is irrelevant.
			Detail: strings.Repeat("x", 200),
		}); err != nil {
			t.Fatalf("Append %d (cap disabled): %v", i, err)
		}
	}
	if drops := rec.hardCapSnapshot(); len(drops) != 0 {
		t.Errorf("expected zero hard-cap drops with feature disabled, got %d: %v",
			len(drops), drops)
	}
}

// TestFilePersister_MaxBytes_SalvageCompactionAdmits — the
// happy-path-under-flood case: hard cap tight enough that we
// briefly cross the threshold, but the salvage compaction
// trims the head and frees enough headroom to admit the next
// record. No drops should fire; the next-record write
// succeeds.
func TestFilePersister_MaxBytes_SalvageCompactionAdmits(t *testing.T) {
	rec := withCaptureRecorder(t)

	// softCap=3 trims aggressively; the salvage compaction
	// will rewrite ~3 records (well under the hard cap).
	fp, _ := newFilePersisterT(t, 3)

	// Record size: ~ 80 bytes after JSON encoding (Seq +
	// Kind + a small Detail). The first 8 records grow the
	// file to ~ 640 bytes; setting the hard cap to 1024 lets
	// the compaction loop hit softCap=3 (3 records ≈ 240
	// bytes) and then admit subsequent records below 1024.
	fp.SetMaxBytes(1024)

	for i := uint64(1); i <= 20; i++ {
		if err := fp.Append(Rejection{
			Seq: i, Kind: KindArchSpoofUnknown,
			Detail: strings.Repeat("a", 50),
		}); err != nil {
			// A salvage compaction failure manifests as
			// ErrHardCapExceeded — that's the assertion
			// failure mode of this test.
			t.Fatalf("Append %d unexpectedly returned %v "+
				"(salvage compaction should have admitted it)", i, err)
		}
	}

	if drops := rec.hardCapSnapshot(); len(drops) != 0 {
		t.Errorf("salvage path should not drop; got %d drops: %v",
			len(drops), drops)
	}
	// File should be bounded by softCap after the loop —
	// the compaction loop trims back to len==softCap on
	// every cycle.
	if got := fp.RecordsOnDisk(); got > 5 {
		t.Errorf("RecordsOnDisk after salvage cycles = %d, want ≤ 5 (softCap+slack)", got)
	}
}

// TestFilePersister_MaxBytes_HardRefusal_FiresTelemetry — the
// adversarial case: hard cap so tight that even after a
// salvage compaction the next record won't fit. Append must
// return ErrHardCapExceeded AND the drop hook must fire so
// Prometheus catches the flood.
func TestFilePersister_MaxBytes_HardRefusal_FiresTelemetry(t *testing.T) {
	rec := withCaptureRecorder(t)

	fp, _ := newFilePersisterT(t, 3)
	// Tiny cap below ANY single record's encoded size — the
	// salvage compaction succeeds (file becomes empty since
	// loadAllLocked returns ≤ softCap records, the no-trim
	// early-return leaves it unchanged) but the cap math
	// still fails.
	fp.SetMaxBytes(8) // 8 bytes — smaller than even the JSON braces

	rec.mu.Lock()
	rec.hardCapDrops = nil
	rec.mu.Unlock()

	err := fp.Append(Rejection{
		Seq: 1, Kind: KindArchSpoofUnknown,
		Detail: "this record is way bigger than 8 bytes",
	})
	if err == nil {
		t.Fatalf("expected ErrHardCapExceeded, got nil")
	}
	if !errors.Is(err, ErrHardCapExceeded) {
		t.Errorf("error should wrap ErrHardCapExceeded; got %v", err)
	}
	drops := rec.hardCapSnapshot()
	if len(drops) != 1 {
		t.Fatalf("hard-cap drop hook fired %d times, want 1: %v", len(drops), drops)
	}
	if drops[0] <= 0 {
		t.Errorf("dropped byte count = %d, want > 0", drops[0])
	}

	// A second drop must fire on the next attempt — the
	// counter is cumulative, not "first attempt only".
	if err := fp.Append(Rejection{Seq: 2, Kind: KindArchSpoofUnknown}); !errors.Is(err, ErrHardCapExceeded) {
		t.Errorf("second Append: got %v, want ErrHardCapExceeded", err)
	}
	if got := len(rec.hardCapSnapshot()); got != 2 {
		t.Errorf("after second drop: hook fired %d times, want 2", got)
	}

	// In-memory ring is unaffected by on-disk drops — that's
	// the whole point. RecordsOnDisk should be 0 (no
	// successful Append).
	if got := fp.RecordsOnDisk(); got != 0 {
		t.Errorf("RecordsOnDisk after hard-cap drops = %d, want 0", got)
	}
}
