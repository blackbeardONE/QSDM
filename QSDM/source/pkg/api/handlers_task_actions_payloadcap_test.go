package api

// GHSA-h5gg, owner decision 2026-11-15: one task action carries at most 4096 bytes of payload (it was 100,000).
// Larger is refused at submit with 413 and nothing is written to the ledger or the mempool.

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/wallet"
)

func capEnv(payload string) QSDMTaskActionEnvelope {
	return QSDMTaskActionEnvelope{
		ID: strings.Repeat("a", 32), Sender: strings.Repeat("b", 64), TaskID: "catalog-task",
		Action: chain.TaskActionCatalogRegister, Payload: payload,
		Timestamp: time.Now().UTC().Format(time.RFC3339), PublicKey: "01", Signature: "02",
	}
}

func TestTaskActionPayloadCap_Boundary(t *testing.T) {
	if QSDMTaskActionMaxPayloadBytes != 4096 {
		t.Fatalf("cap %d, want 4096", QSDMTaskActionMaxPayloadBytes)
	}
	if err := validateQSDMTaskActionEnvelope(capEnv(strings.Repeat("x", 4096))); err != nil {
		t.Fatalf("4096 bytes refused: %v", err)
	}
	err := validateQSDMTaskActionEnvelope(capEnv(strings.Repeat("x", 4097)))
	if !errors.Is(err, ErrTaskActionPayloadTooLarge) {
		t.Fatalf("4097 bytes: want ErrTaskActionPayloadTooLarge, got %v", err)
	}
	// bytes, not characters: 2,049 two-byte runes are 4,098 bytes
	if err := validateQSDMTaskActionEnvelope(capEnv(strings.Repeat("é", 2049))); !errors.Is(err, ErrTaskActionPayloadTooLarge) {
		t.Fatalf("multi-byte payload over 4096 bytes: got %v", err)
	}
}

func TestTaskActionPayloadCap_SubmitAnswers413AndWritesNothing(t *testing.T) {
	ws, err := wallet.NewWalletService()
	if err != nil {
		t.Skipf("wallet requires Dilithium: %v", err)
	}
	actionLog := filepath.Join(t.TempDir(), "task-actions.jsonl")
	t.Setenv(qsdmTaskActionLogPathEnv, actionLog)
	sub := &fakeSubmitter{}
	SetTaskActionMempool(sub)
	t.Cleanup(func() { SetTaskActionMempool(nil) })
	h := setupTestHandlersWithSubmesh(nil, ws)

	env := buildSignedTaskAction(t, ws, "task-1", "start")
	env.Payload = strings.Repeat("x", 100000) // the old allowance
	signTaskActionEnvelope(t, ws, &env)
	rec := postTaskAction(t, h, env)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d, want 413; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "4096") {
		t.Fatalf("the answer does not say the limit: %s", rec.Body.String())
	}
	if _, err := os.Stat(actionLog); !os.IsNotExist(err) {
		t.Fatal("an oversized action was written to the ledger")
	}

	ok := buildSignedTaskAction(t, ws, "task-1", "start")
	ok.Payload = strings.Repeat("x", 4096)
	signTaskActionEnvelope(t, ws, &ok)
	if rec := postTaskAction(t, h, ok); rec.Code != http.StatusOK {
		t.Fatalf("a 4096-byte payload was refused: %d %s", rec.Code, rec.Body.String())
	}
}
