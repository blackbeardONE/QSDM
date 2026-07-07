package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mining/enrollment"
)

// TestStateReplayDiagnostic is opt-in operational diagnostics for comparing a
// stopped/stalled validator snapshot with one canonical HTTP chain block. It
// is skipped in normal test runs and never writes to the supplied state dir.
func TestStateReplayDiagnostic(t *testing.T) {
	stateDir := os.Getenv("QSDM_DIAG_STATE_DIR")
	blockURL := os.Getenv("QSDM_DIAG_BLOCK_URL")
	if stateDir == "" || blockURL == "" {
		t.Skip("set QSDM_DIAG_STATE_DIR and QSDM_DIAG_BLOCK_URL")
	}

	blocks, err := chain.LoadChainNDJSON(filepath.Join(stateDir, "qsdm_chain.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) == 0 {
		t.Fatal("chain journal is empty")
	}

	accounts := chain.NewAccountStore()
	if _, err := accounts.Load(filepath.Join(stateDir, "qsdm_accounts.json")); err != nil {
		t.Fatal(err)
	}
	tasks := chain.NewTaskStateStore()
	if _, err := replayTaskStateFromBlocks(tasks, blocks); err != nil {
		t.Fatal(err)
	}
	enrollments := enrollment.NewInMemoryState()
	if _, err := enrollments.Load(filepath.Join(stateDir, "qsdm_enrollment.json")); err != nil {
		t.Fatal(err)
	}
	applier := chain.NewEnrollmentAwareApplier(
		accounts,
		chain.NewEnrollmentApplier(accounts, enrollments),
	)
	applier.SetTaskStateStore(tasks)

	tip := blocks[len(blocks)-1]
	if got := applier.StateRoot(); got != tip.StateRoot {
		t.Fatalf("pre-state mismatch: got %s want %s at height %d", got, tip.StateRoot, tip.Height)
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(blockURL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("canonical block endpoint returned %s", resp.Status)
	}
	var window struct {
		Blocks []*chain.Block `json:"blocks"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&window); err != nil {
		t.Fatal(err)
	}
	if len(window.Blocks) != 1 || window.Blocks[0] == nil {
		t.Fatalf("expected exactly one canonical block, got %d", len(window.Blocks))
	}
	block := window.Blocks[0]
	for index, tx := range block.Transactions {
		if err := applier.ApplyTx(tx); err != nil {
			t.Fatalf("tx %d %s: %v", index, tx.ID, err)
		}
		t.Logf("after tx %d %s root=%s", index, tx.ID, applier.StateRoot())
	}
	if got := applier.StateRoot(); got != block.StateRoot {
		t.Fatalf("post-state mismatch: got %s want %s at height %d", got, block.StateRoot, block.Height)
	}
}
