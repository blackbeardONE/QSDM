/**
 * @jest-environment node
 */

import axios from 'axios';

const getAverageSlotTimeTaskNode = jest.fn();

jest.mock('axios', () => ({
  get: jest.fn(),
}));

jest.mock('config/qsdm', () => ({
  QSDM_TASK_RUNTIME_MODE: 'qsdm-native',
  buildQsdmCoreApiUrl: (path: string) =>
    `http://localhost:8080/api/v1/${path.replace(/^\/+/, '')}`,
}));

jest.mock('vendor/qsdm-chain/taskNode', () => ({
  getAverageSlotTime: getAverageSlotTimeTaskNode,
}));

jest.mock('main/services/sdk', () => ({
  __esModule: true,
  default: {
    k2Connection: {},
  },
}));

const mockedAxiosGet = axios.get as jest.Mock;

describe('getAverageSlotTime qsdm-native', () => {
  beforeEach(() => {
    mockedAxiosGet.mockReset();
    getAverageSlotTimeTaskNode.mockReset();
  });

  it('uses QSDM Core target block time in milliseconds', async () => {
    mockedAxiosGet.mockResolvedValue({
      data: {
        tokenomics: {
          target_block_time_seconds: 2,
        },
      },
    });

    const { default: getAverageSlotTime } = await import(
      './getAverageSlotTime'
    );
    const result = await getAverageSlotTime();

    expect(mockedAxiosGet).toHaveBeenCalledWith(
      'http://localhost:8080/api/v1/status',
      { timeout: 10000 }
    );
    expect(getAverageSlotTimeTaskNode).not.toHaveBeenCalled();
    expect(result).toBe(2000);
  });

  it('uses the existing fallback when native status has no timing data', async () => {
    mockedAxiosGet.mockResolvedValue({
      data: {},
    });

    const { default: getAverageSlotTime } = await import(
      './getAverageSlotTime'
    );
    const result = await getAverageSlotTime();

    expect(result).toBe(420);
  });
});
