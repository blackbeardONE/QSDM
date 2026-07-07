package chain

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

func TestTaskStateStore_ApplyLifecycleStakeAndSubmission(t *testing.T) {
	store := NewTaskStateStore()
	actions := []TaskAction{
		{ID: "action-stake-0001", Sender: "alice", TaskID: "task-1", Action: "stake", Amount: 12.5, Nonce: 1, Timestamp: "2026-05-28T00:00:00Z"},
		{ID: "action-start-0001", Sender: "alice", TaskID: "task-1", Action: "start", Nonce: 2, Timestamp: "2026-05-28T00:01:00Z"},
		{ID: "action-submit-0001", Sender: "alice", TaskID: "task-1", Action: "submit", Payload: `{"round":7,"slot":42,"submission_value":"cid-1"}`, Nonce: 3, Timestamp: "2026-05-28T00:02:00Z"},
		{ID: "action-stop-0001", Sender: "alice", TaskID: "task-1", Action: "stop", Nonce: 4, Timestamp: "2026-05-28T00:03:00Z"},
	}
	if err := store.ApplyActions(actions); err != nil {
		t.Fatalf("ApplyActions: %v", err)
	}

	state, ok := store.GetTask("task-1")
	if !ok {
		t.Fatal("task state missing")
	}
	if state.RunningCount != 0 {
		t.Fatalf("running_count: got %d want 0", state.RunningCount)
	}
	if state.TotalStakeAmount != 12.5 {
		t.Fatalf("stake: got %f want 12.5", state.TotalStakeAmount)
	}
	participant := state.Participants["alice"]
	if participant.Running {
		t.Fatal("participant should be stopped")
	}
	if participant.SubmissionCount != 1 {
		t.Fatalf("submission_count: got %d want 1", participant.SubmissionCount)
	}
	submission := state.Submissions["7"]["alice"]
	if submission.SubmissionValue != "cid-1" || submission.Slot != 42 {
		t.Fatalf("submission projection mismatch: %+v", submission)
	}
}

