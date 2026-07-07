import { BrowserWindow, dialog } from 'electron';
import path from 'path';

import log from 'electron-log';
import { autoUpdater } from 'electron-updater';

import { RendererEndpoints } from 'config/endpoints';

import { app } from './app';
import getUserConfig from './controllers/getUserConfig';
import { getAppDataPath } from './node/helpers/getAppDataPath';

const CHECK_INTERVAL = 6 * 1000 * 60 * 60;
const QSDM_HIVE_UPDATE_FEED_URL = 'https://qsdm.tech/downloads';

let interval: NodeJS.Timer | null = null;
let updaterConfigured = false;
let listenersConfigured = false;

export function shouldEnableAutoUpdates(env: NodeJS.ProcessEnv = process.env) {
  if (env.QSDM_DISABLE_AUTO_UPDATES === '1') {
    return false;
  }
  if (env.QSDM_ENABLE_AUTO_UPDATES === '1') {
    return true;
  }

  return app.isPackaged || env.NODE_ENV === 'production';
}

export function getQsdmHiveUpdateFeedUrl(env: NodeJS.ProcessEnv = process.env) {
  return (
    env.QSDM_HIVE_UPDATE_FEED_URL?.trim() ||
    env.QSDM_HIVE_UPDATE_BASE_URL?.trim() ||
    QSDM_HIVE_UPDATE_FEED_URL
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
  return autoUpdater.checkForUpdatesAndNotify();
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

  console.log('QSDM Hive updater feed ', getQsdmHiveUpdateFeedUrl());
  console.log(
    'original updater cache path ',
    (autoUpdater as any).app.baseCachePath
  );
  Object.defineProperty((autoUpdater as any).app, 'baseCachePath', {
    get() {
      return path.join(getAppDataPath(), 'updater-cache');
    },
  });
  console.log(
    'overwritten updater cache path ',
    (autoUpdater as any).app.baseCachePath
  );
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

  autoUpdater.on('update-available', (info) => {
    getUserConfig()
      .then((appConfig) => {
        // const mainWindow = BrowserWindow.getFocusedWindow();
        if (!appConfig?.autoUpdatesDisabled) {
          // If autoUpdatesDisabled is not set, autoupdates are enabled
          // if auto updates are enabled, download the update
          autoUpdater.downloadUpdate();
        } else if (mainWindow) {
          // if auto updates are disabled, inform the user about the update
          mainWindow.webContents.send(RendererEndpoints.UPDATE_AVAILABLE, info);
        }
      })
      .catch((error) => {
        console.log(error);
      });
  });

  autoUpdater.on('update-downloaded', (info) => {
    console.log('Update downloaded');
    console.log(info);

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
      .then(() => autoUpdater.checkForUpdates())
      .catch((error) => console.error('QSDM Hive update check failed', error));
  }, 25000);
}
