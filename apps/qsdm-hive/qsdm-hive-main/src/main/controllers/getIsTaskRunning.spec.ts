/**
 * @jest-environment node
 */

import qsdmHiveTasks from 'main/services/qsdmHiveTasks';

import { getIsTaskRunning } from './getIsTaskRunning';

jest.mock('main/services/qsdmHiveTasks', () => ({
  __esModule: true,
  default: {
    RUNNING_TASKS: {},
    getRunningTaskPubKeys: jest.fn(),
    reconcileQsdmSystemTaskRuntime: jest.fn(),
  },
}));

const taskId = 'qsdm-mother-hive';
const mockedTasks = qsdmHiveTasks as typeof qsdmHiveTasks & {
  getRunningTaskPubKeys: jest.Mock;
  reconcileQsdmSystemTaskRuntime: jest.Mock;
};

describe('getIsTaskRunning', () => {
  beforeEach(() => {
    mockedTasks.RUNNING_TASKS = {};
    mockedTasks.getRunningTaskPubKeys.mockReset();
    mockedTasks.reconcileQsdmSystemTaskRuntime.mockReset();
  });

  it('does not restore a task that was never marked as running', async () => {
    mockedTasks.getRunningTaskPubKeys.mockResolvedValue([]);

    await expect(
      getIsTaskRunning({} as never, { taskPublicKey: taskId })
    ).resolves.toBe(false);
    expect(mockedTasks.reconcileQsdmSystemTaskRuntime).not.toHaveBeenCalled();
  });

  it('restores a persisted task that is intended to be running', async () => {
    mockedTasks.getRunningTaskPubKeys.mockResolvedValue([taskId]);
    mockedTasks.reconcileQsdmSystemTaskRuntime.mockImplementation(async () => {
      mockedTasks.RUNNING_TASKS[taskId] = {} as never;
      return true;
    });

    await expect(
      getIsTaskRunning({} as never, { taskPublicKey: taskId })
    ).resolves.toBe(true);
    expect(mockedTasks.reconcileQsdmSystemTaskRuntime).toHaveBeenCalledWith(
      taskId,
      { submitStartAction: true }
    );
  });

  it('returns an active runtime without touching persistence or restore', async () => {
    mockedTasks.RUNNING_TASKS[taskId] = {} as never;

    await expect(
      getIsTaskRunning({} as never, { taskPublicKey: taskId })
    ).resolves.toBe(true);
    expect(mockedTasks.getRunningTaskPubKeys).not.toHaveBeenCalled();
    expect(mockedTasks.reconcileQsdmSystemTaskRuntime).not.toHaveBeenCalled();
  });
});
