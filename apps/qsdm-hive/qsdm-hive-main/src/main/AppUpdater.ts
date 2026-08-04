import { BrowserWindow, dialog } from 'electron';
import fs from 'fs';
import path from 'path';

import log from 'electron-log';
import { autoUpdater } from 'electron-updater';

import { RendererEndpoints } from 'config/endpoints';

import { app } from './app';
import { getAppDataPath } from './node/helpers/getAppDataPath';
import {
  getCurrentHiveVersion,
  isUnsignedPreviewHiveVersion,
} from './services/hiveVersionPolicy';
import {
  getVerifiedQsdmHiveRelease,
  VerifiedQsdmHiveRelease,
  verifyDownloadedQsdmHiveUpdate,
} from './services/qsdmReleaseManifest';

import type {
  UpdateCheckResult,
  UpdateDownloadedEvent,
  UpdateInfo,
} from 'electron-updater';

const CHECK_INTERVAL = 30 * 60 * 1000;
const RETRY_INTERVAL = 60 * 1000;
const INITIAL_CHECK_DELAY = 1000;
const QSDM_HIVE_UPDATE_FEED_URL = 'https://qsdm.tech/downloads';
const QSDM_HIVE_UNSIGNED_PREVIEW_UPDATE_FEED_URL =
  'https://qsdm.tech/downloads/unsigned-preview';

type AutoUpdaterCacheApp = {
  baseCachePath: string;
};

let interval: NodeJS.Timeout | null = null;
let initialCheckTimeout: NodeJS.Timeout | null = null;
let retryTimeout: NodeJS.Timeout | null = null;
let updaterConfigured = false;
let listenersConfigured = false;
let trustedRelease: VerifiedQsdmHiveRelease | null = null;
let updateCheckPromise: Promise<UpdateCheckResult | null> | null = null;
let updateDownloadPromise: Promise<string[]> | null = null;
let installPromptOpen = false;

export function shouldEnableAutoUpdates(
  env: NodeJS.ProcessEnv = process.env,
  currentVersion = getCurrentHiveVersion()
) {
  // An unsigned preview must never become an automatic trust transition.
  // Preview users move to the first signed stable release manually.
  if (isUnsignedPreviewHiveVersion(currentVersion)) {
    return false;
  }

  // Required production updates are part of the exact-version security gate.
  // Environment overrides are development controls and cannot disable them.
  if (app.isPackaged || env.NODE_ENV === 'production') {
    return true;
  }

  if (env.QSDM_DISABLE_AUTO_UPDATES === '1') {
    return false;
  }
  if (env.QSDM_ENABLE_AUTO_UPDATES === '1') {
    return true;
  }

  return false;
}

export function getQsdmHiveUpdateFeedUrl(
  env: NodeJS.ProcessEnv = process.env,
  currentVersion = getCurrentHiveVersion()
) {
  const defaultFeedUrl = isUnsignedPreviewHiveVersion(currentVersion)
    ? QSDM_HIVE_UNSIGNED_PREVIEW_UPDATE_FEED_URL
    : QSDM_HIVE_UPDATE_FEED_URL;

  return (
    env.QSDM_HIVE_UPDATE_FEED_URL?.trim() ||
    env.QSDM_HIVE_UPDATE_BASE_URL?.trim() ||
    defaultFeedUrl
  );
}

export async function initializeAppUpdater(
  mainWindow: BrowserWindow | undefined,
  appCleanup: () => Promise<void>
) {
  if (!shouldEnableAutoUpdates()) {
    console.log(
      'QSDM Hive auto updates are disabled for this runtime. Set QSDM_ENABLE_AUTO_UPDATES=1 to enable release checks in development.'
    );
    return;
  }

  await ensureAppUpdaterConfigured();
  setListeners(mainWindow, appCleanup);
  createCheckForTheUpdatesInterval();
}

export async function checkForUpdates() {
  return runUpdateCheck('manual');
}

export async function downloadTrustedAppUpdate(): Promise<string[]> {
  if (updateDownloadPromise) {
    return updateDownloadPromise;
  }

  const result = await runUpdateCheck('manual download');
  if (!result?.isUpdateAvailable) {
    const release =
      trustedRelease || (await ensureTrustedReleaseForUpdate(true));
    throw new Error(
      release.manifest.version === getCurrentHiveVersion()
        ? 'QSDM Hive is already on the approved version.'
        : `Automatic installation is unavailable for this version transition. Install QSDM Hive ${release.manifest.version} from ${release.installerUrl}.`
    );
  }

  const release = trustedRelease || (await ensureTrustedReleaseForUpdate(true));
  assertUpdateInfoMatchesTrustedRelease(result.updateInfo, release);
  return startTrustedUpdateDownload(result.updateInfo, release);
}

