import { Event } from 'electron';

import axios from 'axios';

import { getQsdmTaskActionSender } from 'main/services/qsdmTaskActionSigner';

import { getQsdmCellAccount } from './getQsdmCellAccount';

jest.mock('axios', () => ({
  get: jest.fn(),
}));
jest.mock('main/services/qsdmTaskActionSigner', () => ({
  getQsdmTaskActionSender: jest.fn(),
}));
jest.mock('main/services/qsdmCanonicalChain', () => ({
  getQsdmCanonicalChainSafety: jest.fn().mockResolvedValue({
    safe: true,
    state: 'canonical',
  }),
}));

const mockedAxiosGet = axios.get as jest.Mock;
const mockedGetQsdmTaskActionSender = getQsdmTaskActionSender as jest.Mock;

describe('getQsdmCellAccount', () => {
  beforeEach(() => {
    // eslint-disable-next-line global-require
    require('main/services/qsdmHttpRead').clearQsdmReadCircuitState();
    mockedAxiosGet.mockReset();
    mockedGetQsdmTaskActionSender.mockReset();
    mockedGetQsdmTaskActionSender.mockReturnValue('');
  });

  it('queries the active signer wallet when no address is requested', async () => {
    mockedGetQsdmTaskActionSender.mockReturnValue('active-linux-signer');
    mockedAxiosGet
      .mockResolvedValueOnce({
        data: { address: 'active-linux-signer', balance: 25 },
      })
      .mockResolvedValueOnce({
        data: { sender: 'active-linux-signer', nonce: 3, next: 4 },
      })
      .mockResolvedValueOnce({
        data: { address: 'active-linux-signer', present: false },
      });

    const response = await getQsdmCellAccount({} as Event);

    expect(response.address).toBe('active-linux-signer');
    expect(response.balance).toBe(25);
    expect(mockedAxiosGet).toHaveBeenNthCalledWith(
      1,
      'http://127.0.0.1:8080/api/v1/wallet/balance?address=active-linux-signer',
      { timeout: 4000 }
    );
  });

  it('returns a not-configured response when no QSDM address is available', async () => {
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
      { timeout: 4000 }
    );
    expect(mockedAxiosGet).toHaveBeenNthCalledWith(
      2,
      'http://127.0.0.1:8080/api/v1/wallet/nonce?sender=abc123',
      { timeout: 4000 }
    );
    expect(mockedAxiosGet).toHaveBeenNthCalledWith(
      3,
      'http://127.0.0.1:8080/api/v1/mining/account?address=abc123',
      { timeout: 4000 }
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
