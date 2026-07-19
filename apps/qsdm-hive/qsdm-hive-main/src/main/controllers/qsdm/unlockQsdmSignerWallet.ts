import { spawnSync } from 'child_process';
import { Event } from 'electron';
import fs from 'fs';
import os from 'os';
import path from 'path';

import {
  backupQsdmEncryptedPassphrase,
  persistQsdmSignerPassphrase,
} from 'main/services/qsdmSignerSecretStore';
import {
  activateQsdmImportedSignerPaths,
  getQsdmDefaultLocalSignerPaths,
  getQsdmTaskActionCliPath,
} from 'main/services/qsdmTaskActionSigner';
import {
  QsdmSignerWalletImportResponse,
  QsdmSignerWalletUnlockRequest,
} from 'models/api/qsdm';

type WalletShowResult = {
  address?: string;
  public_key?: string;
};

const getSpawnError = (result: ReturnType<typeof spawnSync>) => {
  if (result.error) return result.error.message;
  return result.stderr?.toString().trim() || result.stdout?.toString().trim();
};

export const unlockQsdmSignerWallet = async (
  _: Event,
  payload: QsdmSignerWalletUnlockRequest
): Promise<QsdmSignerWalletImportResponse> => {
  const passphrase = payload?.passphrase;
  if (typeof passphrase !== 'string' || !passphrase) {
    throw new Error('QSDM wallet passphrase is required');
  }

  const { signerDir, keystorePath } = getQsdmDefaultLocalSignerPaths();
  if (!fs.existsSync(keystorePath)) {
    throw new Error('No existing QSDM wallet was found on this device');
  }

  const cliPath = getQsdmTaskActionCliPath();
  if (!cliPath) {
    throw new Error('QSDM CLI is unavailable');
  }

  const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'qsdm-hive-unlock-'));
  const tmpPassphrasePath = path.join(tmpDir, 'passphrase.txt');

  try {
    fs.writeFileSync(tmpPassphrasePath, passphrase, { mode: 0o600 });

    const showResult = spawnSync(
      cliPath,
      ['wallet', 'show', '--in', keystorePath, '--json'],
      { encoding: 'utf-8', timeout: 15000, windowsHide: true }
    );
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

    const inspectResult = spawnSync(
      cliPath,
      [
        'wallet',
        'inspect',
        '--in',
        keystorePath,
        '--passphrase-file',
        tmpPassphrasePath,
      ],
      { encoding: 'utf-8', timeout: 15000, windowsHide: true }
    );
    if (inspectResult.status !== 0) {
      throw new Error(
        `QSDM passphrase did not unlock this keystore: ${getSpawnError(
          inspectResult
        )}`
      );
    }

    backupQsdmEncryptedPassphrase(signerDir);
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
  } finally {
    fs.rmSync(tmpDir, { recursive: true, force: true });
  }
};
