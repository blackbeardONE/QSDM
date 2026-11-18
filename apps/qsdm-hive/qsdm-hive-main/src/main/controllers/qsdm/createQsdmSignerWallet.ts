import { spawnSync } from 'child_process';
import fs from 'fs';
import os from 'os';
import path from 'path';

import { persistQsdmSignerPassphrase } from 'main/services/qsdmSignerSecretStore';
import { getQsdmTaskActionCliPath } from 'main/services/qsdmTaskActionSigner';
import { addQsdmWalletToStore } from 'main/services/qsdmWalletStore';

import {
  activateAddedQsdmWallet,
  assertQsdmWalletSwitchAllowed,
} from './qsdmWallets';

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

  assertQsdmWalletSwitchAllowed(payload.makeActive);

  const tmpDir = fs.mkdtempSync(
    path.join(os.tmpdir(), 'qsdm-hive-new-signer-')
  );
  const tmpKeystorePath = path.join(tmpDir, 'wallet.json');
  const tmpPassphrasePath = path.join(tmpDir, 'passphrase.txt');
  const tmpRecoveryPath = path.join(tmpDir, 'recovery-words.txt');

  try {
    writeFilePrivate(tmpPassphrasePath, passphrase);
    const createResult = runQsdmCli([
      'wallet',
      'new',
      '--out',
      tmpKeystorePath,
      '--passphrase-file',
      tmpPassphrasePath,
      '--recovery-out',
      tmpRecoveryPath,
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

    const recoveryWords = fs.readFileSync(tmpRecoveryPath, 'utf-8').trim();
    if (recoveryWords.split(/\s+/).length !== 24) {
      throw new Error(
        'QSDM CLI did not return a valid 24-word wallet recovery phrase'
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

    // 2026-11-19: a new wallet is ADDED to Hive's wallet list; with a wallet already in Hive it becomes active only
    // when asked (makeActive). Nothing is replaced.
    const added = addQsdmWalletToStore({
      keystorePath: tmpKeystorePath,
      label: payload.label,
      source: 'created',
      persistPassphrase: (walletDir) => {
        persistQsdmSignerPassphrase({ passphrase, signerDir: walletDir });
      },
    });
    const active = await activateAddedQsdmWallet({
      address: added.address,
      makeActive: payload.makeActive,
      hadActiveWallet: added.hadActiveWallet,
    });

    return {
      address: walletInfo.address,
      publicKey: walletInfo.public_key,
      keystorePath: active.keystorePath,
      passphraseFile: active.passphraseFile,
      recoveryWords,
      active: active.active,
    };
  } finally {
    fs.rmSync(tmpDir, { recursive: true, force: true });
  }
};
