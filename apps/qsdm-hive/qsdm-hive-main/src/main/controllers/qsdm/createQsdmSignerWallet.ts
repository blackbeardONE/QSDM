import { spawnSync } from 'child_process';
import fs from 'fs';
import os from 'os';
import path from 'path';

import {
  activateQsdmImportedSignerPaths,
  getQsdmDefaultLocalSignerPaths,
  getQsdmTaskActionCliPath,
} from 'main/services/qsdmTaskActionSigner';
import {
  hasQsdmStoredPassphrase,
  persistQsdmSignerPassphrase,
} from 'main/services/qsdmSignerSecretStore';

import type { Event } from 'electron';
import type {
  QsdmSignerWalletCreateRequest,
  QsdmSignerWalletImportResponse,
} from 'models/api/qsdm';

type WalletShowResult = {
  address?: string;
  public_key?: string;
};

const getSpawnError = (result: ReturnType<typeof spawnSync>) => {
  if (result.error) return result.error.message;
  return result.stderr?.toString().trim() || result.stdout?.toString().trim();
};

const writeFilePrivate = (filePath: string, content: string) => {
  fs.writeFileSync(filePath, content, { mode: 0o600, flag: 'wx' });
  try {
    fs.chmodSync(filePath, 0o600);
  } catch {
    // Windows applies the user-profile ACL instead of POSIX file modes.
  }
};

const runQsdmCli = (args: string[]) => {
  const cliPath = getQsdmTaskActionCliPath();
  if (!cliPath) {
    throw new Error(
      'The native QSDM signer is unavailable. Reinstall the current QSDM Hive release.'
    );
  }

  return spawnSync(cliPath, args, {
    encoding: 'utf-8',
    timeout: 30000,
    windowsHide: true,
  });
};

export const createQsdmSignerWallet = async (
  _: Event,
  payload: QsdmSignerWalletCreateRequest
): Promise<QsdmSignerWalletImportResponse> => {
  const passphrase = payload?.passphrase;
  if (typeof passphrase !== 'string' || passphrase.length < 12) {
    throw new Error('QSDM wallet passphrase must be at least 12 characters');
  }

  const { signerDir, keystorePath, passphraseFile } =
    getQsdmDefaultLocalSignerPaths();
  if (fs.existsSync(keystorePath) || hasQsdmStoredPassphrase(signerDir)) {
    throw new Error(
      'A QSDM signer wallet already exists. Back it up before replacing it.'
    );
  }

  const tmpDir = fs.mkdtempSync(
    path.join(os.tmpdir(), 'qsdm-hive-new-signer-')
  );
  const tmpKeystorePath = path.join(tmpDir, 'wallet.json');
  const tmpPassphrasePath = path.join(tmpDir, 'passphrase.txt');

  try {
    writeFilePrivate(tmpPassphrasePath, passphrase);
    const createResult = runQsdmCli([
      'wallet',
      'new',
      '--out',
      tmpKeystorePath,
      '--passphrase-file',
      tmpPassphrasePath,
    ]);
    if (createResult.status !== 0) {
      throw new Error(
        `QSDM wallet creation failed: ${
          getSpawnError(createResult) || 'native signer exited unexpectedly'
        }`
      );
    }

    const showResult = runQsdmCli([
      'wallet',
      'show',
      '--in',
      tmpKeystorePath,
      '--json',
    ]);
    if (showResult.status !== 0) {
      throw new Error(
        `QSDM wallet validation failed: ${getSpawnError(showResult)}`
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
        'Created QSDM wallet is missing its address or public key'
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
        `Created QSDM wallet could not be unlocked: ${getSpawnError(
          inspectResult
        )}`
      );
    }

    fs.mkdirSync(signerDir, { recursive: true, mode: 0o700 });
    try {
      fs.chmodSync(signerDir, 0o700);
    } catch {
      // Windows applies the user-profile ACL instead of POSIX directory modes.
    }

    try {
      writeFilePrivate(keystorePath, fs.readFileSync(tmpKeystorePath, 'utf-8'));
      const storedPassphrase = persistQsdmSignerPassphrase({
        passphrase,
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
    } catch (error) {
      fs.rmSync(keystorePath, { force: true });
      fs.rmSync(passphraseFile, { force: true });
      throw error;
    }
  } finally {
    fs.rmSync(tmpDir, { recursive: true, force: true });
  }
};
