/**
 * @jest-environment node
 */

import { Event } from 'electron';
import fs from 'fs';
import os from 'os';
import path from 'path';

const mockSpawnSync = jest.fn();
const mockActivateQsdmImportedSignerPaths = jest.fn();
const mockPersistQsdmSignerPassphrase = jest.fn();
let mockSignerDir = '';

jest.mock('child_process', () => ({
  spawnSync: mockSpawnSync,
}));

jest.mock('main/services/qsdmTaskActionSigner', () => ({
  activateQsdmImportedSignerPaths: mockActivateQsdmImportedSignerPaths,
  getQsdmDefaultLocalSignerPaths: () => {
    // eslint-disable-next-line global-require
    const pathModule = require('path');
    return {
      signerDir: mockSignerDir,
      keystorePath: pathModule.join(mockSignerDir, 'wallet.json'),
      passphraseFile: pathModule.join(mockSignerDir, 'passphrase.txt'),
    };
  },
  getQsdmTaskActionCliPath: () => 'qsdmcli.exe',
}));

jest.mock('main/services/qsdmSignerSecretStore', () => ({
  backupQsdmEncryptedPassphrase: jest.fn(),
  persistQsdmSignerPassphrase: (payload: unknown) =>
    mockPersistQsdmSignerPassphrase(payload),
}));

// eslint-disable-next-line import/first
import { unlockQsdmSignerWallet } from './unlockQsdmSignerWallet';

describe('unlockQsdmSignerWallet', () => {
  beforeEach(() => {
    mockSignerDir = fs.mkdtempSync(path.join(os.tmpdir(), 'qsdm-unlock-test-'));
    fs.writeFileSync(
      path.join(mockSignerDir, 'wallet.json'),
      JSON.stringify({ type: 'qsdm-keystore', address: 'qsdm-address-1' })
    );
    mockSpawnSync.mockReset();
    mockActivateQsdmImportedSignerPaths.mockReset();
    mockPersistQsdmSignerPassphrase.mockReset();
    mockPersistQsdmSignerPassphrase.mockReturnValue({
      passphraseFile: path.join(mockSignerDir, 'session-passphrase.txt'),
      protectedAtRest: true,
    });
    mockSpawnSync
      .mockReturnValueOnce({
        status: 0,
        stdout: JSON.stringify({
          address: 'qsdm-address-1',
          public_key: 'qsdm-public-key-1',
        }),
        stderr: '',
      })
      .mockReturnValueOnce({ status: 0, stdout: 'ok', stderr: '' });
  });

  afterEach(() => {
    fs.rmSync(mockSignerDir, { recursive: true, force: true });
  });

  it('unlocks the wallet already stored on this device', async () => {
    const response = await unlockQsdmSignerWallet({} as Event, {
      passphrase: 'correct horse battery staple',
    });

    expect(response).toEqual({
      address: 'qsdm-address-1',
      publicKey: 'qsdm-public-key-1',
      keystorePath: path.join(mockSignerDir, 'wallet.json'),
      passphraseFile: path.join(mockSignerDir, 'session-passphrase.txt'),
    });
    expect(mockPersistQsdmSignerPassphrase).toHaveBeenCalledWith({
      passphrase: 'correct horse battery staple',
      signerDir: mockSignerDir,
    });
    expect(mockActivateQsdmImportedSignerPaths).toHaveBeenCalledWith({
      keystorePath: path.join(mockSignerDir, 'wallet.json'),
      passphraseFile: path.join(mockSignerDir, 'session-passphrase.txt'),
      sender: 'qsdm-address-1',
    });
  });

  it('does not replace protected storage when the passphrase is wrong', async () => {
    mockSpawnSync
      .mockReset()
      .mockReturnValueOnce({
        status: 0,
        stdout: JSON.stringify({
          address: 'qsdm-address-1',
          public_key: 'qsdm-public-key-1',
        }),
        stderr: '',
      })
      .mockReturnValueOnce({ status: 1, stdout: '', stderr: 'invalid key' });

    await expect(
      unlockQsdmSignerWallet({} as Event, { passphrase: 'wrong' })
    ).rejects.toThrow('did not unlock this keystore');
    expect(mockPersistQsdmSignerPassphrase).not.toHaveBeenCalled();
    expect(mockActivateQsdmImportedSignerPaths).not.toHaveBeenCalled();
  });
});
