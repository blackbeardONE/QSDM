/**
 * @jest-environment node
 */

import axios from 'axios';

import { clearQsdmReadCircuitState } from 'main/services/qsdmHttpRead';

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
    clearQsdmReadCircuitState();
  });

  it('returns zero when native status is unavailable before a tip is known', async () => {
    mockedAxiosGet.mockRejectedValue(new Error('offline'));

    const { default: getCurrentSlot } = await import('./getCurrentSlot');
    const result = await getCurrentSlot();

    expect(result).toBe(0);
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
      { timeout: 4000 }
    );
    expect(getCurrentSlotTaskNode).not.toHaveBeenCalled();
    expect(result).toBe(1234);
  });

  it('retains the last confirmed chain tip during a transient outage', async () => {
    mockedAxiosGet
      .mockResolvedValueOnce({ data: { chain_tip: 5678 } })
      .mockRejectedValueOnce(new Error('timeout'));

    const { default: getCurrentSlot } = await import('./getCurrentSlot');

    expect(await getCurrentSlot()).toBe(5678);
    expect(await getCurrentSlot()).toBe(5678);
  });
});
