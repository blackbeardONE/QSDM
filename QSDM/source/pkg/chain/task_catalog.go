package chain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

const (
	TaskCatalogSchemaVersion = 1

	TaskActionCatalogRegister = "catalog-register"
	TaskActionCatalogUpdate   = "catalog-update"
	TaskActionCatalogPause    = "catalog-pause"
	TaskActionCatalogResume   = "catalog-resume"
)

var (
	ErrTaskManifestExists          = errors.New("chain: task manifest already exists")
	ErrTaskManifestNotFound        = errors.New("chain: task manifest not found")
	ErrTaskManifestManagerMismatch = errors.New("chain: task manifest manager mismatch")
	ErrTaskManifestVersion         = errors.New("chain: invalid task manifest version")

	taskCatalogIDPattern       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	taskCapabilityPattern      = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)
	taskSemanticVersionPattern = regexp.MustCompile(`^\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)?$`)
)

// TaskRuntimeManifest describes executable compatibility without allowing a
// catalog publisher to inject native JavaScript into Hive. capability runtimes
// map to code already shipped by Hive; wasm runtimes must be hash-pinned and
// are executed only by clients that support the declared ABI.
type TaskRuntimeManifest struct {
	Kind              string `json:"kind"`
	Capability        string `json:"capability,omitempty"`
	ModuleURL         string `json:"module_url,omitempty"`
	ModuleSHA256      string `json:"module_sha256,omitempty"`
	ABI               string `json:"abi,omitempty"`
	MinHiveVersion    string `json:"min_hive_version,omitempty"`
	MaxMemoryMB       uint64 `json:"max_memory_mb,omitempty"`
	MaxRuntimeSeconds uint64 `json:"max_runtime_seconds,omitempty"`
}

// TaskManifest is the consensus-backed task catalog record. Economic and
// participant state remain in TaskState; this structure defines identity,
// presentation, compatibility, and immutable publisher ownership.
type TaskManifest struct {
	SchemaVersion      uint64              `json:"schema_version"`
	TaskID             string              `json:"task_id"`
	Version            uint64              `json:"version"`
	Name               string              `json:"name"`
	Description        string              `json:"description,omitempty"`
	Manager            string              `json:"manager"`
	Active             bool                `json:"active"`
	Runtime            TaskRuntimeManifest `json:"runtime"`
	MinimumStakeAmount float64             `json:"minimum_stake_amount,omitempty"`
	RewardPerRound     float64             `json:"reward_per_round,omitempty"`
	RoundTime          uint64              `json:"round_time"`
	SubmissionWindow   uint64              `json:"submission_window,omitempty"`
	AuditWindow        uint64              `json:"audit_window,omitempty"`
	MetadataURL        string              `json:"metadata_url,omitempty"`
	SourceURL          string              `json:"source_url,omitempty"`
	IconURL            string              `json:"icon_url,omitempty"`
	Tags               []string            `json:"tags,omitempty"`
	AuthorizedRelayIDs []string            `json:"authorized_relay_ids,omitempty"`
}

func isTaskCatalogAction(action string) bool {
	switch action {
	case TaskActionCatalogRegister, TaskActionCatalogUpdate,
		TaskActionCatalogPause, TaskActionCatalogResume:
		return true
	default:
		return false
	}
}

func decodeTaskManifest(action TaskAction) (TaskManifest, error) {
	var manifest TaskManifest
	if strings.TrimSpace(action.Payload) == "" {
		return manifest, errors.New("chain: task manifest payload required")
	}
	if err := json.Unmarshal([]byte(action.Payload), &manifest); err != nil {
		return manifest, fmt.Errorf("chain: decode task manifest: %w", err)
	}
	manifest.TaskID = strings.TrimSpace(manifest.TaskID)
	manifest.Name = strings.TrimSpace(manifest.Name)
	manifest.Description = strings.TrimSpace(manifest.Description)
	manifest.Manager = strings.ToLower(strings.TrimSpace(manifest.Manager))
	manifest.Runtime.Kind = strings.ToLower(strings.TrimSpace(manifest.Runtime.Kind))
	manifest.Runtime.Capability = strings.ToLower(strings.TrimSpace(manifest.Runtime.Capability))
	manifest.Runtime.ModuleURL = strings.TrimSpace(manifest.Runtime.ModuleURL)
	manifest.Runtime.ModuleSHA256 = strings.ToLower(strings.TrimSpace(manifest.Runtime.ModuleSHA256))
	manifest.Runtime.ABI = strings.TrimSpace(manifest.Runtime.ABI)
	manifest.Runtime.MinHiveVersion = strings.TrimSpace(manifest.Runtime.MinHiveVersion)
	manifest.MetadataURL = strings.TrimSpace(manifest.MetadataURL)
	manifest.SourceURL = strings.TrimSpace(manifest.SourceURL)
	manifest.IconURL = strings.TrimSpace(manifest.IconURL)
	manifest.Tags = normalizeTaskManifestTags(manifest.Tags)
	manifest.AuthorizedRelayIDs = normalizeTaskRelayIDs(manifest.AuthorizedRelayIDs)
	return manifest, validateTaskManifest(action, manifest)
}

