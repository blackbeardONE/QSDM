package chain

import (
	"errors"
	"testing"
)

// migrateAt applies a `migrate` at a block height through the consensus path.
func migrateAt(s *TaskStateStore, id, taskID, sender, target string, height uint64) error {
	_, err := s.applyAction(TaskAction{
		ID: id, Action: "migrate", TaskID: taskID, Sender: sender,
		Payload: `{"migrated_to":"` + target + `"}`, Timestamp: "2026-11-13T00:00:00Z",
	}, false, true, height)
	return err
}

func migrateStore(t *testing.T) *TaskStateStore {
	t.Helper()
	s := NewTaskStateStore()
	for i, reg := range []struct{ id, task, manager string }{
		{"reg-1", "shared-edge", "alice"}, {"reg-2", "shared-edge-v2", "alice"},
		{"reg-3", "shared-edge-v3", "alice"}, {"reg-4", "eve-task", "eve"},
	} {
		if err := s.ApplyAction(testCatalogAction(t, reg.id, TaskActionCatalogRegister, reg.task, reg.manager, uint64(i+1), 1)); err != nil {
			t.Fatalf("register %s: %v", reg.task, err)
		}
	}
	return s
}

func TestTaskMigrate_BeforeActivationReplaysAsProduced(t *testing.T) {
	s := migrateStore(t)
	// The old rule: any signed sender moved the task-wide pointer. Blocks below
	// the activation height must replay exactly that way.
	if err := migrateAt(s, "m-old", "shared-edge", "eve", "eve-task", TaskMigrateAuthActivationHeight-1); err != nil {
		t.Fatalf("historical foreign migrate: %v", err)
	}
	task, _ := s.GetTask("shared-edge")
	if !task.IsMigrated || task.MigratedTo != "eve-task" {
		t.Fatalf("historical replay changed: %+v", task)
	}
}

func TestTaskMigrate_ForeignSenderRefusedFromActivation(t *testing.T) {
	s := migrateStore(t)
	root := s.StateRoot()
	for _, sender := range []string{"eve", "mallory"} {
		err := migrateAt(s, "m-"+sender, "shared-edge", sender, "eve-task", TaskMigrateAuthActivationHeight)
		if !errors.Is(err, ErrTaskManifestManagerMismatch) {
			t.Fatalf("%s: want ErrTaskManifestManagerMismatch, got %v", sender, err)
		}
	}
	if s.StateRoot() != root {
		t.Fatal("a refused migrate changed the task state root")
	}
	if task, _ := s.GetTask("shared-edge"); task.IsMigrated || task.MigratedTo != "" {
		t.Fatalf("refused migrate moved the pointer: %+v", task)
	}
	// The refused action id is not burned: the manager can still use an id.
	if err := migrateAt(s, "m-eve", "shared-edge", "alice", "shared-edge-v2", TaskMigrateAuthActivationHeight); err != nil {
		t.Fatalf("manager migrate after refusals: %v", err)
	}
}

func TestTaskMigrate_ManagerToOwnTask(t *testing.T) {
	s := migrateStore(t)
	if err := migrateAt(s, "m-1", "shared-edge", "ALICE ", "shared-edge-v2", TaskMigrateAuthActivationHeight+5); err != nil {
		t.Fatalf("manager migrate: %v", err)
	}
	task, _ := s.GetTask("shared-edge")
	if !task.IsMigrated || task.MigratedTo != "shared-edge-v2" || task.Manifest.Manager != "alice" {
		t.Fatalf("unexpected task after migrate: %+v", task)
	}
}

func TestTaskMigrate_TargetRules(t *testing.T) {
	s := migrateStore(t)
	h := TaskMigrateAuthActivationHeight
	cases := []struct{ name, target string }{
		{"empty target", ""}, {"itself", "shared-edge"}, {"unregistered target", "nowhere"},
		{"another manager's task", "eve-task"},
	}
	for i, c := range cases {
		if err := migrateAt(s, "t-"+string(rune('a'+i)), "shared-edge", "alice", c.target, h); !errors.Is(err, ErrTaskMigrateTarget) {
			t.Fatalf("%s: want ErrTaskMigrateTarget, got %v", c.name, err)
		}
	}
	if err := s.ApplyAction(TaskAction{ID: "pause-v2", Action: TaskActionCatalogPause, TaskID: "shared-edge-v2", Sender: "alice", Nonce: 9}); err != nil {
		t.Fatalf("pause v2: %v", err)
	}
	if err := migrateAt(s, "t-paused", "shared-edge", "alice", "shared-edge-v2", h); !errors.Is(err, ErrTaskMigrateTarget) {
		t.Fatalf("paused target: want ErrTaskMigrateTarget, got %v", err)
	}
}

func TestTaskMigrate_UnregisteredTaskRefusedAndNotCreated(t *testing.T) {
	s := migrateStore(t)
	before := s.Count()
	if err := migrateAt(s, "g-1", "ghost", "eve", "eve-task", TaskMigrateAuthActivationHeight); !errors.Is(err, ErrTaskMigrateUnregistered) {
		t.Fatalf("want ErrTaskMigrateUnregistered, got %v", err)
	}
	if s.Count() != before {
		t.Fatalf("a refused migrate created a task: %d -> %d", before, s.Count())
	}
}

func TestTaskMigrate_NoCycle(t *testing.T) {
	s := migrateStore(t)
	h := TaskMigrateAuthActivationHeight
	if err := migrateAt(s, "c-1", "shared-edge", "alice", "shared-edge-v2", h); err != nil {
		t.Fatalf("v1 -> v2: %v", err)
	}
	if err := migrateAt(s, "c-2", "shared-edge-v2", "alice", "shared-edge-v3", h); err != nil {
		t.Fatalf("v2 -> v3: %v", err)
	}
	if err := migrateAt(s, "c-3", "shared-edge-v3", "alice", "shared-edge", h); !errors.Is(err, ErrTaskMigrateCycle) {
		t.Fatalf("v3 -> v1 (cycle): want ErrTaskMigrateCycle, got %v", err)
	}
}

// builtInTaskManager is the manager of the network's built-in tasks (the
// project's catalog key) on the live chain.
const builtInTaskManager = "13d786706accfbe77c5ddf6fc6757e1cca07bd01aff0cad3dcf9411d92cf11c9"

func TestTaskMigrate_BuiltInTask(t *testing.T) {
	s := NewTaskStateStore()
	if err := s.ApplyAction(testCatalogAction(t, "r-1", TaskActionCatalogRegister, "qsdm-edge-worker", builtInTaskManager, 1, 1)); err != nil {
		t.Fatalf("register qsdm-edge-worker: %v", err)
	}
	if err := migrateAt(s, "r-eve", "qsdm-edge-worker", "eve", "attacker-task", TaskMigrateAuthActivationHeight); !errors.Is(err, ErrTaskManifestManagerMismatch) {
		t.Fatalf("foreign migrate of a built-in task: want ErrTaskManifestManagerMismatch, got %v", err)
	}
	if task, _ := s.GetTask("qsdm-edge-worker"); task.IsMigrated || task.MigratedTo != "" {
		t.Fatalf("refused migrate moved the built-in task: %+v", task)
	}
}

func TestCheckTaskMigrate_NilGetter(t *testing.T) {
	if err := CheckTaskMigrate(nil, TaskAction{TaskID: "x", Sender: "alice"}); !errors.Is(err, ErrTaskMigrateUnregistered) {
		t.Fatalf("nil getter: want ErrTaskMigrateUnregistered, got %v", err)
	}
}
