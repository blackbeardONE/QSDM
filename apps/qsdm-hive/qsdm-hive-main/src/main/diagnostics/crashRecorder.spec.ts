/**
 * @jest-environment node
 */
/* eslint-disable @cspell/spellchecker */
import crypto from 'crypto';
import { app } from 'electron';
import { EventEmitter } from 'events';
import fs from 'fs';
import os from 'os';
import path from 'path';

import {
  CRASH_FILE_PATTERN,
  crashFileName,
  CrashRecorderOptions,
  installCrashRecorder,
  recordCrash,
} from './crashRecorder';

jest.mock('electron', () => ({
  app: {
    getPath: jest.fn(),
    getName: jest.fn(() => 'QSDM Hive'),
    getVersion: jest.fn(() => '1.4.22'),
    isPackaged: false,
    on: jest.fn(),
    removeListener: jest.fn(),
  },
}));

const PHRASE =
  'legal winner thank year wave sausage worth useful legal winner thank yellow';
const PRIVATE_KEY_HEX = crypto
  .createHash('sha256')
  .update('crash recorder key')
  .digest('hex');
const EMAIL = 'alice@example.com';
const PASSWORD = 'hunter2-Correct-Horse';
const SECRETS = [PHRASE, PRIVATE_KEY_HEX, EMAIL, PASSWORD, 'sausage'];

const FILE_NAME =
  /^crash-\d{4}-\d{2}-\d{2}T\d{2}-\d{2}-\d{2}\.\d{3}Z-[0-9a-f]{12}\.json$/;

// eslint-disable-next-line @typescript-eslint/no-explicit-any
type CrashRecordJson = Record<string, any>;

