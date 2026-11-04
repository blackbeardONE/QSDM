/**
 * "Save a diagnostics report…" and "Open the diagnostics folder".
 *
 * The report is one plain-text file, saved wherever the user chooses, that
 * they can attach to a support request: app and runtime versions, the
 * operating system, a basic GPU summary, every local crash record and Hive's
 * current log file. All of it is scrubbed (see ./scrub.ts). Nothing is
 * uploaded; sharing the file is the user's own choice.
 */
import { app, dialog, shell } from 'electron';
import fs from 'fs';
import path from 'path';

import { getAppDataPath } from '../node/helpers/getAppDataPath';

import {
  CRASH_FILE_PATTERN,
  getDiagnosticsDir,
  getRuntimeSummary,
} from './crashRecorder';
import { scrubText, scrubValue } from './scrub';

import type { RuntimeSummary } from './crashRecorder';
import type {
  BrowserWindow,
  MenuItemConstructorOptions,
  MessageBoxOptions,
  SaveDialogOptions,
} from 'electron';

export const SAVE_DIAGNOSTICS_REPORT_LABEL = 'Save a diagnostics report…';
export const OPEN_DIAGNOSTICS_FOLDER_LABEL = 'Open the diagnostics folder';

/** How long to wait for Electron's GPU summary before leaving it out. */
const GPU_INFO_TIMEOUT_MS = 10000;

/** Hive's current log file, written by electron-log (see ../logger.ts). */
export const defaultLogFiles = (): string[] => [
  path.join(getAppDataPath(), 'logs', 'main.log'),
];

const errorText = (error: unknown) =>
  error instanceof Error ? error.message : String(error);

const logSafely = (...args: unknown[]) => {
  try {
    console.error(...args);
  } catch {
    // Nothing else to do.
  }
};

const withTimeout = <T>(work: () => Promise<T>, ms: number) =>
  new Promise<T>((resolve, reject) => {
    const timer = setTimeout(
      () => reject(new Error(`no answer within ${ms / 1000} seconds`)),
      ms
    );
    Promise.resolve()
      .then(work)
      .then(
        (value) => {
          clearTimeout(timer);
          resolve(value);
        },
        (error) => {
          clearTimeout(timer);
          reject(error);
        }
      );
  });

const readGpuSummary = async (getGpuInfo: () => Promise<unknown>) => {
  try {
    const info = await withTimeout(getGpuInfo, GPU_INFO_TIMEOUT_MS);
    return JSON.stringify(scrubValue(info), null, 2) ?? 'Not available.';
  } catch (error) {
    return `Not available: ${errorText(error)}`;
  }
};

interface ReportEntry {
  name: string;
  text: string;
}

const readCrashRecord = async (
  dir: string,
  name: string
): Promise<ReportEntry> => {
  let raw: string;
  try {
    raw = await fs.promises.readFile(path.join(dir, name), 'utf8');
  } catch (error) {
    return { name, text: `(Could not be read: ${errorText(error)})` };
  }
  try {
    // Scrub again: the file may predate a scrubber fix or come from elsewhere.
    return {
      name,
      text: JSON.stringify(scrubValue(JSON.parse(raw)), null, 2),
    };
  } catch {
    return { name, text: `(Not valid JSON; shown as text.)\n${raw}` };
  }
};

const readCrashRecords = async (dir: string) => {
  let names: string[];
  try {
    names = await fs.promises.readdir(dir);
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code === 'ENOENT') {
      return { entries: [] as ReportEntry[], note: '' };
    }
    return {
      entries: [] as ReportEntry[],
      note: `The folder could not be read: ${errorText(error)}`,
    };
  }
  // One file at a time, so thousands of records cannot exhaust file handles.
  const entries = await names
    .filter((name) => CRASH_FILE_PATTERN.test(name))
    .sort()
    .reduce(async (previous, name) => {
      const list = await previous;
      list.push(await readCrashRecord(dir, name));
      return list;
    }, Promise.resolve([] as ReportEntry[]));
  return { entries, note: '' };
};

const readLogFile = async (file: string): Promise<ReportEntry> => {
  try {
    return { name: file, text: await fs.promises.readFile(file, 'utf8') };
  } catch (error) {
    return {
      name: file,
      text:
        (error as NodeJS.ErrnoException).code === 'ENOENT'
          ? '(Not found.)'
          : `(Could not be read: ${errorText(error)})`,
    };
  }
};

