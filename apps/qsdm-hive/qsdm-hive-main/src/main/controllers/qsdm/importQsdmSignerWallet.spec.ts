/**
 * @jest-environment node
 */

import fs from 'fs';
import os from 'os';
import path from 'path';
import { Event } from 'electron';

const mockSpawnSync = jest.fn();
const mockActivateQsdmImportedSignerPaths = jest.fn();
const mockRunningTasks: Record<string, unknown> = {};
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

// passphrase.safe stands in for the DPAPI-protected entry; the session file is what the signer reads.
jest.mock('main/services/qsdmSignerSecretStore', () => ({
  backupQsdmEncryptedPassphrase: jest.fn(),
  persistQsdmSignerPassphrase: ({ passphrase, signerDir }: any) => {
    const pathModule = require('path');
    const fsModule = require('fs');
    const encryptedPath = pathModule.join(signerDir, 'passphrase.safe');
    fsModule.writeFileSync(encryptedPath, `protected:${passphrase}`);
    return { passphraseFile: 'unused', protectedAtRest: true, encryptedPath };
  },
  initializeQsdmSignerSecretStore: () => {
    const pathModule = require('path');
    return {
      configured: true,
      protectedAtRest: true,
      passphraseFile: pathModule.join(mockSignerDir, 'session-passphrase.txt'),
    };
  },
}));

jest.mock('main/services/qsdmHiveTasks', () => ({
  __esModule: true,
  default: { RUNNING_TASKS: mockRunningTasks },
}));

import { importQsdmSignerWallet } from './importQsdmSignerWallet';

const EXISTING = 'a'.repeat(64);
const IMPORTED = 'b'.repeat(64);

const keystoreFor = (address: string) =>
  JSON.stringify({
    type: 'qsdm-keystore',
    address,
    public_key: `pk-${address.slice(0, 4)}`,
    cipher: 'test',
  });

const keystoreJson = keystoreFor(IMPORTED);

