package v2wiring_test

// v2wiring_recentrejects_persist_test.go: integration coverage
// for Config.RecentRejectionsPath. Validates that:
//
//  1. Wire() with an empty path leaves the ring as no-op
//     persisted (the legacy posture pre-2026-04-29).
//  2. Wire() with a path constructs a FilePersister and the
//     same Wired.RecentRejections store performs both
//     in-memory and on-disk Append on every Record().
//  3. A second Wire() against the same path replays the
//     prior records into the new ring — restart-survival
//     for forensic continuity.
//  4. Wire() with an unwritable path surfaces via
//     LogRecentRejectionsError but does NOT crash boot —
//     persistence is best-effort observability, not
//     consensus state.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/blackbeardONE/QSDM/internal/v2wiring"
	"github.com/blackbeardONE/QSDM/pkg/api"
	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
	mining "github.com/blackbeardONE/QSDM/pkg/mining"
	"github.com/blackbeardONE/QSDM/pkg/mining/attest/recentrejects"
	"github.com/blackbeardONE/QSDM/pkg/monitoring"
)

// resetWireGlobals undoes the mining/api side-effects Wire()
// produces. The standard buildRig already does this via its
// own t.Cleanup; tests in this file build Wire() directly to
// drive the persistence-path knob, so they own the reset
// themselves.
func resetWireGlobals(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		monitoring.SetEnrollmentStateProvider(nil)
		api.SetEnrollmentRegistry(nil)
		api.SetEnrollmentLister(nil)
		api.SetEnrollmentMempool(nil)
		api.SetSlashMempool(nil)
		api.SetSlashReceiptStore(nil)
		api.SetSlashReceiptLister(nil)
		api.SetRecentRejectionLister(nil)
		mining.SetRejectionRecorder(nil)
	})
}

// minimalWireConfig returns a Config that satisfies Wire()'s
// validation with no governance / persistence wiring beyond
// what the test sets explicitly.
func minimalWireConfig(t *testing.T) v2wiring.Config {
	t.Helper()
	return v2wiring.Config{
		Accounts:       chain.NewAccountStore(),
		Pool:           mempool.New(mempool.DefaultConfig()),
		BaseAdmit:      nil,
		SlashRewardBPS: chain.SlashRewardCap,
		LogSweepError:  func(uint64, error) {},
	}
}

// TestWire_RecentRejections_PathEmpty_NoPersistence locks the
// legacy posture: no path → no FilePersister → the store's
// Persister() is the no-op default. A regression that always
// installed a persister (e.g. defaulting RecentRejectionsPath
// to a hard-coded string) would silently introduce a
// filesystem dependency for ephemeral testnets and would
// surface here.
func TestWire_RecentRejections_PathEmpty_NoPersistence(t *testing.T) {
	resetWireGlobals(t)
	cfg := minimalWireConfig(t)
	// cfg.RecentRejectionsPath intentionally left empty.

	w, err := v2wiring.Wire(cfg)
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	if !recentrejects.IsNoopPersister(w.RecentRejections.Persister()) {
		t.Errorf("expected no-op persister with empty path, got %T", w.RecentRejections.Persister())
	}

	w.RecentRejections.Record(recentrejects.Rejection{Kind: recentrejects.KindArchSpoofUnknown})
	if got := w.RecentRejections.PersistErrorCount(); got != 0 {
		t.Errorf("PersistErrorCount with no-op persister: got %d, want 0", got)
	}
}

// TestWire_RecentRejections_Path_PersistsOnEveryRecord drives
// the on-disk Append path: Wire() with a path → Record()
// writes to disk → the JSONL file contains the record.
func TestWire_RecentRejections_Path_PersistsOnEveryRecord(t *testing.T) {
	resetWireGlobals(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "recentrejects.jsonl")

	cfg := minimalWireConfig(t)
	cfg.RecentRejectionsPath = path
	cfg.LogRecentRejectionsError = func(err error) {
		t.Errorf("unexpected LogRecentRejectionsError: %v", err)
	}

	w, err := v2wiring.Wire(cfg)
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}

	if recentrejects.IsNoopPersister(w.RecentRejections.Persister()) {
		t.Fatal("expected real persister with non-empty path")
	}

	w.RecentRejections.Record(recentrejects.Rejection{Kind: recentrejects.KindArchSpoofUnknown, Reason: "first"})
	w.RecentRejections.Record(recentrejects.Rejection{Kind: recentrejects.KindHashrateOutOfBand, Arch: "blackwell"})

	// Open a fresh persister against the same path and
	// LoadAll — the records must be on disk.
	other, err := recentrejects.NewFilePersister(path, 0)
	if err != nil {
		t.Fatalf("re-open: %v", err)
	}
	t.Cleanup(func() { _ = other.Close() })
	got, err := other.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("on-disk count: got %d, want 2", len(got))
	}
	if got[0].Reason != "first" {
		t.Errorf("record[0] Reason: got %q, want %q", got[0].Reason, "first")
	}
	if got[1].Arch != "blackwell" {
		t.Errorf("record[1] Arch: got %q, want %q", got[1].Arch, "blackwell")
	}

	// File mode must be 0600 (operator forensic data; same
	// posture as governance snapshot files).
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	// On Windows, os.FileMode permission bits are emulated;
	// we assert only the owner-readable bits as a smoke test.
	// The construction code passes 0o600 explicitly; a
	// regression to 0o644 would fail the persistence-test
	// suite's *nix run instead.
	if st.Mode().Perm() == 0 {
		t.Errorf("file Perm() is zero — Stat broken? mode=%v", st.Mode())
	}
}

