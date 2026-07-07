package chain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

const TaskContractID = "qsdm/tasks/v1"

const taskAmountEpsilon = 1e-9

var (
	ErrDuplicateTaskAction        = errors.New("chain: duplicate task action")
	ErrTaskActionNonceReplay      = errors.New("chain: task action nonce replay")
	ErrNotTaskActionTx            = errors.New("chain: tx is not a task action")
	ErrInsufficientTaskStake      = errors.New("chain: insufficient task stake")
	ErrInsufficientTaskRewardPool = errors.New("chain: insufficient task reward pool")
	ErrNoTaskReward               = errors.New("chain: no claimable task reward")
	ErrTaskActionRequiresStake    = errors.New("chain: task action requires task stake")
	// ErrLegacyResourceWorkerProof identifies the pre-activation worker proof
	// shape that omitted the resource discriminator. Consensus accepts it only
	// while replaying historical blocks below ResourceWorkerProofActivationHeight.
	ErrLegacyResourceWorkerProof = errors.New("chain: legacy resource worker proof")
)

func taskAmountLessThan(have, need float64) bool {
	return have+taskAmountEpsilon < need
}

func clampTaskAmount(value float64) float64 {
	if value < taskAmountEpsilon {
		return 0
	}
	return value
}

type TaskAction struct {
	ID        string  `json:"id"`
	Sender    string  `json:"sender"`
	TaskID    string  `json:"task_id"`
	Action    string  `json:"action"`
	Amount    float64 `json:"amount,omitempty"`
	Payload   string  `json:"payload,omitempty"`
	Nonce     uint64  `json:"nonce,omitempty"`
	Timestamp string  `json:"timestamp"`
}

type TaskParticipantState struct {
	Sender                   string  `json:"sender"`
	Running                  bool    `json:"running"`
	Stake                    float64 `json:"stake"`
	LastAction               string  `json:"last_action,omitempty"`
	LastActionID             string  `json:"last_action_id,omitempty"`
	LastActionAt             string  `json:"last_action_at,omitempty"`
	LastStartedAt            string  `json:"last_started_at,omitempty"`
	LastStoppedAt            string  `json:"last_stopped_at,omitempty"`
	SubmissionCount          uint64  `json:"submission_count"`
	ClaimCount               uint64  `json:"claim_count"`
	PendingRewardAmount      float64 `json:"pending_reward_amount"`
	TotalRewardClaimedAmount float64 `json:"total_reward_claimed_amount"`
	LastClaimedAt            string  `json:"last_claimed_at,omitempty"`
}

type TaskSubmissionState struct {
	ActionID        string  `json:"action_id"`
	Sender          string  `json:"sender"`
	Round           uint64  `json:"round"`
	Slot            uint64  `json:"slot"`
	SubmissionValue string  `json:"submission_value"`
	Payload         string  `json:"payload,omitempty"`
	RewardAmount    float64 `json:"reward_amount,omitempty"`
	Claimed         bool    `json:"claimed"`
	ClaimedAt       string  `json:"claimed_at,omitempty"`
	Timestamp       string  `json:"timestamp"`
}

type TaskState struct {
	TaskID                string                                    `json:"task_id"`
	Manifest              *TaskManifest                             `json:"manifest,omitempty"`
	CatalogPaused         bool                                      `json:"catalog_paused,omitempty"`
	CatalogPublishedAt    string                                    `json:"catalog_published_at,omitempty"`
	CatalogUpdatedAt      string                                    `json:"catalog_updated_at,omitempty"`
	TotalStakeAmount      float64                                   `json:"total_stake_amount"`
	RewardPoolAmount      float64                                   `json:"reward_pool_amount"`
	PendingRewardAmount   float64                                   `json:"pending_reward_amount"`
	TotalRewardPaidAmount float64                                   `json:"total_reward_paid_amount"`
	RunningCount          int                                       `json:"running_count"`
	LastAction            string                                    `json:"last_action,omitempty"`
	LastActionID          string                                    `json:"last_action_id,omitempty"`
	LastActionAt          string                                    `json:"last_action_at,omitempty"`
	IsMigrated            bool                                      `json:"is_migrated"`
	MigratedTo            string                                    `json:"migrated_to,omitempty"`
	Participants          map[string]TaskParticipantState           `json:"participants"`
	Submissions           map[string]map[string]TaskSubmissionState `json:"submissions"`
}

