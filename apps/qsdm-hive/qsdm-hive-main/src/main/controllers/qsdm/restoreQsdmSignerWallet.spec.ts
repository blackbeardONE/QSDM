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
const mockRunningTasks: Record<string, unknown> = {};
const RESTORED = 'd'.repeat(64);
const EXISTING = 'e'.repeat(64);
const restoredJson = JSON.stringify({
  type: 'qsdm-keystore',
  address: RESTORED,
});

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
    const encryptedPath = pathModule.join(signerDir, 'passphrase.safe');
    fsModule.writeFileSync(encryptedPath, `protected:${passphrase}`);
    return { passphraseFile: 'unused', protectedAtRest: true, encryptedPath };
  },
  initializeQsdmSignerSecretStore: () => {
    const pathModule = jest.requireActual<typeof import('path')>('path');
    return {
      configured: true,
      protectedAtRest: true,
      passphraseFile: pathModule.join(mockSignerDir, 'session-passphrase.txt'),
    };
  },
}));

jest.mock('main/services/qsdmHiveTasks', () => ({
  __esModule: true,
  default: {
    get RUNNING_TASKS() {
      return mockRunningTasks;
    },
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
          fs.writeFileSync(outputPath, restoredJson);
          callback(null, `${RESTORED}\n`, '');
          return;
        }
        if (command === 'show') {
          callback(
            null,
            JSON.stringify({
              address: RESTORED,
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
      address: RESTORED,
      publicKey: 'qsdm-restored-public-key',
      keystorePath,
      passphraseFile,
      active: true,
    });
    expect(mockExecFile).toHaveBeenCalledTimes(3);
    expect(mockActivateQsdmImportedSignerPaths).toHaveBeenCalledWith(
      expect.objectContaining({ keystorePath, sender: RESTORED })
    );
  });

  it('adds the restored wallet beside the active one without replacing it', async () => {
    const existingJson = JSON.stringify({
      type: 'qsdm-keystore',
      address: EXISTING,
    });
    fs.writeFileSync(path.join(mockSignerDir, 'wallet.json'), existingJson);
    fs.writeFileSync(
      path.join(mockSignerDir, 'passphrase.safe'),
      'protected:existing'
    );

    const result = await restoreQsdmSignerWallet({} as Event, {
      recoveryWords,
      passphrase: 'correct horse battery staple',
    });

    expect(result.active).toBe(false);
    expect(
      fs.readFileSync(path.join(mockSignerDir, 'wallet.json'), 'utf-8')
    ).toBe(existingJson);
    expect(
      fs.readFileSync(path.join(mockSignerDir, 'passphrase.safe'), 'utf-8')
    ).toBe('protected:existing');
    expect(
      fs.readFileSync(
        path.join(mockSignerDir, 'wallets', RESTORED, 'passphrase.safe'),
        'utf-8'
      )
    ).toBe('protected:correct horse battery staple');
    expect(mockActivateQsdmImportedSignerPaths).not.toHaveBeenCalled();
  });

  it('restoring the active wallet again renews its passphrase entry in place (forgotten passphrase)', async () => {
    fs.writeFileSync(path.join(mockSignerDir, 'wallet.json'), restoredJson);
    fs.writeFileSync(
      path.join(mockSignerDir, 'passphrase.safe'),
      'protected:forgotten'
    );

    const result = await restoreQsdmSignerWallet({} as Event, {
      recoveryWords,
      passphrase: 'a brand new passphrase',
    });

    expect(result.active).toBe(true);
    expect(
      fs.readFileSync(path.join(mockSignerDir, 'passphrase.safe'), 'utf-8')
    ).toBe('protected:a brand new passphrase');
    const walletDir = path.join(mockSignerDir, 'wallets', RESTORED);
    expect(
      fs
        .readdirSync(walletDir)
        .filter((name) => name.startsWith('passphrase.safe.bak-'))
    ).toHaveLength(1);
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
