package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"

	"github.com/blackbeardONE/QSDM/pkg/chain"
)

const qsdmTaskRegistryPathEnv = "QSDM_TASK_REGISTRY_PATH"

const qsdmTaskDefaultPublicKey = "11111111111111111111111111111111"

const qsdmTaskRegistrySource = "operator-registry"

type QSDMTaskSubmission struct {
	SubmissionValue string  `json:"submission_value"`
	Slot            uint64  `json:"slot"`
	RewardAmount    float64 `json:"reward_amount,omitempty"`
	Claimed         bool    `json:"claimed,omitempty"`
	ClaimedAt       string  `json:"claimed_at,omitempty"`
}

type QSDMTask struct {
	TaskID                        string                                   `json:"task_id"`
	TaskName                      string                                   `json:"task_name"`
	TaskManager                   string                                   `json:"task_manager"`
	IsAllowlisted                 bool                                     `json:"is_allowlisted"`
	IsActive                      bool                                     `json:"is_active"`
	TaskAuditProgram              string                                   `json:"task_audit_program"`
	StakePotAccount               string                                   `json:"stake_pot_account"`
	TotalBountyAmount             float64                                  `json:"total_bounty_amount"`
	BountyAmountPerRound          float64                                  `json:"bounty_amount_per_round"`
	CurrentRound                  uint64                                   `json:"current_round"`
	AvailableBalances             map[string]float64                       `json:"available_balances"`
	StakeList                     map[string]float64                       `json:"stake_list"`
	TaskMetadata                  string                                   `json:"task_metadata"`
	TaskDescription               string                                   `json:"task_description"`
	Submissions                   map[string]map[string]QSDMTaskSubmission `json:"submissions"`
	SubmissionsAuditTrigger       map[string]map[string]interface{}        `json:"submissions_audit_trigger"`
	TotalStakeAmount              float64                                  `json:"total_stake_amount"`
	RewardPoolAmount              float64                                  `json:"reward_pool_amount,omitempty"`
	PendingRewardAmount           float64                                  `json:"pending_reward_amount,omitempty"`
	TotalRewardPaidAmount         float64                                  `json:"total_reward_paid_amount,omitempty"`
	MinimumStakeAmount            float64                                  `json:"minimum_stake_amount"`
	IPAddressList                 map[string]string                        `json:"ip_address_list"`
	RoundTime                     uint64                                   `json:"round_time"`
	StartingSlot                  uint64                                   `json:"starting_slot"`
	AuditWindow                   uint64                                   `json:"audit_window"`
	SubmissionWindow              uint64                                   `json:"submission_window"`
	TaskExecutableNetwork         string                                   `json:"task_executable_network"`
	DistributionRewardsSubmission map[string]map[string]QSDMTaskSubmission `json:"distribution_rewards_submission"`
	DistributionsAuditTrigger     map[string]map[string]interface{}        `json:"distributions_audit_trigger"`
	DistributionsAuditRecord      map[string]string                        `json:"distributions_audit_record"`
	TaskVars                      string                                   `json:"task_vars"`
	KoiiVars                      string                                   `json:"koii_vars"`
	IsMigrated                    bool                                     `json:"is_migrated"`
	MigratedTo                    string                                   `json:"migrated_to"`
	AllowedFailedDistributions    uint64                                   `json:"allowed_failed_distributions"`
	TaskType                      string                                   `json:"task_type,omitempty"`
	TokenType                     string                                   `json:"token_type,omitempty"`
	NativeRuntime                 string                                   `json:"native_runtime,omitempty"`
	Manifest                      *chain.TaskManifest                      `json:"manifest,omitempty"`
	CatalogVersion                uint64                                   `json:"catalog_version,omitempty"`
	CatalogPaused                 bool                                     `json:"catalog_paused,omitempty"`
	CatalogPublishedAt            string                                   `json:"catalog_published_at,omitempty"`
	CatalogUpdatedAt              string                                   `json:"catalog_updated_at,omitempty"`
}

type qsdmTaskRegistryFile struct {
	Tasks []QSDMTask `json:"tasks"`
}

type QSDMTasksListResponse struct {
	Runtime          string     `json:"runtime"`
	Configured       bool       `json:"configured"`
	Source           string     `json:"source,omitempty"`
	CatalogSource    string     `json:"catalog_source,omitempty"`
	CatalogStateRoot string     `json:"catalog_state_root,omitempty"`
	Tasks            []QSDMTask `json:"tasks"`
}

