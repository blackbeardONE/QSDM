/**
 * Local crash records for QSDM Hive.
 *
 * Every crash Hive notices becomes ONE scrubbed JSON file:
 * `<userData>/diagnostics/crash-<UTC time>-<random>.json`. The files stay on
 * this computer and nothing is sent anywhere; "Save a diagnostics report…"
 * bundles them into a file the user can attach to a support request.
 *
 * Recorded events: uncaughtException and unhandledRejection in the main
 * process, and Electron's render-process-gone and child-process-gone (except
 * clean exits, which are not crashes).
 *
 * There is deliberately no retention limit, count or size cap: Hive keeps
 * every record until the user deletes it.
 */
import crypto from 'crypto';
import { app } from 'electron';
import fs from 'fs';
import os from 'os';
import path from 'path';

import { isErrorLike, scrubText, scrubValue } from './scrub';

export type CrashKind =
  | 'uncaughtException'
  | 'unhandledRejection'
  | 'render-process-gone'
  | 'child-process-gone';

export interface CrashEvent {
  kind: CrashKind;
  /** The thrown value or rejection reason, when there is one. */
  error?: unknown;
  /** Event details such as the reason and exit code. */
  details?: Record<string, unknown>;
}

export interface RuntimeSummary {
  app: { name: string | null; version: string | null; packaged: boolean };
  runtime: {
    electron: string | null;
    chrome: string | null;
    node: string | null;
    v8: string | null;
  };
  os: {
    platform: string;
    arch: string;
    release: string | null;
    version: string | null;
  };
}

/** The parts of Electron's `app` the diagnostics code reads. */
export interface DiagnosticsApp {
  getPath(name: 'userData'): string;
  getName(): string;
  getVersion(): string;
  isPackaged: boolean;
}

// Event listeners take event-specific arguments (Electron's own typing).
// eslint-disable-next-line @typescript-eslint/no-explicit-any
type Listener = (...args: any[]) => void;

interface Emitter {
  on(event: string, listener: Listener): unknown;
  removeListener(event: string, listener: Listener): unknown;
}

export const DIAGNOSTICS_FOLDER_NAME = 'diagnostics';
export const CRASH_FILE_PATTERN = /^crash-.+\.json$/;

export const getDiagnosticsDir = (
  appRef: Pick<DiagnosticsApp, 'getPath'> = app
): string => path.join(appRef.getPath('userData'), DIAGNOSTICS_FOLDER_NAME);

const attempt = <T>(read: () => T, fallback: T): T => {
  try {
    return read();
  } catch {
    return fallback;
  }
};

export const getRuntimeSummary = (
  appRef: DiagnosticsApp = app
): RuntimeSummary => ({
  app: {
    name: attempt<string | null>(() => appRef.getName(), null),
    version: attempt<string | null>(() => appRef.getVersion(), null),
    packaged: attempt(() => Boolean(appRef.isPackaged), false),
  },
  runtime: {
    electron: process.versions.electron ?? null,
    chrome: process.versions.chrome ?? null,
    node: process.versions.node ?? null,
    v8: process.versions.v8 ?? null,
  },
  os: {
    platform: process.platform,
    arch: process.arch,
    release: attempt<string | null>(() => os.release(), null),
    version: attempt<string | null>(() => os.version(), null),
  },
});

const describeType = (value: unknown) => {
  if (value === null) return 'null';
  if (Array.isArray(value)) return 'array';
  if (typeof value === 'object') {
    const name = (value as { constructor?: { name?: unknown } }).constructor
      ?.name;
    return typeof name === 'string' && name ? name : 'object';
  }
  return typeof value;
};

/** An error, or any other thrown value, as a scrubbed plain object. */
export const describeThrown = (value: unknown): Record<string, unknown> => {
  if (isErrorLike(value)) return scrubValue(value) as Record<string, unknown>;
  if (typeof value === 'string') {
    return { name: 'NonError', message: scrubText(value) };
  }
  return {
    name: 'NonError',
    message: `A value of type ${describeType(
      value
    )} was thrown instead of an Error.`,
    value: scrubValue(value),
  };
};

export const buildCrashRecord = (
  event: CrashEvent,
  context: { now: Date; appRef?: DiagnosticsApp; appQuitting?: boolean }
) => {
  const summary = scrubValue(getRuntimeSummary(context.appRef)) as Record<
    string,
    unknown
  >;
  return {
    time: context.now.toISOString(),
    kind: event.kind,
    app: summary.app,
    runtime: summary.runtime,
    os: summary.os,
    processUptimeSeconds: Math.round(process.uptime()),
    appQuitting: Boolean(context.appQuitting),
    details: event.details ? scrubValue(event.details) : null,
    error: 'error' in event ? describeThrown(event.error) : null,
  };
};

export const crashFileName = (now: Date, suffix: string) =>
  `crash-${now.toISOString().replace(/:/g, '-')}-${suffix}.json`;

const randomSuffix = () => crypto.randomBytes(6).toString('hex');