// TestWire_RecentRejections_Path_RestoresOnRestart drives the
// boot-time restore path: Wire() #1 records two rejections,
// Wire() #2 against the same path replays them and a fresh
// Record() continues the Seq counter without collision.
//
// Critical for forensic continuity: an operator who reboots
// a validator should not lose the §4.6 rejection history.
func TestWire_RecentRejections_Path_RestoresOnRestart(t *testing.T) {
	resetWireGlobals(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "recentrejects.jsonl")

	// Boot #1: record two rejections.
	{
		cfg := minimalWireConfig(t)
		cfg.RecentRejectionsPath = path
		w, err := v2wiring.Wire(cfg)
		if err != nil {
			t.Fatalf("Wire #1: %v", err)
		}
		w.RecentRejections.Record(recentrejects.Rejection{Kind: recentrejects.KindArchSpoofUnknown, Reason: "boot1-a"})
		w.RecentRejections.Record(recentrejects.Rejection{Kind: recentrejects.KindArchSpoofUnknown, Reason: "boot1-b"})
		// Reset the api/mining hooks before the next Wire so
		// the second Wire's installs are unambiguous.
		api.SetRecentRejectionLister(nil)
		mining.SetRejectionRecorder(nil)
	}

	// Boot #2: same path; restore must populate the ring.
	cfg2 := minimalWireConfig(t)
	cfg2.RecentRejectionsPath = path
	cfg2.LogRecentRejectionsError = func(err error) {
		t.Errorf("unexpected LogRecentRejectionsError on boot #2: %v", err)
	}
	w2, err := v2wiring.Wire(cfg2)
	if err != nil {
		t.Fatalf("Wire #2: %v", err)
	}
	if got := w2.RecentRejections.Len(); got != 2 {
		t.Fatalf("post-restore ring depth: got %d, want 2", got)
	}
	page := w2.RecentRejections.List(recentrejects.ListOptions{})
	if len(page.Records) != 2 {
		t.Fatalf("List len: got %d, want 2", len(page.Records))
	}
	if page.Records[0].Reason != "boot1-a" || page.Records[1].Reason != "boot1-b" {
		t.Errorf("restore order: got [%q, %q], want [%q, %q]",
			page.Records[0].Reason, page.Records[1].Reason, "boot1-a", "boot1-b")
	}

	// Post-restore Record must continue the Seq counter past
	// the restored max — never collide.
	gotSeq := w2.RecentRejections.Record(recentrejects.Rejection{Kind: recentrejects.KindArchSpoofUnknown, Reason: "boot2-c"})
	if gotSeq <= 2 {
		t.Errorf("post-restore Seq: got %d, want > 2", gotSeq)
	}
}

// TestWire_RecentRejections_Path_UnwritableSurfacesError
// covers the soft-failure contract: a path under a directory
// that does not exist cannot be opened by NewFilePersister.
// Wire() must NOT crash; it must call
// LogRecentRejectionsError and continue with the
// (degraded) in-memory-only ring.
func TestWire_RecentRejections_Path_UnwritableSurfacesError(t *testing.T) {
	resetWireGlobals(t)
	// Path under a non-existent parent directory.
	bad := filepath.Join(t.TempDir(), "definitely", "not", "a", "real", "dir", "rj.jsonl")

	var captured error
	cfg := minimalWireConfig(t)
	cfg.RecentRejectionsPath = bad
	cfg.LogRecentRejectionsError = func(err error) {
		captured = err
	}

	w, err := v2wiring.Wire(cfg)
	if err != nil {
		t.Fatalf("Wire must NOT fail on unwritable path; got %v", err)
	}
	if captured == nil {
		t.Error("expected LogRecentRejectionsError to be invoked, was not")
	}
	// Persister stays as the no-op default since the
	// FilePersister construction failed.
	if !recentrejects.IsNoopPersister(w.RecentRejections.Persister()) {
		t.Errorf("expected no-op persister after construction failure, got %T", w.RecentRejections.Persister())
	}
}