const section = (title: string, body: string) =>
  `${title}\n${'-'.repeat(title.length)}\n${body}\n`;

const orUnknown = (value: string | null) => value ?? 'unknown';

export interface DiagnosticsReportSources {
  now: Date;
  diagnosticsDir: string;
  logFiles: string[];
  getGpuInfo: () => Promise<unknown>;
  runtime?: RuntimeSummary;
}

/** The whole report as text, scrubbed. */
export const buildDiagnosticsReport = async (
  sources: DiagnosticsReportSources
): Promise<string> => {
  const runtime = sources.runtime ?? getRuntimeSummary();
  const gpu = await readGpuSummary(sources.getGpuInfo);
  const crashes = await readCrashRecords(sources.diagnosticsDir);
  const logs = await Promise.all(sources.logFiles.map(readLogFile));

  const crashLines = [
    `Folder: ${sources.diagnosticsDir}`,
    '',
    crashes.entries.length
      ? crashes.entries
          .map((entry) => `### ${entry.name}\n${entry.text}`)
          .join('\n\n')
      : 'None. Hive has not recorded a crash on this computer.',
  ];
  if (crashes.note) crashLines.push(crashes.note);
  const crashBody = crashLines.join('\n');

  const report = [
    'QSDM Hive diagnostics report',
    '============================',
    `Created: ${sources.now.toISOString()}`,
    '',
    'Hive made this report on this computer and has not sent it anywhere.',
    'It removed the secrets it could find: recovery phrases, private keys and',
    'other key material, tokens, passwords, email addresses, and your home',
    'folder (shown as ~). Please read the report before you share it, then',
    'attach it to your support request.',
    '',
    section(
      'App',
      [
        `Name: ${orUnknown(runtime.app.name)}`,
        `Version: ${orUnknown(runtime.app.version)}`,
        `Packaged: ${runtime.app.packaged ? 'yes' : 'no'}`,
      ].join('\n')
    ),
    section(
      'Runtime',
      [
        `Electron: ${orUnknown(runtime.runtime.electron)}`,
        `Chrome: ${orUnknown(runtime.runtime.chrome)}`,
        `Node: ${orUnknown(runtime.runtime.node)}`,
        `V8: ${orUnknown(runtime.runtime.v8)}`,
      ].join('\n')
    ),
    section(
      'Operating system',
      [
        `Platform: ${runtime.os.platform}`,
        `Architecture: ${runtime.os.arch}`,
        `Release: ${orUnknown(runtime.os.release)}`,
        `Version: ${orUnknown(runtime.os.version)}`,
      ].join('\n')
    ),
    section('GPU (basic)', gpu),
    section(`Crash records (${crashes.entries.length})`, crashBody),
    ...logs.map((log) => {
      const name = path.basename(log.name);
      return section(
        `Log file: ${name}`,
        `Path: ${log.name}\n\n${log.text}\n--- end of ${name} ---`
      );
    }),
  ].join('\n');

  // Everything above passes through the scrubber once more as a whole.
  return scrubText(report);
};

/** Suggested file name, e.g. qsdm-hive-diagnostics-2026-11-03T05-15-30Z.txt */
export const reportFileName = (now: Date) =>
  `qsdm-hive-diagnostics-${now
    .toISOString()
    .replace(/\.\d+Z$/, 'Z')
    .replace(/:/g, '-')}.txt`;

const defaultReportFolder = () => {
  try {
    return app.getPath('downloads');
  } catch {
    try {
      return app.getPath('home');
    } catch {
      return '';
    }
  }
};

/**
 * A dialog attached to a hidden window (Hive minimized to the tray) may not
 * show at all, so dialogs only get a parent window while it is visible.
 */
const visibleWindow = (window?: BrowserWindow | null) => {
  try {
    return window && !window.isDestroyed() && window.isVisible()
      ? window
      : undefined;
  } catch {
    return undefined;
  }
};

const showMessage = (
  parent: BrowserWindow | undefined,
  options: MessageBoxOptions
) =>
  parent
    ? dialog.showMessageBox(parent, options)
    : dialog.showMessageBox(options);

