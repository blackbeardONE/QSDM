import { BrowserWindow, dialog } from 'electron';
import fs from 'fs';
import path from 'path';

import log from 'electron-log';
import {
  autoUpdater,
  UpdateDownloadedEvent,
  UpdateInfo,
} from 'electron-updater';

import { RendererEndpoints } from 'config/endpoints';

import { app } from './app';
import getUserConfig from './controllers/getUserConfig';
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

const CHECK_INTERVAL = 6 * 1000 * 60 * 60;
const QSDM_HIVE_UPDATE_FEED_URL = 'https://qsdm.tech/downloads';
const QSDM_HIVE_UNSIGNED_PREVIEW_UPDATE_FEED_URL =
  'https://qsdm.tech/downloads/unsigned-preview';

type AutoUpdaterCacheApp = {
  baseCachePath: string;
};

let interval: NodeJS.Timer | null = null;
let updaterConfigured = false;
let listenersConfigured = false;
let trustedRelease: VerifiedQsdmHiveRelease | null = null;

export function shouldEnableAutoUpdates(
  env: NodeJS.ProcessEnv = process.env,
  currentVersion = getCurrentHiveVersion()
) {
  // An unsigned preview must never become an automatic trust transition.
  // Preview users move to the first signed stable release manually.
  if (isUnsignedPreviewHiveVersion(currentVersion)) {
    return false;
  }

  if (env.QSDM_DISABLE_AUTO_UPDATES === '1') {
    return false;
  }
  if (env.QSDM_ENABLE_AUTO_UPDATES === '1') {
    return true;
  }

  return app.isPackaged || env.NODE_ENV === 'production';
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
  createCheckForTheUpdatesInterval();
  setListeners(mainWindow, appCleanup);
}

export async function checkForUpdates() {
  await ensureAppUpdaterConfigured();
  await ensureTrustedReleaseForUpdate(true);
  return autoUpdater.checkForUpdatesAndNotify();
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
    try {
      const release = await ensureTrustedReleaseForUpdate();
      assertUpdateInfoMatchesTrustedRelease(info, release);
      const appConfig = await getUserConfig();
      // const mainWindow = BrowserWindow.getFocusedWindow();
      if (!appConfig?.autoUpdatesDisabled) {
        await autoUpdater.downloadUpdate();
      } else if (mainWindow) {
        mainWindow.webContents.send(RendererEndpoints.UPDATE_AVAILABLE, info);
      }
    } catch (error) {
      log.error('QSDM Hive refused an untrusted update offer', error);
      dialog.showErrorBox(
        'QSDM Hive Update Blocked',
        `Hive could not authenticate the release metadata. ${
          error instanceof Error ? error.message : String(error)
        }`
      );
    }
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

    getUserConfig()
      .then((appConfig) => {
        if (appConfig?.autoUpdatesDisabled) {
          mainWindow?.webContents.send(RendererEndpoints.UPDATE_DOWNLOADED);
        }
      })
      .catch((error) => {
        console.log(error);
      });

    setTimeout(() => {
      dialog
        .showMessageBox({
          type: 'question',
          title: 'QSDM Hive',
          buttons: ['Update and Restart'],
          defaultId: 0,
          cancelId: -1,
          noLink: true,
          message: 'The approved QSDM Hive update is ready.',
          detail: `Version ${info.version} must be installed before Hive can continue.`,
        })
        .then(async () => {
          await appCleanup();
          app.isQuitting = true;
          autoUpdater.quitAndInstall();
        });
    }, 2000);
  });
}

function createCheckForTheUpdatesInterval() {
  if (!interval) {
    interval = setInterval(() => {
      console.log('interval update check');
      ensureAppUpdaterConfigured()
        .then(() => ensureTrustedReleaseForUpdate(true))
        .then(() => autoUpdater.checkForUpdates())
        .catch((error) =>
          console.error('QSDM Hive update check failed', error)
        );
    }, CHECK_INTERVAL);
  }

  // runs the first check 25sec after the app initialization
  setTimeout(() => {
    console.log('initial update check');
    ensureAppUpdaterConfigured()
      .then(() => ensureTrustedReleaseForUpdate(true))
      .then(() => autoUpdater.checkForUpdates())
      .catch((error) => console.error('QSDM Hive update check failed', error));
  }, 25000);
}
