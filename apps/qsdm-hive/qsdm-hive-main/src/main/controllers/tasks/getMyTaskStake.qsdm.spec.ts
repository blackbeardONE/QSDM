/**
 * @jest-environment node
 */

import axios from 'axios';

const sender =
  'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa';
const getTaskDataFromCache = jest.fn();
const saveStakeRecordToCache = jest.fn();
const getMyTaskStakeInfo = jest.fn();

jest.mock('axios', () => ({
  get: jest.fn(),
}));

jest.mock('config/qsdm', () => ({
  QSDM_TASK_RUNTIME_MODE: 'qsdm-native',
  QSDM_CELL_DECIMALS: 9,
  buildQsdmCoreApiUrl: (path: string) =>
    `http://127.0.0.1:8080/api/v1/${path.replace(/^\/+/, '')}`,
  buildQsdmApiUrl: (path: string) =>
    `http://localhost:8080/api/v1/${path.replace(/^\/+/, '')}`,
  buildQsdmTaskReadUrls: (path: string) => [
    `http://127.0.0.1:8080/api/v1/${path.replace(/^\/+/, '')}`,
    `http://localhost:8080/api/v1/${path.replace(/^\/+/, '')}`,
  ],
}));

jest.mock('main/services/qsdmTaskActionSigner', () => ({
  getQsdmTaskActionSender: () =>
    'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
}));

jest.mock('vendor/qsdm-chain/taskNode', () => ({
  getMyTaskStakeInfo,
}));

jest.mock('main/node/helpers', () => ({
  getKplStakingAccountKeypair: jest.fn(),
  getStakingAccountKeypair: jest.fn(),
}));

jest.mock('main/services/sdk', () => ({
  __esModule: true,
  default: {
    k2Connection: {},
  },
}));

jest.mock('main/services/tasks-cache-utils', () => ({
  getTaskDataFromCache,
  saveStakeRecordToCache,
}));

const mockedAxiosGet = axios.get as jest.Mock;

describe('getMyTaskStake qsdm-native', () => {
  beforeEach(() => {
    // eslint-disable-next-line global-require
    require('main/services/qsdmHttpRead').clearQsdmReadCircuitState();
    mockedAxiosGet.mockReset();
    getTaskDataFromCache.mockReset();
    saveStakeRecordToCache.mockReset();
    getMyTaskStakeInfo.mockReset();
    getTaskDataFromCache.mockResolvedValue({});
  });

  it('reads live native stake from QSDM Core and caches it', async () => {
    mockedAxiosGet.mockResolvedValue({
      data: {
        task: {
          participants: {
            [sender]: {
              stake: 7.25,
            },
          },
        },
      },
    });

    const { getMyTaskStake } = await import('./getMyTaskStake');
    const result = await getMyTaskStake({} as Event, {
      taskAccountPubKey: 'task-1',
      taskType: 'CELL',
    });

    expect(mockedAxiosGet).toHaveBeenCalledWith(
      'http://127.0.0.1:8080/api/v1/tasks/task-1/state',
      { timeout: 4000 }
    );
    expect(saveStakeRecordToCache).toHaveBeenCalledWith(
      'task-1',
      sender,
      7250000000
    );
    expect(getMyTaskStakeInfo).not.toHaveBeenCalled();
    expect(result).toBe(7250000000);
  });

  it('ignores cached native stake and reads core as the source of truth', async () => {
    getTaskDataFromCache.mockResolvedValue({
      stake_list: {
        [sender]: 4,
      },
    });
    mockedAxiosGet.mockResolvedValue({
      data: {
        task: {
          participants: {
            [sender]: {
              stake: 2,
            },
          },
        },
      },
    });

    const { getMyTaskStake } = await import('./getMyTaskStake');
    const result = await getMyTaskStake({} as Event, {
      taskAccountPubKey: 'task-1',
      taskType: 'CELL',
    });

    expect(mockedAxiosGet).toHaveBeenCalledWith(
      'http://127.0.0.1:8080/api/v1/tasks/task-1/state',
      { timeout: 4000 }
    );
    expect(saveStakeRecordToCache).toHaveBeenCalledWith(
      'task-1',
      sender,
      2000000000
    );
    expect(result).toBe(2000000000);
  });

  it('falls back to the registry task projection when state is unavailable', async () => {
    mockedAxiosGet
      .mockRejectedValueOnce(
        Object.assign(new Error('local state unavailable'), {
          response: { status: 404 },
        })
      )
      .mockRejectedValueOnce(
        Object.assign(new Error('gateway state unavailable'), {
          response: { status: 404 },
        })
      )
      .mockResolvedValueOnce({
        data: {
          task: {
            stake_list: {
              [sender]: 1.5,
            },
          },
        },
      });

    const { getMyTaskStake } = await import('./getMyTaskStake');
    const result = await getMyTaskStake({} as Event, {
      taskAccountPubKey: 'task-1',
      taskType: 'CELL',
      revalidate: true,
    });

    expect(mockedAxiosGet).toHaveBeenNthCalledWith(
      1,
      'http://127.0.0.1:8080/api/v1/tasks/task-1/state',
      { timeout: 4000 }
    );
    expect(mockedAxiosGet).toHaveBeenNthCalledWith(
      2,
      'http://localhost:8080/api/v1/tasks/task-1/state',
      { timeout: 4000 }
    );
    expect(mockedAxiosGet).toHaveBeenNthCalledWith(
      3,
      'http://127.0.0.1:8080/api/v1/tasks/task-1',
      { timeout: 4000 }
    );
    expect(result).toBe(1500000000);
  });

  it('returns cached native stake when live stake endpoints are unavailable', async () => {
    getTaskDataFromCache.mockResolvedValue({
      stake_list: {
        [sender]: 3000000000,
      },
    });
    mockedAxiosGet.mockRejectedValue(new Error('offline'));

    const { getMyTaskStake } = await import('./getMyTaskStake');
    const result = await getMyTaskStake({} as Event, {
      taskAccountPubKey: 'task-1',
      taskType: 'CELL',
    });

    expect(mockedAxiosGet).toHaveBeenCalledTimes(4);
    expect(mockedAxiosGet).toHaveBeenNthCalledWith(
      1,
      'http://127.0.0.1:8080/api/v1/tasks/task-1/state',
      { timeout: 4000 }
    );
    expect(mockedAxiosGet).toHaveBeenNthCalledWith(
      2,
      'http://localhost:8080/api/v1/tasks/task-1/state',
      { timeout: 4000 }
    );
    expect(mockedAxiosGet).toHaveBeenNthCalledWith(
      3,
      'http://127.0.0.1:8080/api/v1/tasks/task-1',
      { timeout: 4000 }
    );
    expect(mockedAxiosGet).toHaveBeenNthCalledWith(
      4,
      'http://localhost:8080/api/v1/tasks/task-1',
      { timeout: 4000 }
    );
    expect(saveStakeRecordToCache).not.toHaveBeenCalled();
    expect(result).toBe(3000000000);
  });
});