type QSDMTaskResponse struct {
	Runtime          string   `json:"runtime"`
	Configured       bool     `json:"configured"`
	Source           string   `json:"source,omitempty"`
	CatalogSource    string   `json:"catalog_source,omitempty"`
	CatalogStateRoot string   `json:"catalog_state_root,omitempty"`
	Task             QSDMTask `json:"task"`
}

type QSDMTaskSubmissionsResponse struct {
	Runtime     string                                   `json:"runtime"`
	Configured  bool                                     `json:"configured"`
	TaskID      string                                   `json:"task_id"`
	Submissions map[string]map[string]QSDMTaskSubmission `json:"submissions"`
}

type qsdmTaskCatalogFingerprintRecord struct {
	TaskID                     string              `json:"task_id"`
	TaskName                   string              `json:"task_name"`
	TaskManager                string              `json:"task_manager"`
	IsAllowlisted              bool                `json:"is_allowlisted"`
	IsActive                   bool                `json:"is_active"`
	TaskAuditProgram           string              `json:"task_audit_program"`
	StakePotAccount            string              `json:"stake_pot_account"`
	TotalBountyAmount          float64             `json:"total_bounty_amount"`
	BountyAmountPerRound       float64             `json:"bounty_amount_per_round"`
	MinimumStakeAmount         float64             `json:"minimum_stake_amount"`
	TaskMetadata               string              `json:"task_metadata"`
	TaskDescription            string              `json:"task_description"`
	RoundTime                  uint64              `json:"round_time"`
	StartingSlot               uint64              `json:"starting_slot"`
	AuditWindow                uint64              `json:"audit_window"`
	SubmissionWindow           uint64              `json:"submission_window"`
	TaskExecutableNetwork      string              `json:"task_executable_network"`
	TaskVars                   string              `json:"task_vars"`
	TaskType                   string              `json:"task_type"`
	TokenType                  string              `json:"token_type"`
	NativeRuntime              string              `json:"native_runtime"`
	AllowedFailedDistributions uint64              `json:"allowed_failed_distributions"`
	Manifest                   *chain.TaskManifest `json:"manifest,omitempty"`
	CatalogVersion             uint64              `json:"catalog_version,omitempty"`
	CatalogPaused              bool                `json:"catalog_paused,omitempty"`
	CatalogPublishedAt         string              `json:"catalog_published_at,omitempty"`
	CatalogUpdatedAt           string              `json:"catalog_updated_at,omitempty"`
}

// qsdmTaskCatalogFingerprint hashes only task definitions and compatibility
// metadata. Live stake, rewards, submissions, and round counters are excluded,
// so the fingerprint changes only when the effective catalog changes.
func qsdmTaskCatalogFingerprint(tasks []QSDMTask) string {
	records := make([]qsdmTaskCatalogFingerprintRecord, 0, len(tasks))
	for _, task := range tasks {
		records = append(records, qsdmTaskCatalogFingerprintRecord{
			TaskID:                     task.TaskID,
			TaskName:                   task.TaskName,
			TaskManager:                task.TaskManager,
			IsAllowlisted:              task.IsAllowlisted,
			IsActive:                   task.IsActive,
			TaskAuditProgram:           task.TaskAuditProgram,
			StakePotAccount:            task.StakePotAccount,
			TotalBountyAmount:          task.TotalBountyAmount,
			BountyAmountPerRound:       task.BountyAmountPerRound,
			MinimumStakeAmount:         task.MinimumStakeAmount,
			TaskMetadata:               task.TaskMetadata,
			TaskDescription:            task.TaskDescription,
			RoundTime:                  task.RoundTime,
			StartingSlot:               task.StartingSlot,
			AuditWindow:                task.AuditWindow,
			SubmissionWindow:           task.SubmissionWindow,
			TaskExecutableNetwork:      task.TaskExecutableNetwork,
			TaskVars:                   task.TaskVars,
			TaskType:                   task.TaskType,
			TokenType:                  task.TokenType,
			NativeRuntime:              task.NativeRuntime,
			AllowedFailedDistributions: task.AllowedFailedDistributions,
			Manifest:                   task.Manifest,
			CatalogVersion:             task.CatalogVersion,
			CatalogPaused:              task.CatalogPaused,
			CatalogPublishedAt:         task.CatalogPublishedAt,
			CatalogUpdatedAt:           task.CatalogUpdatedAt,
		})
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i].TaskID < records[j].TaskID
	})
	raw, err := json.Marshal(records)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func loadQSDMTasksFromRegistry() (QSDMTasksListResponse, error) {
	path := strings.TrimSpace(os.Getenv(qsdmTaskRegistryPathEnv))
	if path == "" {
		return QSDMTasksListResponse{
			Runtime:    "qsdm-native",
			Configured: false,
			Tasks:      []QSDMTask{},
		}, nil
	}

	raw, err := os.ReadFile(path) // #nosec G304,G703 -- path is selected from trusted startup configuration, never the request.
	if err != nil {
		return QSDMTasksListResponse{}, err
	}
	// Windows PowerShell 5.1 writes UTF-8 with a BOM by default. Accept an
	// existing BOM so an otherwise valid operator registry cannot take the
	// public task catalog offline.
	raw = bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF})

	var wrapped qsdmTaskRegistryFile
	if err := json.Unmarshal(raw, &wrapped); err != nil || wrapped.Tasks == nil {
		var tasks []QSDMTask
		if arrayErr := json.Unmarshal(raw, &tasks); arrayErr != nil {
			if err != nil {
				return QSDMTasksListResponse{}, err
			}
			return QSDMTasksListResponse{}, arrayErr
		}
		wrapped.Tasks = tasks
	}

	tasks := make([]QSDMTask, 0, len(wrapped.Tasks))
	for _, task := range wrapped.Tasks {
		task = normalizeQSDMTask(task)
		if task.TaskID != "" {
			tasks = append(tasks, task)
		}
	}

	return QSDMTasksListResponse{
		Runtime:    "qsdm-native",
		Configured: true,
		Source:     qsdmTaskRegistrySource,
		Tasks:      tasks,
	}, nil
}

