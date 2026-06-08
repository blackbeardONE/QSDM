/**
 * @jest-environment node
 */

export {};

import axios from 'axios';

const submitQsdmTaskActionIntent = jest.fn();
const getTaskDataFromCache = jest.fn();
const saveStakeRecordToCache = jest.fn();
const updateStartedTasksData = jest.fn();

jest.mock('config/qsdm', () => ({
  QSDM_TASK_RUNTIME_MODE: 'qsdm-native',
  QSDM_CELL_DECIMALS: 9,
  buildQsdmCoreApiUrl: (path: string) =>
    `http://127.0.0.1:8080/api/v1/${path.replace(/^\/+/, '')}`,
  buildQsdmApiUrl: (path: string) =>
    `http://localhost:8080/api/v1/${path.replace(/^\/+/, '')}`,
}));

jest.mock('main/services/qsdmTaskActionSigner', () => ({
  getQsdmTaskActionSender: () =>
    'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
}));

jest.mock('axios', () => ({
  get: jest.fn(),
}));

jest.mock('main/services/qsdmTaskActions', () => ({
  submitQsdmTaskActionIntent,
}));

jest.mock('main/services/tasks-cache-utils', () => ({
  getTaskDataFromCache,
  saveStakeRecordToCache,
}));

jest.mock('main/services/qsdmHiveTasks', () => ({
  __esModule: true,
  default: {
    updateStartedTasksData,
  },
}));

jest.mock('main/util', () => ({
  sleep: jest.fn().mockResolvedValue(undefined),
}));

const mockedAxiosGet = axios.get as jest.Mock;

describe('delegateStake qsdm-native', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    getTaskDataFromCache.mockResolvedValue({});
    mockedAxiosGet.mockReset();
    mockedAxiosGet
      .mockResolvedValueOnce({
        data: {
          task: {
            participants: {
              aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa: {
                stake: 0,
              },
            },
          },
        },
      })
      .mockResolvedValue({
        data: {
          task: {
            participants: {
              aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa: {
                stake: 2.5,
              },
            },
          },
        },
      });
    submitQsdmTaskActionIntent.mockResolvedValue({
      action_id: 'hive_stake_1',
      status: 'accepted',
    });
  });

  it('submits a signed native stake action and updates stake cache', async () => {
    const { default: delegateStake } = await import('./delegateStake');

    const result = await delegateStake({} as Event, {
      taskAccountPubKey: 'task-1',
      stakePotAccount: 'unused-native-pot',
      stakeAmount: 2.5,
      isNetworkingTask: true,
      useStakingWallet: false,
    });

    expect(submitQsdmTaskActionIntent).toHaveBeenCalledWith({
      taskId: 'task-1',
      action: 'stake',
      amount: 2.5,
      payload: {
        source: 'qsdm-hive',
        isNetworkingTask: true,
        useStakingWallet: false,
      },
    });
    expect(saveStakeRecordToCache).toHaveBeenCalledWith(
      'task-1',
      'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
      2500000000
    );
    expect(updateStartedTasksData).toHaveBeenCalled();
    expect(result).toBe('hive_stake_1');
  });

  it('converts renderer minimum-unit stake amounts into CELL for QSDM Core', async () => {
    const { default: delegateStake } = await import('./delegateStake');

    await delegateStake({} as Event, {
      taskAccountPubKey: 'task-1',
      stakePotAccount: 'unused-native-pot',
      stakeAmount: 2_500_000_000,
      isNetworkingTask: false,
      useStakingWallet: false,
    });

    expect(submitQsdmTaskActionIntent).toHaveBeenCalledWith(
      expect.objectContaining({
        taskId: 'task-1',
        action: 'stake',
        amount: 2.5,
      })
    );
    expect(saveStakeRecordToCache).toHaveBeenCalledWith(
      'task-1',
      'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
      2500000000
    );
  });

  it('skips staking when a native cached stake already exists', async () => {
    const sender =
      'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa';
    mockedAxiosGet.mockReset();
    mockedAxiosGet.mockResolvedValue({
      data: {
        task: {
          participants: {
            [sender]: {
              stake: 4,
            },
          },
        },
      },
    });
    getTaskDataFromCache.mockResolvedValue({
      stake_list: {
        [sender]: 4,
      },
    });
    const { default: delegateStake } = await import('./delegateStake');

    const result = await delegateStake({} as Event, {
      taskAccountPubKey: 'task-1',
      stakePotAccount: 'unused-native-pot',
      stakeAmount: 2.5,
      skipIfItIsAlreadyStaked: true,
    });

    expect(submitQsdmTaskActionIntent).not.toHaveBeenCalled();
    expect(result).toBe('4000000000');
  });

  it('skips staking from cached native stake when live stake reads fail', async () => {
    const sender =
      'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa';
    mockedAxiosGet.mockReset();
    mockedAxiosGet.mockRejectedValue(new Error('offline'));
    getTaskDataFromCache.mockResolvedValue({
      stake_list: {
        [sender]: 4000000000,
      },
    });
    const { default: delegateStake } = await import('./delegateStake');

    const result = await delegateStake({} as Event, {
      taskAccountPubKey: 'task-1',
      stakePotAccount: 'unused-native-pot',
      stakeAmount: 2.5,
      skipIfItIsAlreadyStaked: true,
    });

    expect(mockedAxiosGet).toHaveBeenCalledTimes(4);
    expect(submitQsdmTaskActionIntent).not.toHaveBeenCalled();
    expect(saveStakeRecordToCache).toHaveBeenCalledWith(
      'task-1',
      sender,
      4000000000
    );
    expect(result).toBe('4000000000');
  });
});
