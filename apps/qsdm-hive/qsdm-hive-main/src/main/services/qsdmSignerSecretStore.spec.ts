/**
 * @jest-environment node
 */
// cspell:ignore ciphertext dpapi

import { safeStorage } from 'electron';
import fs from 'fs';
import os from 'os';
import path from 'path';

import {
  cleanupQsdmSignerSecretStore,
  initializeQsdmSignerSecretStore,
  persistQsdmSignerPassphrase,
} from './qsdmSignerSecretStore';

const mockActivateQsdmImportedSignerPaths = jest.fn();
const mockGetQsdmDefaultLocalSignerPaths = jest.fn();

jest.mock('main/services/qsdmTaskActionSigner', () => ({
  activateQsdmImportedSignerPaths: (paths: unknown) =>
    mockActivateQsdmImportedSignerPaths(paths),
  getQsdmDefaultLocalSignerPaths: () => mockGetQsdmDefaultLocalSignerPaths(),
}));

describe('qsdmSignerSecretStore', () => {
  let signerDir = '';
  let keystorePath = '';
  let legacyPassphrasePath = '';

  beforeEach(() => {
    signerDir = fs.mkdtempSync(path.join(os.tmpdir(), 'qsdm-secret-store-'));
    keystorePath = path.join(signerDir, 'wallet.json');
    legacyPassphrasePath = path.join(signerDir, 'passphrase.txt');
    mockGetQsdmDefaultLocalSignerPaths.mockReturnValue({
      signerDir,
      keystorePath,
      passphraseFile: legacyPassphrasePath,
    });
    mockActivateQsdmImportedSignerPaths.mockReset();
    (safeStorage.isEncryptionAvailable as jest.Mock).mockReturnValue(true);
    (safeStorage.getSelectedStorageBackend as jest.Mock).mockReturnValue(
      'dpapi'
    );
  });

  afterEach(() => {
    cleanupQsdmSignerSecretStore();
    fs.rmSync(signerDir, { recursive: true, force: true });
  });

  it('stores the durable passphrase encrypted and materializes only a session file', () => {
    const result = persistQsdmSignerPassphrase({
      passphrase: 'correct horse battery staple',
      signerDir,
    });

    expect(result.protectedAtRest).toBe(true);
    expect(fs.existsSync(path.join(signerDir, 'passphrase.safe'))).toBe(true);
    expect(fs.existsSync(legacyPassphrasePath)).toBe(false);
    expect(fs.readFileSync(result.passphraseFile, 'utf-8')).toBe(
      'correct horse battery staple'
    );
    expect(path.dirname(result.passphraseFile)).not.toBe(signerDir);
  });

  it('migrates a legacy plaintext passphrase into protected storage', () => {
    fs.writeFileSync(
      keystorePath,
      JSON.stringify({ address: 'active-wallet-address' })
    );
    fs.writeFileSync(legacyPassphrasePath, 'legacy-passphrase');

    const result = initializeQsdmSignerSecretStore();

    expect(result).toEqual(
      expect.objectContaining({ configured: true, protectedAtRest: true })
    );
    expect(fs.existsSync(path.join(signerDir, 'passphrase.safe'))).toBe(true);
    expect(fs.existsSync(legacyPassphrasePath)).toBe(false);
    expect(mockActivateQsdmImportedSignerPaths).toHaveBeenCalledWith(
      expect.objectContaining({
        keystorePath,
        sender: 'active-wallet-address',
      })
    );
  });

  it('quarantines an unreadable protected passphrase without losing the wallet', () => {
    fs.writeFileSync(
      keystorePath,
      JSON.stringify({ address: 'active-wallet-address' })
    );
    const encryptedPath = path.join(signerDir, 'passphrase.safe');
    fs.writeFileSync(encryptedPath, Buffer.from('unreadable-ciphertext'));
    (safeStorage.decryptString as jest.Mock).mockImplementationOnce(() => {
      throw new Error('decrypt failed');
    });

    const result = initializeQsdmSignerSecretStore();

    expect(result).toEqual(
      expect.objectContaining({
        configured: false,
        protectedAtRest: true,
        reason: expect.stringContaining('Unlock the existing wallet'),
        quarantinePath: expect.stringContaining('passphrase.safe.unreadable-'),
      })
    );
    expect(fs.existsSync(keystorePath)).toBe(true);
    expect(fs.existsSync(encryptedPath)).toBe(false);
    expect(mockActivateQsdmImportedSignerPaths).not.toHaveBeenCalled();
  });
});