export async function ensureTrustedReleaseForUpdate(forceRefresh = false) {
  const release = await getVerifiedQsdmHiveRelease({
    platform: process.platform,
    baseUrl: getQsdmHiveUpdateFeedUrl(),
    forceRefresh,
  });
  trustedRelease = release;
  return release;
}

function assertUpdateInfoMatchesTrustedRelease(
  info: UpdateInfo,
  release: VerifiedQsdmHiveRelease
) {
  if (info.version !== release.manifest.version) {
    throw new Error(
      `Updater offered ${info.version}, but the signed QSDM release is ${release.manifest.version}.`
    );
  }
}

export async function ensureAppUpdaterConfigured() {
  if (updaterConfigured) {
    return;
  }

  autoUpdater.logger = log;
  autoUpdater.autoDownload = false;
  // The release manifest is an exact-version policy, not a semver range.
  // Never let electron-updater turn an unexpected newer build into an
  // implicit downgrade. HiveVersionGate blocks both directions and sends the
  // operator to the approved installer instead.
  autoUpdater.allowDowngrade = false;
  autoUpdater.allowPrerelease = false;
  autoUpdater.autoInstallOnAppQuit = false;
  (
    autoUpdater as unknown as { disableWebInstaller?: boolean }
  ).disableWebInstaller = true;
  autoUpdater.setFeedURL({
    provider: 'generic',
    url: getQsdmHiveUpdateFeedUrl(),
  });

  const updaterCacheApp = (
    autoUpdater as unknown as { app: AutoUpdaterCacheApp }
  ).app;
  console.log('QSDM Hive updater feed ', getQsdmHiveUpdateFeedUrl());
  console.log('original updater cache path ', updaterCacheApp.baseCachePath);
  Object.defineProperty(updaterCacheApp, 'baseCachePath', {
    configurable: true,
    get() {
      return path.join(getAppDataPath(), 'updater-cache');
    },
  });
  console.log('overwritten updater cache path ', updaterCacheApp.baseCachePath);
  updaterConfigured = true;
}

function setListeners(
  mainWindow: BrowserWindow | undefined,
  appCleanup: () => Promise<void>
) {
  if (listenersConfigured) {
    return;
  }
  listenersConfigured = true;

  autoUpdater.on('update-available', async (info) => {
    sendRendererEvent(mainWindow, RendererEndpoints.UPDATE_AVAILABLE, info);
    revealMainWindow(mainWindow);

    let release: VerifiedQsdmHiveRelease;
    try {
      release = await ensureTrustedReleaseForUpdate();
      assertUpdateInfoMatchesTrustedRelease(info, release);
    } catch (error) {
      log.error('QSDM Hive refused an untrusted update offer', error);
      dialog.showErrorBox(
        'QSDM Hive Update Blocked',
        `Hive could not authenticate the release metadata. ${
          error instanceof Error ? error.message : String(error)
        }`
      );
      return;
    }

    try {
      await startTrustedUpdateDownload(info, release);
    } catch (error) {
      log.error(
        'QSDM Hive update download failed; a retry is scheduled',
        error
      );
    }
  });

  autoUpdater.on('error', (error) => {
    log.error('QSDM Hive updater error; a retry is scheduled', error);
    scheduleRetryCheck();
  });

  autoUpdater.on('update-downloaded', async (info: UpdateDownloadedEvent) => {
    console.log('Update downloaded');
    console.log(info);

    try {
      const release =
        trustedRelease || (await ensureTrustedReleaseForUpdate(true));
      assertUpdateInfoMatchesTrustedRelease(info, release);
      await verifyDownloadedQsdmHiveUpdate(info.downloadedFile, release);
    } catch (error) {
      log.error('QSDM Hive refused a downloaded update', error);
      if (info.downloadedFile) {
        await fs.promises.unlink(info.downloadedFile).catch((unlinkError) => {
          log.warn('Could not remove rejected Hive update', unlinkError);
        });
      }
      dialog.showErrorBox(
        'QSDM Hive Update Rejected',
        `The downloaded installer did not match the signed QSDM release. ${
          error instanceof Error ? error.message : String(error)
        }`
      );
      return;
    }

    sendRendererEvent(mainWindow, RendererEndpoints.UPDATE_DOWNLOADED, info);
    revealMainWindow(mainWindow);

    if (installPromptOpen) {
      return;
    }
    installPromptOpen = true;
    try {
      const options = {
        type: 'question' as const,
        title: 'QSDM Hive Update Ready',
        buttons: ['Update and Restart'],
        defaultId: 0,
        cancelId: 0,
        noLink: true,
        message: 'The approved QSDM Hive update is ready.',
        detail: `Version ${info.version} must be installed before Hive can continue.`,
      };
      const result =
        mainWindow && !mainWindow.isDestroyed()
          ? await dialog.showMessageBox(mainWindow, options)
          : await dialog.showMessageBox(options);
      if (result.response !== 0) {
        return;
      }
      await appCleanup();
      app.isQuitting = true;
      autoUpdater.quitAndInstall();
    } finally {
      installPromptOpen = false;
    }
  });
}

