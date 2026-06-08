import { Event } from 'electron';

import { PublicKey } from 'vendor/qsdm-chain/web3';
import sdk from 'main/services/sdk';

import getAccountBalance from './getAccountBalance';

jest.mock('main/services/sdk', () => ({
  k2Connection: {
    getBalance: jest.fn(),
  },
}));

jest.mock('config/qsdm', () => ({
  buildQsdmCoreApiUrl: (path: string) =>
    `http://127.0.0.1:8080/api/v1/${path.replace(/^\/+/, '')}`,
  QSDM_CELL_DECIMALS: 9,
  QSDM_TASK_RUNTIME_MODE: 'k2-compat',
  QSDM_WALLET_ADDRESS: '',
}));

describe('getAccountBalance', () => {
  afterEach(() => {
    jest.resetAllMocks();
  });

  it('should return zero when an error occurs', async () => {
    const pubkey = '7x8tP5ipyqPfrRSXoxgGz6EzfTe3S84J3WUvJwbTwgnY';

    (sdk.k2Connection.getBalance as jest.Mock).mockRejectedValueOnce(
      new Error('network unavailable')
    );

    const result = await getAccountBalance({} as Event, pubkey);

    expect(result).toBe(0);
    expect(sdk.k2Connection.getBalance).toHaveBeenCalledWith(
      new PublicKey(pubkey),
      'processed'
    );
  });

  it('should return the account balance', async () => {
    const pubkey = '7x8tP5ipyqPfrRSXoxgGz6EzfTe3S84J3WUvJwbTwgnY';
    const expectedBalance = 1000;

    (sdk.k2Connection.getBalance as jest.Mock).mockResolvedValueOnce(
      expectedBalance
    );

    const result = await getAccountBalance({} as Event, pubkey);

    expect(result).toBe(expectedBalance);
    expect(sdk.k2Connection.getBalance).toHaveBeenCalledWith(
      new PublicKey(pubkey),
      'processed'
    );
  });
});
