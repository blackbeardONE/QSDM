import { execFile } from 'child_process';
import fs from 'fs';
import os from 'os';
import path from 'path';

import {
  backupQsdmEncryptedPassphrase,
  persistQsdmSignerPassphrase,
} from 'main/services/qsdmSignerSecretStore';
import { resolveQsdmTaskActionApiUrl } from 'main/services/qsdmTaskActions';
import {
  activateQsdmImportedSignerPaths,
  getQsdmDefaultLocalSignerPaths,
  getQsdmTaskActionCliPath,
} from 'main/services/qsdmTaskActionSigner';

import type { Event } from 'electron';
import type {
  QsdmSignerWalletImportResponse,
  QsdmSignerWalletRecoveryRequest,
} from 'models/api/qsdm';

type WalletShowResult = {
  address?: string;
  public_key?: string;
};

const writeFilePrivate = (filePath: string, content: string) => {
  fs.writeFileSync(filePath, content, { mode: 0o600, flag: 'wx' });
  try {
    fs.chmodSync(filePath, 0o600);
  } catch {
    // Windows protects files with the current user's profile ACL.
  }
};

const backupExistingFile = (filePath: string) => {
  if (fs.existsSync(filePath)) {
    fs.copyFileSync(filePath, `${filePath}.bak-${Date.now()}`);
  }
};

type QsdmCliResult = {
  status: number | null;
  stdout: string;
  stderr: string;
  error?: Error;
};

const getSpawnError = (result: QsdmCliResult) =>
  result.error?.message ||
  result.stderr?.toString().trim() ||
  result.stdout?.toString().trim();

const runQsdmCli = (args: string[]) => {
  const cliPath = getQsdmTaskActionCliPath();
  if (!cliPath) {
    throw new Error(
      'The native QSDM signer is unavailable. Reinstall the current QSDM Hive release.'
    );
  }
  return new Promise<QsdmCliResult>((resolve) => {
    execFile(
      cliPath,
      args,
      {
        encoding: 'utf-8',
        timeout: 120000,
        windowsHide: true,
        maxBuffer: 1024 * 1024,
      },
      (error, stdout, stderr) => {
        const errorCode = (error as (Error & { code?: number | string }) | null)
          ?.code;
        resolve({
          status: error
            ? typeof errorCode === 'number'
              ? errorCode
              : null
            : 0,
          stdout,
          stderr,
          error: error || undefined,
        });
      }
    );
  });
};

export const restoreQsdmSignerWallet = async (
  _: Event,
  payload: QsdmSignerWalletRecoveryRequest
): Promise<QsdmSignerWalletImportResponse> => {
  const recoveryWords = payload?.recoveryWords?.trim();
  const recoveryType = payload?.recoveryType || 'native';
  if (!recoveryWords || recoveryWords.split(/\s+/).length !== 24) {
    throw new Error('Enter all 24 QSDM Recovery Words');
  }
  if (
    typeof payload?.passphrase !== 'string' ||
    payload.passphrase.length < 12
  ) {
    throw new Error(
      'New QSDM wallet passphrase must be at least 12 characters'
    );
  }

  const tmpDir = fs.mkdtempSync(
    path.join(os.tmpdir(), 'qsdm-hive-restore-signer-')
  );
  const tmpKeystorePath = path.join(tmpDir, 'wallet.json');
  const tmpPassphrasePath = path.join(tmpDir, 'passphrase.txt');
  const tmpRecoveryPath = path.join(tmpDir, 'recovery-words.txt');

  try {
    writeFilePrivate(tmpPassphrasePath, payload.passphrase);
    writeFilePrivate(tmpRecoveryPath, recoveryWords);
    const restoreArgs = [
      'wallet',
      recoveryType === 'legacy' ? 'restore-legacy' : 'restore',
      '--out',
      tmpKeystorePath,
      '--passphrase-file',
      tmpPassphrasePath,
      '--recovery-file',
      tmpRecoveryPath,
    ];
    if (recoveryType === 'legacy') {
      restoreArgs.push('--api-url', resolveQsdmTaskActionApiUrl());
    }
    const restoreResult = await runQsdmCli(restoreArgs);
    if (restoreResult.status !== 0) {
      throw new Error(
        `QSDM wallet restore failed: ${
          getSpawnError(restoreResult) || 'native signer exited unexpectedly'
        }`
      );
    }

    const showResult = await runQsdmCli([
      'wallet',
      'show',
      '--in',
      tmpKeystorePath,
      '--json',
    ]);
    if (showResult.status !== 0) {
      throw new Error(
        `Restored QSDM wallet validation failed: ${getSpawnError(showResult)}`
      );
    }
    let walletInfo: WalletShowResult;
    try {
      walletInfo = JSON.parse(showResult.stdout.trim()) as WalletShowResult;
    } catch {
      throw new Error('QSDM CLI returned an unreadable wallet description');
    }
    if (!walletInfo.address || !walletInfo.public_key) {
      throw new Error('Restored QSDM wallet is missing key metadata');
    }

    const inspectResult = await runQsdmCli([
      'wallet',
      'inspect',
      '--in',
      tmpKeystorePath,
      '--passphrase-file',
      tmpPassphrasePath,
    ]);
    if (inspectResult.status !== 0) {
      throw new Error(
        `Restored QSDM wallet could not be unlocked: ${getSpawnError(
          inspectResult
        )}`
      );
    }

    const { signerDir, keystorePath, passphraseFile } =
      getQsdmDefaultLocalSignerPaths();
    fs.mkdirSync(signerDir, { recursive: true, mode: 0o700 });
    backupExistingFile(keystorePath);
    backupExistingFile(passphraseFile);
    backupQsdmEncryptedPassphrase(signerDir);
    fs.copyFileSync(tmpKeystorePath, keystorePath);
    try {
      fs.chmodSync(keystorePath, 0o600);
    } catch {
      // Windows protects files with the current user's profile ACL.
    }
    const storedPassphrase = persistQsdmSignerPassphrase({
      passphrase: payload.passphrase,
      signerDir,
    });
    activateQsdmImportedSignerPaths({
      keystorePath,
      passphraseFile: storedPassphrase.passphraseFile,
      sender: walletInfo.address,
    });

    return {
      address: walletInfo.address,
      publicKey: walletInfo.public_key,
      keystorePath,
      passphraseFile: storedPassphrase.passphraseFile,
    };
  } finally {
    fs.rmSync(tmpDir, { recursive: true, force: true });
  }
};
