/**
 * @jest-environment node
 */

import { spawnSync } from 'child_process';
import fs from 'fs';
import os from 'os';
import path from 'path';

import { activateQsdmImportedSignerPaths } from 'main/services/qsdmTaskActionSigner';

import { createQsdmSignerWallet } from './createQsdmSignerWallet';

import type { Event } from 'electron';

jest.mock('child_process', () => ({
  spawnSync: jest.fn(),
}));

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
  hasQsdmStoredPassphrase: () => false,
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

const mockSpawnSync = spawnSync as jest.Mock;
const mockActivateQsdmImportedSignerPaths =
  activateQsdmImportedSignerPaths as jest.Mock;
let mockSignerDir = '';
const mockRunningTasks: Record<string, unknown> = {};
const CREATED = 'c'.repeat(64);
const EXISTING = 'e'.repeat(64);

const walletJson = JSON.stringify({
  type: 'qsdm-keystore',
  address: CREATED,
  public_key: 'qsdm-created-public-key',
});

describe('createQsdmSignerWallet', () => {
  beforeEach(() => {
    mockSignerDir = fs.mkdtempSync(path.join(os.tmpdir(), 'qsdm-new-wallet-'));
    mockSpawnSync.mockReset();
    mockActivateQsdmImportedSignerPaths.mockReset();
    mockSpawnSync.mockImplementation((_cli: string, args: string[]) => {
      const command = args[1];
      if (command === 'new') {
        const outputPath = args[args.indexOf('--out') + 1];
        const recoveryPath = args[args.indexOf('--recovery-out') + 1];
        fs.writeFileSync(outputPath, walletJson);
        fs.writeFileSync(
          recoveryPath,
          Array.from({ length: 24 }, (_, index) => `word${index + 1}`).join(' ')
        );
        return { status: 0, stdout: `${CREATED}\n`, stderr: '' };
      }
      if (command === 'show') {
        return {
          status: 0,
          stdout: JSON.stringify({
            address: CREATED,
            public_key: 'qsdm-created-public-key',
          }),
          stderr: '',
        };
      }
      return { status: 0, stdout: 'ok', stderr: '' };
    });
  });

  afterEach(() => {
    fs.rmSync(mockSignerDir, { recursive: true, force: true });
  });

  it('creates, validates, and activates a native QSDM wallet', async () => {
    const response = await createQsdmSignerWallet({} as Event, {
      passphrase: 'correct horse battery staple',
    });

    const keystorePath = path.join(mockSignerDir, 'wallet.json');
    const passphraseFile = path.join(mockSignerDir, 'session-passphrase.txt');
    expect(response).toEqual({
      address: CREATED,
      publicKey: 'qsdm-created-public-key',
      keystorePath,
      passphraseFile,
      recoveryWords: Array.from(
        { length: 24 },
        (_, index) => `word${index + 1}`
      ).join(' '),
      active: true,
    });
    expect(fs.readFileSync(keystorePath, 'utf-8')).toBe(walletJson);
    expect(
      fs.readFileSync(path.join(mockSignerDir, 'passphrase.safe'), 'utf-8')
    ).toBe('protected:correct horse battery staple');
    expect(mockSpawnSync).toHaveBeenCalledTimes(3);
    expect(mockActivateQsdmImportedSignerPaths).toHaveBeenCalledWith(
      expect.objectContaining({ keystorePath, sender: CREATED })
    );
  });

  it('adds a new wallet beside an existing one instead of refusing or replacing it', async () => {
    const existingJson = JSON.stringify({
      type: 'qsdm-keystore',
      address: EXISTING,
    });
    fs.writeFileSync(path.join(mockSignerDir, 'wallet.json'), existingJson);
    fs.writeFileSync(
      path.join(mockSignerDir, 'passphrase.safe'),
      'protected:existing'
    );

    const response = await createQsdmSignerWallet({} as Event, {
      passphrase: 'correct horse battery staple',
      label: 'Savings',
    });

    expect(response.active).toBe(false);
    expect(response.recoveryWords?.split(' ')).toHaveLength(24);
    expect(
      fs.readFileSync(path.join(mockSignerDir, 'wallet.json'), 'utf-8')
    ).toBe(existingJson);
    expect(
      fs.readFileSync(path.join(mockSignerDir, 'passphrase.safe'), 'utf-8')
    ).toBe('protected:existing');
    expect(
      fs.readFileSync(
        path.join(mockSignerDir, 'wallets', CREATED, 'wallet.json'),
        'utf-8'
      )
    ).toBe(walletJson);
    expect(mockActivateQsdmImportedSignerPaths).not.toHaveBeenCalled();
  });
});
