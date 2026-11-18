import { spawnSync } from 'child_process';
import { Event } from 'electron';
import fs from 'fs';
import os from 'os';
import path from 'path';

import { persistQsdmSignerPassphrase } from 'main/services/qsdmSignerSecretStore';
import { getQsdmTaskActionCliPath } from 'main/services/qsdmTaskActionSigner';
import { addQsdmWalletToStore } from 'main/services/qsdmWalletStore';
import {
  QsdmSignerWalletImportRequest,
  QsdmSignerWalletImportResponse,
} from 'models/api/qsdm';

import {
  activateAddedQsdmWallet,
  assertQsdmWalletSwitchAllowed,
} from './qsdmWallets';

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

  assertQsdmWalletSwitchAllowed(payload.makeActive);

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

    // 2026-11-19: an import ADDS the wallet to Hive's wallet list; it never replaces the active wallet (Hive 1.4.24
    // overwrote it and kept only a .bak copy). It becomes active only when Hive has no wallet yet or when asked.
    const added = addQsdmWalletToStore({
      keystorePath: tmpKeystorePath,
      label: payload.label,
      source: 'imported',
      persistPassphrase: (walletDir) => {
        persistQsdmSignerPassphrase({
          passphrase: payload.passphrase,
          signerDir: walletDir,
        });
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
      active: active.active,
    };
  } finally {
    fs.rmSync(tmpDir, { recursive: true, force: true });
  }
};
