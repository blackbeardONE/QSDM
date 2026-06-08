import axios from 'axios';

import { getQsdmCoreStatus } from './getQsdmCoreStatus';

jest.mock('axios', () => ({
  get: jest.fn(),
}));

const mockedAxiosGet = axios.get as jest.Mock;

describe('getQsdmCoreStatus', () => {
  beforeEach(() => {
    mockedAxiosGet.mockReset();
  });

  it('returns QSDM Core status when the local API responds', async () => {
    mockedAxiosGet
      .mockResolvedValueOnce({ data: { ok: true } })
      .mockResolvedValueOnce({ data: { node: 'qsdm' } });

    const response = await getQsdmCoreStatus();

    expect(mockedAxiosGet).toHaveBeenNthCalledWith(
      1,
      'http://127.0.0.1:8080/api/v1/health',
      { timeout: 10000 }
    );
    expect(mockedAxiosGet).toHaveBeenNthCalledWith(
      2,
      'http://127.0.0.1:8080/api/v1/status',
      { timeout: 10000 }
    );
    expect(response).toMatchObject({
      apiUrl: 'http://127.0.0.1:8080/api/v1',
      dashboardUrl: 'http://localhost:8081',
      tokenSymbol: 'CELL',
      protocolSymbol: 'CELL',
      runtimeMode: 'qsdm-native',
      healthy: true,
      health: { ok: true },
      status: { node: 'qsdm' },
    });
  });

  it('returns an offline response instead of throwing', async () => {
    mockedAxiosGet.mockRejectedValue(new Error('offline'));

    const response = await getQsdmCoreStatus();

    expect(response).toMatchObject({
      healthy: false,
      error: 'offline',
    });
  });
});
