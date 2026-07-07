/**
 * @jest-environment node
 */

import axios from 'axios';

const sender =
  'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa';
const getMyTaskSubmissionRoundInfoK2 = jest.fn();

jest.mock('axios', () => ({
  get: jest.fn(),
}));

jest.mock('config/qsdm', () => ({
  QSDM_TASK_RUNTIME_MODE: 'qsdm-native',
  buildQsdmTaskReadUrls: (path: string) => [
    `http://127.0.0.1:8080/api/v1/${path.replace(/^\/+/, '')}`,
  ],
}));

jest.mock('main/services/qsdmTaskActionSigner', () => ({
  getQsdmTaskActionSender: () =>
    'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
}));

jest.mock('vendor/qsdm-chain/taskNode', () => ({
  getMyTaskSubmissionRoundInfo: getMyTaskSubmissionRoundInfoK2,
}));

jest.mock('main/node/helpers', () => ({
  getStakingAccountKeypair: jest.fn(),
}));

jest.mock('main/services/sdk', () => ({
  __esModule: true,
  default: {
    k2Connection: {},
  },
}));

const mockedAxiosGet = axios.get as jest.Mock;

describe('getMyTaskSubmissionRoundInfo qsdm-native', () => {
  beforeEach(() => {
    mockedAxiosGet.mockReset();
    getMyTaskSubmissionRoundInfoK2.mockReset();
  });

  it('returns the sender submission for the requested native round', async () => {
    const submission = {
      submission_value: 'proof-cid',
      slot: 140,
      reward_amount: 2,
      claimed: false,
    };
    mockedAxiosGet.mockResolvedValue({
      data: {
        task: {
          submissions: {
            '5': {
              [sender]: submission,
            },
          },
        },
      },
    });

    const { getMyTaskSubmissionRoundInfo } = await import(
      './getMyTaskSubmissionRoundInfo'
    );
    const result = await getMyTaskSubmissionRoundInfo({} as Event, {
      taskAccountPubKey: 'task-1',
      round: 5,
    });

    expect(mockedAxiosGet).toHaveBeenCalledWith(
      'http://127.0.0.1:8080/api/v1/tasks/task-1',
      { timeout: 4000 }
    );
    expect(getMyTaskSubmissionRoundInfoK2).not.toHaveBeenCalled();
    expect(result).toBe(submission);
  });

  it('returns null when the native round has no sender submission', async () => {
    mockedAxiosGet.mockResolvedValue({
      data: {
        task: {
          submissions: {
            '5': {},
          },
        },
      },
    });

    const { getMyTaskSubmissionRoundInfo } = await import(
      './getMyTaskSubmissionRoundInfo'
    );
    const result = await getMyTaskSubmissionRoundInfo({} as Event, {
      taskAccountPubKey: 'task-1',
      round: 5,
    });

    expect(result).toBeNull();
  });
});
