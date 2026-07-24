import { spawnSync } from 'child_process';
import { dialog } from 'electron';
import fs from 'fs';
import os from 'os';
import path from 'path';

import {
  getQsdmTaskActionCliPath,
  getQsdmTaskActionKeystorePath,
  getQsdmTaskActionSender,
} from 'main/services/qsdmTaskActionSigner';

import type { Event } from 'electron';
import type {
  QsdmSignerWalletRecoveryExportRequest,
  QsdmSignerWalletRecoveryExportResponse,
} from 'models/api/qsdm';

const safeFilePart = (value: string) =>
  value.replace(/[^a-zA-Z0-9._-]/g, '').slice(0, 24) || 'wallet';

export const exportQsdmSignerRecoveryWords = async (
  _: Event,
  payload: QsdmSignerWalletRecoveryExportRequest
): Promise<QsdmSignerWalletRecoveryExportResponse> => {
  if (typeof payload?.passphrase !== 'string' || !payload.passphrase) {
    throw new Error('QSDM wallet passphrase is required');
  }
  const cliPath = getQsdmTaskActionCliPath();
  const keystorePath = getQsdmTaskActionKeystorePath();
  const address = getQsdmTaskActionSender();
  if (!cliPath) {
    throw new Error(
      'The native QSDM signer is unavailable. Reinstall the current QSDM Hive release.'
    );
  }
  if (!keystorePath || !fs.existsSync(keystorePath)) {
    throw new Error('QSDM keystore JSON was not found');
  }

  const selection = await dialog.showSaveDialog({
    title: 'Save QSDM Recovery Words',
    buttonLabel: 'Save Recovery Words',
    defaultPath: `qsdm-recovery-${safeFilePart(address)}.txt`,
    filters: [{ name: 'Text file', extensions: ['txt'] }],
  });
  if (selection.canceled || !selection.filePath) {
    return { exported: false, address: address || undefined };
  }

  const tmpDir = fs.mkdtempSync(
    path.join(os.tmpdir(), 'qsdm-hive-export-recovery-')
  );
  const passphrasePath = path.join(tmpDir, 'passphrase.txt');
  try {
    fs.writeFileSync(passphrasePath, payload.passphrase, {
      mode: 0o600,
      flag: 'wx',
    });
    const result = spawnSync(
      cliPath,
      [
        'wallet',
        'export-recovery',
        '--in',
        keystorePath,
        '--out',
        selection.filePath,
        '--passphrase-file',
        passphrasePath,
        '--force',
      ],
      {
        encoding: 'utf-8',
        timeout: 30000,
        windowsHide: true,
      }
    );
    if (result.status !== 0) {
      const detail =
        result.error?.message ||
        result.stderr?.toString().trim() ||
        result.stdout?.toString().trim() ||
        'native signer exited unexpectedly';
      throw new Error(`QSDM recovery export failed: ${detail}`);
    }
    try {
      fs.chmodSync(selection.filePath, 0o600);
    } catch {
      // Windows protects files with the current user's profile ACL.
    }
    return {
      exported: true,
      address: address || undefined,
      recoveryBackupPath: selection.filePath,
    };
  } finally {
    fs.rmSync(tmpDir, { recursive: true, force: true });
  }
};