func TestTaskStateStore_FundSubmitRewardAndClaim(t *testing.T) {
	store := NewTaskStateStore()
	actions := []TaskAction{
		{ID: "action-stake-0001", Sender: "alice", TaskID: "task-1", Action: "stake", Amount: 2, Nonce: 1, Timestamp: "2026-05-28T00:00:00Z"},
		{ID: "action-fund-0001", Sender: "manager", TaskID: "task-1", Action: "fund", Amount: 10, Nonce: 1, Timestamp: "2026-05-28T00:01:00Z"},
		{ID: "action-submit-0001", Sender: "alice", TaskID: "task-1", Action: "submit", Payload: `{"round":7,"slot":42,"submission_value":"cid-1","reward_amount":3}`, Nonce: 2, Timestamp: "2026-05-28T00:02:00Z"},
	}
	if err := store.ApplyActions(actions); err != nil {
		t.Fatalf("ApplyActions: %v", err)
	}

	state, ok := store.GetTask("task-1")
	if !ok {
		t.Fatal("task state missing")
	}
	if state.RewardPoolAmount != 7 || state.PendingRewardAmount != 3 {
		t.Fatalf("reward accounting after submit: pool=%v pending=%v", state.RewardPoolAmount, state.PendingRewardAmount)
	}
	participant := state.Participants["alice"]
	if participant.PendingRewardAmount != 3 || participant.TotalRewardClaimedAmount != 0 {
		t.Fatalf("participant rewards after submit: %+v", participant)
	}
	submission := state.Submissions["7"]["alice"]
	if submission.RewardAmount != 3 || submission.Claimed {
		t.Fatalf("submission reward after submit: %+v", submission)
	}
	if got := store.ClaimableReward(TaskAction{Sender: "alice", TaskID: "task-1", Payload: `{"round":7}`}); got != 3 {
		t.Fatalf("claimable reward: got %v want 3", got)
	}

	if err := store.ApplyAction(TaskAction{
		ID:        "action-claim-0001",
		Sender:    "alice",
		TaskID:    "task-1",
		Action:    "claim",
		Payload:   `{"round":7}`,
		Nonce:     3,
		Timestamp: "2026-05-28T00:03:00Z",
	}); err != nil {
		t.Fatalf("claim ApplyAction: %v", err)
	}

	state, _ = store.GetTask("task-1")
	participant = state.Participants["alice"]
	submission = state.Submissions["7"]["alice"]
	if state.PendingRewardAmount != 0 || state.TotalRewardPaidAmount != 3 {
		t.Fatalf("task rewards after claim: %+v", state)
	}
	if participant.PendingRewardAmount != 0 || participant.TotalRewardClaimedAmount != 3 || participant.ClaimCount != 1 {
		t.Fatalf("participant rewards after claim: %+v", participant)
	}
	if !submission.Claimed || submission.ClaimedAt == "" {
		t.Fatalf("submission was not marked claimed: %+v", submission)
	}
	if got := store.ClaimableReward(TaskAction{Sender: "alice", TaskID: "task-1", Payload: `{"round":7}`}); got != 0 {
		t.Fatalf("claimable reward after claim: got %v want 0", got)
	}

	if err := store.ApplyAction(TaskAction{
		ID:        "action-submit-duplicate-after-claim",
		Sender:    "alice",
		TaskID:    "task-1",
		Action:    "submit",
		Payload:   `{"round":7,"slot":43,"submission_value":"cid-duplicate","reward_amount":3}`,
		Nonce:     4,
		Timestamp: "2026-05-28T00:04:00Z",
	}); err != nil {
		t.Fatalf("duplicate submit after claim should be ignored: %v", err)
	}

	state, _ = store.GetTask("task-1")
	participant = state.Participants["alice"]
	submission = state.Submissions["7"]["alice"]
	if submission.SubmissionValue != "cid-1" || !submission.Claimed {
		t.Fatalf("claimed submission was replaced: %+v", submission)
	}
	if state.RewardPoolAmount != 7 || state.TotalRewardPaidAmount != 3 {
		t.Fatalf("duplicate submit changed reward accounting: %+v", state)
	}
	if participant.TotalRewardClaimedAmount != 3 || participant.PendingRewardAmount != 0 {
		t.Fatalf("duplicate submit changed participant rewards: %+v", participant)
	}

	if err := store.ApplyAction(TaskAction{
		ID:        "action-claim-duplicate-after-claim",
		Sender:    "alice",
		TaskID:    "task-1",
		Action:    "claim",
		Payload:   `{"round":7}`,
		Nonce:     5,
		Timestamp: "2026-05-28T00:05:00Z",
	}); err != nil {
		t.Fatalf("duplicate claim after claim should be ignored: %v", err)
	}
	state, _ = store.GetTask("task-1")
	participant = state.Participants["alice"]
	if participant.ClaimCount != 1 || state.TotalRewardPaidAmount != 3 {
		t.Fatalf("duplicate claim changed reward accounting: task=%+v participant=%+v", state, participant)
	}
}

func TestTaskStateStore_SubmitRewardRequiresStakeAndFundedPool(t *testing.T) {
	store := NewTaskStateStore()
	if err := store.ApplyAction(TaskAction{
		ID:        "action-fund-0001",
		Sender:    "manager",
		TaskID:    "task-1",
		Action:    "fund",
		Amount:    1,
		Timestamp: "2026-05-28T00:00:00Z",
	}); err != nil {
		t.Fatalf("fund ApplyAction: %v", err)
	}

	err := store.ApplyAction(TaskAction{
		ID:        "action-submit-0001",
		Sender:    "alice",
		TaskID:    "task-1",
		Action:    "submit",
		Payload:   `{"round":1,"submission_value":"proof","reward_amount":1}`,
		Timestamp: "2026-05-28T00:01:00Z",
	})
	if !errors.Is(err, ErrTaskActionRequiresStake) {
		t.Fatalf("want ErrTaskActionRequiresStake, got %v", err)
	}

	if err := store.ApplyAction(TaskAction{
		ID:        "action-stake-0001",
		Sender:    "alice",
		TaskID:    "task-1",
		Action:    "stake",
		Amount:    1,
		Timestamp: "2026-05-28T00:02:00Z",
	}); err != nil {
		t.Fatalf("stake ApplyAction: %v", err)
	}
	err = store.ApplyAction(TaskAction{
		ID:        "action-submit-0002",
		Sender:    "alice",
		TaskID:    "task-1",
		Action:    "submit",
		Payload:   `{"round":1,"submission_value":"proof","reward_amount":2}`,
		Timestamp: "2026-05-28T00:03:00Z",
	})
	if !errors.Is(err, ErrInsufficientTaskRewardPool) {
		t.Fatalf("want ErrInsufficientTaskRewardPool, got %v", err)
	}
	state, _ := store.GetTask("task-1")
	if state.RewardPoolAmount != 1 || state.PendingRewardAmount != 0 {
		t.Fatalf("reward state mutated after rejected submit: %+v", state)
	}
}

