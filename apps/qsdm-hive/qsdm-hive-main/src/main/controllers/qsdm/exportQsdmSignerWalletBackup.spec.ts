/**
 * @jest-environment node
 */

import fs from 'fs';
import os from 'os';
import path from 'path';
import { Event } from 'electron';

const mockShowOpenDialog = jest.fn();
let mockKeystorePath = '';
let mockPassphraseFile = '';
let mockSender = '13d786706accfbe77c5ddf6fc6757e1cca07bd01aff0cad3dcf9411d92cf11c9';

jest.mock('electron', () => ({
  dialog: {
    showOpenDialog: mockShowOpenDialog,
  },
}));

jest.mock('main/services/qsdmTaskActionSigner', () => ({
  getQsdmTaskActionKeystorePath: () => mockKeystorePath,
  getQsdmTaskActionPassphraseFile: () => mockPassphraseFile,
  getQsdmTaskActionSender: () => mockSender,
}));

import { exportQsdmSignerWalletBackup } from './exportQsdmSignerWalletBackup';

describe('exportQsdmSignerWalletBackup', () => {
  let sourceDir = '';
  let backupDir = '';

  beforeEach(() => {
    sourceDir = fs.mkdtempSync(path.join(os.tmpdir(), 'qsdm-signer-source-'));
    backupDir = fs.mkdtempSync(path.join(os.tmpdir(), 'qsdm-signer-backup-'));
    mockKeystorePath = path.join(sourceDir, 'wallet.json');
    mockPassphraseFile = path.join(sourceDir, 'passphrase.txt');
    fs.writeFileSync(
      mockKeystorePath,
      JSON.stringify({ type: 'qsdm-keystore', address: mockSender })
    );
    fs.writeFileSync(mockPassphraseFile, 'correct horse battery staple');
    mockShowOpenDialog.mockReset();
    mockShowOpenDialog.mockResolvedValue({
      canceled: false,
      filePaths: [backupDir],
    });
  });

  afterEach(() => {
    fs.rmSync(sourceDir, { recursive: true, force: true });
    fs.rmSync(backupDir, { recursive: true, force: true });
  });

  it('copies the active QSDM keystore and passphrase into a backup folder', async () => {
    const response = await exportQsdmSignerWalletBackup({} as Event);

    expect(response.exported).toBe(true);
    expect(response.address).toBe(mockSender);
    expect(response.keystoreBackupPath).toContain(backupDir);
    expect(response.passphraseBackupPath).toContain(backupDir);
    expect(
      fs.readFileSync(response.keystoreBackupPath as string, 'utf-8')
    ).toContain('qsdm-keystore');
    expect(
      fs.readFileSync(response.passphraseBackupPath as string, 'utf-8')
    ).toBe('correct horse battery staple');
  });

  it('returns exported=false when the folder picker is cancelled', async () => {
    mockShowOpenDialog.mockResolvedValue({ canceled: true, filePaths: [] });

    const response = await exportQsdmSignerWalletBackup({} as Event);

    expect(response).toEqual({ exported: false, address: mockSender });
  });
});
