package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/wallet"
)

// 2026-11-18 report: the task-action log's NonceReplay flag was never set. It now uses the chain's check: a nonce
// at or below the sender's last applied nonce is refused before the mempool and before the log.
func TestQSDMTaskActionSubmitSigned_ChainNonceReplayRefusedByLog(t *testing.T) {
	ws, err := wallet.NewWalletService()
	if err != nil {
		t.Skipf("wallet requires Dilithium: %v", err)
	}
	actionLog := filepath.Join(t.TempDir(), "task-actions.jsonl")
	t.Setenv(qsdmTaskActionLogPathEnv, actionLog)
	pool := &fakeSubmitter{}
	SetTaskActionMempool(pool)
	t.Cleanup(func() { SetTaskActionMempool(nil) })
	h := setupTestHandlersWithSubmesh(nil, ws)

	replay := buildSignedTaskAction(t, ws, "task-1", "stop")
	replay.Nonce = 8
	signTaskActionEnvelope(t, ws, &replay)

	store := chain.NewTaskStateStore()
	if err := store.ApplyAction(chain.TaskAction{ID: "applied-37", Sender: replay.Sender, TaskID: "task-1", Action: "stake",
		Amount: 1, Nonce: 37, Timestamp: "2026-11-18T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	SetTaskStateProvider(store)
	t.Cleanup(func() { SetTaskStateProvider(nil) })

	res, err := inspectQSDMTaskActionLog(actionLog, replay)
	if err != nil || !res.NonceReplay || res.LastNonce != 37 {
		t.Fatalf("inspect: %+v %v, want NonceReplay with last nonce 37", res, err)
	}

	rec := postTaskAction(t, h, replay)
	if rec.Code != http.StatusConflict {
		t.Fatalf("replayed nonce: status %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	var resp QSDMTaskActionSubmitResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Status != "nonce_replay" || resp.LastNonce != 37 {
		t.Fatalf("response %+v", resp)
	}
	if len(pool.added) != 0 {
		t.Fatalf("a replayed nonce reached the mempool")
	}
	if b, _ := os.ReadFile(actionLog); len(b) != 0 {
		t.Fatalf("a replayed nonce was written to the log")
	}

	fresh := buildSignedTaskAction(t, ws, "task-1", "stop")
	fresh.Nonce = 38
	signTaskActionEnvelope(t, ws, &fresh)
	if rec := postTaskAction(t, h, fresh); rec.Code != http.StatusOK {
		t.Fatalf("next nonce: status %d; body=%s", rec.Code, rec.Body.String())
	}
}
