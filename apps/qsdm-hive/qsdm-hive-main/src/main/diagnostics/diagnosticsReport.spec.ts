/**
 * @jest-environment node
 */
/* eslint-disable @cspell/spellchecker */
import crypto from 'crypto';
import { app, dialog, shell } from 'electron';
import fs from 'fs';
import os from 'os';
import path from 'path';

import { recordCrash } from './crashRecorder';
import {
  buildDiagnosticsReport,
  diagnosticsMenuItems,
  OPEN_DIAGNOSTICS_FOLDER_LABEL,
  openDiagnosticsFolder,
  reportFileName,
  SAVE_DIAGNOSTICS_REPORT_LABEL,
  saveDiagnosticsReport,
} from './diagnosticsReport';

import type { BrowserWindow } from 'electron';

jest.mock('electron', () => ({
  app: {
    getPath: jest.fn(),
    getName: jest.fn(() => 'QSDM Hive'),
    getVersion: jest.fn(() => '1.4.22'),
    isPackaged: true,
    getGPUInfo: jest.fn(),
  },
  dialog: { showSaveDialog: jest.fn(), showMessageBox: jest.fn() },
  shell: { openPath: jest.fn(), showItemInFolder: jest.fn() },
}));

let mockAppDataDir = '';
jest.mock('../node/helpers/getAppDataPath', () => ({
  getAppDataPath: () => mockAppDataDir,
}));

const PHRASE =
  'legal winner thank year wave sausage worth useful legal winner thank yellow';
const PRIVATE_KEY_HEX = crypto
  .createHash('sha256')
  .update('report key')
  .digest('hex');
const base64url = (value: string) => Buffer.from(value).toString('base64url');
const JWT_TOKEN = [
  base64url(JSON.stringify({ alg: 'HS256', typ: 'JWT' })),
  base64url(JSON.stringify({ sub: 'hive-account-7' })),
  base64url('signature-bytes-for-the-test'),
].join('.');
const EMAIL = 'alice@example.com';
const PASSWORD = 'pw-Correct-Horse-1';
const HOME = os.homedir();
const SECRETS = [
  PHRASE,
  'sausage',
  PRIVATE_KEY_HEX,
  JWT_TOKEN,
  EMAIL,
  PASSWORD,
  'abc123secret',
  HOME,
];

const GPU_INFO = {
  gpuDevice: [{ active: true, vendorId: 4318, deviceId: 9348 }],
  auxAttributes: { glRenderer: 'ANGLE (NVIDIA)', sessionToken: 'abc123secret' },
  machineModelName: 'Desktop',
};

const NOW = new Date('2026-11-03T06:00:00.000Z');
const CRASH_NAMES = [
  'crash-2026-11-02T00-00-00.000Z-0123456789ab.json',
  'crash-2026-11-03T00-00-00.000Z-ba9876543210.json',
];

