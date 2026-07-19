import { safeStorage } from 'electron';
import fs from 'fs';
import os from 'os';
import path from 'path';

import {
  activateQsdmImportedSignerPaths,
  getQsdmDefaultLocalSignerPaths,
} from './qsdmTaskActionSigner';

const ENCRYPTED_PASSPHRASE_NAME = 'passphrase.safe';
const sessionDirectories = new Set<string>();

const writePrivate = (filePath: string, content: Buffer | string) => {
  fs.writeFileSync(filePath, content, { mode: 0o600 });
  try {
    fs.chmodSync(filePath, 0o600);
  } catch {
    // Windows protects files through the current user's profile ACL.
  }
};

const getEncryptedPassphrasePath = (signerDir: string) =>
  path.join(signerDir, ENCRYPTED_PASSPHRASE_NAME);

const quarantineUnreadablePassphrase = (encryptedPath: string) => {
  const quarantinePath = `${encryptedPath}.unreadable-${Date.now()}`;
  try {
    fs.renameSync(encryptedPath, quarantinePath);
    return quarantinePath;
  } catch {
    return undefined;
  }
};

const getSafeStorageBackend = () => {
  try {
    return safeStorage.getSelectedStorageBackend?.() || '';
  } catch {
    return '';
  }
};

export const isQsdmProtectedSecretStorageAvailable = () => {
  try {
    if (!safeStorage.isEncryptionAvailable()) return false;
    return (
      process.platform !== 'linux' || getSafeStorageBackend() !== 'basic_text'
    );
  } catch {
    return false;
  }
};

const createSessionPassphraseFile = (passphrase: string) => {
  const sessionDir = fs.mkdtempSync(
    path.join(os.tmpdir(), `qsdm-hive-signer-${process.pid}-`)
  );
  try {
    fs.chmodSync(sessionDir, 0o700);
  } catch {
    // The Windows user temp directory already carries a per-user ACL.
  }
  sessionDirectories.add(sessionDir);
  const passphraseFile = path.join(sessionDir, 'passphrase.txt');
  writePrivate(passphraseFile, passphrase);
  return passphraseFile;
};

const readKeystoreAddress = (keystorePath: string) => {
  try {
    const wallet = JSON.parse(fs.readFileSync(keystorePath, 'utf-8')) as {
      address?: string;
    };
    return wallet.address?.trim();
  } catch {
    return undefined;
  }
};

export const hasQsdmStoredPassphrase = (signerDir: string) =>
  fs.existsSync(getEncryptedPassphrasePath(signerDir)) ||
  fs.existsSync(path.join(signerDir, 'passphrase.txt'));

export const persistQsdmSignerPassphrase = ({
  passphrase,
  signerDir,
}: {
  passphrase: string;
  signerDir: string;
}) => {
  fs.mkdirSync(signerDir, { recursive: true, mode: 0o700 });
  const legacyPath = path.join(signerDir, 'passphrase.txt');

  if (!isQsdmProtectedSecretStorageAvailable()) {
    writePrivate(legacyPath, passphrase);
    return {
      passphraseFile: legacyPath,
      protectedAtRest: false,
      encryptedPath: undefined,
    };
  }

  const encryptedPath = getEncryptedPassphrasePath(signerDir);
  const encrypted = safeStorage.encryptString(passphrase);
  writePrivate(encryptedPath, encrypted);
  const passphraseFile = createSessionPassphraseFile(passphrase);
  fs.rmSync(legacyPath, { force: true });

  return { passphraseFile, protectedAtRest: true, encryptedPath };
};

export const initializeQsdmSignerSecretStore = () => {
  const {
    signerDir,
    keystorePath,
    passphraseFile: legacyPath,
  } = getQsdmDefaultLocalSignerPaths();
  const encryptedPath = getEncryptedPassphrasePath(signerDir);

  if (!fs.existsSync(keystorePath)) {
    return { configured: false, protectedAtRest: false };
  }

  if (!isQsdmProtectedSecretStorageAvailable()) {
    if (fs.existsSync(legacyPath)) {
      activateQsdmImportedSignerPaths({
        keystorePath,
        passphraseFile: legacyPath,
        sender: readKeystoreAddress(keystorePath),
      });
    }
    return {
      configured: fs.existsSync(legacyPath),
      protectedAtRest: false,
      reason: 'OS-protected secret storage is unavailable',
    };
  }

  let passphrase = '';
  if (fs.existsSync(encryptedPath)) {
    try {
      passphrase = safeStorage.decryptString(fs.readFileSync(encryptedPath));
    } catch {
      const quarantinePath = quarantineUnreadablePassphrase(encryptedPath);
      return {
        configured: false,
        protectedAtRest: true,
        reason:
          'The stored QSDM wallet passphrase could not be decrypted. Unlock the existing wallet again in Settings > Wallet.',
        quarantinePath,
      };
    }
  } else if (fs.existsSync(legacyPath)) {
    passphrase = fs.readFileSync(legacyPath, 'utf-8');
    writePrivate(encryptedPath, safeStorage.encryptString(passphrase));
    fs.rmSync(legacyPath, { force: true });
  }

  if (!passphrase) {
    return {
      configured: false,
      protectedAtRest: true,
      reason: 'The encrypted QSDM wallet passphrase was not found',
    };
  }

  const sessionPassphraseFile = createSessionPassphraseFile(passphrase);
  activateQsdmImportedSignerPaths({
    keystorePath,
    passphraseFile: sessionPassphraseFile,
    sender: readKeystoreAddress(keystorePath),
  });

  return {
    configured: true,
    protectedAtRest: true,
    encryptedPath,
    passphraseFile: sessionPassphraseFile,
  };
};

export const cleanupQsdmSignerSecretStore = () => {
  for (const sessionDir of sessionDirectories) {
    fs.rmSync(sessionDir, { recursive: true, force: true });
  }
  sessionDirectories.clear();
};

export const backupQsdmEncryptedPassphrase = (signerDir: string) => {
  const encryptedPath = getEncryptedPassphrasePath(signerDir);
  if (!fs.existsSync(encryptedPath)) return;
  fs.copyFileSync(encryptedPath, `${encryptedPath}.bak-${Date.now()}`);
};