func normalizeQSDMTask(task QSDMTask) QSDMTask {
	task.TaskID = strings.TrimSpace(task.TaskID)
	task.TaskName = strings.TrimSpace(task.TaskName)
	if task.TaskName == "" {
		task.TaskName = task.TaskID
	}
	if strings.TrimSpace(task.TaskManager) == "" {
		task.TaskManager = qsdmTaskDefaultPublicKey
	}
	if strings.TrimSpace(task.StakePotAccount) == "" {
		task.StakePotAccount = qsdmTaskDefaultPublicKey
	}
	if strings.TrimSpace(task.TaskAuditProgram) == "" {
		task.TaskAuditProgram = task.TaskID
	}
	if strings.TrimSpace(task.TaskMetadata) == "" {
		task.TaskMetadata = task.TaskID
	}
	if strings.TrimSpace(task.TaskExecutableNetwork) == "" {
		task.TaskExecutableNetwork = "IPFS"
	}
	if strings.TrimSpace(task.TaskVars) == "" {
		task.TaskVars = "{}"
	}
	if strings.TrimSpace(task.KoiiVars) == "" {
		task.KoiiVars = "{}"
	}
	if strings.EqualFold(strings.TrimSpace(task.TaskType), "KPL") ||
		strings.TrimSpace(task.TokenType) != "" {
		task.TaskType = "KPL"
	} else {
		// Early QSDM registries used KOII for native tasks. Catalog consumers
		// must receive the actual native denomination so CELL balances are used.
		task.TaskType = "CELL"
	}
	task.NativeRuntime = "qsdm"

	if task.AvailableBalances == nil {
		task.AvailableBalances = map[string]float64{}
	}
	if task.StakeList == nil {
		task.StakeList = map[string]float64{}
	}
	if task.IPAddressList == nil {
		task.IPAddressList = map[string]string{}
	}
	if task.Submissions == nil {
		task.Submissions = map[string]map[string]QSDMTaskSubmission{}
	}
	if task.SubmissionsAuditTrigger == nil {
		task.SubmissionsAuditTrigger = map[string]map[string]interface{}{}
	}
	if task.DistributionRewardsSubmission == nil {
		task.DistributionRewardsSubmission = map[string]map[string]QSDMTaskSubmission{}
	}
	if task.DistributionsAuditTrigger == nil {
		task.DistributionsAuditTrigger = map[string]map[string]interface{}{}
	}
	if task.DistributionsAuditRecord == nil {
		task.DistributionsAuditRecord = map[string]string{}
	}
	return task
}

func findQSDMTask(tasks []QSDMTask, taskID string) (QSDMTask, bool) {
	for _, task := range tasks {
		if task.TaskID == taskID {
			return task, true
		}
	}
	return QSDMTask{}, false
}

