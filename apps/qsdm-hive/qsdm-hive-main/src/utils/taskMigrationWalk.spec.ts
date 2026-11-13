import { TaskMigrationWalk } from './taskMigrationWalk';

type FakeTask = { manager: string; active: boolean; migratedTo?: string };

// The walk of QsdmHiveTaskService.findLatestTaskVersion, over a fake catalog.
async function latest(
  catalog: Record<string, FakeTask>,
  taskId: string,
  walk: TaskMigrationWalk = new TaskMigrationWalk()
): Promise<string | null> {
  if (!walk.enter(taskId)) return null;
  const task = catalog[taskId];
  if (!task) return null;
  if (!walk.sameManager(task.manager)) return null;
  if (task.migratedTo) return latest(catalog, task.migratedTo, walk);
  return task.active ? taskId : null;
}

describe('TaskMigrationWalk', () => {
  it('follows a chain of versions of one manager to the active one', async () => {
    const catalog = {
      v1: { manager: 'alice', active: false, migratedTo: 'v2' },
      v2: { manager: 'alice', active: false, migratedTo: 'v3' },
      v3: { manager: 'alice', active: true },
    };
    expect(await latest(catalog, 'v1')).toBe('v3');
  });

  it('stops on a cycle instead of looping', async () => {
    const catalog = {
      a: { manager: 'alice', active: true, migratedTo: 'b' },
      b: { manager: 'alice', active: true, migratedTo: 'a' },
    };
    expect(await latest(catalog, 'a')).toBeNull();
    expect(await latest(catalog, 'b', new TaskMigrationWalk('a', 'alice'))).toBeNull();
  });

  it("refuses another manager's task as an upgrade", async () => {
    const catalog = {
      mine: { manager: 'alice', active: false, migratedTo: 'theirs' },
      theirs: { manager: 'eve', active: true },
    };
    expect(await latest(catalog, 'mine')).toBeNull();
    expect(await latest(catalog, 'theirs', new TaskMigrationWalk('mine', 'alice'))).toBeNull();
  });

  it('returns null for an inactive last version or a missing task', async () => {
    expect(await latest({ v1: { manager: 'alice', active: false } }, 'v1')).toBeNull();
    expect(await latest({}, 'nowhere')).toBeNull();
  });

  it('treats an empty id as the end of the walk', () => {
    const walk = new TaskMigrationWalk();
    expect(walk.enter('')).toBe(false);
    expect(walk.enter(undefined)).toBe(false);
    expect(walk.enter('x')).toBe(true);
    expect(walk.enter('x')).toBe(false);
  });

  it('compares managers as strings', () => {
    const walk = new TaskMigrationWalk(undefined, 'alice');
    expect(walk.sameManager({ toString: () => 'alice' })).toBe(true);
    expect(walk.sameManager('eve')).toBe(false);
  });
});