type TaskStateStore struct {
	mu          sync.RWMutex
	tasks       map[string]*TaskState
	actionIDs   map[string]struct{}
	senderNonce map[string]uint64
}

type taskActionPayload struct {
	Round           uint64  `json:"round"`
	Slot            uint64  `json:"slot"`
	SubmissionValue string  `json:"submission_value"`
	MigratedTo      string  `json:"migrated_to"`
	RewardAmount    float64 `json:"reward_amount"`
}

type resourceWorkerProofPayload struct {
	Source          string          `json:"source"`
	Resource        string          `json:"resource"`
	SubmissionValue string          `json:"submission_value"`
	Proof           json.RawMessage `json:"proof"`
}

type resourceWorkerProofDetails struct {
	Algorithm   string `json:"algorithm"`
	Digest      string `json:"digest"`
	Units       uint64 `json:"units"`
	ReceiptRoot string `json:"receipt_root"`
	JobCount    int    `json:"job_count"`
	TotalUnits  uint64 `json:"total_units"`
}

var systemResourceTaskPolicies = map[string]struct {
	Resource       string
	MaxRoundReward float64
}{
	"qsdm-edge-worker":     {Resource: "cpu", MaxRoundReward: 0.05},
	"qsdm-edge-worker-gpu": {Resource: "gpu", MaxRoundReward: 0.10},
	"qsdm-edge-worker-ram": {Resource: "ram", MaxRoundReward: 0.05},
}

// ResourceWorkerProofActivationHeight is the first canonical block that uses
// the resource-bound proof envelope. Historical CPU worker submissions before
// this height used worker_kind and an older proof detail shape. They remain
// replayable so an upgraded validator can reproduce the existing state root,
// but the live ApplyAction/ApplyTx paths never accept that legacy shape.
const ResourceWorkerProofActivationHeight uint64 = 155_103

func NewTaskStateStore() *TaskStateStore {
	return &TaskStateStore{
		tasks:       map[string]*TaskState{},
		actionIDs:   map[string]struct{}{},
		senderNonce: map[string]uint64{},
	}
}

func (s *TaskStateStore) ApplyAction(action TaskAction) error {
	return s.applyAction(action, false)
}

