import axios from 'axios';
import { Event } from 'electron';

import { getQsdmCellAccount } from './getQsdmCellAccount';

jest.mock('axios', () => ({
  get: jest.fn(),
}));

const mockedAxiosGet = axios.get as jest.Mock;

describe('getQsdmCellAccount', () => {
  beforeEach(() => {
    mockedAxiosGet.mockReset();
  });

  it('returns an unconfigured response when no QSDM address is available', async () => {
    const response = await getQsdmCellAccount({} as Event);

    expect(mockedAxiosGet).not.toHaveBeenCalled();
    expect(response).toMatchObject({
      configured: false,
      reachable: false,
      tokenSymbol: 'CELL',
      apiUrl: 'http://127.0.0.1:8080/api/v1',
    });
  });

  it('reads CELL balance, nonce, and mining-ledger state for a QSDM address', async () => {
    mockedAxiosGet
      .mockResolvedValueOnce({
        data: { address: 'abc123', balance: 42, source: 'storage' },
      })
      .mockResolvedValueOnce({
        data: { sender: 'abc123', nonce: 7, next: 8 },
      })
      .mockResolvedValueOnce({
        data: { address: 'abc123', balance: 45, nonce: 9, present: true },
      });

    const response = await getQsdmCellAccount({} as Event, {
      address: 'abc123',
    });

    expect(mockedAxiosGet).toHaveBeenNthCalledWith(
      1,
      'http://127.0.0.1:8080/api/v1/wallet/balance?address=abc123',
      { timeout: 2500 }
    );
    expect(mockedAxiosGet).toHaveBeenNthCalledWith(
      2,
      'http://127.0.0.1:8080/api/v1/wallet/nonce?sender=abc123',
      { timeout: 2500 }
    );
    expect(mockedAxiosGet).toHaveBeenNthCalledWith(
      3,
      'http://127.0.0.1:8080/api/v1/mining/account?address=abc123',
      { timeout: 2500 }
    );
    expect(response).toMatchObject({
      configured: true,
      reachable: true,
      address: 'abc123',
      balance: 42,
      balanceSource: 'storage',
      nonce: 7,
      nextNonce: 8,
      miningAccount: {
        address: 'abc123',
        balance: 45,
        nonce: 9,
        present: true,
      },
    });
  });
});