const showProblem = async (
  parent: BrowserWindow | undefined,
  message: string,
  detail: string
) => {
  try {
    await showMessage(parent, {
      type: 'error',
      title: 'QSDM Hive',
      message,
      detail,
      buttons: ['Close'],
      noLink: true,
    });
  } catch {
    // The problem is already in the log.
  }
};

export interface SaveDiagnosticsReportOptions {
  window?: BrowserWindow | null;
  now?: Date;
  diagnosticsDir?: string;
  logFiles?: string[];
  getGpuInfo?: () => Promise<unknown>;
}

export type SaveDiagnosticsReportResult =
  | { status: 'saved'; filePath: string }
  | { status: 'canceled' }
  | { status: 'busy' }
  | { status: 'failed'; message: string };

let saving = false;

/**
 * Asks where to save, writes the scrubbed report there and says where it
 * went. Never throws.
 */
export const saveDiagnosticsReport = async (
  options: SaveDiagnosticsReportOptions = {}
): Promise<SaveDiagnosticsReportResult> => {
  if (saving) return { status: 'busy' };
  saving = true;
  const parent = visibleWindow(options.window);
  try {
    const now = options.now ?? new Date();
    const saveOptions: SaveDialogOptions = {
      title: 'Save a diagnostics report',
      defaultPath: path.join(defaultReportFolder(), reportFileName(now)),
      buttonLabel: 'Save report',
      filters: [{ name: 'Text file', extensions: ['txt'] }],
    };
    const choice = parent
      ? await dialog.showSaveDialog(parent, saveOptions)
      : await dialog.showSaveDialog(saveOptions);
    if (choice.canceled || !choice.filePath) return { status: 'canceled' };

    const report = await buildDiagnosticsReport({
      now,
      diagnosticsDir: options.diagnosticsDir ?? getDiagnosticsDir(),
      logFiles: options.logFiles ?? defaultLogFiles(),
      getGpuInfo: options.getGpuInfo ?? (() => app.getGPUInfo('basic')),
    });
    await fs.promises.writeFile(choice.filePath, report, 'utf8');

    const { response } = await showMessage(parent, {
      type: 'info',
      title: 'QSDM Hive',
      message: 'Diagnostics report saved',
      detail:
        `Saved to ${choice.filePath}\n\n` +
        'Nothing was sent anywhere. Hive removed the secrets it could find, ' +
        'such as recovery phrases, private keys, passwords and email ' +
        'addresses. Please read the file before you share it, then attach ' +
        'it to your support request.',
      buttons: ['Show the file', 'Close'],
      defaultId: 1,
      cancelId: 1,
      noLink: true,
    });
    if (response === 0) shell.showItemInFolder(choice.filePath);
    return { status: 'saved', filePath: choice.filePath };
  } catch (error) {
    const message = scrubText(errorText(error));
    logSafely('Hive could not save the diagnostics report ->', message);
    await showProblem(
      parent,
      'Hive could not save the diagnostics report',
      message
    );
    return { status: 'failed', message };
  } finally {
    saving = false;
  }
};

/** Opens <userData>/diagnostics, creating it first. Never throws. */
export const openDiagnosticsFolder = async (
  options: { window?: BrowserWindow | null; diagnosticsDir?: string } = {}
): Promise<boolean> => {
  let dir = options.diagnosticsDir;
  try {
    dir = dir ?? getDiagnosticsDir();
    await fs.promises.mkdir(dir, { recursive: true });
    const failure = await shell.openPath(dir);
    if (!failure) return true;
    throw new Error(failure);
  } catch (error) {
    const message = errorText(error);
    logSafely('Hive could not open the diagnostics folder ->', message);
    await showProblem(
      visibleWindow(options.window),
      'Hive could not open the diagnostics folder',
      dir ? `${message}\n\nThe folder is ${dir}` : message
    );
    return false;
  }
};

/** A separator and the two diagnostics items, for the Help and tray menus. */
export const diagnosticsMenuItems = (
  getWindow: () => BrowserWindow | null = () => null
): MenuItemConstructorOptions[] => [
  { type: 'separator' },
  {
    label: SAVE_DIAGNOSTICS_REPORT_LABEL,
    click: () => {
      saveDiagnosticsReport({ window: getWindow() }).catch(() => undefined);
    },
  },
  {
    label: OPEN_DIAGNOSTICS_FOLDER_LABEL,
    click: () => {
      openDiagnosticsFolder({ window: getWindow() }).catch(() => undefined);
    },
  },
];
