package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/wallet"
)

func migrateTestManifest(t *testing.T, taskID, manager string) string {
	t.Helper()
	raw, err := json.Marshal(chain.TaskManifest{
		SchemaVersion:      chain.TaskCatalogSchemaVersion,
		TaskID:             taskID,
		Version:            1,
		Name:               "Migrate test task",
		Description:        "A catalog task used by the migrate authorization tests.",
		Manager:            manager,
		Active:             true,
		MinimumStakeAmount: 1,
		RewardPerRound:     0.25,
		RoundTime:          60,
		SubmissionWindow:   30,
		AuditWindow:        15,
		MetadataURL:        "https://qsdm.tech/tasks/migrate-test",
		SourceURL:          "https://qsdm.tech/docs/#/tasks/migrate-test",
		IconURL:            "https://qsdm.tech/assets/qsdm-task.png",
		Tags:               []string{"CELL", "qsdm"},
		AuthorizedRelayIDs: []string{strings.Repeat("a", 64)},
		Runtime: chain.TaskRuntimeManifest{
			Kind:              "capability",
			Capability:        "generic-proof-v1",
			MinHiveVersion:    "1.3.60",
			MaxMemoryMB:       256,
			MaxRuntimeSeconds: 30,
		},
	})
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	return string(raw)
}

func TestQSDMTaskActionSubmitSigned_MigrateRequiresTheManager(t *testing.T) {
	ws, err := wallet.NewWalletService()
	if err != nil {
		t.Skipf("wallet requires Dilithium: %v", err)
	}
	pub := ws.GetPublicKey()
	sum := sha256.Sum256(pub)
	me := hex.EncodeToString(sum[:])
	other := strings.Repeat("e", 64)

	store := chain.NewTaskStateStore()
	for i, reg := range []struct{ task, manager string }{
		{"theirs", other}, {"mine", me}, {"mine-v2", me},
	} {
		if err := store.ApplyAction(chain.TaskAction{
			ID: "reg-" + reg.task, Action: chain.TaskActionCatalogRegister, TaskID: reg.task, Sender: reg.manager,
			Nonce: uint64(i + 1), Timestamp: "2026-11-13T00:00:00Z", Payload: migrateTestManifest(t, reg.task, reg.manager),
		}); err != nil {
			t.Fatalf("register %s: %v", reg.task, err)
		}
	}

	t.Setenv(qsdmTaskActionLogPathEnv, filepath.Join(t.TempDir(), "task-actions.jsonl"))
	pool := &fakeSubmitter{}
	SetTaskActionMempool(pool)
	t.Cleanup(func() { SetTaskActionMempool(nil) })
	h := setupTestHandlersWithSubmesh(nil, ws)

	migrate := func(task, target string) QSDMTaskActionEnvelope {
		env := buildSignedTaskAction(t, ws, task, "migrate")
		env.Payload = `{"migrated_to":"` + target + `"}`
		signTaskActionEnvelope(t, ws, &env)
		return env
	}

	SetTaskStateProvider(nil)
	if rec := postTaskAction(t, h, migrate("mine", "mine-v2")); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("no task state: status %d, want 503; body=%s", rec.Code, rec.Body.String())
	}
	SetTaskStateProvider(store)
	t.Cleanup(func() { SetTaskStateProvider(nil) })

	rec := postTaskAction(t, h, migrate("theirs", "mine"))
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "manager mismatch") {
		t.Fatalf("foreign migrate: status %d, want 403 manager mismatch; body=%s", rec.Code, rec.Body.String())
	}
	rec = postTaskAction(t, h, migrate("mine", "theirs"))
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "target") {
		t.Fatalf("migrate to another manager's task: status %d, want 403 target; body=%s", rec.Code, rec.Body.String())
	}
	if len(pool.added) != 0 {
		t.Fatalf("a refused migrate reached the mempool: %d txs", len(pool.added))
	}

	rec = postTaskAction(t, h, migrate("mine", "mine-v2"))
	if rec.Code != http.StatusOK {
		t.Fatalf("manager migrate: status %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if len(pool.added) != 1 {
		t.Fatalf("manager migrate: mempool got %d txs, want 1", len(pool.added))
	}
}
