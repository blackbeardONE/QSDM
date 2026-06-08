/**
 * @jest-environment node
 */

import axios from 'axios';

const getCurrentSlotTaskNode = jest.fn();

jest.mock('axios', () => ({
  get: jest.fn(),
}));

jest.mock('config/qsdm', () => ({
  QSDM_TASK_RUNTIME_MODE: 'qsdm-native',
  buildQsdmCoreApiUrl: (path: string) =>
    `http://localhost:8080/api/v1/${path.replace(/^\/+/, '')}`,
}));

jest.mock('vendor/qsdm-chain/taskNode', () => ({
  getCurrentSlot: getCurrentSlotTaskNode,
}));

jest.mock('main/services/sdk', () => ({
  __esModule: true,
  default: {
    k2Connection: {},
  },
}));

const mockedAxiosGet = axios.get as jest.Mock;

describe('getCurrentSlot qsdm-native', () => {
  beforeEach(() => {
    mockedAxiosGet.mockReset();
    getCurrentSlotTaskNode.mockReset();
  });

  it('returns QSDM Core chain tip as the current slot', async () => {
    mockedAxiosGet.mockResolvedValue({
      data: {
        chain_tip: 1234,
      },
    });

    const { default: getCurrentSlot } = await import('./getCurrentSlot');
    const result = await getCurrentSlot();

    expect(mockedAxiosGet).toHaveBeenCalledWith(
      'http://localhost:8080/api/v1/status',
      { timeout: 10000 }
    );
    expect(getCurrentSlotTaskNode).not.toHaveBeenCalled();
    expect(result).toBe(1234);
  });

  it('returns zero when native status is unavailable', async () => {
    mockedAxiosGet.mockRejectedValue(new Error('offline'));

    const { default: getCurrentSlot } = await import('./getCurrentSlot');
    const result = await getCurrentSlot();

    expect(result).toBe(0);
  });
});