func TestTaskStateStore_SubmitRewardToleratesSubDustFloatNoise(t *testing.T) {
	store := NewTaskStateStore()
	actions := []TaskAction{
		{ID: "action-stake-0001", Sender: "alice", TaskID: "task-1", Action: "stake", Amount: 1, Nonce: 1, Timestamp: "2026-05-28T00:00:00Z"},
		{ID: "action-fund-0001", Sender: "manager", TaskID: "task-1", Action: "fund", Amount: 0.05 - taskAmountEpsilon/2, Nonce: 1, Timestamp: "2026-05-28T00:01:00Z"},
		{ID: "action-submit-0001", Sender: "alice", TaskID: "task-1", Action: "submit", Payload: `{"round":1,"submission_value":"proof","reward_amount":0.05}`, Nonce: 2, Timestamp: "2026-05-28T00:02:00Z"},
	}
	if err := store.ApplyActions(actions); err != nil {
		t.Fatalf("ApplyActions: %v", err)
	}
	state, ok := store.GetTask("task-1")
	if !ok {
		t.Fatal("task state missing")
	}
	if state.RewardPoolAmount != 0 {
		t.Fatalf("tiny negative reward pool was not clamped: %+v", state)
	}
	if state.PendingRewardAmount != 0.05 {
		t.Fatalf("pending reward: got %.12f want 0.05", state.PendingRewardAmount)
	}
}

func TestTaskStateStore_ResourceWorkerProofAndRewardPolicy(t *testing.T) {
	const digest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	store := NewTaskStateStore()
	if err := store.ApplyActions([]TaskAction{
		{ID: "edge-stake", Sender: "alice", TaskID: "qsdm-edge-worker", Action: "stake", Amount: 1, Timestamp: "2026-07-01T00:00:00Z"},
		{ID: "edge-fund", Sender: "sponsor", TaskID: "qsdm-edge-worker", Action: "fund", Amount: 1, Timestamp: "2026-07-01T00:00:01Z"},
	}); err != nil {
		t.Fatal(err)
	}
	validPayload := `{"round":1,"slot":10,"source":"qsdm-edge-worker-cpu","resource":"cpu","submission_value":"` + digest + `","reward_amount":0.05,"proof":{"algorithm":"sha256-iterated","units":50000,"digest":"` + digest + `"}}`
	if err := store.ApplyAction(TaskAction{
		ID: "edge-submit-valid", Sender: "alice", TaskID: "qsdm-edge-worker",
		Action: "submit", Payload: validPayload, Timestamp: "2026-07-01T00:00:02Z",
	}); err != nil {
		t.Fatalf("valid CPU resource proof was rejected: %v", err)
	}

	replayed := strings.Replace(validPayload, `"round":1`, `"round":2`, 1)
	if err := store.ApplyAction(TaskAction{
		ID: "edge-submit-replay", Sender: "alice", TaskID: "qsdm-edge-worker",
		Action: "submit", Payload: replayed, Timestamp: "2026-07-01T00:00:03Z",
	}); err == nil || !strings.Contains(err.Error(), "already submitted") {
		t.Fatalf("replayed CPU resource proof was not rejected: %v", err)
	}

	overCap := strings.Replace(validPayload, `"round":1`, `"round":3`, 1)
	overCap = strings.Replace(overCap, `"reward_amount":0.05`, `"reward_amount":0.06`, 1)
	if err := store.ApplyAction(TaskAction{
		ID: "edge-submit-over-cap", Sender: "alice", TaskID: "qsdm-edge-worker",
		Action: "submit", Payload: overCap, Timestamp: "2026-07-01T00:00:04Z",
	}); err == nil || !strings.Contains(err.Error(), "consensus cap") {
		t.Fatalf("over-cap CPU reward was not rejected: %v", err)
	}

	wrongResource := strings.Replace(validPayload, `"round":1`, `"round":4`, 1)
	wrongResource = strings.Replace(wrongResource, `"resource":"cpu"`, `"resource":"gpu"`, 1)
	if err := store.ApplyAction(TaskAction{
		ID: "edge-submit-wrong-resource", Sender: "alice", TaskID: "qsdm-edge-worker",
		Action: "submit", Payload: wrongResource, Timestamp: "2026-07-01T00:00:05Z",
	}); err == nil || !strings.Contains(err.Error(), "expected \"cpu\"") {
		t.Fatalf("wrong resource proof was not rejected: %v", err)
	}

	missingResource := strings.Replace(validPayload, `"round":1`, `"round":5`, 1)
	missingResource = strings.Replace(missingResource, `"resource":"cpu",`, "", 1)
	if err := store.ApplyAction(TaskAction{
		ID: "edge-submit-missing-resource", Sender: "alice", TaskID: "qsdm-edge-worker",
		Action: "submit", Payload: missingResource, Timestamp: "2026-07-01T00:00:06Z",
	}); !errors.Is(err, ErrLegacyResourceWorkerProof) {
		t.Fatalf("missing resource error = %v, want ErrLegacyResourceWorkerProof", err)
	}
}

