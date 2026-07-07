import { dialog } from 'electron';
import type { Event } from 'electron';
import fs from 'fs';
import path from 'path';

import {
  getQsdmTaskActionKeystorePath,
  getQsdmTaskActionPassphraseFile,
  getQsdmTaskActionSender,
} from 'main/services/qsdmTaskActionSigner';
import { QsdmSignerWalletBackupResponse } from 'models/api/qsdm';

const requireFile = (filePath: string, label: string) => {
  if (!filePath || !fs.existsSync(filePath) || !fs.statSync(filePath).isFile()) {
    throw new Error(`${label} was not found. Import or create a QSDM signer first.`);
  }
};

const safeFilePart = (value: string) =>
  value.replace(/[^a-zA-Z0-9._-]/g, '').slice(0, 24) || 'signer';

const copyPrivate = (source: string, destination: string) => {
  fs.copyFileSync(source, destination);
  try {
    fs.chmodSync(destination, 0o600);
  } catch {
    // Windows ignores POSIX modes; the user profile ACL still protects the file.
  }
};

export const exportQsdmSignerWalletBackup = async (
  _: Event
): Promise<QsdmSignerWalletBackupResponse> => {
  const keystorePath = getQsdmTaskActionKeystorePath();
  const passphraseFile = getQsdmTaskActionPassphraseFile();
  const address = getQsdmTaskActionSender();

  requireFile(keystorePath, 'QSDM keystore JSON');
  requireFile(passphraseFile, 'QSDM wallet passphrase file');

  const selection = await dialog.showOpenDialog({
    title: 'Choose QSDM wallet backup folder',
    buttonLabel: 'Back Up Wallet',
    properties: ['openDirectory', 'createDirectory'],
  });

  if (selection.canceled || !selection.filePaths[0]) {
    return { exported: false, address: address || undefined };
  }

  const backupDir = selection.filePaths[0];
  const stamp = new Date().toISOString().replace(/[:.]/g, '-');
  const suffix = `${safeFilePart(address)}-${stamp}`;
  const keystoreBackupPath = path.join(backupDir, `qsdm-wallet-${suffix}.json`);
  const passphraseBackupPath = path.join(
    backupDir,
    `qsdm-wallet-${suffix}.passphrase.txt`
  );

  copyPrivate(keystorePath, keystoreBackupPath);
  copyPrivate(passphraseFile, passphraseBackupPath);

  return {
    exported: true,
    address: address || undefined,
    keystoreBackupPath,
    passphraseBackupPath,
  };
};
