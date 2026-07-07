import axios from 'axios';

import { clearQsdmReadCircuitState } from 'main/services/qsdmHttpRead';

import {
  clearQsdmCoreStatusSnapshot,
  getQsdmCoreStatus,
} from './getQsdmCoreStatus';

jest.mock('axios', () => ({
  get: jest.fn(),
}));

jest.mock('main/services/qsdmCanonicalChain', () => ({
  getQsdmCanonicalChainSafety: jest.fn().mockResolvedValue({
    safe: true,
    state: 'canonical',
    configuredApiUrl: 'http://127.0.0.1:8080/api/v1',
    effectiveApiUrl: 'http://127.0.0.1:8080/api/v1',
    canonicalApiUrl: 'https://api.qsdm.tech/api/v1',
    usingGatewayFallback: false,
    checkedAt: '2026-07-03T00:00:00Z',
  }),
}));

const mockedAxiosGet = axios.get as jest.Mock;

describe('getQsdmCoreStatus', () => {
  beforeEach(() => {
    mockedAxiosGet.mockReset();
    clearQsdmReadCircuitState();
    clearQsdmCoreStatusSnapshot();
  });

  it('returns QSDM Core status when the local API responds', async () => {
    mockedAxiosGet
      .mockResolvedValueOnce({ data: { ok: true } })
      .mockResolvedValueOnce({ data: { node: 'qsdm' } });

    const response = await getQsdmCoreStatus();

    expect(mockedAxiosGet).toHaveBeenNthCalledWith(
      1,
      'http://127.0.0.1:8080/api/v1/status',
      { timeout: 8000 }
    );
    expect(response).toMatchObject({
      apiUrl: 'http://127.0.0.1:8080/api/v1',
      coreConnectionMode: 'local',
      dashboardUrl: 'http://localhost:8081',
      tokenSymbol: 'CELL',
      protocolSymbol: 'CELL',
      runtimeMode: 'qsdm-native',
      healthy: true,
      connectionState: 'online',
      status: { ok: true },
    });
  });

  it('returns an offline response instead of throwing', async () => {
    mockedAxiosGet.mockRejectedValue(new Error('offline'));

    const response = await getQsdmCoreStatus();

    expect(response).toMatchObject({
      healthy: false,
      connectionState: 'offline',
      error: 'offline',
    });
  });

  it('retains the last confirmed status while a transient read reconnects', async () => {
    mockedAxiosGet.mockResolvedValueOnce({
      data: { node: 'qsdm', chain_tip: 101 },
    });
    const online = await getQsdmCoreStatus();

    mockedAxiosGet.mockRejectedValue(new Error('temporary timeout'));
    const reconnecting = await getQsdmCoreStatus();

    expect(online).toMatchObject({
      healthy: true,
      connectionState: 'online',
      status: { chain_tip: 101 },
    });
    expect(reconnecting).toMatchObject({
      healthy: false,
      connectionState: 'degraded',
      consecutiveFailures: 1,
      status: { chain_tip: 101 },
    });
    expect(reconnecting.lastSuccessfulAt).toBeTruthy();
  });
});