function createCheckForTheUpdatesInterval() {
  if (!interval) {
    interval = setInterval(() => {
      runScheduledUpdateCheck('interval');
    }, CHECK_INTERVAL);
  }

  if (!initialCheckTimeout) {
    initialCheckTimeout = setTimeout(() => {
      initialCheckTimeout = null;
      runScheduledUpdateCheck('startup');
    }, INITIAL_CHECK_DELAY);
  }
}

async function runUpdateCheck(
  reason: string
): Promise<UpdateCheckResult | null> {
  if (updateCheckPromise) {
    return updateCheckPromise;
  }

  updateCheckPromise = (async () => {
    log.info(`QSDM Hive ${reason} update check`);
    await ensureAppUpdaterConfigured();
    await ensureTrustedReleaseForUpdate(true);
    const result = await autoUpdater.checkForUpdates();
    clearRetryTimeout();
    return result;
  })()
    .catch((error) => {
      scheduleRetryCheck();
      throw error;
    })
    .finally(() => {
      updateCheckPromise = null;
    });

  return updateCheckPromise;
}

async function runScheduledUpdateCheck(reason: string) {
  try {
    await runUpdateCheck(reason);
  } catch (error) {
    log.error('QSDM Hive update check failed', error);
  }
}

function scheduleRetryCheck() {
  if (retryTimeout) {
    return;
  }
  retryTimeout = setTimeout(() => {
    retryTimeout = null;
    runScheduledUpdateCheck('retry');
  }, RETRY_INTERVAL);
}

function clearRetryTimeout() {
  if (!retryTimeout) {
    return;
  }
  clearTimeout(retryTimeout);
  retryTimeout = null;
}

function startTrustedUpdateDownload(
  info: UpdateInfo,
  release: VerifiedQsdmHiveRelease
): Promise<string[]> {
  assertUpdateInfoMatchesTrustedRelease(info, release);
  trustedRelease = release;
  if (updateDownloadPromise) {
    return updateDownloadPromise;
  }

  updateDownloadPromise = autoUpdater
    .downloadUpdate()
    .catch((error) => {
      scheduleRetryCheck();
      throw error;
    })
    .finally(() => {
      updateDownloadPromise = null;
    });
  return updateDownloadPromise;
}

function sendRendererEvent(
  mainWindow: BrowserWindow | undefined,
  endpoint: RendererEndpoints,
  payload?: unknown
) {
  if (
    !mainWindow ||
    mainWindow.isDestroyed() ||
    mainWindow.webContents.isDestroyed()
  ) {
    return;
  }
  mainWindow.webContents.send(endpoint, payload);
}

function revealMainWindow(mainWindow: BrowserWindow | undefined) {
  if (!mainWindow || mainWindow.isDestroyed()) {
    return;
  }
  if (mainWindow.isMinimized()) {
    mainWindow.restore();
  }
  mainWindow.show();
  mainWindow.focus();
}

export function resetAppUpdaterForTests() {
  if (interval) {
    clearInterval(interval);
  }
  if (initialCheckTimeout) {
    clearTimeout(initialCheckTimeout);
  }
  clearRetryTimeout();
  interval = null;
  initialCheckTimeout = null;
  updaterConfigured = false;
  listenersConfigured = false;
  trustedRelease = null;
  updateCheckPromise = null;
  updateDownloadPromise = null;
  installPromptOpen = false;
}