describe('diagnostics report', () => {
  let root: string;
  let userData: string;
  let diagnosticsDir: string;
  let logFile: string;
  let consoleError: jest.SpyInstance;

  const expectNoSecrets = (text: string) =>
    SECRETS.forEach((secret) => expect(text).not.toContain(secret));

  /** A recorded crash, an old unscrubbed record, a broken one, a stray file. */
  const seedDiagnostics = () => {
    const recorded = recordCrash(
      {
        kind: 'uncaughtException',
        error: new Error(`boom ${PHRASE}`),
      },
      { now: () => new Date('2026-11-01T00:00:00.000Z') }
    ) as string;
    fs.writeFileSync(
      path.join(diagnosticsDir, CRASH_NAMES[0]),
      JSON.stringify({
        kind: 'unhandledRejection',
        error: {
          message: `signing failed for ${PRIVATE_KEY_HEX}`,
          config: { password: PASSWORD },
          owner: EMAIL,
        },
      })
    );
    fs.writeFileSync(
      path.join(diagnosticsDir, CRASH_NAMES[1]),
      `{"kind": "uncaughtException", "message": "${PHRASE} ${JWT_TOKEN}`
    );
    fs.writeFileSync(path.join(diagnosticsDir, 'notes.txt'), 'stray file');
    return path.basename(recorded);
  };

  const seedLog = () => {
    fs.mkdirSync(path.dirname(logFile), { recursive: true });
    fs.writeFileSync(
      logFile,
      [
        `[2026-11-03 05:15:30.123] [info]  Hive started from ${path.join(
          HOME,
          'AppData',
          'Local',
          'Programs',
          'qsdm-hive'
        )}`,
        `[2026-11-03 05:15:31.000] [error] import failed { mnemonic: '${PHRASE}' }`,
        `[2026-11-03 05:15:32.000] [info]  signer key ${PRIVATE_KEY_HEX}`,
        `[2026-11-03 05:15:33.000] [info]  account ${EMAIL} session ${JWT_TOKEN}`,
        '[2026-11-03 05:15:34.000] [info]  task 7 finished',
        '',
      ].join('\n')
    );
  };

  beforeEach(() => {
    jest.clearAllMocks();
    root = fs.mkdtempSync(path.join(os.tmpdir(), 'qsdm-hive-report-'));
    userData = path.join(root, 'userData');
    diagnosticsDir = path.join(userData, 'diagnostics');
    mockAppDataDir = path.join(root, 'appData', 'QSDM-Hive');
    logFile = path.join(mockAppDataDir, 'logs', 'main.log');
    fs.mkdirSync(userData, { recursive: true });
    (app.getPath as jest.Mock).mockImplementation((name: string) => {
      if (name === 'userData') return userData;
      if (name === 'downloads') return path.join(root, 'downloads');
      throw new Error(`unexpected path ${name}`);
    });
    (app.getGPUInfo as jest.Mock).mockResolvedValue(GPU_INFO);
    (dialog.showMessageBox as jest.Mock).mockResolvedValue({ response: 1 });
    (shell.openPath as jest.Mock).mockResolvedValue('');
    consoleError = jest
      .spyOn(console, 'error')
      .mockImplementation(() => undefined);
  });

  afterEach(() => {
    consoleError.mockRestore();
    fs.rmSync(root, { recursive: true, force: true });
  });

  describe('buildDiagnosticsReport', () => {
    it('collects versions, OS, GPU, every crash record and the log, scrubbed', async () => {
      const recordedName = seedDiagnostics();
      seedLog();

      const report = await buildDiagnosticsReport({
        now: NOW,
        diagnosticsDir,
        logFiles: [logFile],
        getGpuInfo: () => Promise.resolve(GPU_INFO),
      });

      expect(report).toContain('QSDM Hive diagnostics report');
      expect(report).toContain('Created: 2026-11-03T06:00:00.000Z');
      expect(report).toContain('has not sent it anywhere');
      // Sections are separated by a blank line.
      expect(report).toContain('Packaged: yes\n\nRuntime\n-------\n');
      expect(report).toContain('Name: QSDM Hive');
      expect(report).toContain('Version: 1.4.22');
      expect(report).toContain('Packaged: yes');
      expect(report).toContain(`Node: ${process.versions.node}`);
      expect(report).toContain(`Platform: ${process.platform}`);
      expect(report).toContain(`Architecture: ${process.arch}`);
      expect(report).toContain(`Release: ${os.release()}`);

      expect(report).toContain('"vendorId": 4318');
      expect(report).toContain('"sessionToken": "<redacted>"');

      expect(report).toContain('Crash records (3)');
      const order = [recordedName, ...CRASH_NAMES].map((name) =>
        report.indexOf(`### ${name}`)
      );
      order.forEach((index) => expect(index).toBeGreaterThan(-1));
      expect([...order].sort((a, b) => a - b)).toEqual(order);
      expect(report).not.toContain('stray file');
      expect(report).toContain('"password": "<redacted>"');
      expect(report).toContain('(Not valid JSON; shown as text.)');

      expect(report).toContain('Log file: main.log');
      expect(report).toContain('task 7 finished');
      expect(report).toContain('--- end of main.log ---');
      expect(report).toContain('<redacted-recovery-phrase>');
      expect(report).toContain('<redacted-hex-64>');
      expect(report).toContain('<redacted-jwt>');
      expect(report).toContain('<redacted-email>');

      expectNoSecrets(report);
    });

    it('says plainly when there is no crash, no log and no GPU summary', async () => {
      const report = await buildDiagnosticsReport({
        now: NOW,
        diagnosticsDir: path.join(root, 'nowhere'),
        logFiles: [path.join(root, 'missing.log')],
        getGpuInfo: () => Promise.reject(new Error('GPU process unavailable')),
      });

      expect(report).toContain('Crash records (0)');
      expect(report).toContain(
        'None. Hive has not recorded a crash on this computer.'
      );
      expect(report).toContain('(Not found.)');
      expect(report).toContain('Not available: GPU process unavailable');
    });
  });

  describe('saveDiagnosticsReport', () => {
    it('writes the scrubbed report where the user chose and says so', async () => {
      seedDiagnostics();
      seedLog();
      const target = path.join(root, 'chosen', 'report.txt');
      fs.mkdirSync(path.dirname(target));
      (dialog.showSaveDialog as jest.Mock).mockResolvedValue({
        canceled: false,
        filePath: target,
      });
      (dialog.showMessageBox as jest.Mock).mockResolvedValue({ response: 0 });

      const result = await saveDiagnosticsReport({ now: NOW });

      expect(result).toEqual({ status: 'saved', filePath: target });
      expect(dialog.showSaveDialog).toHaveBeenCalledWith(
        expect.objectContaining({
          defaultPath: path.join(
            root,
            'downloads',
            'qsdm-hive-diagnostics-2026-11-03T06-00-00Z.txt'
          ),
          filters: [{ name: 'Text file', extensions: ['txt'] }],
        })
      );
      expect(app.getGPUInfo).toHaveBeenCalledWith('basic');

      const saved = fs.readFileSync(target, 'utf8');
      expect(saved).toContain('QSDM Hive diagnostics report');
      expect(saved).toContain('Crash records (3)');
      expect(saved).toContain('task 7 finished');
      expectNoSecrets(saved);

      expect(dialog.showMessageBox).toHaveBeenCalledWith(
        expect.objectContaining({
          type: 'info',
          message: 'Diagnostics report saved',
          detail: expect.stringContaining('Nothing was sent anywhere.'),
        })
      );
      expect(shell.showItemInFolder).toHaveBeenCalledWith(target);
    });

    it('writes nothing when the user cancels', async () => {
      (dialog.showSaveDialog as jest.Mock).mockResolvedValue({
        canceled: true,
        filePath: '',
      });

      await expect(saveDiagnosticsReport()).resolves.toEqual({
        status: 'canceled',
      });
      expect(app.getGPUInfo).not.toHaveBeenCalled();
      expect(dialog.showMessageBox).not.toHaveBeenCalled();
    });

    it('attaches dialogs to the Hive window only while it is visible', async () => {
      (dialog.showSaveDialog as jest.Mock).mockResolvedValue({
        canceled: true,
      });
      const visible = { isDestroyed: () => false, isVisible: () => true };
      const hidden = { isDestroyed: () => false, isVisible: () => false };

      await saveDiagnosticsReport({
        window: visible as unknown as BrowserWindow,
      });
      await saveDiagnosticsReport({
        window: hidden as unknown as BrowserWindow,
      });

      const { calls } = (dialog.showSaveDialog as jest.Mock).mock;
      expect(calls[0][0]).toBe(visible);
      expect(calls[1]).toHaveLength(1);
      expect(calls[1][0]).toEqual(
        expect.objectContaining({ title: 'Save a diagnostics report' })
      );
    });

    it('tells the user when saving fails, and does not throw', async () => {
      (dialog.showSaveDialog as jest.Mock).mockResolvedValue({
        canceled: false,
        filePath: path.join(root, 'no-such-folder', 'report.txt'),
      });

      const result = await saveDiagnosticsReport();

      expect(result.status).toBe('failed');
      expect(dialog.showMessageBox).toHaveBeenCalledWith(
        expect.objectContaining({
          type: 'error',
          message: 'Hive could not save the diagnostics report',
        })
      );
      expect(shell.showItemInFolder).not.toHaveBeenCalled();
    });

    it('ignores a second request while the first is still open', async () => {
      let answer: (value: unknown) => void = () => undefined;
      (dialog.showSaveDialog as jest.Mock).mockImplementation(
        () =>
          new Promise((resolve) => {
            answer = resolve;
          })
      );

      const first = saveDiagnosticsReport();
      await expect(saveDiagnosticsReport()).resolves.toEqual({
        status: 'busy',
      });
      answer({ canceled: true });
      await expect(first).resolves.toEqual({ status: 'canceled' });
    });
  });

  describe('openDiagnosticsFolder', () => {
    it('creates and opens <userData>/diagnostics', async () => {
      await expect(openDiagnosticsFolder()).resolves.toBe(true);
      expect(fs.statSync(diagnosticsDir).isDirectory()).toBe(true);
      expect(shell.openPath).toHaveBeenCalledWith(diagnosticsDir);
    });

    it('tells the user where the folder is when it cannot be opened', async () => {
      (shell.openPath as jest.Mock).mockResolvedValue('Failed to open path');

      await expect(openDiagnosticsFolder()).resolves.toBe(false);
      expect(dialog.showMessageBox).toHaveBeenCalledWith(
        expect.objectContaining({
          type: 'error',
          message: 'Hive could not open the diagnostics folder',
          detail: expect.stringContaining(diagnosticsDir),
        })
      );
    });
  });

  describe('menu items', () => {
    it('adds the two plainly named items after a separator', async () => {
      (dialog.showSaveDialog as jest.Mock).mockResolvedValue({
        canceled: true,
      });
      const items = diagnosticsMenuItems(() => null);

      expect(SAVE_DIAGNOSTICS_REPORT_LABEL).toBe('Save a diagnostics report…');
      expect(OPEN_DIAGNOSTICS_FOLDER_LABEL).toBe('Open the diagnostics folder');
      expect(items.map((item) => item.type ?? item.label)).toEqual([
        'separator',
        SAVE_DIAGNOSTICS_REPORT_LABEL,
        OPEN_DIAGNOSTICS_FOLDER_LABEL,
      ]);

      (items[1].click as () => void)();
      expect(dialog.showSaveDialog).toHaveBeenCalledTimes(1);

      (items[2].click as () => void)();
      await new Promise((resolve) => {
        setTimeout(resolve, 50);
      });
      expect(shell.openPath).toHaveBeenCalledWith(diagnosticsDir);
    });
  });

  it('suggests a file name with the UTC time and no characters Windows rejects', () => {
    expect(reportFileName(new Date('2026-11-03T05:15:30.987Z'))).toBe(
      'qsdm-hive-diagnostics-2026-11-03T05-15-30Z.txt'
    );
  });
});
