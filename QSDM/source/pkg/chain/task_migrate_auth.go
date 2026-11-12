package chain

import (
	"errors"
	"strings"
)

// TaskMigrateAuthActivationHeight is the first block at which the `migrate`
// task action is authorized like a catalog mutation (CheckTaskMigrate).
// Before it, `migrate` set the task-wide migration pointer for ANY signed
// sender: any funded wallet could point someone else's registered task, a
// built-in task included, at a task of its choosing, and QSDM Hive
// follows that pointer when it looks for a task's newer version. Blocks below
// this height replay as they were produced. The producer refuses an
// unauthorized `migrate` at its API from the release that carries this rule,
// so no such action reaches a block before the height.
const TaskMigrateAuthActivationHeight uint64 = 1_150_000

// taskMigrateMaxChain bounds the walk along existing migration pointers when a
// new pointer is checked for a cycle.
const taskMigrateMaxChain = 64

var (
	// ErrTaskMigrateUnregistered: only a task registered in the catalog (it has
	// a manifest, and so a manager) can be migrated.
	ErrTaskMigrateUnregistered = errors.New("chain: migrate needs a task registered in the catalog")
	// ErrTaskMigrateTarget: the target must be another registered, unpaused
	// catalog task with the same manager.
	ErrTaskMigrateTarget = errors.New("chain: migrate target must be another registered, unpaused catalog task of the same manager")
	// ErrTaskMigrateCycle: the new pointer would lead back to the task.
	ErrTaskMigrateCycle = errors.New("chain: migrate would create a migration cycle")
)

// CheckTaskMigrate is the authorization rule for the `migrate` task action:
//   - the task is registered in the catalog (it has a manifest);
//   - the signed sender is the manifest's manager (as for catalog-update,
//     catalog-pause and catalog-resume), which also protects the built-in
//     tasks, whose manager is the project's task manager wallet;
//   - the target (payload migrated_to) is another registered, unpaused catalog
//     task with the same manager;
//   - following the target's existing migration pointers never leads back to
//     the task (no cycle).
//
// get reads a task without side effects: the producer's API passes its live
// task-state reader, and the consensus path passes the store's own map.
func CheckTaskMigrate(get func(taskID string) (TaskState, bool), action TaskAction) error {
	if get == nil {
		return ErrTaskMigrateUnregistered
	}
	taskID := strings.TrimSpace(action.TaskID)
	task, ok := get(taskID)
	if !ok || task.Manifest == nil {
		return ErrTaskMigrateUnregistered
	}
	manager := task.Manifest.Manager
	if manager == "" || manager != strings.ToLower(strings.TrimSpace(action.Sender)) {
		return ErrTaskManifestManagerMismatch
	}
	target := strings.TrimSpace(parseTaskActionPayload(action.Payload).MigratedTo)
	if target == "" || target == taskID {
		return ErrTaskMigrateTarget
	}
	next, ok := get(target)
	if !ok || next.Manifest == nil || next.CatalogPaused || next.Manifest.Manager != manager {
		return ErrTaskMigrateTarget
	}
	seen := map[string]struct{}{taskID: {}, target: {}}
	for i := 0; i < taskMigrateMaxChain && next.IsMigrated && next.MigratedTo != ""; i++ {
		if _, loop := seen[next.MigratedTo]; loop {
			return ErrTaskMigrateCycle
		}
		seen[next.MigratedTo] = struct{}{}
		var found bool
		if next, found = get(next.MigratedTo); !found {
			return nil
		}
	}
	if next.IsMigrated && next.MigratedTo != "" {
		return ErrTaskMigrateCycle // a chain longer than the bound is refused as well
	}
	return nil
}

// peekTaskLocked reads a task for CheckTaskMigrate inside the store's lock,
// without creating it. The caller holds s.mu.
func (s *TaskStateStore) peekTaskLocked(taskID string) (TaskState, bool) {
	task := s.tasks[taskID]
	if task == nil {
		return TaskState{}, false
	}
	return *task, true
}
