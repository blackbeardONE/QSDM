import { execFile } from 'child_process';
import { dialog } from 'electron';
import fs from 'fs';
import os from 'os';
import path from 'path';

import { resolveQsdmTaskActionApiUrl } from 'main/services/qsdmTaskActions';
import {
  getQsdmTaskActionCliPath,
  getQsdmTaskActionKeystorePath,
  getQsdmTaskActionSender,
  getQsdmTaskActionSignerStatus,
} from 'main/services/qsdmTaskActionSigner';

import type { Event } from 'electron';
import type {
  QsdmSignerLegacyRecoveryEnableRequest,
  QsdmSignerLegacyRecoveryEnableResponse,
} from 'models/api/qsdm';

const safeFilePart = (value: string) =>
  value.replace(/[^a-zA-Z0-9._-]/g, '').slice(0, 24) || 'wallet';

const writeFilePrivate = (filePath: string, content: string) => {
  fs.writeFileSync(filePath, content, { mode: 0o600, flag: 'wx' });
  try {
    fs.chmodSync(filePath, 0o600);
  } catch {
    // Windows protects files with the current user's profile ACL.
  }
};

type QsdmCliResult = {
  status: number | null;
  stdout: string;
  stderr: string;
  error?: Error;
};

const runQsdmCli = (cliPath: string, args: string[]) =>
  new Promise<QsdmCliResult>((resolve) => {
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

const getSpawnError = (result: QsdmCliResult) =>
  result.error?.message ||
  result.stderr?.toString().trim() ||
  result.stdout?.toString().trim() ||
  'native signer exited unexpectedly';

const findKeystoreBackupPath = (stderr: string) => {
  const match = stderr.match(/^original keystore backup:\s*(.+)$/m);
  return match?.[1]?.trim();
};

const readKeystoreAddress = (keystorePath: string) => {
  const stat = fs.statSync(keystorePath);
  if (!stat.isFile() || stat.size > 2 * 1024 * 1024) {
    throw new Error('The configured QSDM keystore is not a valid wallet file');
  }
  const parsed = JSON.parse(fs.readFileSync(keystorePath, 'utf-8')) as {
    address?: unknown;
  };
  if (
    typeof parsed.address !== 'string' ||
    !/^[0-9a-f]{64}$/.test(parsed.address)
  ) {
    throw new Error('The configured QSDM keystore has an invalid address');
  }
  return parsed.address;
};

export const enableQsdmSignerLegacyRecovery = async (
  _: Event,
  payload: QsdmSignerLegacyRecoveryEnableRequest
): Promise<QsdmSignerLegacyRecoveryEnableResponse> => {
  if (typeof payload?.passphrase !== 'string' || !payload.passphrase) {
    throw new Error('Existing QSDM wallet passphrase is required');
  }

  const cliPath = getQsdmTaskActionCliPath();
  const keystorePath = getQsdmTaskActionKeystorePath();
  const address = getQsdmTaskActionSender();
  const signer = getQsdmTaskActionSignerStatus();
  if (!cliPath) {
    throw new Error(
      'The native QSDM signer is unavailable. Reinstall the current QSDM Hive release.'
    );
  }
  if (
    !signer.ready ||
    !address ||
    !keystorePath ||
    !fs.existsSync(keystorePath)
  ) {
    throw new Error('Unlock the existing QSDM wallet before enabling recovery');
  }
  if (signer.recoveryEnabled) {
    throw new Error(
      'This wallet already has recovery enabled. Use Export Words instead.'
    );
  }
  const keystoreAddress = readKeystoreAddress(keystorePath);
  if (keystoreAddress !== address) {
    throw new Error(
      `The active signer address does not match the configured keystore. Expected ${address}, found ${keystoreAddress}. Unlock or import the correct wallet before enabling recovery.`
    );
  }

  const selection = await dialog.showSaveDialog({
    title: 'Save 24 QSDM Recovery Words',
    buttonLabel: 'Enable and Save Recovery Words',
    defaultPath: `qsdm-recovery-${safeFilePart(address)}.txt`,
    filters: [{ name: 'Text file', extensions: ['txt'] }],
  });
  if (selection.canceled || !selection.filePath) {
    return { enabled: false, address };
  }

  const tmpDir = fs.mkdtempSync(
    path.join(os.tmpdir(), 'qsdm-hive-enable-recovery-')
  );
  const passphrasePath = path.join(tmpDir, 'passphrase.txt');
  try {
    writeFilePrivate(passphrasePath, payload.passphrase);
    const result = await runQsdmCli(cliPath, [
      'wallet',
      'enable-recovery',
      '--in',
      keystorePath,
      '--passphrase-file',
      passphrasePath,
      '--recovery-out',
      selection.filePath,
      '--expected-address',
      address,
      '--api-url',
      resolveQsdmTaskActionApiUrl(),
      '--confirm-timeout',
      '90s',
      '--force',
    ]);
    if (result.status !== 0) {
      throw new Error(
        `QSDM recovery activation failed: ${getSpawnError(result)}`
      );
    }

    const reportedAddress = result.stdout
      ?.toString()
      .trim()
      .split(/\r?\n/)
      .pop();
    if (reportedAddress !== address) {
      throw new Error(
        'QSDM recovery activation returned a different wallet address. The original wallet backup was retained.'
      );
    }
    try {
      fs.chmodSync(selection.filePath, 0o600);
    } catch {
      // Windows protects files with the current user's profile ACL.
    }

    return {
      enabled: true,
      address,
      recoveryBackupPath: selection.filePath,
      keystoreBackupPath: findKeystoreBackupPath(
        result.stderr?.toString() || ''
      ),
    };
  } finally {
    fs.rmSync(tmpDir, { recursive: true, force: true });
  }
};
