/**
 * @jest-environment node
 */

import axios from 'axios';

const getTaskDataFromCache = jest.fn();

jest.mock('axios', () => ({
  get: jest.fn(),
}));

jest.mock('config/qsdm', () => ({
  QSDM_TASK_RUNTIME_MODE: 'qsdm-native',
  buildQsdmTaskReadUrls: (path: string) => [
    `http://127.0.0.1:8080/api/v1/${path.replace(/^\/+/, '')}`,
  ],
}));

jest.mock('main/services/tasks-cache-utils', () => ({
  getTaskDataFromCache,
}));

const mockedAxiosGet = axios.get as jest.Mock;

describe('getTaskSubmissions qsdm-native', () => {
  beforeEach(() => {
    mockedAxiosGet.mockReset();
    getTaskDataFromCache.mockReset();
  });

  it('reads submissions from QSDM Core', async () => {
    const submissions = {
      '12': {
        senderA: {
          submission_value: 'proof-cid',
          slot: 91,
          reward_amount: 1.5,
        },
      },
    };
    mockedAxiosGet.mockResolvedValue({
      data: {
        task: {
          submissions,
        },
      },
    });

    const { getTaskSubmissions } = await import('./getTaskSubmissions');
    const result = await getTaskSubmissions({} as Event, {
      taskPubKey: 'task-1',
    });

    expect(mockedAxiosGet).toHaveBeenCalledWith(
      'http://127.0.0.1:8080/api/v1/tasks/task-1',
      { timeout: 4000 }
    );
    expect(getTaskDataFromCache).not.toHaveBeenCalled();
    expect(result).toBe(submissions);
  });

  it('falls back to cached submissions when QSDM Core is unavailable', async () => {
    const cachedSubmissions = {
      '8': {
        senderA: {
          submission_value: 'cached-proof',
          slot: 44,
        },
      },
    };
    mockedAxiosGet.mockRejectedValue(new Error('offline'));
    getTaskDataFromCache.mockResolvedValue({
      submissions: cachedSubmissions,
    });

    const { getTaskSubmissions } = await import('./getTaskSubmissions');
    const result = await getTaskSubmissions({} as Event, {
      taskPubKey: 'task-1',
    });

    expect(getTaskDataFromCache).toHaveBeenCalledWith('task-1', 'submissions');
    expect(result).toBe(cachedSubmissions);
  });
});
