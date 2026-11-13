import { getLatestTaskById, getLatestTasksById } from './k2';

type FakeTask = {
  taskManager: string;
  isActive: boolean;
  isMigrated: boolean;
  migratedTo: string;
};

const version = (taskManager: string, migratedTo = ''): FakeTask => ({
  taskManager,
  isActive: !migratedTo,
  isMigrated: !!migratedTo,
  migratedTo,
});

function useCatalog(catalog: Record<string, FakeTask>) {
  const getTaskInfo = jest.fn(
    async ({ taskAccountPubKey }: { taskAccountPubKey: string }) =>
      catalog[taskAccountPubKey] ?? null
  );
  window.main = {
    ...window.main,
    getTaskInfo,
  } as unknown as typeof window.main;
  return getTaskInfo;
}

describe('getLatestTaskById', () => {
  it("follows a manager's own versions to the newest one", async () => {
    useCatalog({
      v1: version('alice', 'v2'),
      v2: version('alice', 'v3'),
      v3: version('alice'),
    });
    expect((await getLatestTaskById('v1'))?.publicKey).toBe('v3');
  });

  it('stops on a migration cycle instead of looping', async () => {
    const getTaskInfo = useCatalog({
      a: version('alice', 'b'),
      b: version('alice', 'a'),
    });
    expect(await getLatestTaskById('a')).toBeNull();
    expect(getTaskInfo).toHaveBeenCalledTimes(2);
  });

  it("does not offer another manager's task as the new version", async () => {
    useCatalog({
      mine: version('alice', 'theirs'),
      theirs: version('eve'),
    });
    expect(await getLatestTaskById('mine')).toBeNull();
  });

  it('starts a separate walk for each id of a list', async () => {
    useCatalog({
      a1: version('alice', 'a2'),
      a2: version('alice'),
      e1: version('eve'),
    });
    const tasks = await getLatestTasksById(['a1', 'e1']);
    expect(tasks.map((task) => task.publicKey)).toEqual(['a2', 'e1']);
  });
});