func TestTaskStateStore_RejectsUnverifiablePooledResourceProof(t *testing.T) {
	const root = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	for _, source := range []string{"qsdm-edge-pool", "qsdm-edge-relay"} {
		t.Run(source, func(t *testing.T) {
			store := NewTaskStateStore()
			if err := store.ApplyActions([]TaskAction{
				{ID: source + "-stake", Sender: "alice", TaskID: "qsdm-edge-worker", Action: "stake", Amount: 1, Timestamp: "2026-07-08T00:00:00Z"},
				{ID: source + "-fund", Sender: "sponsor", TaskID: "qsdm-edge-worker", Action: "fund", Amount: 1, Timestamp: "2026-07-08T00:00:01Z"},
			}); err != nil {
				t.Fatal(err)
			}
			payload := `{"round":1,"slot":10,"source":"` + source + `","resource":"cpu","submission_value":"` + root + `","reward_amount":0.05,"proof":{"job_count":2,"total_units":1000,"receipt_root":"` + root + `"}}`
			err := store.ApplyAction(TaskAction{
				ID: source + "-submit", Sender: "alice", TaskID: "qsdm-edge-worker",
				Action: "submit", Payload: payload, Timestamp: "2026-07-08T00:00:02Z",
			})
			if !errors.Is(err, ErrPooledResourceProofNotEnforceable) {
				t.Fatalf("pooled proof error = %v, want ErrPooledResourceProofNotEnforceable", err)
			}
			state, _ := store.GetTask("qsdm-edge-worker")
			if state.RewardPoolAmount != 1 || state.PendingRewardAmount != 0 {
				t.Fatalf("pooled proof mutated reward accounting: %+v", state)
			}
		})
	}
}

