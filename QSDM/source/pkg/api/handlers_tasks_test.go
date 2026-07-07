package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/wallet"
)

func writeTaskRegistry(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "qsdm_tasks.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write task registry: %v", err)
	}
	return path
}

func apiTaskManifestPayload(t *testing.T, taskID, manager, name string) string {
	t.Helper()
	raw, err := json.Marshal(chain.TaskManifest{
		SchemaVersion:      chain.TaskCatalogSchemaVersion,
		TaskID:             taskID,
		Version:            1,
		Name:               name,
		Description:        "Published through QSDM consensus.",
		Manager:            manager,
		Active:             true,
		MinimumStakeAmount: 2,
		RewardPerRound:     0.5,
		RoundTime:          120,
		SubmissionWindow:   60,
		AuditWindow:        30,
		MetadataURL:        "https://qsdm.tech/tasks/consensus-task",
		Tags:               []string{"qsdm", "cell"},
		Runtime: chain.TaskRuntimeManifest{
			Kind:           "capability",
			Capability:     "generic-proof-v1",
			MinHiveVersion: "1.3.60",
		},
	})
	if err != nil {
		t.Fatalf("marshal API task manifest: %v", err)
	}
	return string(raw)
}

func TestQSDMTasksListHandler_Unconfigured(t *testing.T) {
	t.Setenv(qsdmTaskRegistryPathEnv, "")
	t.Setenv(qsdmTaskActionLogPathEnv, "")
	h := setupTestHandlers()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil)
	rec := httptest.NewRecorder()
	h.QSDMTasksListHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var got QSDMTasksListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Configured {
		t.Fatal("configured = true, want false")
	}
	if got.Runtime != "qsdm-native" {
		t.Fatalf("runtime = %q, want qsdm-native", got.Runtime)
	}
	if len(got.Tasks) != 0 {
		t.Fatalf("tasks len = %d, want 0", len(got.Tasks))
	}
	if !strings.Contains(rec.Body.String(), `"tasks":[]`) {
		t.Fatalf("empty catalog must encode as an array, body=%s", rec.Body.String())
	}
}

func TestQSDMTasksListHandler_LoadsRegistryAndDefaults(t *testing.T) {
	path := writeTaskRegistry(t, `{
		"tasks": [
			{
				"task_id": "task-1",
				"task_name": "Demo Task",
				"is_allowlisted": true,
				"is_active": true,
				"minimum_stake_amount": 3,
				"submissions": {
					"4": {
						"miner-address": {
							"submission_value": "proof",
							"slot": 10
						}
					}
				}
			}
		]
	}`)
	t.Setenv(qsdmTaskRegistryPathEnv, path)
	t.Setenv(qsdmTaskActionLogPathEnv, "")
	h := setupTestHandlers()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil)
	rec := httptest.NewRecorder()
	h.QSDMTasksListHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var got QSDMTasksListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !got.Configured {
		t.Fatal("configured = false, want true")
	}
	if got.Source != qsdmTaskRegistrySource {
		t.Fatalf("source = %q, want %q", got.Source, qsdmTaskRegistrySource)
	}
	if len(got.Tasks) != 1 {
		t.Fatalf("tasks len = %d, want 1", len(got.Tasks))
	}
	task := got.Tasks[0]
	if task.TaskID != "task-1" || task.TaskName != "Demo Task" {
		t.Fatalf("task identity = %q/%q", task.TaskID, task.TaskName)
	}
	if task.TaskManager != qsdmTaskDefaultPublicKey {
		t.Fatalf("task manager default = %q", task.TaskManager)
	}
	if task.StakePotAccount != qsdmTaskDefaultPublicKey {
		t.Fatalf("stake pot default = %q", task.StakePotAccount)
	}
	if task.TaskExecutableNetwork != "IPFS" {
		t.Fatalf("task executable network = %q, want IPFS", task.TaskExecutableNetwork)
	}
	if task.NativeRuntime != "qsdm" {
		t.Fatalf("native runtime = %q, want qsdm", task.NativeRuntime)
	}
	if task.TaskType != "CELL" {
		t.Fatalf("task type = %q, want CELL", task.TaskType)
	}
}