const logSafely = (...args: unknown[]) => {
  try {
    console.error(...args);
  } catch {
    // Logging must not turn a crash record into a second crash.
  }
};

export interface RecordCrashOptions {
  appRef?: DiagnosticsApp;
  /** Folder for the record; defaults to <userData>/diagnostics. */
  getDiagnosticsDir?: () => string;
  now?: () => Date;
  appQuitting?: boolean;
}

/**
 * Writes one scrubbed JSON record for `event` and returns its path, or null
 * when it could not be written. Never throws.
 */
export const recordCrash = (
  event: CrashEvent,
  options: RecordCrashOptions = {}
): string | null => {
  try {
    const appRef = options.appRef ?? app;
    const now = options.now ? options.now() : new Date();
    let json: string;
    try {
      json = JSON.stringify(
        buildCrashRecord(event, {
          now,
          appRef,
          appQuitting: options.appQuitting,
        }),
        null,
        2
      );
    } catch (describeError) {
      json = JSON.stringify(
        {
          time: now.toISOString(),
          kind: event.kind,
          note: 'Hive could not describe this crash in full.',
          describeError: scrubText(String(describeError)),
        },
        null,
        2
      );
    }

    const dir = options.getDiagnosticsDir
      ? options.getDiagnosticsDir()
      : getDiagnosticsDir(appRef);
    fs.mkdirSync(dir, { recursive: true });
    // 'wx' never overwrites an earlier record; a name clash just picks
    // another random suffix.
    for (let tries = 0; tries < 5; tries += 1) {
      const file = path.join(dir, crashFileName(now, randomSuffix()));
      try {
        fs.writeFileSync(file, `${json}\n`, { encoding: 'utf8', flag: 'wx' });
        return file;
      } catch (error) {
        if ((error as NodeJS.ErrnoException).code !== 'EEXIST') throw error;
      }
    }
    throw new Error('every crash record name was already taken');
  } catch (error) {
    logSafely('Hive could not write a crash record ->', error);
    return null;
  }
};

export interface CrashRecorderOptions {
  appRef?: DiagnosticsApp & Emitter;
  processRef?: Emitter;
  getDiagnosticsDir?: () => string;
  now?: () => Date;
}

interface ProcessGoneDetails {
  reason?: string;
  exitCode?: number;
  type?: string;
  serviceName?: string;
  name?: string;
}

/**
 * Records main-process exceptions and rejections, renderer crashes and child
 * process crashes. Returns a function that removes the listeners again.
 */
export const installCrashRecorder = (
  options: CrashRecorderOptions = {}
): (() => void) => {
  const appRef: DiagnosticsApp & Emitter = options.appRef ?? app;
  const processRef: Emitter = options.processRef ?? process;
  let appQuitting = false;

  const record = (event: CrashEvent) =>
    recordCrash(event, {
      appRef,
      getDiagnosticsDir: options.getDiagnosticsDir,
      now: options.now,
      appQuitting,
    });

  // main.ts already logs uncaught exceptions; this listener only records.
  const onUncaughtException = (error: unknown, origin?: unknown) => {
    record({
      kind: 'uncaughtException',
      error,
      details: { origin: typeof origin === 'string' ? origin : null },
    });
  };
  const onUnhandledRejection = (reason: unknown) => {
    logSafely('Unhandled Rejection ->', reason);
    record({ kind: 'unhandledRejection', error: reason });
  };
  const onRenderProcessGone = (
    _event: unknown,
    _webContents: unknown,
    details?: ProcessGoneDetails
  ) => {
    if (!details || details.reason === 'clean-exit') return;
    logSafely('Render process gone ->', details);
    record({
      kind: 'render-process-gone',
      details: { reason: details.reason, exitCode: details.exitCode },
    });
  };
  const onChildProcessGone = (
    _event: unknown,
    details?: ProcessGoneDetails
  ) => {
    if (!details || details.reason === 'clean-exit') return;
    logSafely('Child process gone ->', details);
    record({
      kind: 'child-process-gone',
      details: {
        type: details.type,
        reason: details.reason,
        exitCode: details.exitCode,
        serviceName: details.serviceName,
        name: details.name,
      },
    });
  };
  const onQuit = () => {
    appQuitting = true;
  };

  const processListeners: Array<[string, Listener]> = [
    ['uncaughtException', onUncaughtException],
    ['unhandledRejection', onUnhandledRejection],
  ];
  const appListeners: Array<[string, Listener]> = [
    ['render-process-gone', onRenderProcessGone],
    ['child-process-gone', onChildProcessGone],
    ['before-quit', onQuit],
    ['will-quit', onQuit],
  ];
  processListeners.forEach(([event, listener]) =>
    processRef.on(event, listener)
  );
  appListeners.forEach(([event, listener]) => appRef.on(event, listener));

  return () => {
    processListeners.forEach(([event, listener]) =>
      processRef.removeListener(event, listener)
    );
    appListeners.forEach(([event, listener]) =>
      appRef.removeListener(event, listener)
    );
  };
};
