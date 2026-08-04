/**
 * @jest-environment node
 */

import { BrowserWindow, dialog } from 'electron';

import { autoUpdater } from 'electron-updater';

import { RendererEndpoints } from 'config/endpoints';

import { app } from './app';
import {
  getQsdmHiveUpdateFeedUrl,
  initializeAppUpdater,
  resetAppUpdaterForTests,
  shouldEnableAutoUpdates,
} from './AppUpdater';
import {
  getVerifiedQsdmHiveRelease,
  verifyDownloadedQsdmHiveUpdate,
} from './services/qsdmReleaseManifest';

import type { UpdateDownloadedEvent, UpdateInfo } from 'electron-updater';
import type { EventEmitter } from 'events';

jest.mock('electron-updater', () => {
  const { EventEmitter } = jest.requireActual('events');
  const updater = new EventEmitter();
  Object.assign(updater, {
    app: { baseCachePath: 'original-cache' },
    checkForUpdates: jest.fn(),
    downloadUpdate: jest.fn(),
    quitAndInstall: jest.fn(),
    setFeedURL: jest.fn(),
  });
  return { autoUpdater: updater };
});

jest.mock('./services/qsdmReleaseManifest', () => ({
  getVerifiedQsdmHiveRelease: jest.fn(),
  verifyDownloadedQsdmHiveUpdate: jest.fn(),
}));

type MockUpdater = EventEmitter & {
  app: { baseCachePath: string };
  checkForUpdates: jest.Mock;
  downloadUpdate: jest.Mock;
  quitAndInstall: jest.Mock;
  setFeedURL: jest.Mock;
};

const updater = autoUpdater as unknown as MockUpdater;
const release = {
  manifest: { version: '1.4.12' },
  installerUrl: 'https://qsdm.tech/downloads/qsdm-hive-1.4.12.exe',
  installer: { name: 'qsdm-hive-1.4.12.exe' },
};

const flushPromises = () =>
  new Promise<void>((resolve) => {
    setImmediate(resolve);
  });

const updateInfo: UpdateInfo = {
  version: '1.4.12',
  files: [],
  path: 'qsdm-hive-1.4.12.exe',
  sha512: 'test-sha512',
  releaseDate: '2026-08-04T00:00:00.000Z',
};

describe('AppUpdater release channels', () => {
  it('keeps stable builds on the production feed', () => {
    expect(getQsdmHiveUpdateFeedUrl({}, '1.3.95')).toBe(
      'https://qsdm.tech/downloads'
    );
  });

  it('isolates unsigned previews and disables their automatic updater', () => {
    const previewVersion = '1.3.95-unsigned-preview.1';

    expect(getQsdmHiveUpdateFeedUrl({}, previewVersion)).toBe(
      'https://qsdm.tech/downloads/unsigned-preview'
    );
    expect(shouldEnableAutoUpdates({}, previewVersion)).toBe(false);
    expect(
      shouldEnableAutoUpdates({ QSDM_ENABLE_AUTO_UPDATES: '1' }, previewVersion)
    ).toBe(false);
  });

  it('does not let environment overrides disable stable production updates', () => {
    expect(
      shouldEnableAutoUpdates(
        {
          NODE_ENV: 'production',
          QSDM_DISABLE_AUTO_UPDATES: '1',
        },
        '1.4.12'
      )
    ).toBe(true);
  });
});