func TestNormalizeQSDMTask_CanonicalizesLegacyTaskTypes(t *testing.T) {
	tests := []struct {
		name      string
		taskType  string
		tokenType string
		want      string
	}{
		{name: "missing native type", want: "CELL"},
		{name: "legacy native type", taskType: "KOII", want: "CELL"},
		{name: "native cell type", taskType: "CELL", want: "CELL"},
		{name: "explicit kpl type", taskType: "KPL", want: "KPL"},
		{name: "legacy token task", taskType: "KOII", tokenType: "mint", want: "KPL"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeQSDMTask(QSDMTask{
				TaskID:    "task-type-test",
				TaskType:  tt.taskType,
				TokenType: tt.tokenType,
			})
			if got.TaskType != tt.want {
				t.Fatalf("task type = %q, want %q", got.TaskType, tt.want)
			}
		})
	}
}

func TestQSDMTasksListHandler_AcceptsUTF8BOM(t *testing.T) {
	path := filepath.Join(t.TempDir(), "qsdm_tasks_bom.json")
	body := append([]byte{0xEF, 0xBB, 0xBF}, []byte(`{
		"tasks": [{
			"task_id": "task-bom",
			"task_name": "BOM Task",
			"is_allowlisted": true,
			"is_active": true
		}]
	}`)...)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write BOM task registry: %v", err)
	}
	t.Setenv(qsdmTaskRegistryPathEnv, path)
	t.Setenv(qsdmTaskActionLogPathEnv, "")
	h := setupTestHandlers()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil)
	rec := httptest.NewRecorder()
	h.QSDMTasksListHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got QSDMTasksListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(got.Tasks) != 1 || got.Tasks[0].TaskID != "task-bom" {
		t.Fatalf("tasks = %#v, want task-bom", got.Tasks)
	}
}

func TestQSDMTasksListHandler_IncludesConsensusCatalogWithoutRegistry(t *testing.T) {
	t.Setenv(qsdmTaskRegistryPathEnv, "")
	t.Setenv(qsdmTaskActionLogPathEnv, "")
	manager := strings.Repeat("a", 64)
	live := chain.NewTaskStateStore()
	if err := live.ApplyAction(chain.TaskAction{
		ID:        "catalog-register-api-1",
		Sender:    manager,
		TaskID:    "consensus-task",
		Action:    chain.TaskActionCatalogRegister,
		Payload:   apiTaskManifestPayload(t, "consensus-task", manager, "Consensus Task"),
		Nonce:     1,
		Timestamp: "2026-07-01T00:00:00Z",
	}); err != nil {
		t.Fatalf("seed catalog task: %v", err)
	}
	SetTaskStateProvider(live)
	t.Cleanup(func() { SetTaskStateProvider(nil) })

	h := setupTestHandlers()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil)
	rec := httptest.NewRecorder()
	h.QSDMTasksListHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}

	var got QSDMTasksListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !got.Configured || got.CatalogSource != "chain" || got.CatalogStateRoot == "" {
		t.Fatalf("catalog metadata mismatch: configured=%v source=%q root=%q", got.Configured, got.CatalogSource, got.CatalogStateRoot)
	}
	if len(got.Tasks) != 1 {
		t.Fatalf("tasks len = %d, want 1", len(got.Tasks))
	}
	task := got.Tasks[0]
	if task.TaskID != "consensus-task" || task.TaskName != "Consensus Task" || task.Manifest == nil {
		t.Fatalf("consensus task mismatch: %+v", task)
	}
	if task.IsAllowlisted {
		t.Fatal("permissionless catalog task must not self-assert bootstrap allowlisting")
	}
	if !task.IsActive || task.CatalogVersion != 1 || task.TaskExecutableNetwork != "QSDM-CAPABILITY" {
		t.Fatalf("consensus runtime projection mismatch: %+v", task)
	}

	catalogRoot := got.CatalogStateRoot
	if err := live.ApplyAction(chain.TaskAction{
		ID: "stake-api-1", Sender: "worker", TaskID: "consensus-task", Action: "stake", Amount: 2,
	}); err != nil {
		t.Fatalf("stake catalog task: %v", err)
	}
	rec = httptest.NewRecorder()
	h.QSDMTasksListHandler(rec, httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil))
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response after stake: %v", err)
	}
	if got.CatalogStateRoot != catalogRoot {
		t.Fatalf("stake changed catalog root: got %q want %q", got.CatalogStateRoot, catalogRoot)
	}

	if err := live.ApplyAction(chain.TaskAction{
		ID: "catalog-pause-api-1", Sender: manager, TaskID: "consensus-task",
		Action: chain.TaskActionCatalogPause, Nonce: 2, Timestamp: "2026-07-01T00:01:00Z",
	}); err != nil {
		t.Fatalf("pause catalog task: %v", err)
	}
	rec = httptest.NewRecorder()
	h.QSDMTasksListHandler(rec, httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil))
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response after pause: %v", err)
	}
	if got.CatalogStateRoot == catalogRoot {
		t.Fatal("catalog pause did not change catalog root")
	}
}

