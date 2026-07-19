/**
 * @jest-environment node
 */

import { dialog } from 'electron';
import fs from 'fs';
import os from 'os';
import path from 'path';

import {
  getQsdmWalletProviderPermissions,
  handleWalletProviderRequest,
  QSDM_WALLET_PROVIDER_VERSION,
  revokeQsdmWalletProviderPermission,
} from './qsdmWalletProviderBroker';

const mockGetAppDataPath = jest.fn();
const mockGetQsdmTaskActionSignerStatus = jest.fn();
const mockGetQsdmTaskActionSender = jest.fn();
const mockSignQsdmMessageWithCli = jest.fn();
const mockSubmitQsdmWalletTransferIntent = jest.fn();

jest.mock('main/node/helpers/getAppDataPath', () => ({
  getAppDataPath: () => mockGetAppDataPath(),
}));

jest.mock('main/services/qsdmTaskActionSigner', () => ({
  getQsdmTaskActionSignerStatus: () => mockGetQsdmTaskActionSignerStatus(),
  getQsdmTaskActionSender: () => mockGetQsdmTaskActionSender(),
}));

jest.mock('main/services/qsdmTaskActions', () => ({
  signQsdmMessageWithCli: (message: string) =>
    mockSignQsdmMessageWithCli(message),
}));

jest.mock('main/services/qsdmWalletTransfer', () => ({
  submitQsdmWalletTransferIntent: (payload: unknown) =>
    mockSubmitQsdmWalletTransferIntent(payload),
}));

jest.mock('main/controllers/qsdm/getQsdmCellAccount', () => ({
  getQsdmCellAccount: jest.fn(),
}));

const sender =
  'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa';
const options = {
  openWallet: jest.fn(),
  showHive: jest.fn(),
};

const request = (method: string, params?: unknown) => ({
  version: QSDM_WALLET_PROVIDER_VERSION,
  id: 'request-1',
  origin: 'https://example.com',
  method,
  params,
});

describe('qsdmWalletProviderBroker', () => {
  let appDataPath = '';

  beforeEach(() => {
    appDataPath = fs.mkdtempSync(
      path.join(os.tmpdir(), 'qsdm-wallet-provider-')
    );
    mockGetAppDataPath.mockReturnValue(appDataPath);
    mockGetQsdmTaskActionSignerStatus.mockReturnValue({
      ready: true,
      sender,
    });
    mockGetQsdmTaskActionSender.mockReturnValue(sender);
    mockSignQsdmMessageWithCli.mockResolvedValue({
      address: sender,
      public_key: 'public-key',
      signature: 'signature',
    });
    mockSubmitQsdmWalletTransferIntent.mockResolvedValue({
      transaction_id: 'transaction-id',
      status: 'accepted',
    });
    (dialog.showMessageBox as jest.Mock).mockReset();
    (dialog.showMessageBox as jest.Mock).mockResolvedValue({ response: 0 });
    options.openWallet.mockReset();
    options.showHive.mockReset();
  });

  afterEach(() => {
    fs.rmSync(appDataPath, { recursive: true, force: true });
  });

  it('rejects obsolete protocol versions and insecure remote origins', async () => {
    await expect(
      handleWalletProviderRequest(
        { ...request('qsdm_ping'), version: 'obsolete' },
        options
      )
    ).rejects.toThrow('Unsupported QSDM wallet provider version');

    await expect(
      handleWalletProviderRequest(
        { ...request('qsdm_ping'), origin: 'http://example.com' },
        options
      )
    ).rejects.toThrow('require HTTPS');
  });

  it('persists an exact-origin grant without exposing wallet secrets', async () => {
    await expect(
      handleWalletProviderRequest(request('qsdm_requestAccounts'), options)
    ).resolves.toEqual([sender]);

    expect(options.showHive).toHaveBeenCalledTimes(1);
    expect(dialog.showMessageBox).toHaveBeenCalledTimes(1);
    expect(getQsdmWalletProviderPermissions().permissions).toEqual([
      expect.objectContaining({
        origin: 'https://example.com',
        address: sender,
      }),
    ]);

    const stored = fs.readFileSync(
      path.join(appDataPath, 'wallet-provider', 'permissions.json'),
      'utf-8'
    );
    expect(stored).not.toContain('passphrase');
    expect(stored).not.toContain('private_key');
  });

  it('requires a grant and user approval before signing', async () => {
    await expect(
      handleWalletProviderRequest(
        request('qsdm_signMessage', { message: 'ownership challenge' }),
        options
      )
    ).rejects.toThrow('not connected');

    await handleWalletProviderRequest(request('qsdm_requestAccounts'), options);
    await expect(
      handleWalletProviderRequest(
        request('qsdm_signMessage', { message: 'ownership challenge' }),
        options
      )
    ).resolves.toEqual({
      address: sender,
      public_key: 'public-key',
      signature: 'signature',
    });
    expect(mockSignQsdmMessageWithCli).toHaveBeenCalledWith(
      'ownership challenge'
    );
    expect(dialog.showMessageBox).toHaveBeenCalledTimes(2);
  });

  it('revokes a site grant immediately', async () => {
    await handleWalletProviderRequest(request('qsdm_requestAccounts'), options);

    expect(revokeQsdmWalletProviderPermission('https://example.com')).toEqual({
      origin: 'https://example.com',
      revoked: true,
    });
    expect(getQsdmWalletProviderPermissions().permissions).toEqual([]);
    await expect(
      handleWalletProviderRequest(request('qsdm_accounts'), options)
    ).resolves.toEqual([]);
  });

  it('rejects invalid recipients before displaying a transfer approval', async () => {
    await handleWalletProviderRequest(request('qsdm_requestAccounts'), options);
    (dialog.showMessageBox as jest.Mock).mockClear();

    await expect(
      handleWalletProviderRequest(
        request('qsdm_sendTransaction', {
          recipient: '../not-a-wallet',
          amount: 1,
        }),
        options
      )
    ).rejects.toThrow('valid QSDM wallet address');
    expect(dialog.showMessageBox).not.toHaveBeenCalled();
    expect(mockSubmitQsdmWalletTransferIntent).not.toHaveBeenCalled();
  });
});
