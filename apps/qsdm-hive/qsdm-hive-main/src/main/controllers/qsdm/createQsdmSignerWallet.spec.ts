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
    // eslint-disable-next-line global-require
    const pathModule = require('path');
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
    const pathModule = require('path');
    const fsModule = require('fs');
    const passphraseFile = pathModule.join(signerDir, 'session-passphrase.txt');
    fsModule.writeFileSync(passphraseFile, passphrase);
    return { passphraseFile, protectedAtRest: true };
  },
}));

const mockSpawnSync = spawnSync as jest.Mock;
const mockActivateQsdmImportedSignerPaths =
  activateQsdmImportedSignerPaths as jest.Mock;
let mockSignerDir = '';

const walletJson = JSON.stringify({
  type: 'qsdm-keystore',
  address: 'qsdm-created-address',
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
        fs.writeFileSync(outputPath, walletJson);
        return { status: 0, stdout: 'qsdm-created-address\n', stderr: '' };
      }
      if (command === 'show') {
        return {
          status: 0,
          stdout: JSON.stringify({
            address: 'qsdm-created-address',
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
      address: 'qsdm-created-address',
      publicKey: 'qsdm-created-public-key',
      keystorePath,
      passphraseFile,
    });
    expect(fs.readFileSync(keystorePath, 'utf-8')).toBe(walletJson);
    expect(fs.readFileSync(passphraseFile, 'utf-8')).toBe(
      'correct horse battery staple'
    );
    expect(mockSpawnSync).toHaveBeenCalledTimes(3);
    expect(mockActivateQsdmImportedSignerPaths).toHaveBeenCalledWith({
      keystorePath,
      passphraseFile,
      sender: 'qsdm-created-address',
    });
  });

  it('refuses to overwrite an existing signer wallet', async () => {
    fs.writeFileSync(path.join(mockSignerDir, 'wallet.json'), walletJson);

    await expect(
      createQsdmSignerWallet({} as Event, {
        passphrase: 'correct horse battery staple',
      })
    ).rejects.toThrow('already exists');
    expect(mockSpawnSync).not.toHaveBeenCalled();
  });
});
