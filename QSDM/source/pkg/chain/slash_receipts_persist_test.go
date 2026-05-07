package chain

// slash_receipts_persist_test.go — pins the NDJSON-append
// persistence + LoadNDJSON-restore semantics added to
// SlashReceiptStore so an operator's "did my slash work?"
// answer survives validator restarts.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mining/slashing"
)

func newTestPublishedStore(t *testing.T, path string, recvErr *error) *SlashReceiptStore {
	t.Helper()
	st := NewSlashReceiptStore(0, func() time.Time {
		return time.Date(2026, 5, 7, 4, 0, 0, 0, time.UTC)
	})
	if path != "" {
		st.SetPersistencePath(path, func(err error) {
			if recvErr != nil {
				*recvErr = err
			}
		})
	}
	return st
}

func makeAppliedEvent(txID string, h uint64) MiningSlashEvent {
	return MiningSlashEvent{
		TxID:                    txID,
		Outcome:                 "applied",
		Height:                  h,
		Slasher:                 "qsdm1slasher",
		NodeID:                  "rtx3050-bad",
		EvidenceKind:            slashing.EvidenceKindForgedAttestation,
		SlashedDust:             1_000_000_000,
		RewardedDust:            100_000_000,
		BurnedDust:              900_000_000,
		AutoRevoked:             true,
		AutoRevokeRemainingDust: 0,
	}
}

func makeRejectedEvent(txID string, h uint64, reason string) MiningSlashEvent {
	return MiningSlashEvent{
		TxID:         txID,
		Outcome:      "rejected",
		Height:       h,
		Slasher:      "qsdm1slasher",
		NodeID:       "rtx3050-victim",
		EvidenceKind: slashing.EvidenceKindFreshnessCheat,
		RejectReason: reason,
		Err:          errors.New("verifier rejected: stale"),
	}
}

func TestSlashReceipts_PublishAppendsNDJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "slashes.ndjson")
	var lastErr error
	st := newTestPublishedStore(t, path, &lastErr)

	st.PublishMiningSlash(makeAppliedEvent("tx-1", 100))
	st.PublishMiningSlash(makeRejectedEvent("tx-2", 101, "freshness:stale"))

	if lastErr != nil {
		t.Fatalf("unexpected persistence error: %v", lastErr)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(got), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines = %d, want 2: %q", len(lines), got)
	}
	if !strings.Contains(lines[0], `"tx_id":"tx-1"`) || !strings.Contains(lines[0], `"outcome":"applied"`) {
		t.Errorf("line 0 unexpected: %s", lines[0])
	}
	if !strings.Contains(lines[1], `"tx_id":"tx-2"`) || !strings.Contains(lines[1], `"outcome":"rejected"`) {
		t.Errorf("line 1 unexpected: %s", lines[1])
	}
	if !strings.Contains(lines[1], `"error":"verifier rejected: stale"`) {
		t.Errorf("line 1 missing error: %s", lines[1])
	}
}

func TestSlashReceipts_PublishDoesNotDoubleAppendOnDuplicate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "slashes.ndjson")
	st := newTestPublishedStore(t, path, nil)

	st.PublishMiningSlash(makeAppliedEvent("tx-dup", 100))
	st.PublishMiningSlash(makeAppliedEvent("tx-dup", 100)) // same id again

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(got), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("lines = %d, want 1 (dup must not re-append): %q", len(lines), got)
	}
}

func TestSlashReceipts_LoadNDJSONRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "slashes.ndjson")
	src := newTestPublishedStore(t, path, nil)
	src.PublishMiningSlash(makeAppliedEvent("tx-a", 10))
	src.PublishMiningSlash(makeRejectedEvent("tx-b", 11, "double-mining:already-seen"))

	dst := NewSlashReceiptStore(0, nil)
	loaded, err := dst.LoadNDJSON(path)
	if err != nil {
		t.Fatalf("LoadNDJSON: %v", err)
	}
	if loaded != 2 {
		t.Fatalf("loaded = %d, want 2", loaded)
	}
	if got := dst.Len(); got != 2 {
		t.Fatalf("Len = %d, want 2", got)
	}

	// Verify both shapes survive the round-trip without
	// material loss.
	a, ok := dst.Lookup("tx-a")
	if !ok || a.Outcome != "applied" || a.SlashedDust != 1_000_000_000 || !a.AutoRevoked {
		t.Errorf("tx-a round-trip mismatch: %+v ok=%v", a, ok)
	}
	b, ok := dst.Lookup("tx-b")
	if !ok || b.Outcome != "rejected" || b.RejectReason != "double-mining:already-seen" || b.Err == "" {
		t.Errorf("tx-b round-trip mismatch: %+v ok=%v", b, ok)
	}

	// RecordedAt must be the original publish time, NOT a
	// fresh now() — that's the whole point of restore.
	want := time.Date(2026, 5, 7, 4, 0, 0, 0, time.UTC)
	if !a.RecordedAt.Equal(want) {
		t.Errorf("tx-a RecordedAt=%v, want %v (must preserve original)", a.RecordedAt, want)
	}
}