describe('AppUpdater mandatory update flow', () => {
  const originalEnableAutoUpdates = process.env.QSDM_ENABLE_AUTO_UPDATES;
  const mainWindow = {
    isDestroyed: jest.fn(() => false),
    isMinimized: jest.fn(() => true),
    restore: jest.fn(),
    show: jest.fn(),
    focus: jest.fn(),
    webContents: {
      isDestroyed: jest.fn(() => false),
      send: jest.fn(),
    },
  };
  const appCleanup = jest.fn(() => Promise.resolve());

  beforeEach(() => {
    jest.useFakeTimers({ doNotFake: ['setImmediate'] });
    process.env.QSDM_ENABLE_AUTO_UPDATES = '1';
    resetAppUpdaterForTests();
    updater.removeAllListeners();
    updater.app = { baseCachePath: 'original-cache' };
    updater.checkForUpdates.mockReset();
    updater.downloadUpdate.mockReset();
    updater.quitAndInstall.mockReset();
    updater.setFeedURL.mockReset();
    updater.downloadUpdate.mockResolvedValue(['installer']);
    (getVerifiedQsdmHiveRelease as jest.Mock).mockReset();
    (getVerifiedQsdmHiveRelease as jest.Mock).mockResolvedValue(release);
    (verifyDownloadedQsdmHiveUpdate as jest.Mock).mockReset();
    (verifyDownloadedQsdmHiveUpdate as jest.Mock).mockResolvedValue(undefined);
    (dialog.showErrorBox as jest.Mock).mockReset();
    (dialog.showMessageBox as jest.Mock).mockReset();
    (dialog.showMessageBox as jest.Mock).mockResolvedValue({ response: 0 });
    mainWindow.isDestroyed.mockClear();
    mainWindow.isMinimized.mockClear();
    mainWindow.restore.mockClear();
    mainWindow.show.mockClear();
    mainWindow.focus.mockClear();
    mainWindow.webContents.isDestroyed.mockClear();
    mainWindow.webContents.send.mockClear();
    appCleanup.mockClear();
    app.isQuitting = false;
  });

  afterEach(() => {
    resetAppUpdaterForTests();
    updater.removeAllListeners();
    jest.useRealTimers();
    if (originalEnableAutoUpdates === undefined) {
      delete process.env.QSDM_ENABLE_AUTO_UPDATES;
    } else {
      process.env.QSDM_ENABLE_AUTO_UPDATES = originalEnableAutoUpdates;
    }
  });

  it('checks one second after startup and always starts the trusted download', async () => {
    updater.checkForUpdates.mockImplementation(async () => {
      updater.emit('update-available', updateInfo);
      return {
        isUpdateAvailable: true,
        updateInfo,
      };
    });

    await initializeAppUpdater(
      mainWindow as unknown as BrowserWindow,
      appCleanup
    );
    expect(updater.listenerCount('update-available')).toBe(1);
    expect(updater.checkForUpdates).not.toHaveBeenCalled();

    await jest.advanceTimersByTimeAsync(1000);
    await flushPromises();

    expect(updater.checkForUpdates).toHaveBeenCalledTimes(1);
    expect(updater.downloadUpdate).toHaveBeenCalledTimes(1);
    expect(mainWindow.webContents.send).toHaveBeenCalledWith(
      RendererEndpoints.UPDATE_AVAILABLE,
      updateInfo
    );
    expect(mainWindow.restore).toHaveBeenCalledTimes(1);
    expect(mainWindow.show).toHaveBeenCalledTimes(1);
    expect(mainWindow.focus).toHaveBeenCalledTimes(1);
  });

  it('retries a transient startup failure after one minute', async () => {
    updater.checkForUpdates
      .mockRejectedValueOnce(new Error('temporary network failure'))
      .mockResolvedValueOnce({
        isUpdateAvailable: false,
        updateInfo: { version: '1.4.11' },
      });

    await initializeAppUpdater(
      mainWindow as unknown as BrowserWindow,
      appCleanup
    );
    await jest.advanceTimersByTimeAsync(1000);
    await flushPromises();
    expect(updater.checkForUpdates).toHaveBeenCalledTimes(1);

    await jest.advanceTimersByTimeAsync(60_000);
    await flushPromises();
    expect(updater.checkForUpdates).toHaveBeenCalledTimes(2);
  });

  it('verifies the installer, notifies every renderer, and forces a restart prompt', async () => {
    updater.checkForUpdates.mockResolvedValue({
      isUpdateAvailable: false,
      updateInfo: { version: '1.4.11' },
    });
    await initializeAppUpdater(
      mainWindow as unknown as BrowserWindow,
      appCleanup
    );

    const downloadedInfo: UpdateDownloadedEvent = {
      ...updateInfo,
      downloadedFile: 'qsdm-hive-1.4.12.exe',
    };
    updater.emit('update-downloaded', downloadedInfo);
    await flushPromises();
    await flushPromises();

    expect(verifyDownloadedQsdmHiveUpdate).toHaveBeenCalledWith(
      'qsdm-hive-1.4.12.exe',
      release
    );
    expect(mainWindow.webContents.send).toHaveBeenCalledWith(
      RendererEndpoints.UPDATE_DOWNLOADED,
      expect.objectContaining({ version: '1.4.12' })
    );
    expect(dialog.showMessageBox).toHaveBeenCalledWith(
      mainWindow,
      expect.objectContaining({
        title: 'QSDM Hive Update Ready',
        buttons: ['Update and Restart'],
      })
    );
    expect(appCleanup).toHaveBeenCalledTimes(1);
    expect(updater.quitAndInstall).toHaveBeenCalledTimes(1);
    expect(app.isQuitting).toBe(true);
  });
});
