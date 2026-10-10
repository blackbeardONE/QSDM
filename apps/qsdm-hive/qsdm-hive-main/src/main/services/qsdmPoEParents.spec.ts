import axios from 'axios';

import { setQsdmRuntimeCoreApiUrl } from 'config/qsdm';

import {
  fetchQsdmPoEParents,
  isQsdmParentId,
  isQsdmPoEParentsRejection,
} from './qsdmPoEParents';

jest.mock('axios', () => ({
  get: jest.fn(),
  isAxiosError: jest.fn((error) => !!error?.isAxiosError),
}));

const mockedAxiosGet = axios.get as jest.Mock;

const notFound = { isAxiosError: true, response: { status: 404, data: '' } };

describe('qsdmPoEParents', () => {
  beforeEach(() => {
    mockedAxiosGet.mockReset();
    setQsdmRuntimeCoreApiUrl('http://127.0.0.1:8080/api/v1');
  });

  afterAll(() => {
    setQsdmRuntimeCoreApiUrl();
  });

  it('uses the parents the node suggests', async () => {
    mockedAxiosGet.mockResolvedValueOnce({
      data: {
        tip: 805400,
        parents: [
          'solo-heartbeat-805400-1791611907090994535',
          'solo-heartbeat-805399-1791611897090994535',
        ],
      },
    });
    await expect(fetchQsdmPoEParents()).resolves.toEqual([
      'solo-heartbeat-805400-1791611907090994535',
      'solo-heartbeat-805399-1791611897090994535',
    ]);
    expect(mockedAxiosGet).toHaveBeenCalledWith(
      'http://127.0.0.1:8080/api/v1/chain/parents',
      { timeout: 10000 }
    );
  });

  it('falls back to committed receipts on nodes without the parents route', async () => {
    mockedAxiosGet.mockRejectedValueOnce(notFound).mockResolvedValueOnce({
      data: {
        receipts: [
          { tx_id: 'failed-transfer-000000001', status: 0 },
          { tx_id: 'short', status: 1 },
          { tx_id: 'solo-heartbeat-9-1791611927090994535', status: 1 },
          { tx_id: 'solo-heartbeat-9-1791611927090994535', status: 1 },
          { tx_id: 'solo-reward-9-abcdef0123456789', status: 1 },
        ],
      },
    });
    await expect(fetchQsdmPoEParents()).resolves.toEqual([
      'solo-heartbeat-9-1791611927090994535',
      'solo-reward-9-abcdef0123456789',
    ]);
    expect(mockedAxiosGet.mock.calls[1][0]).toBe(
      'http://127.0.0.1:8080/api/v1/receipts?limit=32'
    );
  });

  it('returns no parents when the node cannot supply them', async () => {
    mockedAxiosGet.mockRejectedValueOnce({
      isAxiosError: true,
      response: { status: 503, data: 'warming up' },
    });
    await expect(fetchQsdmPoEParents()).resolves.toEqual([]);
    expect(mockedAxiosGet).toHaveBeenCalledTimes(1);

    mockedAxiosGet.mockResolvedValueOnce({
      data: { parents: ['parent1', 'parent2'] },
    });
    mockedAxiosGet.mockRejectedValueOnce(notFound);
    await expect(fetchQsdmPoEParents()).resolves.toEqual([]);
  });

  it('validates parent IDs like the node', () => {
    expect(isQsdmParentId('solo-heartbeat-1-1791611907090994535')).toBe(true);
    expect(isQsdmParentId('hive_wallet_1784876606130_1a4983ed8ce85b54')).toBe(
      true
    );
    expect(isQsdmParentId('parent1')).toBe(false);
    expect(isQsdmParentId('a'.repeat(129))).toBe(false);
    expect(isQsdmParentId('has space 0000000000')).toBe(false);
  });

  it('recognizes a proof-of-entanglement refusal', () => {
    expect(
      isQsdmPoEParentsRejection({
        isAxiosError: true,
        response: {
          status: 422,
          data: { error: 'proof-of-entanglement: parent is not committed' },
        },
      })
    ).toBe(true);
    expect(
      isQsdmPoEParentsRejection({
        isAxiosError: true,
        response: { status: 422, data: { error: 'signature does not verify' } },
      })
    ).toBe(false);
    expect(isQsdmPoEParentsRejection(new Error('network'))).toBe(false);
  });
});