func TestSlashReceipts_LoadNDJSONMissingFileIsZero(t *testing.T) {
	dst := NewSlashReceiptStore(0, nil)
	loaded, err := dst.LoadNDJSON(filepath.Join(t.TempDir(), "nope.ndjson"))
	if err != nil {
		t.Fatalf("LoadNDJSON missing: %v", err)
	}
	if loaded != 0 {
		t.Errorf("loaded = %d, want 0", loaded)
	}
}

func TestSlashReceipts_LoadNDJSONTruncatedTail(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "torn.ndjson")
	src := newTestPublishedStore(t, path, nil)
	src.PublishMiningSlash(makeAppliedEvent("tx-good-1", 10))
	src.PublishMiningSlash(makeAppliedEvent("tx-good-2", 11))

	// Corrupt the trailing line (simulates a torn-write from
	// a hard kill).
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if err := os.WriteFile(path, append(body, []byte(`{"tx_id":"torn`)...), 0o644); err != nil {
		t.Fatalf("write torn tail: %v", err)
	}

	dst := NewSlashReceiptStore(0, nil)
	loaded, err := dst.LoadNDJSON(path)
	if err == nil {
		t.Fatal("expected parse error for torn tail")
	}
	if loaded != 2 {
		t.Errorf("loaded = %d, want 2 (records BEFORE torn line must still be accepted)", loaded)
	}
	if _, ok := dst.Lookup("tx-good-1"); !ok {
		t.Error("tx-good-1 should be in store despite torn tail")
	}
}

func TestSlashReceipts_LoadNDJSONSkipsBlankLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "blanks.ndjson")
	src := newTestPublishedStore(t, path, nil)
	src.PublishMiningSlash(makeAppliedEvent("tx-x", 1))

	body, _ := os.ReadFile(path)
	body = append([]byte("\n\n"), body...)
	body = append(body, []byte("\n\n")...)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write padded: %v", err)
	}

	dst := NewSlashReceiptStore(0, nil)
	loaded, err := dst.LoadNDJSON(path)
	if err != nil {
		t.Fatalf("LoadNDJSON: %v", err)
	}
	if loaded != 1 {
		t.Errorf("loaded = %d, want 1 (blanks must be skipped)", loaded)
	}
}

func TestSlashReceipts_LoadNDJSONEmptyPath(t *testing.T) {
	dst := NewSlashReceiptStore(0, nil)
	if _, err := dst.LoadNDJSON(""); err == nil {
		t.Error("expected error for empty path")
	}
}

func TestSlashReceipts_PersistAndListSurvive(t *testing.T) {
	// End-to-end: write, restart (new store), then List
	// should return the same records the original would have.
	dir := t.TempDir()
	path := filepath.Join(dir, "list.ndjson")
	src := newTestPublishedStore(t, path, nil)
	for i, txID := range []string{"a", "b", "c", "d"} {
		src.PublishMiningSlash(makeAppliedEvent(txID, uint64(100+i)))
	}

	dst := NewSlashReceiptStore(0, nil)
	if _, err := dst.LoadNDJSON(path); err != nil {
		t.Fatalf("LoadNDJSON: %v", err)
	}
	page := dst.List(SlashReceiptListOptions{Limit: 10})
	if got := len(page.Records); got != 4 {
		t.Fatalf("page.Records = %d, want 4", got)
	}
	// Newest-first: tx d first.
	if page.Records[0].TxID != "d" {
		t.Errorf("page.Records[0].TxID = %q, want %q", page.Records[0].TxID, "d")
	}
	if page.Records[3].TxID != "a" {
		t.Errorf("page.Records[3].TxID = %q, want %q", page.Records[3].TxID, "a")
	}
}

func TestSlashReceipts_LoadAfterPublishMergesNotDuplicates(t *testing.T) {
	// Defensive: if an operator hand-edits the NDJSON file
	// post-boot and Wire() re-LoadNDJSONs it (a corner-case
	// admin flow), an entry already in memory must NOT be
	// re-counted.
	dir := t.TempDir()
	path := filepath.Join(dir, "slashes.ndjson")
	st := newTestPublishedStore(t, path, nil)
	st.PublishMiningSlash(makeAppliedEvent("tx-1", 1))
	st.PublishMiningSlash(makeAppliedEvent("tx-2", 2))

	loaded, err := st.LoadNDJSON(path)
	if err != nil {
		t.Fatalf("LoadNDJSON: %v", err)
	}
	if loaded != 0 {
		t.Errorf("loaded = %d, want 0 (existing tx_ids should NOT count as new)", loaded)
	}
	if got := st.Len(); got != 2 {
		t.Errorf("Len = %d, want 2 (no doubles)", got)
	}
}