func (h *Handlers) QSDMTasksListHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	response, err := loadQSDMTasksFromRegistry()
	if err != nil {
		writeErrorResponse(w, http.StatusInternalServerError, "failed to load QSDM task registry: "+err.Error())
		return
	}
	projection, err := applyQSDMTaskActionProjection(response.Tasks)
	if err != nil {
		writeErrorResponse(w, http.StatusInternalServerError, "failed to project QSDM task state: "+err.Error())
		return
	}
	response.Tasks = projection.Tasks
	response.Configured = response.Configured || projection.Configured
	response.CatalogSource = projection.Source
	response.CatalogStateRoot = qsdmTaskCatalogFingerprint(response.Tasks)

	writeJSONResponse(w, http.StatusOK, response)
}

func (h *Handlers) QSDMTaskRouteHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/v1/tasks/")
	if path == "" {
		writeErrorResponse(w, http.StatusBadRequest, "task_id required")
		return
	}

	wantsSubmissions := strings.HasSuffix(path, "/submissions")
	if wantsSubmissions {
		path = strings.TrimSuffix(path, "/submissions")
	}
	wantsState := strings.HasSuffix(path, "/state")
	if wantsState {
		path = strings.TrimSuffix(path, "/state")
	}
	taskID, err := url.PathUnescape(strings.Trim(path, "/"))
	if err != nil || strings.TrimSpace(taskID) == "" {
		writeErrorResponse(w, http.StatusBadRequest, "invalid task_id")
		return
	}

	registry, err := loadQSDMTasksFromRegistry()
	if err != nil {
		writeErrorResponse(w, http.StatusInternalServerError, "failed to load QSDM task registry: "+err.Error())
		return
	}
	projection, err := applyQSDMTaskActionProjection(registry.Tasks)
	if err != nil {
		writeErrorResponse(w, http.StatusInternalServerError, "failed to project QSDM task state: "+err.Error())
		return
	}
	registry.Tasks = projection.Tasks
	registry.Configured = registry.Configured || projection.Configured
	registry.CatalogSource = projection.Source
	registry.CatalogStateRoot = qsdmTaskCatalogFingerprint(registry.Tasks)

	task, ok := findQSDMTask(registry.Tasks, taskID)
	if !ok {
		if wantsState {
			store, path, configured, err := loadQSDMTaskActionStateStore()
			if err != nil {
				writeErrorResponse(w, http.StatusInternalServerError, "failed to project QSDM task state: "+err.Error())
				return
			}
			state, stateOK := store.GetTask(taskID)
			if stateOK {
				writeJSONResponse(w, http.StatusOK, QSDMTaskStateResponse{
					Runtime:    registry.Runtime,
					Configured: configured,
					Source:     path,
					StateRoot:  store.StateRoot(),
					Task:       state,
				})
				return
			}
		}
		writeErrorResponse(w, http.StatusNotFound, "task not found")
		return
	}

	if wantsSubmissions {
		writeJSONResponse(w, http.StatusOK, QSDMTaskSubmissionsResponse{
			Runtime:     registry.Runtime,
			Configured:  registry.Configured,
			TaskID:      task.TaskID,
			Submissions: task.Submissions,
		})
		return
	}
	if wantsState {
		store, path, configured, err := loadQSDMTaskActionStateStore()
		if err != nil {
			writeErrorResponse(w, http.StatusInternalServerError, "failed to project QSDM task state: "+err.Error())
			return
		}
		state, ok := store.GetTask(task.TaskID)
		if !ok {
			state = chain.TaskState{
				TaskID:       task.TaskID,
				Participants: map[string]chain.TaskParticipantState{},
				Submissions:  map[string]map[string]chain.TaskSubmissionState{},
			}
		}
		writeJSONResponse(w, http.StatusOK, QSDMTaskStateResponse{
			Runtime:    registry.Runtime,
			Configured: configured,
			Source:     path,
			StateRoot:  store.StateRoot(),
			Task:       state,
		})
		return
	}

	writeJSONResponse(w, http.StatusOK, QSDMTaskResponse{
		Runtime:          registry.Runtime,
		Configured:       registry.Configured,
		Source:           registry.Source,
		CatalogSource:    registry.CatalogSource,
		CatalogStateRoot: registry.CatalogStateRoot,
		Task:             task,
	})
}
