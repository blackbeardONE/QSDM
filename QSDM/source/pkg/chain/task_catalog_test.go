package chain

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

func testTaskManifest(t *testing.T, taskID, manager string, version uint64) string {
	t.Helper()
	manifest := TaskManifest{
		SchemaVersion:      TaskCatalogSchemaVersion,
		TaskID:             taskID,
		Version:            version,
		Name:               "Shared Edge Work",
		Description:        "A consensus-published task used by every compatible Hive client.",
		Manager:            manager,
		Active:             true,
		MinimumStakeAmount: 1,
		RewardPerRound:     0.25,
		RoundTime:          60,
		SubmissionWindow:   30,
		AuditWindow:        15,
		MetadataURL:        "https://qsdm.tech/tasks/shared-edge",
		SourceURL:          "https://qsdm.tech/docs/#/tasks/shared-edge",
		IconURL:            "https://qsdm.tech/assets/qsdm-task.png",
		Tags:               []string{"CELL", "qsdm", "cell"},
		AuthorizedRelayIDs: []string{strings.Repeat("A", 64), strings.Repeat("a", 64)},
		Runtime: TaskRuntimeManifest{
			Kind:              "capability",
			Capability:        "generic-proof-v1",
			MinHiveVersion:    "1.3.60",
			MaxMemoryMB:       256,
			MaxRuntimeSeconds: 30,
		},
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal task manifest: %v", err)
	}
	return string(raw)
}

func testCatalogAction(t *testing.T, id, action, taskID, sender string, nonce, version uint64) TaskAction {
	t.Helper()
	result := TaskAction{
		ID:        id,
		Action:    action,
		TaskID:    taskID,
		Sender:    sender,
		Nonce:     nonce,
		Timestamp: "2026-07-01T00:00:00Z",
	}
	if action == TaskActionCatalogRegister || action == TaskActionCatalogUpdate {
		result.Payload = testTaskManifest(t, taskID, sender, version)
	}
	return result
}

func TestTaskCatalog_RegisterAndClone(t *testing.T) {
	store := NewTaskStateStore()
	action := testCatalogAction(t, "catalog-register-1", TaskActionCatalogRegister, "shared-edge", "alice", 1, 1)
	rootBefore := store.StateRoot()
	if err := store.ApplyAction(action); err != nil {
		t.Fatalf("catalog register: %v", err)
	}

	state, ok := store.GetTask("shared-edge")
	if !ok || state.Manifest == nil {
		t.Fatal("registered manifest is missing")
	}
	if state.Manifest.Manager != "alice" || state.Manifest.Version != 1 || state.Manifest.Runtime.Capability != "generic-proof-v1" {
		t.Fatalf("unexpected manifest: %+v", state.Manifest)
	}
	if got := strings.Join(state.Manifest.Tags, ","); got != "cell,qsdm" {
		t.Fatalf("normalized tags: got %q want %q", got, "cell,qsdm")
	}
	if got := strings.Join(state.Manifest.AuthorizedRelayIDs, ","); got != strings.Repeat("a", 64) {
		t.Fatalf("normalized Relay ids: got %q", got)
	}
	if rootAfter := store.StateRoot(); rootAfter == rootBefore {
		t.Fatal("catalog registration did not change state root")
	}

	clone, ok := store.ChainReplayClone().(*TaskStateStore)
	if !ok {
		t.Fatal("replay clone has unexpected type")
	}
	clonedState, ok := clone.GetTask("shared-edge")
	if !ok || clonedState.Manifest == nil || clonedState.Manifest.Version != 1 {
		t.Fatalf("catalog manifest missing from replay clone: %+v", clonedState.Manifest)
	}
	clonedState.Manifest.Tags[0] = "changed"
	clonedState.Manifest.AuthorizedRelayIDs = append(clonedState.Manifest.AuthorizedRelayIDs, "changed")
	original, _ := store.GetTask("shared-edge")
	if original.Manifest.Tags[0] == "changed" {
		t.Fatal("catalog manifest tags were not deep-cloned")
	}
	if len(original.Manifest.AuthorizedRelayIDs) != 1 {
		t.Fatal("catalog authorized Relay ids were not deep-cloned")
	}
}