func TestQSDMTasksListHandler_CatalogOverridesMetadataAndPreservesTrust(t *testing.T) {
	registryPath := writeTaskRegistry(t, `{"tasks":[{
		"task_id":"consensus-task",
		"task_name":"Old Bootstrap Name",
		"is_allowlisted":true,
		"is_active":true,
		"stake_list":{"stale-wallet":99},
		"total_stake_amount":99
	}]}`)
	t.Setenv(qsdmTaskRegistryPathEnv, registryPath)
	t.Setenv(qsdmTaskActionLogPathEnv, "")
	manager := strings.Repeat("b", 64)
	live := chain.NewTaskStateStore()
	if err := live.ApplyAction(chain.TaskAction{
		ID:        "catalog-register-api-1",
		Sender:    manager,
		TaskID:    "consensus-task",
		Action:    chain.TaskActionCatalogRegister,
		Payload:   apiTaskManifestPayload(t, "consensus-task", manager, "Current Chain Name"),
		Nonce:     1,
		Timestamp: "2026-07-01T00:00:00Z",
	}); err != nil {
		t.Fatalf("seed catalog task: %v", err)
	}
	if err := live.ApplyAction(chain.TaskAction{
		ID:        "catalog-stake-api-1",
		Sender:    manager,
		TaskID:    "consensus-task",
		Action:    "stake",
		Amount:    2,
		Nonce:     2,
		Timestamp: "2026-07-01T00:01:00Z",
	}); err != nil {
		t.Fatalf("seed catalog stake: %v", err)
	}
	if err := live.ApplyAction(chain.TaskAction{
		ID:        "catalog-pause-api-1",
		Sender:    manager,
		TaskID:    "consensus-task",
		Action:    chain.TaskActionCatalogPause,
		Nonce:     3,
		Timestamp: "2026-07-01T00:02:00Z",
	}); err != nil {
		t.Fatalf("pause catalog task: %v", err)
	}
	SetTaskStateProvider(live)
	t.Cleanup(func() { SetTaskStateProvider(nil) })

	h := setupTestHandlers()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil)
	rec := httptest.NewRecorder()
	h.QSDMTasksListHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var got QSDMTasksListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(got.Tasks) != 1 {
		t.Fatalf("tasks len = %d, want 1", len(got.Tasks))
	}
	task := got.Tasks[0]
	if task.TaskName != "Current Chain Name" || !task.IsAllowlisted {
		t.Fatalf("metadata/trust projection mismatch: %+v", task)
	}
	if task.IsActive || !task.CatalogPaused {
		t.Fatalf("paused task is still active: %+v", task)
	}
	if task.TotalStakeAmount != 2 || task.StakeList[manager] != 2 {
		t.Fatalf("live stake mismatch: %+v", task)
	}
	if _, stale := task.StakeList["stale-wallet"]; stale {
		t.Fatalf("stale bootstrap stake survived chain projection: %+v", task.StakeList)
	}
}