func (s *TaskStateStore) applyAction(action TaskAction, allowLegacyResourceProof bool) error {
	if s == nil {
		return errors.New("chain: nil TaskStateStore")
	}
	if action.ID == "" {
		return errors.New("chain: task action id required")
	}
	if action.Sender == "" {
		return errors.New("chain: task action sender required")
	}
	if action.TaskID == "" {
		return errors.New("chain: task action task_id required")
	}
	if action.Action == "" {
		return errors.New("chain: task action action required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.actionIDs[action.ID]; exists {
		return ErrDuplicateTaskAction
	}
	if action.Nonce > 0 && s.senderNonce[action.Sender] >= action.Nonce {
		return ErrTaskActionNonceReplay
	}
	if isTaskCatalogAction(action.Action) {
		task, err := s.applyTaskCatalogActionLocked(action)
		if err != nil {
			return err
		}
		task.LastAction = action.Action
		task.LastActionID = action.ID
		task.LastActionAt = action.Timestamp
		s.actionIDs[action.ID] = struct{}{}
		if action.Nonce > 0 {
			s.senderNonce[action.Sender] = action.Nonce
		}
		return nil
	}

	task := s.getOrCreateTaskLocked(action.TaskID)
	participant := task.Participants[action.Sender]
	if participant.Sender == "" {
		participant.Sender = action.Sender
	}

	switch action.Action {
	case "start":
		if participant.Stake <= 0 {
			return ErrTaskActionRequiresStake
		}
		if !participant.Running {
			task.RunningCount++
		}
		participant.Running = true
		participant.LastStartedAt = action.Timestamp
	case "stop":
		if participant.Running && task.RunningCount > 0 {
			task.RunningCount--
		}
		participant.Running = false
		participant.LastStoppedAt = action.Timestamp
	case "stake":
		if action.Amount <= 0 {
			return errors.New("chain: stake action amount must be positive")
		}
		participant.Stake += action.Amount
		task.TotalStakeAmount += action.Amount
	case "fund":
		if action.Amount <= 0 {
			return errors.New("chain: fund action amount must be positive")
		}
		task.RewardPoolAmount += action.Amount
	case "unstake", "withdraw":
		if action.Amount <= 0 {
			return errors.New("chain: unstake/withdraw action amount must be positive")
		}
		delta := action.Amount
		if delta > participant.Stake {
			delta = participant.Stake
		}
		participant.Stake -= delta
		task.TotalStakeAmount -= delta
		if task.TotalStakeAmount < 0 {
			task.TotalStakeAmount = 0
		}
	case "submit":
		if err := s.applySubmissionLocked(task, &participant, action, allowLegacyResourceProof); err != nil {
			return err
		}
		participant.SubmissionCount++
	case "claim":
		if _, err := s.applyClaimLocked(task, &participant, action); errors.Is(err, ErrNoTaskReward) {
			break
		} else if err != nil {
			return err
		}
		participant.ClaimCount++
	case "migrate":
		payload := parseTaskActionPayload(action.Payload)
		if payload.MigratedTo != "" {
			task.IsMigrated = true
			task.MigratedTo = payload.MigratedTo
		}
	default:
		return fmt.Errorf("chain: unsupported task action %q", action.Action)
	}

	participant.LastAction = action.Action
	participant.LastActionID = action.ID
	participant.LastActionAt = action.Timestamp
	task.Participants[action.Sender] = participant
	task.LastAction = action.Action
	task.LastActionID = action.ID
	task.LastActionAt = action.Timestamp
	s.actionIDs[action.ID] = struct{}{}
	if action.Nonce > 0 {
		s.senderNonce[action.Sender] = action.Nonce
	}
	return nil
}

func (s *TaskStateStore) ApplyActions(actions []TaskAction) error {
	for _, action := range actions {
		if err := s.ApplyAction(action); err != nil {
			return err
		}
	}
	return nil
}

func (s *TaskStateStore) ApplyTx(tx *mempool.Tx) error {
	action, err := DecodeTaskActionTx(tx)
	if err != nil {
		return err
	}
	return s.ApplyAction(action)
}

// ApplyHistoricalTx replays a transaction at its committed block height.
// Only resource-worker proofs committed before the activation boundary receive
// legacy compatibility; every ordinary/live application remains strict.
func (s *TaskStateStore) ApplyHistoricalTx(tx *mempool.Tx, height uint64) error {
	action, err := DecodeTaskActionTx(tx)
	if err != nil {
		return err
	}
	return s.applyAction(action, height < ResourceWorkerProofActivationHeight)
}

func (s *TaskStateStore) ApplyEconomicTx(tx *mempool.Tx, accounts *AccountStore) error {
	if accounts == nil {
		return errors.New("chain: nil AccountStore for task action")
	}
	action, err := DecodeTaskActionTx(tx)
	if err != nil {
		return err
	}
	if tx.Fee < 0 {
		return errors.New("chain: task action fee cannot be negative")
	}

	charge := tx.Fee
	release := 0.0
	claimReward := 0.0
	switch action.Action {
	case "stake":
		if action.Amount <= 0 {
			return errors.New("chain: stake action amount must be positive")
		}
		charge += action.Amount
	case "fund":
		if action.Amount <= 0 {
			return errors.New("chain: fund action amount must be positive")
		}
		charge += action.Amount
	case "unstake", "withdraw":
		if action.Amount <= 0 {
			return errors.New("chain: unstake/withdraw action amount must be positive")
		}
		locked := s.ParticipantStake(action.TaskID, action.Sender)
		if locked < action.Amount {
			return fmt.Errorf("%w: have %.8f, need %.8f", ErrInsufficientTaskStake, locked, action.Amount)
		}
		release = action.Amount
	case "claim":
		claimReward = s.ClaimableReward(action)
		if claimReward <= 0 {
			return ErrNoTaskReward
		}
	}

	accountSnapshot := accounts.Clone()
	taskSnapshot := s.ChainReplayClone()
	restore := func(cause error) error {
		accounts.RestoreFrom(accountSnapshot)
		if taskSnapshot != nil {
			_ = s.RestoreFromChainReplay(taskSnapshot)
		}
		return cause
	}

	if err := accounts.ChargeAndBumpNonce(action.Sender, charge, tx.Nonce); err != nil {
		return err
	}
	if err := s.ApplyAction(action); err != nil {
		return restore(err)
	}
	if release > 0 {
		accounts.Credit(action.Sender, release)
	}
	if claimReward > 0 {
		accounts.Credit(action.Sender, claimReward)
	}
	return nil
}

func DecodeTaskActionTx(tx *mempool.Tx) (TaskAction, error) {
	if tx == nil {
		return TaskAction{}, errors.New("chain: nil task action tx")
	}
	if tx.ContractID != TaskContractID {
		return TaskAction{}, fmt.Errorf("%w: got %q, want %q", ErrNotTaskActionTx, tx.ContractID, TaskContractID)
	}
	var action TaskAction
	if err := json.Unmarshal(tx.Payload, &action); err != nil {
		return TaskAction{}, fmt.Errorf("chain: decode task action payload: %w", err)
	}
	if action.ID == "" {
		action.ID = tx.ID
	} else if tx.ID != "" && action.ID != tx.ID {
		return TaskAction{}, fmt.Errorf("chain: task action id %q does not match tx id %q", action.ID, tx.ID)
	}
	if action.Sender == "" {
		action.Sender = tx.Sender
	} else if tx.Sender != "" && action.Sender != tx.Sender {
		return TaskAction{}, fmt.Errorf("chain: task action sender %q does not match tx sender %q", action.Sender, tx.Sender)
	}
	if action.Nonce == 0 {
		action.Nonce = tx.Nonce
	}
	if action.Nonce != tx.Nonce {
		return TaskAction{}, fmt.Errorf("chain: task action nonce %d does not match tx nonce %d", action.Nonce, tx.Nonce)
	}
	return action, nil
}

func (s *TaskStateStore) GetTask(taskID string) (TaskState, bool) {
	if s == nil {
		return TaskState{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	task, ok := s.tasks[taskID]
	if !ok {
		return TaskState{}, false
	}
	return cloneTaskState(task), true
}

func (s *TaskStateStore) AllTasks() []TaskState {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := make([]string, 0, len(s.tasks))
	for id := range s.tasks {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]TaskState, 0, len(ids))
	for _, id := range ids {
		out = append(out, cloneTaskState(s.tasks[id]))
	}
	return out
}

func (s *TaskStateStore) ParticipantStake(taskID, sender string) float64 {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	task, ok := s.tasks[taskID]
	if !ok {
		return 0
	}
	return task.Participants[sender].Stake
}

func (s *TaskStateStore) RewardPool(taskID string) float64 {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	task, ok := s.tasks[taskID]
	if !ok {
		return 0
	}
	return task.RewardPoolAmount
}

func (s *TaskStateStore) ClaimableReward(action TaskAction) float64 {
	if s == nil {
		return 0
	}
	payload := parseTaskActionPayload(action.Payload)
	s.mu.RLock()
	defer s.mu.RUnlock()
	task, ok := s.tasks[action.TaskID]
	if !ok {
		return 0
	}
	return claimableRewardLocked(task, action.Sender, payload)
}

func (s *TaskStateStore) Count() int {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.tasks)
}

func (s *TaskStateStore) ChainReplayClone() ChainReplayApplier {
	if s == nil {
		return nil
	}
	clone := NewTaskStateStore()
	s.mu.RLock()
	defer s.mu.RUnlock()
	for id, task := range s.tasks {
		cp := cloneTaskState(task)
		clone.tasks[id] = &cp
	}
	for id := range s.actionIDs {
		clone.actionIDs[id] = struct{}{}
	}
	for sender, nonce := range s.senderNonce {
		clone.senderNonce[sender] = nonce
	}
	return clone
}

func (s *TaskStateStore) RestoreFromChainReplay(from ChainReplayApplier) error {
	if s == nil {
		return errors.New("chain: nil TaskStateStore")
	}
	other, ok := from.(*TaskStateStore)
	if !ok || other == nil {
		return errors.New("chain: replay restore expects *TaskStateStore snapshot")
	}
	other.mu.RLock()
	tasks := make(map[string]*TaskState, len(other.tasks))
	for id, task := range other.tasks {
		cp := cloneTaskState(task)
		tasks[id] = &cp
	}
	actionIDs := make(map[string]struct{}, len(other.actionIDs))
	for id := range other.actionIDs {
		actionIDs[id] = struct{}{}
	}
	senderNonce := make(map[string]uint64, len(other.senderNonce))
	for sender, nonce := range other.senderNonce {
		senderNonce[sender] = nonce
	}
	other.mu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	s.tasks = tasks
	s.actionIDs = actionIDs
	s.senderNonce = senderNonce
	return nil
}

func (s *TaskStateStore) StateRoot() string {
	if s == nil {
		return ""
	}
	tasks := s.AllTasks()
	h := sha256.New()
	for _, task := range tasks {
		fmt.Fprintf(h, "%s:%0.8f:%0.8f:%0.8f:%0.8f:%d:%s:%s:%t:%s;",
			task.TaskID,
			task.TotalStakeAmount,
			task.RewardPoolAmount,
			task.PendingRewardAmount,
			task.TotalRewardPaidAmount,
			task.RunningCount,
			task.LastAction,
			task.LastActionID,
			task.IsMigrated,
			task.MigratedTo,
		)
		if task.Manifest != nil {
			manifestJSON, _ := json.Marshal(task.Manifest)
			fmt.Fprintf(h, "catalog:%s:%t:%s:%s;",
				manifestJSON,
				task.CatalogPaused,
				task.CatalogPublishedAt,
				task.CatalogUpdatedAt,
			)
		}
		participants := sortedParticipantIDs(task.Participants)
		for _, sender := range participants {
			p := task.Participants[sender]
			fmt.Fprintf(h, "p:%s:%t:%0.8f:%0.8f:%0.8f:%s:%s:%s:%s:%s:%s:%d:%d;",
				p.Sender, p.Running, p.Stake,
				p.PendingRewardAmount, p.TotalRewardClaimedAmount,
				p.LastAction, p.LastActionID, p.LastActionAt,
				p.LastStartedAt, p.LastStoppedAt, p.LastClaimedAt,
				p.SubmissionCount, p.ClaimCount,
			)
		}
		rounds := make([]string, 0, len(task.Submissions))
		for round := range task.Submissions {
			rounds = append(rounds, round)
		}
		sort.Strings(rounds)
		for _, round := range rounds {
			senders := make([]string, 0, len(task.Submissions[round]))
			for sender := range task.Submissions[round] {
				senders = append(senders, sender)
			}
			sort.Strings(senders)
			for _, sender := range senders {
				sub := task.Submissions[round][sender]
				fmt.Fprintf(h, "s:%s:%s:%s:%d:%d:%s:%s:%0.8f:%t:%s:%s;",
					round, sender, sub.ActionID, sub.Round, sub.Slot,
					sub.SubmissionValue, sub.Payload, sub.RewardAmount,
					sub.Claimed, sub.ClaimedAt, sub.Timestamp,
				)
			}
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (s *TaskStateStore) getOrCreateTaskLocked(taskID string) *TaskState {
	task, ok := s.tasks[taskID]
	if ok {
		return task
	}
	task = &TaskState{
		TaskID:       taskID,
		Participants: map[string]TaskParticipantState{},
		Submissions:  map[string]map[string]TaskSubmissionState{},
	}
	s.tasks[taskID] = task
	return task
}

func (s *TaskStateStore) applySubmissionLocked(task *TaskState, participant *TaskParticipantState, action TaskAction, allowLegacyResourceProof bool) error {
	payload := parseTaskActionPayload(action.Payload)
	if payload.RewardAmount < 0 {
		return errors.New("chain: submission reward amount cannot be negative")
	}
	if err := validateTaskSubmissionPolicy(task, participant, action, payload, allowLegacyResourceProof); err != nil {
		return err
	}
	roundKey := strconv.FormatUint(payload.Round, 10)
	existing := TaskSubmissionState{}
	hasExisting := false
	if task.Submissions[roundKey] != nil {
		existing, hasExisting = task.Submissions[roundKey][action.Sender]
	}
	if hasExisting {
		if existing.Claimed {
			// A restarted task client can legitimately replay a proof for the
			// current round after the previous proof was already claimed. Keep
			// the claimed submission immutable and treat the later submit as a
			// no-op so task-state projection remains replayable.
			return nil
		}
	}
	if _, isResourceTask := systemResourceTaskPolicies[action.TaskID]; isResourceTask {
		value := strings.TrimSpace(payload.SubmissionValue)
		for priorRound, submissions := range task.Submissions {
			if priorRound == roundKey {
				continue
			}
			prior, ok := submissions[action.Sender]
			if ok && strings.EqualFold(strings.TrimSpace(prior.SubmissionValue), value) {
				return fmt.Errorf("chain: resource worker proof was already submitted in round %s", priorRound)
			}
		}
	}
	availableRewardPool := task.RewardPoolAmount
	if hasExisting && existing.RewardAmount > 0 {
		availableRewardPool += existing.RewardAmount
	}
	if payload.RewardAmount > 0 {
		if participant.Stake <= 0 {
			return ErrTaskActionRequiresStake
		}
		if taskAmountLessThan(availableRewardPool, payload.RewardAmount) {
			return fmt.Errorf("%w: have %.8f, need %.8f",
				ErrInsufficientTaskRewardPool, availableRewardPool, payload.RewardAmount)
		}
	}
	if task.Submissions[roundKey] == nil {
		task.Submissions[roundKey] = map[string]TaskSubmissionState{}
	}
	if hasExisting && existing.RewardAmount > 0 {
		task.RewardPoolAmount += existing.RewardAmount
		task.PendingRewardAmount -= existing.RewardAmount
		participant.PendingRewardAmount -= existing.RewardAmount
		clampTaskRewardAccounting(task, participant)
	}
	if payload.RewardAmount > 0 {
		task.RewardPoolAmount -= payload.RewardAmount
		task.PendingRewardAmount += payload.RewardAmount
		participant.PendingRewardAmount += payload.RewardAmount
		clampTaskRewardAccounting(task, participant)
	}
	value := payload.SubmissionValue
	if value == "" {
		value = action.Payload
	}
	task.Submissions[roundKey][action.Sender] = TaskSubmissionState{
		ActionID:        action.ID,
		Sender:          action.Sender,
		Round:           payload.Round,
		Slot:            payload.Slot,
		SubmissionValue: value,
		Payload:         action.Payload,
		RewardAmount:    payload.RewardAmount,
		Timestamp:       action.Timestamp,
	}
	return nil
}

func validateTaskSubmissionPolicy(task *TaskState, participant *TaskParticipantState, action TaskAction, payload taskActionPayload, allowLegacyResourceProof bool) error {
	if task.Manifest != nil {
		if task.CatalogPaused || !task.Manifest.Active {
			return errors.New("chain: task catalog entry is not active")
		}
		if taskAmountLessThan(participant.Stake, task.Manifest.MinimumStakeAmount) {
			return fmt.Errorf("%w: have %.8f, need %.8f",
				ErrInsufficientTaskStake, participant.Stake, task.Manifest.MinimumStakeAmount)
		}
		if taskAmountLessThan(task.Manifest.RewardPerRound, payload.RewardAmount) {
			return fmt.Errorf("chain: submission reward %.8f exceeds catalog reward_per_round %.8f",
				payload.RewardAmount, task.Manifest.RewardPerRound)
		}
	}

	policy, isResourceTask := systemResourceTaskPolicies[action.TaskID]
	if !isResourceTask {
		return nil
	}
	if taskAmountLessThan(policy.MaxRoundReward, payload.RewardAmount) {
		return fmt.Errorf("chain: %s worker reward %.8f exceeds consensus cap %.8f",
			policy.Resource, payload.RewardAmount, policy.MaxRoundReward)
	}
	var envelope resourceWorkerProofPayload
	if err := json.Unmarshal([]byte(action.Payload), &envelope); err != nil {
		return fmt.Errorf("chain: decode resource worker proof: %w", err)
	}
	if allowLegacyResourceProof && strings.TrimSpace(envelope.Resource) == "" {
		return nil
	}
	if strings.TrimSpace(envelope.Resource) == "" {
		return fmt.Errorf("%w: expected %q", ErrLegacyResourceWorkerProof, policy.Resource)
	}
	if strings.ToLower(strings.TrimSpace(envelope.Resource)) != policy.Resource {
		return fmt.Errorf("chain: resource worker proof declares %q, expected %q", envelope.Resource, policy.Resource)
	}
	if strings.TrimSpace(envelope.SubmissionValue) == "" || len(envelope.Proof) == 0 {
		return errors.New("chain: resource worker submission requires a value and proof")
	}
	var proof resourceWorkerProofDetails
	if err := json.Unmarshal(envelope.Proof, &proof); err != nil {
		return fmt.Errorf("chain: decode resource worker proof details: %w", err)
	}
	if strings.EqualFold(strings.TrimSpace(envelope.Source), "qsdm-edge-pool") {
		if proof.JobCount <= 0 || proof.TotalUnits == 0 || !validTaskDigest(proof.ReceiptRoot) {
			return errors.New("chain: pooled resource proof requires verified jobs, units, and receipt root")
		}
		if !strings.EqualFold(envelope.SubmissionValue, proof.ReceiptRoot) {
			return errors.New("chain: pooled resource submission does not match its receipt root")
		}
		return nil
	}
	if strings.TrimSpace(proof.Algorithm) == "" || proof.Units == 0 || !validTaskDigest(proof.Digest) {
		return errors.New("chain: local resource proof requires an algorithm, units, and digest")
	}
	if !strings.EqualFold(envelope.SubmissionValue, proof.Digest) {
		return errors.New("chain: local resource submission does not match its proof digest")
	}
	return nil
}

func validTaskDigest(value string) bool {
	decoded, err := hex.DecodeString(strings.TrimSpace(value))
	return err == nil && len(decoded) == sha256.Size
}

func (s *TaskStateStore) applyClaimLocked(task *TaskState, participant *TaskParticipantState, action TaskAction) (float64, error) {
	payload := parseTaskActionPayload(action.Payload)
	reward := 0.0
	claimAll := payload.Round == 0
	roundFilter := strconv.FormatUint(payload.Round, 10)
	for round, bySender := range task.Submissions {
		if !claimAll && round != roundFilter {
			continue
		}
		submission, ok := bySender[action.Sender]
		if !ok || submission.Claimed || submission.RewardAmount <= 0 {
			continue
		}
		reward += submission.RewardAmount
		submission.Claimed = true
		submission.ClaimedAt = action.Timestamp
		bySender[action.Sender] = submission
	}
	if reward <= 0 {
		return 0, ErrNoTaskReward
	}
	participant.PendingRewardAmount -= reward
	participant.TotalRewardClaimedAmount += reward
	participant.LastClaimedAt = action.Timestamp
	task.PendingRewardAmount -= reward
	task.TotalRewardPaidAmount += reward
	clampTaskRewardAccounting(task, participant)
	return reward, nil
}

func claimableRewardLocked(task *TaskState, sender string, payload taskActionPayload) float64 {
	if task == nil {
		return 0
	}
	reward := 0.0
	claimAll := payload.Round == 0
	roundFilter := strconv.FormatUint(payload.Round, 10)
	for round, bySender := range task.Submissions {
		if !claimAll && round != roundFilter {
			continue
		}
		submission, ok := bySender[sender]
		if !ok || submission.Claimed || submission.RewardAmount <= 0 {
			continue
		}
		reward += submission.RewardAmount
	}
	return reward
}

func clampTaskRewardAccounting(task *TaskState, participant *TaskParticipantState) {
	if task != nil && task.PendingRewardAmount < 0 {
		task.PendingRewardAmount = clampTaskAmount(task.PendingRewardAmount)
	}
	if task != nil && task.RewardPoolAmount < 0 {
		task.RewardPoolAmount = clampTaskAmount(task.RewardPoolAmount)
	}
	if participant != nil && participant.PendingRewardAmount < 0 {
		participant.PendingRewardAmount = clampTaskAmount(participant.PendingRewardAmount)
	}
}

func parseTaskActionPayload(raw string) taskActionPayload {
	var payload taskActionPayload
	if raw == "" {
		return payload
	}
	_ = json.Unmarshal([]byte(raw), &payload)
	return payload
}

func cloneTaskState(task *TaskState) TaskState {
	if task == nil {
		return TaskState{}
	}
	out := *task
	out.Manifest = cloneTaskManifest(task.Manifest)
	out.Participants = make(map[string]TaskParticipantState, len(task.Participants))
	for sender, participant := range task.Participants {
		out.Participants[sender] = participant
	}
	out.Submissions = make(map[string]map[string]TaskSubmissionState, len(task.Submissions))
	for round, bySender := range task.Submissions {
		out.Submissions[round] = make(map[string]TaskSubmissionState, len(bySender))
		for sender, submission := range bySender {
			out.Submissions[round][sender] = submission
		}
	}
	return out
}

func sortedParticipantIDs(participants map[string]TaskParticipantState) []string {
	ids := make([]string, 0, len(participants))
	for id := range participants {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