func TestTaskCatalogRejectsInvalidAuthorizedRelayID(t *testing.T) {
	action := testCatalogAction(t, "catalog-register-relay", TaskActionCatalogRegister, "shared-edge", "alice", 1, 1)
	var manifest TaskManifest
	if err := json.Unmarshal([]byte(action.Payload), &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.AuthorizedRelayIDs = []string{"not-a-relay-id"}
	raw, _ := json.Marshal(manifest)
	action.Payload = string(raw)
	if err := NewTaskStateStore().ApplyAction(action); err == nil || !strings.Contains(err.Error(), "authorized Relay ids") {
		t.Fatalf("invalid authorized Relay id returned %v", err)
	}
}

func TestTaskCatalogRootChangesOnlyForCatalogMutations(t *testing.T) {
	store := NewTaskStateStore()
	if got := store.CatalogRoot(); got != "" {
		t.Fatalf("empty catalog root: got %q want empty", got)
	}
	if err := store.ApplyAction(testCatalogAction(t, "catalog-register-1", TaskActionCatalogRegister, "shared-edge", "alice", 1, 1)); err != nil {
		t.Fatalf("catalog register: %v", err)
	}
	registeredRoot := store.CatalogRoot()
	if registeredRoot == "" {
		t.Fatal("registered catalog has an empty root")
	}

	if err := store.ApplyAction(TaskAction{
		ID: "stake-1", Sender: "worker", TaskID: "shared-edge", Action: "stake", Amount: 2,
	}); err != nil {
		t.Fatalf("stake action: %v", err)
	}
	if got := store.CatalogRoot(); got != registeredRoot {
		t.Fatalf("stake changed catalog root: got %q want %q", got, registeredRoot)
	}
	if err := store.ApplyAction(TaskAction{
		ID: "start-1", Sender: "worker", TaskID: "shared-edge", Action: "start",
	}); err != nil {
		t.Fatalf("start action: %v", err)
	}
	if got := store.CatalogRoot(); got != registeredRoot {
		t.Fatalf("runtime state changed catalog root: got %q want %q", got, registeredRoot)
	}

	if err := store.ApplyAction(TaskAction{
		ID: "catalog-pause-1", Sender: "alice", TaskID: "shared-edge",
		Action: TaskActionCatalogPause, Nonce: 2, Timestamp: "2026-07-01T00:01:00Z",
	}); err != nil {
		t.Fatalf("catalog pause: %v", err)
	}
	if got := store.CatalogRoot(); got == registeredRoot {
		t.Fatal("catalog pause did not change catalog root")
	}
}

func TestTaskCatalog_RejectsDuplicateAndInvalidInitialVersion(t *testing.T) {
	store := NewTaskStateStore()
	invalid := testCatalogAction(t, "catalog-register-invalid", TaskActionCatalogRegister, "shared-edge", "alice", 1, 2)
	if err := store.ApplyAction(invalid); !errors.Is(err, ErrTaskManifestVersion) {
		t.Fatalf("initial version: want ErrTaskManifestVersion, got %v", err)
	}

	valid := testCatalogAction(t, "catalog-register-1", TaskActionCatalogRegister, "shared-edge", "alice", 1, 1)
	if err := store.ApplyAction(valid); err != nil {
		t.Fatalf("catalog register: %v", err)
	}
	duplicate := testCatalogAction(t, "catalog-register-2", TaskActionCatalogRegister, "shared-edge", "alice", 2, 1)
	if err := store.ApplyAction(duplicate); !errors.Is(err, ErrTaskManifestExists) {
		t.Fatalf("duplicate register: want ErrTaskManifestExists, got %v", err)
	}
}

func TestTaskCatalog_UpdateRequiresManagerAndNextVersion(t *testing.T) {
	store := NewTaskStateStore()
	if err := store.ApplyAction(testCatalogAction(t, "catalog-register-1", TaskActionCatalogRegister, "shared-edge", "alice", 1, 1)); err != nil {
		t.Fatalf("catalog register: %v", err)
	}

	attacker := testCatalogAction(t, "catalog-update-attacker", TaskActionCatalogUpdate, "shared-edge", "eve", 1, 2)
	if err := store.ApplyAction(attacker); !errors.Is(err, ErrTaskManifestManagerMismatch) {
		t.Fatalf("attacker update: want ErrTaskManifestManagerMismatch, got %v", err)
	}
	skipped := testCatalogAction(t, "catalog-update-skipped", TaskActionCatalogUpdate, "shared-edge", "alice", 2, 3)
	if err := store.ApplyAction(skipped); !errors.Is(err, ErrTaskManifestVersion) {
		t.Fatalf("skipped version: want ErrTaskManifestVersion, got %v", err)
	}
	valid := testCatalogAction(t, "catalog-update-2", TaskActionCatalogUpdate, "shared-edge", "alice", 2, 2)
	if err := store.ApplyAction(valid); err != nil {
		t.Fatalf("manager update: %v", err)
	}
	state, _ := store.GetTask("shared-edge")
	if state.Manifest.Version != 2 {
		t.Fatalf("manifest version: got %d want 2", state.Manifest.Version)
	}
}

func TestTaskCatalog_PauseResumeRequiresManager(t *testing.T) {
	store := NewTaskStateStore()
	if err := store.ApplyAction(testCatalogAction(t, "catalog-register-1", TaskActionCatalogRegister, "shared-edge", "alice", 1, 1)); err != nil {
		t.Fatalf("catalog register: %v", err)
	}
	if err := store.ApplyAction(TaskAction{ID: "catalog-pause-eve", Action: TaskActionCatalogPause, TaskID: "shared-edge", Sender: "eve", Nonce: 1}); !errors.Is(err, ErrTaskManifestManagerMismatch) {
		t.Fatalf("attacker pause: want ErrTaskManifestManagerMismatch, got %v", err)
	}
	if err := store.ApplyAction(TaskAction{ID: "catalog-pause-1", Action: TaskActionCatalogPause, TaskID: "shared-edge", Sender: "alice", Nonce: 2}); err != nil {
		t.Fatalf("manager pause: %v", err)
	}
	paused, _ := store.GetTask("shared-edge")
	if !paused.CatalogPaused {
		t.Fatal("catalog task was not paused")
	}
	if err := store.ApplyAction(TaskAction{ID: "catalog-resume-1", Action: TaskActionCatalogResume, TaskID: "shared-edge", Sender: "alice", Nonce: 3}); err != nil {
		t.Fatalf("manager resume: %v", err)
	}
	resumed, _ := store.GetTask("shared-edge")
	if resumed.CatalogPaused {
		t.Fatal("catalog task was not resumed")
	}
}

func TestTaskCatalog_RejectsUnsafeRuntime(t *testing.T) {
	action := testCatalogAction(t, "catalog-register-1", TaskActionCatalogRegister, "unsafe-task", "alice", 1, 1)
	var manifest TaskManifest
	if err := json.Unmarshal([]byte(action.Payload), &manifest); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	manifest.Runtime.ModuleURL = "https://example.com/task.js"
	manifest.Runtime.ModuleSHA256 = strings.Repeat("a", 64)
	raw, _ := json.Marshal(manifest)
	action.Payload = string(raw)
	if err := NewTaskStateStore().ApplyAction(action); err == nil || !strings.Contains(err.Error(), "cannot declare a remote module") {
		t.Fatalf("unsafe capability runtime was accepted: %v", err)
	}

	manifest.Runtime = TaskRuntimeManifest{
		Kind:         "wasm",
		ModuleURL:    "https://example.com/task.wasm",
		ModuleSHA256: "not-a-sha256",
		ABI:          "qsdm-task-v1",
	}
	raw, _ = json.Marshal(manifest)
	action.Payload = string(raw)
	if err := NewTaskStateStore().ApplyAction(action); err == nil || !strings.Contains(err.Error(), "module_sha256") {
		t.Fatalf("invalid wasm hash was accepted: %v", err)
	}
}

func TestTaskCatalog_ApplyEconomicTxChargesOnlyFee(t *testing.T) {
	accounts := NewAccountStore()
	accounts.Credit("alice", 10)
	store := NewTaskStateStore()
	action := testCatalogAction(t, "catalog-register-1", TaskActionCatalogRegister, "shared-edge", "alice", 0, 1)
	payload, err := json.Marshal(action)
	if err != nil {
		t.Fatalf("marshal action: %v", err)
	}
	if err := store.ApplyEconomicTx(&mempool.Tx{
		ID:         action.ID,
		Sender:     action.Sender,
		Fee:        0.1,
		Nonce:      0,
		ContractID: TaskContractID,
		Payload:    payload,
	}, accounts); err != nil {
		t.Fatalf("ApplyEconomicTx: %v", err)
	}
	alice, _ := accounts.Get("alice")
	if alice.Balance != 9.9 {
		t.Fatalf("catalog charge: got %.8f want 9.9", alice.Balance)
	}
	state, ok := store.GetTask("shared-edge")
	if !ok || state.Manifest == nil {
		t.Fatal("economic catalog transaction did not persist manifest")
	}
}