func TestQSDMTaskRouteHandler_ByIDAndSubmissions(t *testing.T) {
	path := writeTaskRegistry(t, `[
		{
			"task_id": "task/with slash",
			"task_name": "Routed Task",
			"is_allowlisted": true,
			"is_active": true,
			"submissions": {
				"1": {
					"miner": {
						"submission_value": "ok",
						"slot": 2
					}
				}
			}
		}
	]`)
	t.Setenv(qsdmTaskRegistryPathEnv, path)
	t.Setenv(qsdmTaskActionLogPathEnv, "")
	h := setupTestHandlers()

	taskReq := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/task%2Fwith%20slash", nil)
	taskRec := httptest.NewRecorder()
	h.QSDMTaskRouteHandler(taskRec, taskReq)

	if taskRec.Code != http.StatusOK {
		t.Fatalf("task status = %d, want 200; body=%s", taskRec.Code, taskRec.Body.String())
	}
	var taskResp QSDMTaskResponse
	if err := json.Unmarshal(taskRec.Body.Bytes(), &taskResp); err != nil {
		t.Fatalf("decode task response: %v", err)
	}
	if taskResp.Task.TaskID != "task/with slash" {
		t.Fatalf("task id = %q", taskResp.Task.TaskID)
	}

	subReq := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/task%2Fwith%20slash/submissions", nil)
	subRec := httptest.NewRecorder()
	h.QSDMTaskRouteHandler(subRec, subReq)

	if subRec.Code != http.StatusOK {
		t.Fatalf("submissions status = %d, want 200; body=%s", subRec.Code, subRec.Body.String())
	}
	var subResp QSDMTaskSubmissionsResponse
	if err := json.Unmarshal(subRec.Body.Bytes(), &subResp); err != nil {
		t.Fatalf("decode submissions response: %v", err)
	}
	if subResp.Submissions["1"]["miner"].SubmissionValue != "ok" {
		t.Fatalf("submission value = %q", subResp.Submissions["1"]["miner"].SubmissionValue)
	}
}