func TestTaskStateStore_CatalogRewardAndStakeLimitsAreConsensusEnforced(t *testing.T) {
	store := NewTaskStateStore()
	manifest := TaskManifest{
		SchemaVersion: TaskCatalogSchemaVersion,
		TaskID:        "catalog-edge-task",
		Version:       1,
		Name:          "Catalog Edge Task",
		Manager:       "manager",
		Active:        true,
		Runtime: TaskRuntimeManifest{
			Kind:       "capability",
			Capability: "edge.cpu.v1",
		},
		MinimumStakeAmount: 2,
		RewardPerRound:     0.25,
		RoundTime:          60,
	}
	manifestJSON, _ := json.Marshal(manifest)
	if err := store.ApplyAction(TaskAction{
		ID: "catalog-register", Sender: "manager", TaskID: manifest.TaskID,
		Action: TaskActionCatalogRegister, Payload: string(manifestJSON), Timestamp: "2026-07-01T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyActions([]TaskAction{
		{ID: "catalog-stake", Sender: "alice", TaskID: manifest.TaskID, Action: "stake", Amount: 1, Timestamp: "2026-07-01T00:00:01Z"},
		{ID: "catalog-fund", Sender: "manager", TaskID: manifest.TaskID, Action: "fund", Amount: 5, Timestamp: "2026-07-01T00:00:02Z"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyAction(TaskAction{
		ID: "catalog-submit-low-stake", Sender: "alice", TaskID: manifest.TaskID,
		Action: "submit", Payload: `{"round":1,"submission_value":"proof","reward_amount":0.2}`, Timestamp: "2026-07-01T00:00:03Z",
	}); !errors.Is(err, ErrInsufficientTaskStake) {
		t.Fatalf("catalog minimum stake was not enforced: %v", err)
	}
	if err := store.ApplyAction(TaskAction{
		ID: "catalog-stake-more", Sender: "alice", TaskID: manifest.TaskID,
		Action: "stake", Amount: 1, Timestamp: "2026-07-01T00:00:04Z",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyAction(TaskAction{
		ID: "catalog-submit-over-reward", Sender: "alice", TaskID: manifest.TaskID,
		Action: "submit", Payload: `{"round":1,"submission_value":"proof","reward_amount":0.3}`, Timestamp: "2026-07-01T00:00:05Z",
	}); err == nil || !strings.Contains(err.Error(), "reward_per_round") {
		t.Fatalf("catalog reward cap was not enforced: %v", err)
	}
}

func TestTaskStateStore_DuplicateAndNonceReplay(t *testing.T) {
	store := NewTaskStateStore()
	action := TaskAction{
		ID:        "action-stake-0001",
		Sender:    "alice",
		TaskID:    "task-1",
		Action:    "stake",
		Amount:    1,
		Nonce:     8,
		Timestamp: "2026-05-28T00:00:00Z",
	}
	if err := store.ApplyAction(action); err != nil {
		t.Fatalf("ApplyAction: %v", err)
	}
	if err := store.ApplyAction(action); !errors.Is(err, ErrDuplicateTaskAction) {
		t.Fatalf("duplicate err: got %v", err)
	}
	replay := action
	replay.ID = "action-stake-0002"
	if err := store.ApplyAction(replay); !errors.Is(err, ErrTaskActionNonceReplay) {
		t.Fatalf("nonce replay err: got %v", err)
	}
}

func TestTaskStateStore_ChainReplayClone(t *testing.T) {
	store := NewTaskStateStore()
	if err := store.ApplyAction(TaskAction{
		ID:        "action-stake-0001",
		Sender:    "alice",
		TaskID:    "task-1",
		Action:    "stake",
		Amount:    1,
		Timestamp: "2026-05-28T00:00:00Z",
	}); err != nil {
		t.Fatalf("ApplyAction: %v", err)
	}
	before := store.StateRoot()

	clone := store.ChainReplayClone().(*TaskStateStore)
	if err := clone.ApplyAction(TaskAction{
		ID:        "action-start-0001",
		Sender:    "alice",
		TaskID:    "task-1",
		Action:    "start",
		Timestamp: "2026-05-28T00:01:00Z",
	}); err != nil {
		t.Fatalf("clone ApplyAction: %v", err)
	}
	if store.StateRoot() != before {
		t.Fatal("clone mutation changed live store")
	}
	if err := store.RestoreFromChainReplay(clone); err != nil {
		t.Fatalf("RestoreFromChainReplay: %v", err)
	}
	state, _ := store.GetTask("task-1")
	if !state.Participants["alice"].Running {
		t.Fatal("restore did not apply clone start state")
	}
}

func TestTaskStateStore_ApplyTx(t *testing.T) {
	store := NewTaskStateStore()
	stakePayload, err := json.Marshal(TaskAction{
		TaskID:    "task-1",
		Action:    "stake",
		Amount:    1,
		Timestamp: "2026-05-28T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("marshal stake payload: %v", err)
	}
	if err := store.ApplyTx(&mempool.Tx{
		ID:         "action-stake-0001",
		Sender:     "alice",
		Nonce:      1,
		ContractID: TaskContractID,
		Payload:    stakePayload,
	}); err != nil {
		t.Fatalf("stake ApplyTx: %v", err)
	}
	startPayload, err := json.Marshal(TaskAction{
		TaskID:    "task-1",
		Action:    "start",
		Timestamp: "2026-05-28T00:01:00Z",
	})
	if err != nil {
		t.Fatalf("marshal start payload: %v", err)
	}
	err = store.ApplyTx(&mempool.Tx{
		ID:         "action-start-0001",
		Sender:     "alice",
		Nonce:      2,
		ContractID: TaskContractID,
		Payload:    startPayload,
	})
	if err != nil {
		t.Fatalf("start ApplyTx: %v", err)
	}
	state, ok := store.GetTask("task-1")
	if !ok || !state.Participants["alice"].Running {
		t.Fatalf("tx did not project running state: %+v", state)
	}
}

func TestTaskStateStore_ApplyEconomicTx_StartRequiresConfirmedStake(t *testing.T) {
	accounts := NewAccountStore()
	accounts.Credit("alice", 10)
	store := NewTaskStateStore()

	startPayload, err := json.Marshal(TaskAction{
		ID:        "action-start-0001",
		Sender:    "alice",
		TaskID:    "task-1",
		Action:    "start",
		Timestamp: "2026-05-28T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("marshal start: %v", err)
	}
	err = store.ApplyEconomicTx(&mempool.Tx{
		ID:         "action-start-0001",
		Sender:     "alice",
		Nonce:      0,
		ContractID: TaskContractID,
		Payload:    startPayload,
	}, accounts)
	if !errors.Is(err, ErrTaskActionRequiresStake) {
		t.Fatalf("want ErrTaskActionRequiresStake, got %v", err)
	}
	alice, _ := accounts.Get("alice")
	if alice.Balance != 10 || alice.Nonce != 0 {
		t.Fatalf("account mutated after rejected start: %+v", alice)
	}

	stakePayload, err := json.Marshal(TaskAction{
		ID:        "action-stake-0001",
		Sender:    "alice",
		TaskID:    "task-1",
		Action:    "stake",
		Amount:    2,
		Timestamp: "2026-05-28T00:01:00Z",
	})
	if err != nil {
		t.Fatalf("marshal stake: %v", err)
	}
	if err := store.ApplyEconomicTx(&mempool.Tx{
		ID:         "action-stake-0001",
		Sender:     "alice",
		Nonce:      0,
		ContractID: TaskContractID,
		Payload:    stakePayload,
	}, accounts); err != nil {
		t.Fatalf("stake ApplyEconomicTx: %v", err)
	}

	startPayload, err = json.Marshal(TaskAction{
		ID:        "action-start-0002",
		Sender:    "alice",
		TaskID:    "task-1",
		Action:    "start",
		Nonce:     1,
		Timestamp: "2026-05-28T00:02:00Z",
	})
	if err != nil {
		t.Fatalf("marshal second start: %v", err)
	}
	if err := store.ApplyEconomicTx(&mempool.Tx{
		ID:         "action-start-0002",
		Sender:     "alice",
		Nonce:      1,
		ContractID: TaskContractID,
		Payload:    startPayload,
	}, accounts); err != nil {
		t.Fatalf("start after stake ApplyEconomicTx: %v", err)
	}
	state, _ := store.GetTask("task-1")
	if !state.Participants["alice"].Running {
		t.Fatalf("participant should be running after confirmed stake: %+v", state.Participants["alice"])
	}
	alice, _ = accounts.Get("alice")
	if alice.Balance != 8 || alice.Nonce != 2 {
		t.Fatalf("account after stake/start: %+v, want balance 8 nonce 2", alice)
	}
}

func TestTaskStateStore_ApplyEconomicTx_DebitsStakeAndCreditsWithdraw(t *testing.T) {
	accounts := NewAccountStore()
	accounts.Credit("alice", 20)
	store := NewTaskStateStore()

	stakePayload, err := json.Marshal(TaskAction{
		ID:        "action-stake-0001",
		Sender:    "alice",
		TaskID:    "task-1",
		Action:    "stake",
		Amount:    5,
		Timestamp: "2026-05-28T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("marshal stake: %v", err)
	}
	if err := store.ApplyEconomicTx(&mempool.Tx{
		ID:         "action-stake-0001",
		Sender:     "alice",
		Amount:     5,
		Fee:        0.25,
		Nonce:      0,
		ContractID: TaskContractID,
		Payload:    stakePayload,
	}, accounts); err != nil {
		t.Fatalf("stake ApplyEconomicTx: %v", err)
	}

	alice, _ := accounts.Get("alice")
	if alice.Balance != 14.75 || alice.Nonce != 1 {
		t.Fatalf("post-stake account: %+v, want balance 14.75 nonce 1", alice)
	}
	state, _ := store.GetTask("task-1")
	if state.TotalStakeAmount != 5 || state.Participants["alice"].Stake != 5 {
		t.Fatalf("post-stake task state: %+v", state)
	}

	withdrawPayload, err := json.Marshal(TaskAction{
		ID:        "action-withdraw-0001",
		Sender:    "alice",
		TaskID:    "task-1",
		Action:    "withdraw",
		Amount:    2,
		Nonce:     1,
		Timestamp: "2026-05-28T00:01:00Z",
	})
	if err != nil {
		t.Fatalf("marshal withdraw: %v", err)
	}
	if err := store.ApplyEconomicTx(&mempool.Tx{
		ID:         "action-withdraw-0001",
		Sender:     "alice",
		Fee:        0.1,
		Nonce:      1,
		ContractID: TaskContractID,
		Payload:    withdrawPayload,
	}, accounts); err != nil {
		t.Fatalf("withdraw ApplyEconomicTx: %v", err)
	}

	alice, _ = accounts.Get("alice")
	if alice.Balance != 16.65 || alice.Nonce != 2 {
		t.Fatalf("post-withdraw account: %+v, want balance 16.65 nonce 2", alice)
	}
	state, _ = store.GetTask("task-1")
	if state.TotalStakeAmount != 3 || state.Participants["alice"].Stake != 3 {
		t.Fatalf("post-withdraw task state: %+v", state)
	}
}

func TestTaskStateStore_ApplyEconomicTx_FundSubmitAndClaimSettlesCell(t *testing.T) {
	accounts := NewAccountStore()
	accounts.Credit("alice", 20)
	store := NewTaskStateStore()

	fundPayload, err := json.Marshal(TaskAction{
		ID:        "action-fund-0001",
		Sender:    "alice",
		TaskID:    "task-1",
		Action:    "fund",
		Amount:    6,
		Timestamp: "2026-05-28T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("marshal fund: %v", err)
	}
	if err := store.ApplyEconomicTx(&mempool.Tx{
		ID:         "action-fund-0001",
		Sender:     "alice",
		Amount:     6,
		Fee:        0.25,
		Nonce:      0,
		ContractID: TaskContractID,
		Payload:    fundPayload,
	}, accounts); err != nil {
		t.Fatalf("fund ApplyEconomicTx: %v", err)
	}

	stakePayload, err := json.Marshal(TaskAction{
		ID:        "action-stake-0001",
		Sender:    "alice",
		TaskID:    "task-1",
		Action:    "stake",
		Amount:    2,
		Nonce:     1,
		Timestamp: "2026-05-28T00:01:00Z",
	})
	if err != nil {
		t.Fatalf("marshal stake: %v", err)
	}
	if err := store.ApplyEconomicTx(&mempool.Tx{
		ID:         "action-stake-0001",
		Sender:     "alice",
		Amount:     2,
		Fee:        0.1,
		Nonce:      1,
		ContractID: TaskContractID,
		Payload:    stakePayload,
	}, accounts); err != nil {
		t.Fatalf("stake ApplyEconomicTx: %v", err)
	}

	submitPayload, err := json.Marshal(TaskAction{
		ID:        "action-submit-0001",
		Sender:    "alice",
		TaskID:    "task-1",
		Action:    "submit",
		Payload:   `{"round":5,"slot":9,"submission_value":"proof-cid","reward_amount":3}`,
		Nonce:     2,
		Timestamp: "2026-05-28T00:02:00Z",
	})
	if err != nil {
		t.Fatalf("marshal submit: %v", err)
	}
	if err := store.ApplyEconomicTx(&mempool.Tx{
		ID:         "action-submit-0001",
		Sender:     "alice",
		Fee:        0.05,
		Nonce:      2,
		ContractID: TaskContractID,
		Payload:    submitPayload,
	}, accounts); err != nil {
		t.Fatalf("submit ApplyEconomicTx: %v", err)
	}

	claimPayload, err := json.Marshal(TaskAction{
		ID:        "action-claim-0001",
		Sender:    "alice",
		TaskID:    "task-1",
		Action:    "claim",
		Payload:   `{"round":5}`,
		Nonce:     3,
		Timestamp: "2026-05-28T00:03:00Z",
	})
	if err != nil {
		t.Fatalf("marshal claim: %v", err)
	}
	if err := store.ApplyEconomicTx(&mempool.Tx{
		ID:         "action-claim-0001",
		Sender:     "alice",
		Fee:        0.1,
		Nonce:      3,
		ContractID: TaskContractID,
		Payload:    claimPayload,
	}, accounts); err != nil {
		t.Fatalf("claim ApplyEconomicTx: %v", err)
	}

	alice, _ := accounts.Get("alice")
	if alice.Balance != 14.5 || alice.Nonce != 4 {
		t.Fatalf("alice account after reward claim: %+v, want balance 14.5 nonce 4", alice)
	}
	state, _ := store.GetTask("task-1")
	if state.RewardPoolAmount != 3 || state.PendingRewardAmount != 0 || state.TotalRewardPaidAmount != 3 {
		t.Fatalf("task reward accounting after claim: %+v", state)
	}
	if !state.Submissions["5"]["alice"].Claimed {
		t.Fatalf("submission should be claimed: %+v", state.Submissions["5"]["alice"])
	}

	doubleClaimPayload, err := json.Marshal(TaskAction{
		ID:        "action-claim-0002",
		Sender:    "alice",
		TaskID:    "task-1",
		Action:    "claim",
		Payload:   `{"round":5}`,
		Nonce:     4,
		Timestamp: "2026-05-28T00:04:00Z",
	})
	if err != nil {
		t.Fatalf("marshal double claim: %v", err)
	}
	err = store.ApplyEconomicTx(&mempool.Tx{
		ID:         "action-claim-0002",
		Sender:     "alice",
		Nonce:      4,
		ContractID: TaskContractID,
		Payload:    doubleClaimPayload,
	}, accounts)
	if !errors.Is(err, ErrNoTaskReward) {
		t.Fatalf("want ErrNoTaskReward, got %v", err)
	}
	alice, _ = accounts.Get("alice")
	if alice.Balance != 14.5 || alice.Nonce != 4 {
		t.Fatalf("account mutated after double claim rejection: %+v", alice)
	}
}

func TestTaskStateStore_ApplyEconomicTx_RejectsInsufficientBalance(t *testing.T) {
	accounts := NewAccountStore()
	accounts.Credit("alice", 1)
	store := NewTaskStateStore()
	payload, err := json.Marshal(TaskAction{
		ID:        "action-stake-0001",
		Sender:    "alice",
		TaskID:    "task-1",
		Action:    "stake",
		Amount:    5,
		Timestamp: "2026-05-28T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("marshal stake: %v", err)
	}
	err = store.ApplyEconomicTx(&mempool.Tx{
		ID:         "action-stake-0001",
		Sender:     "alice",
		Amount:     5,
		Nonce:      0,
		ContractID: TaskContractID,
		Payload:    payload,
	}, accounts)
	if err == nil {
		t.Fatal("expected insufficient balance error")
	}
	alice, _ := accounts.Get("alice")
	if alice.Balance != 1 || alice.Nonce != 0 {
		t.Fatalf("account mutated after rejection: %+v", alice)
	}
	if _, ok := store.GetTask("task-1"); ok {
		t.Fatal("task state created after rejected stake")
	}
}

func TestTaskStateStore_ApplyEconomicTx_RejectsOverWithdraw(t *testing.T) {
	accounts := NewAccountStore()
	accounts.Credit("alice", 10)
	store := NewTaskStateStore()
	if err := store.ApplyAction(TaskAction{
		ID:        "action-stake-seed",
		Sender:    "alice",
		TaskID:    "task-1",
		Action:    "stake",
		Amount:    2,
		Timestamp: "2026-05-28T00:00:00Z",
	}); err != nil {
		t.Fatalf("seed stake: %v", err)
	}
	payload, err := json.Marshal(TaskAction{
		ID:        "action-withdraw-0001",
		Sender:    "alice",
		TaskID:    "task-1",
		Action:    "withdraw",
		Amount:    5,
		Timestamp: "2026-05-28T00:01:00Z",
	})
	if err != nil {
		t.Fatalf("marshal withdraw: %v", err)
	}
	err = store.ApplyEconomicTx(&mempool.Tx{
		ID:         "action-withdraw-0001",
		Sender:     "alice",
		Nonce:      0,
		ContractID: TaskContractID,
		Payload:    payload,
	}, accounts)
	if !errors.Is(err, ErrInsufficientTaskStake) {
		t.Fatalf("want ErrInsufficientTaskStake, got %v", err)
	}
	alice, _ := accounts.Get("alice")
	if alice.Balance != 10 || alice.Nonce != 0 {
		t.Fatalf("account mutated after over-withdraw: %+v", alice)
	}
	state, _ := store.GetTask("task-1")
	if state.TotalStakeAmount != 2 || state.Participants["alice"].Stake != 2 {
		t.Fatalf("task state mutated after over-withdraw: %+v", state)
	}
}
