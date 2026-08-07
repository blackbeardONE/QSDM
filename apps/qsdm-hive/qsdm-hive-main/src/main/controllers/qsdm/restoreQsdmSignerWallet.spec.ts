/**
 * @jest-environment node
 */

import { execFile } from 'child_process';
import fs from 'fs';
import os from 'os';
import path from 'path';

import { activateQsdmImportedSignerPaths } from 'main/services/qsdmTaskActionSigner';

import { restoreQsdmSignerWallet } from './restoreQsdmSignerWallet';

import type { Event } from 'electron';

jest.mock('child_process', () => ({
  execFile: jest.fn(),
}));

let mockSignerDir = '';

jest.mock('main/services/qsdmTaskActionSigner', () => ({
  activateQsdmImportedSignerPaths: jest.fn(),
  getQsdmDefaultLocalSignerPaths: () => {
    const pathModule = jest.requireActual<typeof import('path')>('path');
    return {
      signerDir: mockSignerDir,
      keystorePath: pathModule.join(mockSignerDir, 'wallet.json'),
      passphraseFile: pathModule.join(mockSignerDir, 'passphrase.txt'),
    };
  },
  getQsdmTaskActionCliPath: () => 'qsdmcli',
}));

jest.mock('main/services/qsdmSignerSecretStore', () => ({
  backupQsdmEncryptedPassphrase: jest.fn(),
  persistQsdmSignerPassphrase: ({ passphrase, signerDir }: any) => {
    const pathModule = jest.requireActual<typeof import('path')>('path');
    const fsModule = jest.requireActual<typeof import('fs')>('fs');
    const passphraseFile = pathModule.join(signerDir, 'session-passphrase.txt');
    fsModule.writeFileSync(passphraseFile, passphrase);
    return { passphraseFile, protectedAtRest: true };
  },
}));

jest.mock('main/services/qsdmTaskActions', () => ({
  resolveQsdmTaskActionApiUrl: () => 'https://core.example/api/v1',
}));

const mockExecFile = execFile as unknown as jest.Mock;
const mockActivateQsdmImportedSignerPaths =
  activateQsdmImportedSignerPaths as jest.Mock;
const recoveryWords = Array.from(
  { length: 24 },
  (_, index) => `word${index + 1}`
).join(' ');

describe('restoreQsdmSignerWallet', () => {
  beforeEach(() => {
    mockSignerDir = fs.mkdtempSync(
      path.join(os.tmpdir(), 'qsdm-restore-wallet-')
    );
    mockExecFile.mockReset();
    mockActivateQsdmImportedSignerPaths.mockReset();
    mockExecFile.mockImplementation(
      (
        _cli: string,
        args: string[],
        _options: unknown,
        callback: (error: Error | null, stdout: string, stderr: string) => void
      ) => {
        const command = args[1];
        if (command === 'restore' || command === 'restore-legacy') {
          const outputPath = args[args.indexOf('--out') + 1];
          fs.writeFileSync(outputPath, '{"type":"qsdm-keystore"}');
          callback(null, 'qsdm-restored-address\n', '');
          return;
        }
        if (command === 'show') {
          callback(
            null,
            JSON.stringify({
              address: 'qsdm-restored-address',
              public_key: 'qsdm-restored-public-key',
            }),
            ''
          );
          return;
        }
        callback(null, 'ok', '');
      }
    );
  });

  afterEach(() => {
    fs.rmSync(mockSignerDir, { recursive: true, force: true });
  });

  it('restores and activates the same QSDM wallet', async () => {
    const result = await restoreQsdmSignerWallet({} as Event, {
      recoveryWords,
      passphrase: 'correct horse battery staple',
    });

    const keystorePath = path.join(mockSignerDir, 'wallet.json');
    const passphraseFile = path.join(mockSignerDir, 'session-passphrase.txt');
    expect(result).toEqual({
      address: 'qsdm-restored-address',
      publicKey: 'qsdm-restored-public-key',
      keystorePath,
      passphraseFile,
    });
    expect(mockExecFile).toHaveBeenCalledTimes(3);
    expect(mockActivateQsdmImportedSignerPaths).toHaveBeenCalledWith({
      keystorePath,
      passphraseFile,
      sender: 'qsdm-restored-address',
    });
  });

  it('rejects incomplete recovery words before invoking qsdmcli', async () => {
    await expect(
      restoreQsdmSignerWallet({} as Event, {
        recoveryWords: recoveryWords.split(' ').slice(0, 12).join(' '),
        passphrase: 'correct horse battery staple',
      })
    ).rejects.toThrow('all 24');
    expect(mockExecFile).not.toHaveBeenCalled();
  });

  it('restores an upgraded older wallet from its network recovery capsule', async () => {
    await restoreQsdmSignerWallet({} as Event, {
      recoveryWords,
      passphrase: 'correct horse battery staple',
      recoveryType: 'legacy',
    });

    expect(mockExecFile).toHaveBeenNthCalledWith(
      1,
      'qsdmcli',
      expect.arrayContaining([
        'wallet',
        'restore-legacy',
        '--api-url',
        'https://core.example/api/v1',
      ]),
      expect.objectContaining({ timeout: 120000, windowsHide: true }),
      expect.any(Function)
    );
  });
});