func TestQSDMTaskRouteHandler_NotFound(t *testing.T) {
	path := writeTaskRegistry(t, `{"tasks":[{"task_id":"known"}]}`)
	t.Setenv(qsdmTaskRegistryPathEnv, path)
	t.Setenv(qsdmTaskActionLogPathEnv, "")
	h := setupTestHandlers()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/missing", nil)
	rec := httptest.NewRecorder()
	h.QSDMTaskRouteHandler(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

func TestQSDMTaskStateProjection_OverlaysRegistry(t *testing.T) {
	ws, err := wallet.NewWalletService()
	if err != nil {
		t.Skipf("wallet requires Dilithium: %v", err)
	}
	registryPath := writeTaskRegistry(t, `{"tasks":[{"task_id":"task-1","task_name":"Projected Task"}]}`)
	actionLogPath := filepath.Join(t.TempDir(), "task-actions.jsonl")
	t.Setenv(qsdmTaskRegistryPathEnv, registryPath)
	t.Setenv(qsdmTaskActionLogPathEnv, actionLogPath)
	SetTaskActionMempool(&fakeSubmitter{})
	t.Cleanup(func() { SetTaskActionMempool(nil) })
	h := setupTestHandlersWithSubmesh(nil, ws)

	stake := buildSignedTaskAction(t, ws, "task-1", "stake")
	stake.Amount = 7
	stake.Nonce = 1
	signTaskActionEnvelope(t, ws, &stake)
	if rec := postTaskAction(t, h, stake); rec.Code != http.StatusOK {
		t.Fatalf("stake status = %d body=%s", rec.Code, rec.Body.String())
	}
	start := buildSignedTaskAction(t, ws, "task-1", "start")
	start.Nonce = 2
	signTaskActionEnvelope(t, ws, &start)
	if rec := postTaskAction(t, h, start); rec.Code != http.StatusOK {
		t.Fatalf("start status = %d body=%s", rec.Code, rec.Body.String())
	}
	fund := buildSignedTaskAction(t, ws, "task-1", "fund")
	fund.Amount = 5
	fund.Nonce = 3
	signTaskActionEnvelope(t, ws, &fund)
	if rec := postTaskAction(t, h, fund); rec.Code != http.StatusOK {
		t.Fatalf("fund status = %d body=%s", rec.Code, rec.Body.String())
	}
	submit := buildSignedTaskAction(t, ws, "task-1", "submit")
	submit.Payload = `{"round":3,"slot":9,"submission_value":"proof-cid","reward_amount":2}`
	submit.Nonce = 4
	signTaskActionEnvelope(t, ws, &submit)
	if rec := postTaskAction(t, h, submit); rec.Code != http.StatusOK {
		t.Fatalf("submit status = %d body=%s", rec.Code, rec.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil)
	rec := httptest.NewRecorder()
	h.QSDMTasksListHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("tasks status = %d body=%s", rec.Code, rec.Body.String())
	}
	var tasksResp QSDMTasksListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &tasksResp); err != nil {
		t.Fatalf("decode tasks response: %v", err)
	}
	task := tasksResp.Tasks[0]
	if task.StakeList[start.Sender] != 7 || task.TotalStakeAmount != 7 {
		t.Fatalf("stake projection mismatch: %+v", task)
	}
	if task.AvailableBalances[start.Sender] != 2 || task.RewardPoolAmount != 3 || task.PendingRewardAmount != 2 {
		t.Fatalf("reward projection mismatch: %+v", task)
	}
	if task.Submissions["3"][start.Sender].SubmissionValue != "proof-cid" ||
		task.Submissions["3"][start.Sender].RewardAmount != 2 {
		t.Fatalf("submission projection mismatch: %+v", task.Submissions)
	}
	if task.DistributionRewardsSubmission["3"][start.Sender].RewardAmount != 2 {
		t.Fatalf("distribution reward projection mismatch: %+v", task.DistributionRewardsSubmission)
	}

	stateReq := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/task-1/state", nil)
	stateRec := httptest.NewRecorder()
	h.QSDMTaskRouteHandler(stateRec, stateReq)
	if stateRec.Code != http.StatusOK {
		t.Fatalf("state status = %d body=%s", stateRec.Code, stateRec.Body.String())
	}
	var stateResp QSDMTaskStateResponse
	if err := json.Unmarshal(stateRec.Body.Bytes(), &stateResp); err != nil {
		t.Fatalf("decode state response: %v", err)
	}
	if stateResp.Task.RunningCount != 1 {
		t.Fatalf("running_count = %d, want 1", stateResp.Task.RunningCount)
	}
	if stateResp.Task.Participants[start.Sender].SubmissionCount != 1 {
		t.Fatalf("participant projection mismatch: %+v", stateResp.Task.Participants)
	}
	if stateResp.Task.RewardPoolAmount != 3 || stateResp.Task.PendingRewardAmount != 2 {
		t.Fatalf("state reward projection mismatch: %+v", stateResp.Task)
	}
}

func TestQSDMTaskStateProjection_IgnoresUnfundedRewardSubmit(t *testing.T) {
	registryPath := writeTaskRegistry(t, `{"tasks":[{"task_id":"task-1","task_name":"Projected Task"}]}`)
	actionLogPath := filepath.Join(t.TempDir(), "task-actions.jsonl")
	t.Setenv(qsdmTaskRegistryPathEnv, registryPath)
	t.Setenv(qsdmTaskActionLogPathEnv, actionLogPath)
	h := setupTestHandlers()

	_, err := appendQSDMTaskAction(actionLogPath, QSDMTaskActionRecord{
		ReceivedAt: "2026-06-06T00:00:00Z",
		Envelope: QSDMTaskActionEnvelope{
			ID:        "stake-1",
			Sender:    "alice",
			TaskID:    "task-1",
			Action:    "stake",
			Amount:    1,
			Nonce:     1,
			Timestamp: "2026-06-06T00:00:00Z",
		},
	})
	if err != nil {
		t.Fatalf("append stake action: %v", err)
	}
	_, err = appendQSDMTaskAction(actionLogPath, QSDMTaskActionRecord{
		ReceivedAt: "2026-06-06T00:01:00Z",
		Envelope: QSDMTaskActionEnvelope{
			ID:        "submit-1",
			Sender:    "alice",
			TaskID:    "task-1",
			Action:    "submit",
			Payload:   `{"round":1,"slot":2,"submission_value":"proof","reward_amount":0.05}`,
			Nonce:     2,
			Timestamp: "2026-06-06T00:01:00Z",
		},
	})
	if err != nil {
		t.Fatalf("append unfunded submit action: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil)
	rec := httptest.NewRecorder()
	h.QSDMTasksListHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("tasks status = %d body=%s", rec.Code, rec.Body.String())
	}
	var tasksResp QSDMTasksListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &tasksResp); err != nil {
		t.Fatalf("decode tasks response: %v", err)
	}
	if len(tasksResp.Tasks) != 1 {
		t.Fatalf("tasks len = %d, want 1", len(tasksResp.Tasks))
	}
	task := tasksResp.Tasks[0]
	if task.StakeList["alice"] != 1 || task.TotalStakeAmount != 1 {
		t.Fatalf("stake projection mismatch: %+v", task)
	}
	if task.RewardPoolAmount != 0 || task.PendingRewardAmount != 0 || len(task.Submissions) != 0 {
		t.Fatalf("unfunded reward submit was projected: %+v", task)
	}
}

func TestQSDMTaskStateProjection_IgnoresLegacyResourceProof(t *testing.T) {
	const digest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	registryPath := writeTaskRegistry(t, `{"tasks":[{"task_id":"qsdm-edge-worker","task_name":"QSDM Edge Worker"}]}`)
	actionLogPath := filepath.Join(t.TempDir(), "task-actions.jsonl")
	t.Setenv(qsdmTaskRegistryPathEnv, registryPath)
	t.Setenv(qsdmTaskActionLogPathEnv, actionLogPath)
	SetTaskStateProvider(nil)

	actions := []QSDMTaskActionEnvelope{
		{ID: "legacy-stake", Sender: "alice", TaskID: "qsdm-edge-worker", Action: "stake", Amount: 1, Nonce: 1, Timestamp: "2026-06-01T00:00:00Z"},
		{ID: "legacy-fund", Sender: "sponsor", TaskID: "qsdm-edge-worker", Action: "fund", Amount: 1, Nonce: 1, Timestamp: "2026-06-01T00:00:01Z"},
		{
			ID: "legacy-submit", Sender: "alice", TaskID: "qsdm-edge-worker", Action: "submit", Nonce: 2,
			Timestamp: "2026-06-01T00:00:02Z",
			Payload:   `{"round":1,"slot":2,"submission_value":"` + digest + `","reward_amount":0.05,"proof":{"algorithm":"sha256-iterated","units":50000,"digest":"` + digest + `"}}`,
		},
	}
	for _, action := range actions {
		if _, err := appendQSDMTaskAction(actionLogPath, QSDMTaskActionRecord{
			ReceivedAt: action.Timestamp,
			Envelope:   action,
		}); err != nil {
			t.Fatalf("append %s: %v", action.ID, err)
		}
	}

	h := setupTestHandlers()
	rec := httptest.NewRecorder()
	h.QSDMTasksListHandler(rec, httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("tasks status = %d body=%s", rec.Code, rec.Body.String())
	}
	var response QSDMTasksListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode tasks response: %v", err)
	}
	if len(response.Tasks) != 1 || response.Tasks[0].StakeList["alice"] != 1 {
		t.Fatalf("legacy projection lost valid state: %+v", response.Tasks)
	}
	if response.Tasks[0].PendingRewardAmount != 0 {
		t.Fatalf("legacy proof should not be projected without a block height: %+v", response.Tasks[0])
	}
}