func validateTaskManifest(action TaskAction, manifest TaskManifest) error {
	if manifest.SchemaVersion != TaskCatalogSchemaVersion {
		return fmt.Errorf("chain: unsupported task manifest schema_version %d", manifest.SchemaVersion)
	}
	if manifest.TaskID != action.TaskID || !taskCatalogIDPattern.MatchString(manifest.TaskID) {
		return errors.New("chain: task manifest task_id is invalid or does not match action")
	}
	if manifest.Version == 0 {
		return ErrTaskManifestVersion
	}
	if manifest.Manager != strings.ToLower(strings.TrimSpace(action.Sender)) {
		return ErrTaskManifestManagerMismatch
	}
	if len(manifest.Name) == 0 || len(manifest.Name) > 120 {
		return errors.New("chain: task manifest name must contain 1-120 characters")
	}
	if len(manifest.Description) > 2000 {
		return errors.New("chain: task manifest description exceeds 2000 characters")
	}
	if manifest.RoundTime == 0 || manifest.RoundTime > 10_000_000 {
		return errors.New("chain: task manifest round_time is out of range")
	}
	if manifest.SubmissionWindow > manifest.RoundTime || manifest.AuditWindow > manifest.RoundTime {
		return errors.New("chain: task manifest windows cannot exceed round_time")
	}
	if invalidTaskAmount(manifest.MinimumStakeAmount) || invalidTaskAmount(manifest.RewardPerRound) {
		return errors.New("chain: task manifest economic amounts must be finite and non-negative")
	}
	if len(manifest.Tags) > 16 {
		return errors.New("chain: task manifest supports at most 16 tags")
	}
	if len(manifest.AuthorizedRelayIDs) > 16 {
		return errors.New("chain: task manifest supports at most 16 authorized Relay ids")
	}
	for _, relayID := range manifest.AuthorizedRelayIDs {
		decoded, err := hex.DecodeString(relayID)
		if err != nil || len(decoded) != sha256.Size {
			return errors.New("chain: authorized Relay ids must be 32-byte hexadecimal key fingerprints")
		}
	}
	for _, tag := range manifest.Tags {
		if len(tag) == 0 || len(tag) > 32 || !taskCapabilityPattern.MatchString(tag) {
			return fmt.Errorf("chain: invalid task manifest tag %q", tag)
		}
	}
	for field, raw := range map[string]string{
		"metadata_url": manifest.MetadataURL,
		"source_url":   manifest.SourceURL,
		"icon_url":     manifest.IconURL,
	} {
		if err := validateTaskManifestHTTPSURL(field, raw); err != nil {
			return err
		}
	}
	if manifest.Runtime.MinHiveVersion != "" && !taskSemanticVersionPattern.MatchString(manifest.Runtime.MinHiveVersion) {
		return errors.New("chain: runtime min_hive_version must be semantic version x.y.z")
	}
	if manifest.Runtime.MaxMemoryMB > 4096 || manifest.Runtime.MaxRuntimeSeconds > 86400 {
		return errors.New("chain: runtime resource limit is out of range")
	}

	switch manifest.Runtime.Kind {
	case "capability":
		if !taskCapabilityPattern.MatchString(manifest.Runtime.Capability) {
			return errors.New("chain: capability runtime requires a valid capability")
		}
		if manifest.Runtime.ModuleURL != "" || manifest.Runtime.ModuleSHA256 != "" {
			return errors.New("chain: capability runtime cannot declare a remote module")
		}
	case "wasm":
		if err := validateTaskManifestHTTPSURL("runtime.module_url", manifest.Runtime.ModuleURL); err != nil {
			return err
		}
		decodedHash, err := hex.DecodeString(manifest.Runtime.ModuleSHA256)
		if err != nil || len(decodedHash) != 32 {
			return errors.New("chain: wasm runtime requires a 64-character module_sha256")
		}
		if len(manifest.Runtime.ABI) == 0 || len(manifest.Runtime.ABI) > 64 {
			return errors.New("chain: wasm runtime requires a bounded ABI identifier")
		}
	default:
		return fmt.Errorf("chain: unsupported task runtime kind %q", manifest.Runtime.Kind)
	}
	return nil
}

func validateTaskManifestHTTPSURL(field, raw string) error {
	if raw == "" {
		return nil
	}
	if len(raw) > 2048 {
		return fmt.Errorf("chain: %s exceeds 2048 characters", field)
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return fmt.Errorf("chain: %s must be an absolute HTTPS URL without userinfo", field)
	}
	return nil
}