describe('crash recorder', () => {
  let userData: string;
  let diagnosticsDir: string;
  let consoleError: jest.SpyInstance;

  const crashFiles = () =>
    fs.existsSync(diagnosticsDir)
      ? fs.readdirSync(diagnosticsDir).filter((name) => FILE_NAME.test(name))
      : [];
  const readRecord = (name: string) =>
    JSON.parse(
      fs.readFileSync(path.join(diagnosticsDir, name), 'utf8')
    ) as CrashRecordJson;

  const secretError = () => {
    const error = Object.assign(
      // "hex" is not a BIP39 word, so the phrase run ends before it.
      new Error(`wallet import failed: ${PHRASE} (hex ${PRIVATE_KEY_HEX})`),
      { code: 'E_IMPORT', password: PASSWORD }
    );
    (error as Error & { cause?: unknown }).cause = new Error(`owner ${EMAIL}`);
    return error;
  };

  beforeEach(() => {
    userData = fs.mkdtempSync(path.join(os.tmpdir(), 'qsdm-hive-crash-'));
    diagnosticsDir = path.join(userData, 'diagnostics');
    (app.getPath as jest.Mock).mockImplementation((name: string) => {
      if (name !== 'userData') throw new Error(`unexpected path ${name}`);
      return userData;
    });
    consoleError = jest
      .spyOn(console, 'error')
      .mockImplementation(() => undefined);
  });

  afterEach(() => {
    consoleError.mockRestore();
    fs.rmSync(userData, { recursive: true, force: true });
  });

  describe('recordCrash', () => {
    it('writes one scrubbed JSON file into <userData>/diagnostics', () => {
      const file = recordCrash({
        kind: 'uncaughtException',
        error: secretError(),
        details: { origin: 'uncaughtException' },
      });

      expect(file).not.toBeNull();
      expect(path.dirname(file as string)).toBe(diagnosticsDir);
      expect(crashFiles()).toEqual([path.basename(file as string)]);

      const raw = fs.readFileSync(file as string, 'utf8');
      SECRETS.forEach((secret) => expect(raw).not.toContain(secret));

      const record = JSON.parse(raw) as CrashRecordJson;
      expect(new Date(record.time).toISOString()).toBe(record.time);
      expect(record.kind).toBe('uncaughtException');
      expect(record.app).toEqual({
        name: 'QSDM Hive',
        version: '1.4.22',
        packaged: false,
      });
      expect(record.os).toEqual({
        platform: process.platform,
        arch: process.arch,
        release: os.release(),
        version: os.version(),
      });
      expect(record.runtime.node).toBe(process.versions.node);
      expect(record.runtime).toHaveProperty('electron');
      expect(record.runtime).toHaveProperty('chrome');
      expect(record.details).toEqual({ origin: 'uncaughtException' });
      expect(record.appQuitting).toBe(false);
      expect(record.error.name).toBe('Error');
      expect(record.error.code).toBe('E_IMPORT');
      expect(record.error.password).toBe('<redacted>');
      expect(record.error.message).toBe(
        'wallet import failed: <redacted-recovery-phrase> (hex <redacted-hex-64>)'
      );
      expect(record.error.stack).toContain('<redacted-recovery-phrase>');
      expect(record.error.cause.message).toBe('owner <redacted-email>');
    });

    it('names files by UTC time plus a random part, never overwriting', () => {
      const now = () => new Date('2026-11-03T05:15:30.123Z');
      const first = recordCrash(
        { kind: 'unhandledRejection', error: 1 },
        { now }
      );
      const second = recordCrash(
        { kind: 'unhandledRejection', error: 2 },
        { now }
      );

      expect(first).not.toBe(second);
      [first, second].forEach((file) => {
        const name = path.basename(file as string);
        expect(name).toMatch(FILE_NAME);
        expect(name.startsWith('crash-2026-11-03T05-15-30.123Z-')).toBe(true);
        expect(CRASH_FILE_PATTERN.test(name)).toBe(true);
      });
      expect(crashFiles()).toHaveLength(2);
      expect(crashFileName(now(), 'abc')).toBe(
        'crash-2026-11-03T05-15-30.123Z-abc.json'
      );
    });

    it('records rejection reasons that are not errors', () => {
      const asString = recordCrash({
        kind: 'unhandledRejection',
        error: `bad mnemonic ${PHRASE}`,
      });
      const asObject = recordCrash({
        kind: 'unhandledRejection',
        error: { status: 401, token: 'abc123', note: `for ${EMAIL}` },
      });

      expect(readRecord(path.basename(asString as string)).error).toEqual({
        name: 'NonError',
        message: 'bad mnemonic <redacted-recovery-phrase>',
      });
      expect(readRecord(path.basename(asObject as string)).error).toEqual({
        name: 'NonError',
        message: 'A value of type Object was thrown instead of an Error.',
        value: {
          status: 401,
          token: '<redacted>',
          note: 'for <redacted-email>',
        },
      });
    });

    it('never throws when the record cannot be written', () => {
      const blocker = path.join(userData, 'not-a-folder');
      fs.writeFileSync(blocker, 'x');

      expect(
        recordCrash(
          { kind: 'uncaughtException', error: new Error('x') },
          { getDiagnosticsDir: () => path.join(blocker, 'diagnostics') }
        )
      ).toBeNull();

      (app.getPath as jest.Mock).mockImplementation(() => {
        throw new Error('no userData yet');
      });
      expect(
        recordCrash({ kind: 'uncaughtException', error: new Error('x') })
      ).toBeNull();
      expect(consoleError).toHaveBeenCalledWith(
        'Hive could not write a crash record ->',
        expect.any(Error)
      );
    });

    it('still writes a record when the thrown value cannot be read', () => {
      const unreadable = new Proxy(
        {},
        {
          ownKeys() {
            throw new Error('no keys');
          },
        }
      );
      const unreadableFile = recordCrash({
        kind: 'unhandledRejection',
        error: unreadable,
      });
      expect(readRecord(path.basename(unreadableFile as string)).error).toEqual(
        {
          name: 'NonError',
          message: 'A value of type Object was thrown instead of an Error.',
          value: '<unreadable>',
        }
      );

      const hostile = new Proxy(
        {},
        {
          get() {
            throw new Error(`trap for ${EMAIL}`);
          },
        }
      );
      const hostileFile = recordCrash({
        kind: 'uncaughtException',
        error: hostile,
      });
      const record = readRecord(path.basename(hostileFile as string));
      expect(record.kind).toBe('uncaughtException');
      expect(record.note).toBe('Hive could not describe this crash in full.');
      expect(record.describeError).toBe('Error: trap for <redacted-email>');
    });
  });

  describe('installCrashRecorder', () => {
    const install = () => {
      const fakeProcess = new EventEmitter();
      const fakeApp = Object.assign(new EventEmitter(), {
        getPath: () => userData,
        getName: () => 'QSDM Hive',
        getVersion: () => '1.4.22',
        isPackaged: true,
      });
      const uninstall = installCrashRecorder({
        processRef: fakeProcess,
        appRef: fakeApp as CrashRecorderOptions['appRef'],
      });
      return { fakeProcess, fakeApp, uninstall };
    };

    it('records exceptions, rejections and renderer and child crashes', () => {
      const { fakeProcess, fakeApp } = install();

      fakeProcess.emit('uncaughtException', secretError(), 'uncaughtException');
      const reason = new Error(`rejected for ${EMAIL}`);
      fakeProcess.emit('unhandledRejection', reason, Promise.resolve());
      fakeApp.emit(
        'render-process-gone',
        {},
        {},
        {
          reason: 'crashed',
          exitCode: -1073741819,
        }
      );
      fakeApp.emit(
        'child-process-gone',
        {},
        {
          type: 'GPU',
          reason: 'oom',
          exitCode: 1,
          name: 'GPU',
        }
      );

      const records = crashFiles().map(readRecord);
      expect(records.map((record) => record.kind).sort()).toEqual([
        'child-process-gone',
        'render-process-gone',
        'uncaughtException',
        'unhandledRejection',
      ]);
      const byKind = Object.fromEntries(
        records.map((record) => [record.kind, record])
      );
      expect(byKind['render-process-gone'].details).toEqual({
        reason: 'crashed',
        exitCode: -1073741819,
      });
      expect(byKind['render-process-gone'].error).toBeNull();
      expect(byKind['child-process-gone'].details).toEqual({
        type: 'GPU',
        reason: 'oom',
        exitCode: 1,
        name: 'GPU',
      });
      expect(byKind.unhandledRejection.error.message).toBe(
        'rejected for <redacted-email>'
      );
      expect(byKind.uncaughtException.app.packaged).toBe(true);
      records.forEach((record) =>
        SECRETS.forEach((secret) =>
          expect(JSON.stringify(record)).not.toContain(secret)
        )
      );

      // The console keeps logging, as before.
      expect(consoleError).toHaveBeenCalledWith(
        'Unhandled Rejection ->',
        reason
      );
      expect(consoleError).toHaveBeenCalledWith(
        'Render process gone ->',
        expect.objectContaining({ reason: 'crashed' })
      );
      expect(consoleError).toHaveBeenCalledWith(
        'Child process gone ->',
        expect.objectContaining({ type: 'GPU' })
      );
    });

    it('ignores clean exits, which are not crashes', () => {
      const { fakeApp } = install();
      fakeApp.emit(
        'render-process-gone',
        {},
        {},
        {
          reason: 'clean-exit',
          exitCode: 0,
        }
      );
      fakeApp.emit(
        'child-process-gone',
        {},
        {
          type: 'Utility',
          reason: 'clean-exit',
          exitCode: 0,
          name: 'Audio Service',
        }
      );
      expect(crashFiles()).toEqual([]);
    });

    it('marks crashes that happen while Hive is quitting', () => {
      const { fakeApp } = install();
      fakeApp.emit('before-quit');
      fakeApp.emit(
        'child-process-gone',
        {},
        {
          type: 'Utility',
          reason: 'killed',
          exitCode: 15,
          name: 'Network Service',
        }
      );
      const [record] = crashFiles().map(readRecord);
      expect(record.appQuitting).toBe(true);
    });

    it('stops listening once uninstalled', () => {
      const { fakeProcess, fakeApp, uninstall } = install();
      uninstall();

      expect(fakeProcess.listenerCount('uncaughtException')).toBe(0);
      expect(fakeProcess.listenerCount('unhandledRejection')).toBe(0);
      expect(fakeApp.listenerCount('render-process-gone')).toBe(0);
      expect(fakeApp.listenerCount('child-process-gone')).toBe(0);
      fakeProcess.emit('unhandledRejection', new Error('late'));
      expect(crashFiles()).toEqual([]);
    });
  });
});
