import { Event } from 'electron';
import fs from 'fs';
import os from 'os';
import path from 'path';
import { spawnSync } from 'child_process';

import {
  activateQsdmImportedSignerPaths,
  getQsdmDefaultLocalSignerPaths,
  getQsdmTaskActionCliPath,
} from 'main/services/qsdmTaskActionSigner';
import {
  QsdmSignerWalletImportRequest,
  QsdmSignerWalletImportResponse,
} from 'models/api/qsdm';

type WalletShowResult = {
  address?: string;
  public_key?: string;
};

const assertString = (value: unknown, message: string) => {
  if (typeof value !== 'string' || !value.trim()) {
    throw new Error(message);
  }
};

const getSpawnError = (result: ReturnType<typeof spawnSync>) => {
  if (result.error) return result.error.message;
  return result.stderr?.toString().trim() || result.stdout?.toString().trim();
};

const writeFilePrivate = (filePath: string, content: string) => {
  fs.writeFileSync(filePath, content, { mode: 0o600 });
};

const backupExistingFile = (filePath: string) => {
  if (!fs.existsSync(filePath)) return;

  const backupPath = `${filePath}.bak-${Date.now()}`;
  fs.copyFileSync(filePath, backupPath);
};

const runQsdmCli = (args: string[]) =>
  spawnSync(getQsdmTaskActionCliPath(), args, {
    encoding: 'utf-8',
    timeout: 15000,
    windowsHide: true,
  });

export const importQsdmSignerWallet = async (
  _: Event,
  payload: QsdmSignerWalletImportRequest
): Promise<QsdmSignerWalletImportResponse> => {
  assertString(payload?.keystoreJson, 'QSDM keystore JSON is required');
  assertString(payload?.passphrase, 'QSDM wallet passphrase is required');

  let parsedKeystore: unknown;
  try {
    parsedKeystore = JSON.parse(payload.keystoreJson);
  } catch {
    throw new Error('QSDM keystore must be valid JSON');
  }

  if (
    !parsedKeystore ||
    typeof parsedKeystore !== 'object' ||
    (parsedKeystore as { type?: string }).type !== 'qsdm-keystore'
  ) {
    throw new Error('Selected file is not a QSDM keystore JSON');
  }

  const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'qsdm-hive-signer-'));
  const tmpKeystorePath = path.join(tmpDir, 'wallet.json');
  const tmpPassphrasePath = path.join(tmpDir, 'passphrase.txt');

  try {
    writeFilePrivate(tmpKeystorePath, payload.keystoreJson);
    writeFilePrivate(tmpPassphrasePath, payload.passphrase);

    const showResult = runQsdmCli([
      'wallet',
      'show',
      '--in',
      tmpKeystorePath,
      '--json',
    ]);
    if (showResult.status !== 0) {
      throw new Error(
        `QSDM keystore validation failed: ${getSpawnError(showResult)}`
      );
    }

    let walletInfo: WalletShowResult;
    try {
      walletInfo = JSON.parse(showResult.stdout.trim()) as WalletShowResult;
    } catch {
      throw new Error('QSDM CLI returned an unreadable wallet description');
    }

    if (!walletInfo.address || !walletInfo.public_key) {
      throw new Error(
        'QSDM keystore is missing address or public key metadata'
      );
    }

    const inspectResult = runQsdmCli([
      'wallet',
      'inspect',
      '--in',
      tmpKeystorePath,
      '--passphrase-file',
      tmpPassphrasePath,
    ]);
    if (inspectResult.status !== 0) {
      throw new Error(
        `QSDM passphrase did not unlock this keystore: ${getSpawnError(
          inspectResult
        )}`
      );
    }

    const { signerDir, keystorePath, passphraseFile } =
      getQsdmDefaultLocalSignerPaths();
    fs.mkdirSync(signerDir, { recursive: true, mode: 0o700 });
    backupExistingFile(keystorePath);
    backupExistingFile(passphraseFile);
    writeFilePrivate(keystorePath, payload.keystoreJson);
    writeFilePrivate(passphraseFile, payload.passphrase);

    activateQsdmImportedSignerPaths({
      keystorePath,
      passphraseFile,
      sender: walletInfo.address,
    });

    return {
      address: walletInfo.address,
      publicKey: walletInfo.public_key,
      keystorePath,
      passphraseFile,
    };
  } finally {
    fs.rmSync(tmpDir, { recursive: true, force: true });
  }
};
