/**
 * @jest-environment node
 */

import fs from 'fs';
import os from 'os';
import path from 'path';
import { Event } from 'electron';

const mockSpawnSync = jest.fn();
const mockActivateQsdmImportedSignerPaths = jest.fn();
let mockSignerDir = '';

jest.mock('child_process', () => ({
  spawnSync: mockSpawnSync,
}));

jest.mock('main/services/qsdmTaskActionSigner', () => ({
  activateQsdmImportedSignerPaths: mockActivateQsdmImportedSignerPaths,
  getQsdmDefaultLocalSignerPaths: () => {
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
  persistQsdmSignerPassphrase: ({ passphrase, signerDir }: any) => {
    const pathModule = require('path');
    const fsModule = require('fs');
    const passphraseFile = pathModule.join(signerDir, 'session-passphrase.txt');
    fsModule.writeFileSync(passphraseFile, passphrase);
    return { passphraseFile, protectedAtRest: true };
  },
}));

import { importQsdmSignerWallet } from './importQsdmSignerWallet';

const keystoreJson = JSON.stringify({
  type: 'qsdm-keystore',
  address: 'qsdm-address-1',
  public_key: 'qsdm-public-key-1',
  cipher: 'test',
});

describe('importQsdmSignerWallet', () => {
  beforeEach(() => {
    mockSignerDir = fs.mkdtempSync(path.join(os.tmpdir(), 'qsdm-signer-test-'));
    mockSpawnSync.mockReset();
    mockActivateQsdmImportedSignerPaths.mockReset();
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

  it('validates and installs a QSDM keystore plus passphrase', async () => {
    const response = await importQsdmSignerWallet({} as Event, {
      keystoreJson,
      passphrase: 'correct horse battery staple',
    });

    const keystorePath = path.join(mockSignerDir, 'wallet.json');
    const passphraseFile = path.join(mockSignerDir, 'session-passphrase.txt');

    expect(response).toEqual({
      address: 'qsdm-address-1',
      publicKey: 'qsdm-public-key-1',
      keystorePath,
      passphraseFile,
    });
    expect(fs.readFileSync(keystorePath, 'utf-8')).toBe(keystoreJson);
    expect(fs.readFileSync(passphraseFile, 'utf-8')).toBe(
      'correct horse battery staple'
    );
    expect(mockSpawnSync).toHaveBeenNthCalledWith(
      1,
      'qsdmcli.exe',
      ['wallet', 'show', '--in', expect.any(String), '--json'],
      expect.objectContaining({ windowsHide: true })
    );
    expect(mockSpawnSync).toHaveBeenNthCalledWith(
      2,
      'qsdmcli.exe',
      [
        'wallet',
        'inspect',
        '--in',
        expect.any(String),
        '--passphrase-file',
        expect.any(String),
      ],
      expect.objectContaining({ windowsHide: true })
    );
    expect(mockActivateQsdmImportedSignerPaths).toHaveBeenCalledWith({
      keystorePath,
      passphraseFile,
      sender: 'qsdm-address-1',
    });
  });

  it('rejects non-QSDM JSON without invoking qsdmcli', async () => {
    await expect(
      importQsdmSignerWallet({} as Event, {
        keystoreJson: JSON.stringify({ address: 'not-qsdm' }),
        passphrase: 'pass',
      })
    ).rejects.toThrow('Selected file is not a QSDM keystore JSON');

    expect(mockSpawnSync).not.toHaveBeenCalled();
  });
});
