package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/wallet"
)

// A signed task action whose ID already belongs to another transaction (the
// chain submitter answers chain.ErrTxIDTaken) is refused with 409 like a
// duplicate wallet transfer, and nothing is written to the action log.
func TestQSDMTaskActionSubmitSigned_RefusesATakenID(t *testing.T) {
	ws, err := wallet.NewWalletService()
	if err != nil {
		t.Skipf("wallet requires Dilithium: %v", err)
	}
	actionLog := filepath.Join(t.TempDir(), "task-actions.jsonl")
	t.Setenv(qsdmTaskActionLogPathEnv, actionLog)
	pool := &fakeSubmitter{err: fmt.Errorf("%w: %q already has a receipt", chain.ErrTxIDTaken, "victim")}
	SetTaskActionMempool(pool)
	t.Cleanup(func() { SetTaskActionMempool(nil) })
	h := setupTestHandlersWithSubmesh(nil, ws)

	env := buildSignedTaskAction(t, ws, "task-1", "start")
	signTaskActionEnvelope(t, ws, &env)
	rec := postTaskAction(t, h, env)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	var resp QSDMTaskActionSubmitResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Status != "duplicate" || resp.MempoolStatus != "id_taken" || resp.MempoolSubmitted {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if b, err := os.ReadFile(actionLog); err == nil && len(b) != 0 {
		t.Fatalf("the refused action was written to the action log: %q", b)
	}
	if len(pool.added) != 0 {
		t.Fatal("the refused action reached the mempool")
	}
}