func TestQSDMTaskStateProjection_PrefersLiveProviderOverActionLog(t *testing.T) {
	registryPath := writeTaskRegistry(t, `{"tasks":[{"task_id":"task-1","task_name":"Projected Task"}]}`)
	actionLogPath := filepath.Join(t.TempDir(), "task-actions.jsonl")
	t.Setenv(qsdmTaskRegistryPathEnv, registryPath)
	t.Setenv(qsdmTaskActionLogPathEnv, actionLogPath)

	live := chain.NewTaskStateStore()
	if err := live.ApplyAction(chain.TaskAction{
		ID:        "live-stake",
		Sender:    "live-sender",
		TaskID:    "task-1",
		Action:    "stake",
		Amount:    2,
		Nonce:     1,
		Timestamp: "2026-05-30T00:00:00Z",
	}); err != nil {
		t.Fatalf("seed live task state: %v", err)
	}
	SetTaskStateProvider(live)
	t.Cleanup(func() { SetTaskStateProvider(nil) })

	_, err := appendQSDMTaskAction(actionLogPath, QSDMTaskActionRecord{
		ReceivedAt: "2026-05-30T00:00:01Z",
		Envelope: QSDMTaskActionEnvelope{
			ID:        "logged-stake",
			Sender:    "logged-sender",
			TaskID:    "task-1",
			Action:    "stake",
			Amount:    7,
			Nonce:     1,
			Timestamp: "2026-05-30T00:00:01Z",
		},
	})
	if err != nil {
		t.Fatalf("append logged task action: %v", err)
	}
	_, err = appendQSDMTaskAction(actionLogPath, QSDMTaskActionRecord{
		ReceivedAt: "2026-05-30T00:00:02Z",
		Envelope: QSDMTaskActionEnvelope{
			ID:        "legacy-worker-submit",
			Sender:    "legacy-worker",
			TaskID:    "qsdm-edge-worker",
			Action:    "submit",
			Payload:   `{"round":1,"submission_value":"legacy","reward_amount":50000000}`,
			Nonce:     1,
			Timestamp: "2026-05-30T00:00:02Z",
		},
	})
	if err != nil {
		t.Fatalf("append legacy worker action: %v", err)
	}

	h := setupTestHandlers()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/task-1/state", nil)
	rec := httptest.NewRecorder()
	h.QSDMTaskRouteHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("state status = %d body=%s", rec.Code, rec.Body.String())
	}
	var stateResp QSDMTaskStateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &stateResp); err != nil {
		t.Fatalf("decode state response: %v", err)
	}
	if stateResp.Source != qsdmTaskChainSource {
		t.Fatalf("source = %q, want %q", stateResp.Source, qsdmTaskChainSource)
	}
	if got := stateResp.Task.Participants["live-sender"].Stake; got != 2 {
		t.Fatalf("live stake = %v, want 2; task=%+v", got, stateResp.Task)
	}
	if _, ok := stateResp.Task.Participants["logged-sender"]; ok {
		t.Fatalf("live provider should be authoritative for task-1, got logged sender: %+v", stateResp.Task)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil)
	listRec := httptest.NewRecorder()
	h.QSDMTasksListHandler(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("tasks status = %d body=%s", listRec.Code, listRec.Body.String())
	}
	var tasksResp QSDMTasksListResponse
	if err := json.Unmarshal(listRec.Body.Bytes(), &tasksResp); err != nil {
		t.Fatalf("decode tasks response: %v", err)
	}
	if len(tasksResp.Tasks) != 1 {
		t.Fatalf("tasks len = %d, want 1", len(tasksResp.Tasks))
	}
	if got := tasksResp.Tasks[0].StakeList["live-sender"]; got != 2 {
		t.Fatalf("registry live stake projection = %v, want 2; task=%+v", got, tasksResp.Tasks[0])
	}
	if _, ok := tasksResp.Tasks[0].StakeList["logged-sender"]; ok {
		t.Fatalf("registry projection should prefer live provider, got logged sender: %+v", tasksResp.Tasks[0])
	}
}
