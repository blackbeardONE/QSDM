package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/chain"
)

func recoveryBlock(height uint64, prevHash, stateRoot string) *chain.Block {
	blk := &chain.Block{
		Height:     height,
		PrevHash:   prevHash,
		Timestamp:  time.Date(2026, 7, 20, 0, 0, int(height), 0, time.UTC),
		StateRoot:  stateRoot,
		ProducerID: "recovery-test",
	}
	blk.Hash = chain.ComputeBlockHash(blk)
	return blk
}

func writeRecoveryJournal(t *testing.T, path string, blocks []*chain.Block) {
	t.Helper()
	for _, blk := range blocks {
		if err := chain.AppendBlockToFile(path, blk); err != nil {
			t.Fatalf("append recovery fixture height %d: %v", blk.Height, err)
		}
	}
}

func TestReconcilePersistedStateTailKeepsMatchingTip(t *testing.T) {
	accounts := chain.NewAccountStore()
	root := accounts.StateRoot()
	b0 := recoveryBlock(0, "", root)
	b1 := recoveryBlock(1, b0.Hash, root)
	path := filepath.Join(t.TempDir(), "qsdm_chain.ndjson")
	writeRecoveryJournal(t, path, []*chain.Block{b0, b1})

	restored, err := reconcilePersistedStateTail(path, accounts, []*chain.Block{b0, b1}, time.Now())
	if err != nil {
		t.Fatalf("reconcile matching tip: %v", err)
	}
	if restored.recovered || restored.backupPath != "" {
		t.Fatalf("matching tip was unexpectedly recovered: %+v", restored)
	}
	if len(restored.blocks) != 2 || restored.stateRoot != root {
		t.Fatalf("matching restore = %+v, want two blocks and root %s", restored, root)
	}
}

func TestReconcilePersistedStateTailArchivesExactlyOneUncommittedBlock(t *testing.T) {
	accounts := chain.NewAccountStore()
	root := accounts.StateRoot()
	b0 := recoveryBlock(0, "", root)
	b1 := recoveryBlock(1, b0.Hash, root)
	b2 := recoveryBlock(2, b1.Hash, "uncommitted-state-root")
	blocks := []*chain.Block{b0, b1, b2}
	path := filepath.Join(t.TempDir(), "qsdm_chain.ndjson")
	writeRecoveryJournal(t, path, blocks)
	now := time.Date(2026, 7, 20, 1, 2, 3, 4, time.UTC)

	restored, err := reconcilePersistedStateTail(path, accounts, blocks, now)
	if err != nil {
		t.Fatalf("reconcile one-block crash gap: %v", err)
	}
	if !restored.recovered || restored.backupPath == "" {
		t.Fatalf("one-block crash gap was not recovered: %+v", restored)
	}
	if len(restored.blocks) != 2 || restored.blocks[1].Hash != b1.Hash {
		t.Fatalf("restored branch = %+v, want tip height 1", restored.blocks)
	}
	canonical, err := chain.LoadChainNDJSON(path)
	if err != nil {
		t.Fatal(err)
	}
	archived, err := chain.LoadChainNDJSON(restored.backupPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(canonical) != 2 || canonical[1].Hash != b1.Hash {
		t.Fatalf("canonical journal has %d blocks, want two ending at height 1", len(canonical))
	}
	if len(archived) != 3 || archived[2].Hash != b2.Hash {
		t.Fatalf("archived journal has %d blocks, want original three", len(archived))
	}
}

func TestReconcilePersistedStateTailRebuildsTaskStateForPriorTip(t *testing.T) {
	accounts := chain.NewAccountStore()
	accountRoot := accounts.StateRoot()
	b0 := recoveryBlock(0, "", accountRoot)
	action := chain.TaskAction{
		ID:        "stake-before-crash",
		Sender:    "alice",
		TaskID:    "task-1",
		Action:    "stake",
		Amount:    2,
		Nonce:     1,
		Timestamp: "2026-07-20T00:00:01Z",
	}
	tx := taskReplayTx(t, action)
	tasks := chain.NewTaskStateStore()
	if err := tasks.ApplyHistoricalTx(tx, 1); err != nil {
		t.Fatalf("prepare task state: %v", err)
	}
	aware := chain.NewEnrollmentAwareApplier(accounts, nil)
	aware.SetTaskStateStore(tasks)
	b1 := recoveryBlock(1, b0.Hash, aware.StateRoot())
	b1.Transactions = append(b1.Transactions, tx)
	b1.Hash = chain.ComputeBlockHash(b1)
	b2 := recoveryBlock(2, b1.Hash, "uncommitted-state-root")
	blocks := []*chain.Block{b0, b1, b2}
	path := filepath.Join(t.TempDir(), "qsdm_chain.ndjson")
	writeRecoveryJournal(t, path, blocks)

	restored, err := reconcilePersistedStateTail(path, accounts, blocks, time.Now())
	if err != nil {
		t.Fatalf("reconcile task-state crash gap: %v", err)
	}
	if !restored.recovered || restored.taskActions != 1 {
		t.Fatalf("task-state restore = %+v, want one recovered action", restored)
	}
	state, ok := restored.taskState.GetTask("task-1")
	if !ok || state.Participants["alice"].Stake != 2 {
		t.Fatalf("restored task state = %+v, want alice stake 2", state)
	}
}

func TestReconcilePersistedStateTailRejectsBroaderMismatch(t *testing.T) {
	accounts := chain.NewAccountStore()
	b0 := recoveryBlock(0, "", "state-0")
	b1 := recoveryBlock(1, b0.Hash, "state-1")
	b2 := recoveryBlock(2, b1.Hash, "state-2")
	blocks := []*chain.Block{b0, b1, b2}
	dir := t.TempDir()
	path := filepath.Join(dir, "qsdm_chain.ndjson")
	writeRecoveryJournal(t, path, blocks)

	_, err := reconcilePersistedStateTail(path, accounts, blocks, time.Now())
	if err == nil || !strings.Contains(err.Error(), "matches neither canonical tip nor its predecessor") {
		t.Fatalf("broader mismatch error = %v", err)
	}
	loaded, loadErr := chain.LoadChainNDJSON(path)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if len(loaded) != 3 {
		t.Fatalf("fail-closed recovery altered journal: got %d blocks", len(loaded))
	}
	backups, globErr := filepath.Glob(path + ".uncommitted-tail-*.bak")
	if globErr != nil {
		t.Fatal(globErr)
	}
	if len(backups) != 0 {
		t.Fatalf("fail-closed recovery created backups: %v", backups)
	}
}

func TestReconcilePersistedStateTailRejectsSingleBlockMismatch(t *testing.T) {
	accounts := chain.NewAccountStore()
	b0 := recoveryBlock(0, "", "different-root")
	path := filepath.Join(t.TempDir(), "qsdm_chain.ndjson")
	writeRecoveryJournal(t, path, []*chain.Block{b0})

	_, err := reconcilePersistedStateTail(path, accounts, []*chain.Block{b0}, time.Now())
	if err == nil || !strings.Contains(err.Error(), "no preceding block exists") {
		t.Fatalf("single-block mismatch error = %v", err)
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("single-block mismatch altered source journal: %v", statErr)
	}
}
