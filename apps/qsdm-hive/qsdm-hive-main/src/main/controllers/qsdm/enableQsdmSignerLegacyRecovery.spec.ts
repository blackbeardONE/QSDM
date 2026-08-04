/**
 * @jest-environment node
 */

import { execFile } from 'child_process';
import { dialog } from 'electron';
import fs from 'fs';
import os from 'os';
import path from 'path';

import { enableQsdmSignerLegacyRecovery } from './enableQsdmSignerLegacyRecovery';

import type { Event } from 'electron';

jest.mock('child_process', () => ({
  execFile: jest.fn(),
}));

jest.mock('electron', () => ({
  dialog: {
    showSaveDialog: jest.fn(),
  },
}));

let mockKeystorePath = '';
const mockAddress =
  '13d786706accfbe77c5ddf6fc6757e1cca07bd01aff0cad3dcf9411d92cf11c9';
const legacyPassphrase = ['existing', 'wallet', 'passphrase'].join(' ');

jest.mock('main/services/qsdmTaskActionSigner', () => ({
  getQsdmTaskActionCliPath: () => 'qsdmcli',
  getQsdmTaskActionKeystorePath: () => mockKeystorePath,
  getQsdmTaskActionSender: () => mockAddress,
  getQsdmTaskActionSignerStatus: () => ({
    ready: true,
    recoveryEnabled: false,
  }),
}));

jest.mock('main/services/qsdmTaskActions', () => ({
  resolveQsdmTaskActionApiUrl: () => 'https://core.example/api/v1',
}));

const mockExecFile = execFile as unknown as jest.Mock;
const mockShowSaveDialog = dialog.showSaveDialog as jest.Mock;

describe('enableQsdmSignerLegacyRecovery', () => {
  let tmpDir = '';
  let recoveryPath = '';

  beforeEach(() => {
    tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'qsdm-enable-recovery-'));
    mockKeystorePath = path.join(tmpDir, 'wallet.json');
    recoveryPath = path.join(tmpDir, 'recovery.txt');
    fs.writeFileSync(mockKeystorePath, '{"type":"qsdm-keystore"}');
    mockShowSaveDialog.mockReset();
    mockShowSaveDialog.mockResolvedValue({
      canceled: false,
      filePath: recoveryPath,
    });
    mockExecFile.mockReset();
    mockExecFile.mockImplementation(
      (
        _cli: string,
        args: string[],
        _options: unknown,
        callback: (error: Error | null, stdout: string, stderr: string) => void
      ) => {
        const passphrasePath = args[args.indexOf('--passphrase-file') + 1];
        expect(fs.readFileSync(passphrasePath, 'utf-8')).toBe(
          legacyPassphrase
        );
        callback(
          null,
          `${mockAddress}\n`,
          `original keystore backup: ${mockKeystorePath}.pre-recovery.bak\n`
        );
      }
    );
  });

  afterEach(() => {
    fs.rmSync(tmpDir, { recursive: true, force: true });
  });

  it('enables network recovery without replacing the active address', async () => {
    const result = await enableQsdmSignerLegacyRecovery({} as Event, {
      passphrase: legacyPassphrase,
    });

    expect(result).toEqual({
      enabled: true,
      address: mockAddress,
      recoveryBackupPath: recoveryPath,
      keystoreBackupPath: `${mockKeystorePath}.pre-recovery.bak`,
    });
    expect(mockExecFile).toHaveBeenCalledWith(
      'qsdmcli',
      expect.arrayContaining([
        'wallet',
        'enable-recovery',
        '--in',
        mockKeystorePath,
        '--recovery-out',
        recoveryPath,
        '--api-url',
        'https://core.example/api/v1',
      ]),
      expect.objectContaining({ timeout: 120000, windowsHide: true }),
      expect.any(Function)
    );
  });

  it('leaves the wallet unchanged when the save dialog is cancelled', async () => {
    mockShowSaveDialog.mockResolvedValue({ canceled: true });

    await expect(
      enableQsdmSignerLegacyRecovery({} as Event, {
        passphrase: legacyPassphrase,
      })
    ).resolves.toEqual({ enabled: false, address: mockAddress });
    expect(mockExecFile).not.toHaveBeenCalled();
  });
});