func invalidTaskAmount(value float64) bool {
	return value < 0 || math.IsNaN(value) || math.IsInf(value, 0)
}

func normalizeTaskManifestTags(tags []string) []string {
	seen := map[string]struct{}{}
	for _, raw := range tags {
		tag := strings.ToLower(strings.TrimSpace(raw))
		if tag != "" {
			seen[tag] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for tag := range seen {
		out = append(out, tag)
	}
	sort.Strings(out)
	return out
}

func normalizeTaskRelayIDs(ids []string) []string {
	seen := map[string]struct{}{}
	for _, raw := range ids {
		id := strings.ToLower(strings.TrimSpace(raw))
		if id != "" {
			seen[id] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func cloneTaskManifest(manifest *TaskManifest) *TaskManifest {
	if manifest == nil {
		return nil
	}
	clone := *manifest
	clone.Tags = append([]string(nil), manifest.Tags...)
	clone.AuthorizedRelayIDs = append([]string(nil), manifest.AuthorizedRelayIDs...)
	return &clone
}

// TaskCatalogRoot returns a deterministic fingerprint of the effective task
// catalog only. Participant stake, submissions, rewards, and other live task
// state are deliberately excluded so clients can use this value to detect an
// actual catalog change instead of seeing a new fingerprint every round.
func TaskCatalogRoot(tasks []TaskState) string {
	catalogTasks := make([]TaskState, 0, len(tasks))
	for _, task := range tasks {
		if task.Manifest != nil {
			catalogTasks = append(catalogTasks, task)
		}
	}
	if len(catalogTasks) == 0 {
		return ""
	}
	sort.Slice(catalogTasks, func(i, j int) bool {
		return catalogTasks[i].TaskID < catalogTasks[j].TaskID
	})

	h := sha256.New()
	for _, task := range catalogTasks {
		manifestJSON, err := json.Marshal(task.Manifest)
		if err != nil {
			continue
		}
		fmt.Fprintf(h, "%s:%s:%t:%s:%s;",
			task.TaskID,
			manifestJSON,
			task.CatalogPaused,
			task.CatalogPublishedAt,
			task.CatalogUpdatedAt,
		)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// CatalogRoot fingerprints the manifests currently held by the store.
func (s *TaskStateStore) CatalogRoot() string {
	if s == nil {
		return ""
	}
	return TaskCatalogRoot(s.AllTasks())
}

// applyTaskCatalogActionLocked applies a catalog mutation while the caller
// holds TaskStateStore.mu. Registering is permissionless; subsequent changes
// are restricted to the immutable manager wallet recorded in version 1.
func (s *TaskStateStore) applyTaskCatalogActionLocked(action TaskAction) (*TaskState, error) {
	switch action.Action {
	case TaskActionCatalogRegister:
		manifest, err := decodeTaskManifest(action)
		if err != nil {
			return nil, err
		}
		if manifest.Version != 1 {
			return nil, ErrTaskManifestVersion
		}
		if existing := s.tasks[action.TaskID]; existing != nil && existing.Manifest != nil {
			return nil, ErrTaskManifestExists
		}
		task := s.getOrCreateTaskLocked(action.TaskID)
		task.Manifest = cloneTaskManifest(&manifest)
		task.CatalogPaused = false
		task.CatalogPublishedAt = action.Timestamp
		task.CatalogUpdatedAt = action.Timestamp
		return task, nil

	case TaskActionCatalogUpdate:
		task := s.tasks[action.TaskID]
		if task == nil || task.Manifest == nil {
			return nil, ErrTaskManifestNotFound
		}
		if task.Manifest.Manager != strings.ToLower(strings.TrimSpace(action.Sender)) {
			return nil, ErrTaskManifestManagerMismatch
		}
		manifest, err := decodeTaskManifest(action)
		if err != nil {
			return nil, err
		}
		if manifest.Manager != task.Manifest.Manager {
			return nil, ErrTaskManifestManagerMismatch
		}
		if manifest.Version != task.Manifest.Version+1 {
			return nil, ErrTaskManifestVersion
		}
		task.Manifest = cloneTaskManifest(&manifest)
		task.CatalogUpdatedAt = action.Timestamp
		return task, nil

	case TaskActionCatalogPause, TaskActionCatalogResume:
		task := s.tasks[action.TaskID]
		if task == nil || task.Manifest == nil {
			return nil, ErrTaskManifestNotFound
		}
		if task.Manifest.Manager != strings.ToLower(strings.TrimSpace(action.Sender)) {
			return nil, ErrTaskManifestManagerMismatch
		}
		task.CatalogPaused = action.Action == TaskActionCatalogPause
		task.CatalogUpdatedAt = action.Timestamp
		return task, nil
	default:
		return nil, fmt.Errorf("chain: unsupported task catalog action %q", action.Action)
	}
}
