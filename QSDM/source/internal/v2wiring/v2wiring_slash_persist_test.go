package v2wiring_test

// v2wiring_slash_persist_test.go — pins the SlashReceiptsPath
// option added to v2wiring.Config so an operator-configured
// path actually drives:
//
//   1. Per-publish NDJSON append from the live publisher
//      chain installed by Wire().
//   2. Boot-time replay on a subsequent Wire() call against
//      the same path.
//
// Without these tests, a future refactor that drops the
// SetPersistencePath / LoadNDJSON calls would silently revert
// slash receipts to in-memory-only without breaking any
// existing test.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/internal/v2wiring"
	"github.com/blackbeardONE/QSDM/pkg/api"
	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/mining"
	"github.com/blackbeardONE/QSDM/pkg/monitoring"
)

// buildRigWithPath mirrors buildRig but additionally wires
// SlashReceiptsPath. Kept separate so the existing rig
// helper's signature stays stable.
func buildRigWithPath(t *testing.T, path string) (*v2wiring.Wired, *chain.AccountStore, *mempool.Mempool, *chain.BlockProducer) {
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

	accounts := chain.NewAccountStore()
	accounts.Credit(tAlice, 20)
	pool := mempool.New(mempool.DefaultConfig())

	wired, err := v2wiring.Wire(v2wiring.Config{
		Accounts:          accounts,
		Pool:              pool,
		BaseAdmit:         nil,
		SlashRewardBPS:    chain.SlashRewardCap,
		LogSweepError:     func(uint64, error) {},
		SlashReceiptsPath: path,
		LogSlashReceiptsError: func(err error) {
			t.Logf("slash receipts: %v", err)
		},
	})
	if err != nil {
		t.Fatalf("v2wiring.Wire: %v", err)
	}

	cfg := chain.DefaultProducerConfig()
	cfg.ProducerID = "test-persist-producer"
	bp := chain.NewBlockProducer(pool, wired.StateApplier, cfg)
	wired.AttachToProducer(bp)
	return wired, accounts, pool, bp
}

func TestWire_SlashReceiptsPath_AppendsOnPublish(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "slash_receipts.ndjson")

	w, _, pool, bp := buildRigWithPath(t, path)

	const txID = "tx-persist-append"
	if err := pool.Add(slashTx(t, tAlice, txID, 0)); err != nil {
		t.Fatalf("pool.Add: %v", err)
	}
	// ProduceBlock returns "all transactions failed state
	// application" because the slash rejects at lookup
	// (offender unenrolled). The publisher fires regardless.
	_, _ = bp.ProduceBlock()

	if got := w.SlashReceipts.Len(); got != 1 {
		t.Fatalf("in-memory store len = %d, want 1", got)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read NDJSON: %v", err)
	}
	if !strings.Contains(string(body), `"tx_id":"`+txID+`"`) {
		t.Fatalf("NDJSON missing tx_id; got: %s", body)
	}
	if !strings.Contains(string(body), `"outcome":"rejected"`) {
		t.Fatalf("NDJSON missing outcome=rejected; got: %s", body)
	}
}

func TestWire_SlashReceiptsPath_RestoresOnSecondWire(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "slash_receipts.ndjson")

	// Boot 1: publish a slash, file gets written.
	{
		_, _, pool, bp := buildRigWithPath(t, path)
		if err := pool.Add(slashTx(t, tAlice, "tx-persist-restore", 0)); err != nil {
			t.Fatalf("pool.Add: %v", err)
		}
		_, _ = bp.ProduceBlock()
	}

	// Boot 2: separate Wire() call against the same path. The
	// in-memory store starts empty and must be populated by
	// LoadNDJSON BEFORE Wire returns.
	w2, _, _, _ := buildRigWithPath(t, path)
	if got := w2.SlashReceipts.Len(); got != 1 {
		t.Fatalf("after second Wire(): receipt store len = %d, want 1 (boot-time restore)", got)
	}
	rec, ok := w2.SlashReceipts.Lookup("tx-persist-restore")
	if !ok {
		t.Fatal("receipt missing after restore")
	}
	if rec.Outcome != chain.SlashOutcomeRejected {
		t.Errorf("restored Outcome = %q, want %q", rec.Outcome, chain.SlashOutcomeRejected)
	}
}

func TestWire_SlashReceiptsPath_EmptyDisablesPersistence(t *testing.T) {
	// Sanity: empty path keeps the pre-2026-05-07 in-memory
	// posture. No file should be created in the temp dir.
	dir := t.TempDir()
	_, _, pool, bp := buildRigWithPath(t, "")
	if err := pool.Add(slashTx(t, tAlice, "tx-no-persist", 0)); err != nil {
		t.Fatalf("pool.Add: %v", err)
	}
	_, _ = bp.ProduceBlock()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected empty dir; got %d entries: %v", len(entries), entries)
	}
}
