/**
 * @jest-environment node
 */

import { spawnSync } from 'child_process';
import { dialog } from 'electron';
import fs from 'fs';
import os from 'os';
import path from 'path';

import { exportQsdmSignerRecoveryWords } from './exportQsdmSignerRecoveryWords';

import type { Event } from 'electron';

jest.mock('child_process', () => ({
  spawnSync: jest.fn(),
}));

jest.mock('electron', () => ({
  dialog: {
    showSaveDialog: jest.fn(),
  },
}));

let mockKeystorePath = '';
const mockSender =
  '13d786706accfbe77c5ddf6fc6757e1cca07bd01aff0cad3dcf9411d92cf11c9';

jest.mock('main/services/qsdmTaskActionSigner', () => ({
  getQsdmTaskActionCliPath: () => 'qsdmcli',
  getQsdmTaskActionKeystorePath: () => mockKeystorePath,
  getQsdmTaskActionSender: () => mockSender,
}));

const mockSpawnSync = spawnSync as jest.Mock;
const mockShowSaveDialog = dialog.showSaveDialog as jest.Mock;
let capturedPassphrase = '';

describe('exportQsdmSignerRecoveryWords', () => {
  let tmpDir = '';
  let outputPath = '';

  beforeEach(() => {
    tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'qsdm-recovery-export-'));
    mockKeystorePath = path.join(tmpDir, 'wallet.json');
    outputPath = path.join(tmpDir, 'recovery.txt');
    fs.writeFileSync(
      mockKeystorePath,
      JSON.stringify({ type: 'qsdm-keystore', address: mockSender })
    );
    mockShowSaveDialog.mockReset();
    mockShowSaveDialog.mockResolvedValue({
      canceled: false,
      filePath: outputPath,
    });
    mockSpawnSync.mockReset();
    capturedPassphrase = '';
    mockSpawnSync.mockImplementation((_cli: string, args: string[]) => {
      const passphrasePath = args[args.indexOf('--passphrase-file') + 1];
      const selectedOutput = args[args.indexOf('--out') + 1];
      capturedPassphrase = fs.readFileSync(passphrasePath, 'utf-8');
      fs.writeFileSync(selectedOutput, 'twenty four recovery words');
      return { status: 0, stdout: '', stderr: '' };
    });
  });

  afterEach(() => {
    fs.rmSync(tmpDir, { recursive: true, force: true });
  });

  it('exports recovery words through a private temporary passphrase file', async () => {
    const result = await exportQsdmSignerRecoveryWords({} as Event, {
      passphrase: 'correct horse battery staple',
    });

    expect(result).toEqual({
      exported: true,
      address: mockSender,
      recoveryBackupPath: outputPath,
    });
    expect(mockSpawnSync).toHaveBeenCalledWith(
      'qsdmcli',
      expect.arrayContaining([
        'wallet',
        'export-recovery',
        '--in',
        mockKeystorePath,
        '--out',
        outputPath,
      ]),
      expect.objectContaining({ windowsHide: true })
    );
    expect(capturedPassphrase).toBe('correct horse battery staple');
  });

  it('does not invoke qsdmcli when the save dialog is cancelled', async () => {
    mockShowSaveDialog.mockResolvedValue({ canceled: true });

    await expect(
      exportQsdmSignerRecoveryWords({} as Event, {
        passphrase: 'correct horse battery staple',
      })
    ).resolves.toEqual({ exported: false, address: mockSender });
    expect(mockSpawnSync).not.toHaveBeenCalled();
  });
});
