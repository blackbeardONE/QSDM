/**
 * @jest-environment node
 */

export {};

const submitQsdmTaskActionIntent = jest.fn();
const getTaskDataFromCache = jest.fn();
const saveStakeRecordToCache = jest.fn();
const getTaskState = jest.fn();
const updateStartedTasksData = jest.fn();
const getConfirmedQsdmTaskStakeInCell = jest.fn();

jest.mock('config/qsdm', () => ({
  QSDM_CELL_DECIMALS: 9,
  QSDM_TASK_RUNTIME_MODE: 'qsdm-native',
}));

jest.mock('main/services/qsdmTaskActionSigner', () => ({
  getQsdmTaskActionSender: () =>
    'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
}));

jest.mock('main/services/qsdmTaskActions', () => ({
  submitQsdmTaskActionIntent,
}));

jest.mock('main/services/qsdmTaskStake', () => ({
  getConfirmedQsdmTaskStakeInCell,
}));

jest.mock('main/services/tasks-cache-utils', () => ({
  getTaskDataFromCache,
  saveStakeRecordToCache,
  savePendingRewardsRecordToCache: jest.fn(),
}));

jest.mock('main/services/qsdmHiveTasks', () => ({
  __esModule: true,
  default: {
    getTaskState,
    updateStartedTasksData,
  },
}));

describe('withdrawStake qsdm-native', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    getConfirmedQsdmTaskStakeInCell.mockResolvedValue(3);
    getTaskDataFromCache.mockResolvedValue({
      stake_list: {
        aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa: 3,
      },
    });
    submitQsdmTaskActionIntent.mockResolvedValue({
      action_id: 'hive_withdraw_1',
      status: 'accepted',
    });
  });

  it('submits a signed native withdraw action for the confirmed core stake', async () => {
    const { default: withdrawStake } = await import('./withdrawStake');

    const result = await withdrawStake({} as Event, {
      taskAccountPubKey: 'task-1',
      taskType: 'CELL',
    });

    expect(getConfirmedQsdmTaskStakeInCell).toHaveBeenCalledWith(
      'task-1',
      'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'
    );
    expect(getTaskDataFromCache).not.toHaveBeenCalled();
    expect(submitQsdmTaskActionIntent).toHaveBeenCalledWith({
      taskId: 'task-1',
      action: 'withdraw',
      amount: 3,
      payload: {
        source: 'qsdm-hive',
        reason: 'manual-withdraw',
      },
    });
    expect(saveStakeRecordToCache).toHaveBeenCalledWith(
      'task-1',
      'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
      0
    );
    expect(updateStartedTasksData).toHaveBeenCalled();
    expect(result).toBe('hive_withdraw_1');
  });

  it('falls back to native task state when the stake cache is empty', async () => {
    const sender =
      'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa';
    getConfirmedQsdmTaskStakeInCell.mockRejectedValue(new Error('offline'));
    getTaskDataFromCache.mockResolvedValue({});
    getTaskState.mockResolvedValue({
      stake_list: {
        [sender]: 5,
      },
    });
    const { default: withdrawStake } = await import('./withdrawStake');

    await withdrawStake({} as Event, {
      taskAccountPubKey: 'task-1',
      taskType: 'CELL',
    });

    expect(getTaskState).toHaveBeenCalledWith('task-1');
    expect(submitQsdmTaskActionIntent).toHaveBeenCalledWith(
      expect.objectContaining({
        action: 'withdraw',
        amount: 5,
      })
    );
  });

  it('normalizes cached denomination stake when core and task state are unavailable', async () => {
    getConfirmedQsdmTaskStakeInCell.mockRejectedValue(new Error('offline'));
    getTaskState.mockRejectedValue(new Error('offline'));
    getTaskDataFromCache.mockResolvedValue({
      stake_list: {
        aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:
          7_000_000_000,
      },
    });
    const { default: withdrawStake } = await import('./withdrawStake');

    await withdrawStake({} as Event, {
      taskAccountPubKey: 'task-1',
      taskType: 'CELL',
    });

    expect(submitQsdmTaskActionIntent).toHaveBeenCalledWith(
      expect.objectContaining({
        action: 'withdraw',
        amount: 7,
      })
    );
  });
});