describe('importQsdmSignerWallet', () => {
  const activeSlot = () => path.join(mockSignerDir, 'wallet.json');

  beforeEach(() => {
    mockSignerDir = fs.mkdtempSync(path.join(os.tmpdir(), 'qsdm-signer-test-'));
    Object.keys(mockRunningTasks).forEach(
      (key) => delete mockRunningTasks[key]
    );
    mockSpawnSync.mockReset();
    mockActivateQsdmImportedSignerPaths.mockReset();
    mockSpawnSync
      .mockReturnValueOnce({
        status: 0,
        stdout: JSON.stringify({
          address: IMPORTED,
          public_key: 'pk-bbbb',
        }),
        stderr: '',
      })
      .mockReturnValueOnce({ status: 0, stdout: 'ok', stderr: '' });
  });

  afterEach(() => {
    fs.rmSync(mockSignerDir, { recursive: true, force: true });
  });

  it('validates and installs the first wallet of a fresh Hive as the active wallet', async () => {
    const response = await importQsdmSignerWallet({} as Event, {
      keystoreJson,
      passphrase: 'correct horse battery staple',
    });

    expect(response).toEqual({
      address: IMPORTED,
      publicKey: 'pk-bbbb',
      keystorePath: activeSlot(),
      passphraseFile: path.join(mockSignerDir, 'session-passphrase.txt'),
      active: true,
    });
    expect(fs.readFileSync(activeSlot(), 'utf-8')).toBe(keystoreJson);
    expect(
      fs.readFileSync(path.join(mockSignerDir, 'passphrase.safe'), 'utf-8')
    ).toBe('protected:correct horse battery staple');
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
    expect(mockActivateQsdmImportedSignerPaths).toHaveBeenCalledWith(
      expect.objectContaining({ keystorePath: activeSlot(), sender: IMPORTED })
    );
  });

  it('ADDS a wallet beside the existing one and keeps the existing wallet active (the 1.4.24 bug)', async () => {
    fs.writeFileSync(activeSlot(), keystoreFor(EXISTING));
    fs.writeFileSync(
      path.join(mockSignerDir, 'passphrase.safe'),
      'protected:existing'
    );

    const response = await importQsdmSignerWallet({} as Event, {
      keystoreJson,
      passphrase: 'correct horse battery staple',
    });

    expect(response.active).toBe(false);
    expect(response.passphraseFile).toBe('');
    expect(response.keystorePath).toBe(
      path.join(mockSignerDir, 'wallets', IMPORTED, 'wallet.json')
    );
    // The existing wallet is untouched and still active.
    expect(fs.readFileSync(activeSlot(), 'utf-8')).toBe(keystoreFor(EXISTING));
    expect(
      fs.readFileSync(path.join(mockSignerDir, 'passphrase.safe'), 'utf-8')
    ).toBe('protected:existing');
    expect(mockActivateQsdmImportedSignerPaths).not.toHaveBeenCalled();
    // Both wallets are in the list, each with its own protected passphrase entry.
    const index = JSON.parse(
      fs.readFileSync(
        path.join(mockSignerDir, 'wallets', 'index.json'),
        'utf-8'
      )
    ) as { active: string; wallets: { address: string }[] };
    expect(index.active).toBe(EXISTING);
    expect(index.wallets.map((w) => w.address)).toEqual([EXISTING, IMPORTED]);
    expect(
      fs.readFileSync(
        path.join(mockSignerDir, 'wallets', EXISTING, 'passphrase.safe'),
        'utf-8'
      )
    ).toBe('protected:existing');
    expect(
      fs.readFileSync(
        path.join(mockSignerDir, 'wallets', IMPORTED, 'passphrase.safe'),
        'utf-8'
      )
    ).toBe('protected:correct horse battery staple');
    expect(
      fs.readdirSync(mockSignerDir).filter((name) => name.includes('.bak-'))
    ).toEqual([]);
  });

  it('switches to the imported wallet only when asked, keeping the previous one in the list', async () => {
    fs.writeFileSync(activeSlot(), keystoreFor(EXISTING));
    fs.writeFileSync(
      path.join(mockSignerDir, 'passphrase.safe'),
      'protected:existing'
    );

    const response = await importQsdmSignerWallet({} as Event, {
      keystoreJson,
      passphrase: 'correct horse battery staple',
      makeActive: true,
    });

    expect(response.active).toBe(true);
    expect(fs.readFileSync(activeSlot(), 'utf-8')).toBe(keystoreJson);
    expect(
      fs.readFileSync(
        path.join(mockSignerDir, 'wallets', EXISTING, 'wallet.json'),
        'utf-8'
      )
    ).toBe(keystoreFor(EXISTING));
    expect(mockActivateQsdmImportedSignerPaths).toHaveBeenCalledWith(
      expect.objectContaining({ sender: IMPORTED })
    );
  });

  it('refuses to switch while a miner task signs with the active wallet, before writing anything', async () => {
    fs.writeFileSync(activeSlot(), keystoreFor(EXISTING));
    mockRunningTasks['qsdm-system-miner'] = {};

    await expect(
      importQsdmSignerWallet({} as Event, {
        keystoreJson,
        passphrase: 'correct horse battery staple',
        makeActive: true,
      })
    ).rejects.toThrow('Stop the QSDM Miner');
    expect(fs.readFileSync(activeSlot(), 'utf-8')).toBe(keystoreFor(EXISTING));
    expect(fs.existsSync(path.join(mockSignerDir, 'wallets', IMPORTED))).toBe(
      false
    );
    expect(mockSpawnSync).not.toHaveBeenCalled();
  });

  it('refuses to import a wallet that is already in Hive', async () => {
    fs.writeFileSync(activeSlot(), keystoreJson);

    await expect(
      importQsdmSignerWallet({} as Event, {
        keystoreJson,
        passphrase: 'correct horse battery staple',
      })
    ).rejects.toThrow('already in Hive');
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
