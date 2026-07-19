/* eslint-disable @cspell/spellchecker */
import 'dotenv/config';

import {
  BrowserWindow,
  dialog,
  Menu,
  MenuItemConstructorOptions,
  shell,
  Tray,
} from 'electron';
import fsSync from 'fs';
import fs from 'fs/promises';
import http from 'http';
import https from 'https';
import path from 'path';

import { get } from 'lodash';

import { RendererEndpoints } from 'config/endpoints';
import { UserAppConfig } from 'models';

import { app } from './app';
import { initializeAppUpdater } from './AppUpdater';
import {
  copyBrandingFolder,
  getAllAccounts,
  getUserConfig,
  setActiveAccount,
  storeUserConfig,
} from './controllers';
import db from './db';
import { handleDeepLinks } from './handleDeepLinks';
import initHandlers from './initHandlers';
import { configureLogger } from './logger';
import { getCurrentActiveAccountName } from './node/helpers';
import { getAppDataPath } from './node/helpers/getAppDataPath';
import execute from './node/helpers/settleuPnP';
import { setUpPowerStateManagement } from './powerMonitor';
import { configureMainWindowSecurity } from './security/externalNavigation';
import { trackEvent } from './services/analytics';
import ExecutableMonitor from './services/ExecutableMonitorService';
import qsdmHiveTasks from './services/qsdmHiveTasks';
import {
  cleanupQsdmSignerSecretStore,
  initializeQsdmSignerSecretStore,
} from './services/qsdmSignerSecretStore';
import {
  startQsdmWalletProviderBroker,
  stopQsdmWalletProviderBroker,
} from './services/qsdmWalletProviderBroker';
import { registerQsdmWalletProviderNativeHost } from './services/qsdmWalletProviderNativeHost';
import { resolveHtmlPath, sleep } from './util';

import type { Event } from 'electron';

const isDev = process.env.NODE_ENV === 'development';
const isDebug = isDev || process.env.DEBUG_PROD === 'true';
const isProductionRuntime =
  app.isPackaged || process.env.NODE_ENV === 'production';
const DEV_RENDERER_WAIT_ATTEMPTS = 90;
const DEV_RENDERER_WAIT_MS = 1000;
const SMOKE_RENDERER_WAIT_ATTEMPTS = 40;
const SMOKE_RENDERER_WAIT_MS = 250;
const isSmokeTest = process.env.QSDM_HIVE_SMOKE_TEST === '1';

let tray: Tray | null = null;

let mainWindow: BrowserWindow | null = null;

const writeStartupLog = (
  message: string,
  details?: Record<string, unknown>
): void => {
  try {
    const appDataRoot =
      process.env.QSDM_HIVE_APPDATA_ROOT ||
      process.env.APPDATA ||
      path.join(app.getPath('appData'), 'Roaming');
    const logDir = path.join(appDataRoot, 'qsdm-hive', 'logs');
    fsSync.mkdirSync(logDir, { recursive: true });
    const detailText = details ? ` ${JSON.stringify(details)}` : '';
    fsSync.appendFileSync(
      path.join(logDir, 'startup.log'),
      `[${new Date().toISOString()}] ${message}${detailText}\n`
    );
  } catch {
    // Startup diagnostics must never block the app.
  }
};

const writeSmokeResult = (
  status: 'ok' | 'failed',
  details?: Record<string, unknown>
): void => {
  try {
    const appDataRoot =
      process.env.QSDM_HIVE_APPDATA_ROOT ||
      process.env.APPDATA ||
      path.join(app.getPath('appData'), 'Roaming');
    const logDir = path.join(appDataRoot, 'qsdm-hive', 'logs');
    fsSync.mkdirSync(logDir, { recursive: true });
    fsSync.writeFileSync(
      path.join(logDir, 'smoke-result.json'),
      JSON.stringify(
        {
          status,
          timestamp: new Date().toISOString(),
          appData: app.getPath('appData'),
          userData: app.getPath('userData'),
          isPackaged: app.isPackaged,
          ...details,
        },
        null,
        2
      )
    );
  } catch {
    // Smoke diagnostics must never block the app.
  }
};

writeStartupLog('main module loaded', {
  isPackaged: app.isPackaged,
  nodeEnv: process.env.NODE_ENV,
  defaultApp: process.defaultApp,
  argv: process.argv,
  smokeTest: isSmokeTest,
});

const getRuntimePath = (folderName: 'assets' | 'branding') => {
  if (app.isPackaged) {
    return path.join(process.resourcesPath, folderName);
  }

  if (isProductionRuntime) {
    return path.join(__dirname, '../../../../', folderName);
  }

  return path.join(__dirname, '../../', folderName);
};

const getPreloadPath = () => {
  if (isProductionRuntime) {
    return path.join(__dirname, 'preload.js');
  }

  return path.join(__dirname, '../../.erb/dll/preload.js');
};

if (process.env.NODE_ENV === 'production') {
  // eslint-disable-next-line global-require
  const sourceMapSupport = require('source-map-support');
  sourceMapSupport.install();
}

if (isDebug) {
  // eslint-disable-next-line global-require
  require('electron-debug')({ showDevTools: false });
}

const startExecutableMonitor = async (mainWindow: BrowserWindow) => {
  const appDataPath = getAppDataPath();
  const executablesDirPath = path.join(appDataPath, 'executables');

  const executableMonitor = new ExecutableMonitor({
    folderPath: executablesDirPath,
    sendAlert: async (executable, file) => {
      // eslint-disable-next-line no-console
      mainWindow.webContents.send(RendererEndpoints.TASK_EXECUTABLE_CHANGED, {
        executable,
        file,
      });
    },
  });
  executableMonitor.start();
};

const closePortConnection = async () => {
  await db.get('curr_port').then(async (port: any) => {
    if (port && port !== '0' && port !== 'undefined') {
      await execute.closePortCommand(port);
      await db.put('curr_port', '0');
    }
  });
  await db.put('Port_Exposure', 'Pending');
};

const escapeHtml = (value: string) =>
  value
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');

const startupHtml = (title: string, detail: string) => `
<!doctype html>
<html>
  <head>
    <meta charset="utf-8" />
    <meta
      http-equiv="Content-Security-Policy"
      content="default-src 'none'; style-src 'unsafe-inline'; img-src data:"
    />
    <style>
      :root {
        color-scheme: dark;
        font-family: Inter, Segoe UI, Arial, sans-serif;
        background: #061f27;
        color: #f4fbfd;
      }
      body {
        margin: 0;
        min-height: 100vh;
        display: grid;
        place-items: center;
        overflow: hidden;
        background:
          radial-gradient(circle at 52% 38%, rgba(247, 191, 66, 0.16), transparent 16rem),
          radial-gradient(circle at 24% 18%, rgba(80, 226, 202, 0.2), transparent 18rem),
          radial-gradient(circle at 80% 76%, rgba(83, 166, 186, 0.18), transparent 20rem),
          linear-gradient(135deg, #03151b 0%, #063140 48%, #0b5260 100%);
      }
      body::before {
        content: "";
        position: fixed;
        inset: -2px;
        opacity: 0.36;
        background-image:
          linear-gradient(rgba(124, 232, 239, 0.13) 1px, transparent 1px),
          linear-gradient(90deg, rgba(124, 232, 239, 0.13) 1px, transparent 1px);
        background-size: 46px 46px;
        mask-image: radial-gradient(circle at center, black, transparent 72%);
      }
      body::after {
        content: "CELL";
        position: fixed;
        right: clamp(28px, 8vw, 110px);
        bottom: clamp(18px, 6vw, 76px);
        color: rgba(247, 191, 66, 0.08);
        font-size: clamp(84px, 16vw, 190px);
        font-weight: 800;
        letter-spacing: 0;
        pointer-events: none;
      }
      .shell {
        position: relative;
        width: min(560px, calc(100vw - 64px));
        text-align: center;
        z-index: 1;
      }
      .mark {
        position: relative;
        width: 122px;
        height: 122px;
        margin: 0 auto 28px;
        display: grid;
        place-items: center;
        border-radius: 26px;
        background:
          linear-gradient(135deg, rgba(4, 19, 25, 0.94), rgba(8, 53, 64, 0.82)),
          radial-gradient(circle at 50% 50%, rgba(247, 191, 66, 0.18), transparent 64%);
        border: 1px solid rgba(126, 230, 222, 0.34);
        color: #f7bf42;
        font-size: 60px;
        font-weight: 800;
        box-shadow:
          0 22px 70px rgba(0, 0, 0, 0.34),
          0 0 0 12px rgba(124, 232, 239, 0.04);
      }
      .mark::before,
      .mark::after {
        content: "";
        position: absolute;
        border: 1px solid rgba(126, 230, 222, 0.26);
        border-radius: 999px;
      }
      .mark::before {
        inset: -34px;
        border-top-color: rgba(247, 191, 66, 0.68);
        border-right-color: rgba(247, 191, 66, 0.18);
        animation: spin 9s linear infinite;
      }
      .mark::after {
        inset: -18px;
        border-left-color: rgba(124, 232, 239, 0.7);
        animation: spin 6s linear infinite reverse;
      }
      .cell-node {
        position: absolute;
        width: 10px;
        height: 10px;
        border-radius: 999px;
        background: #7ce8ef;
        box-shadow: 0 0 22px rgba(124, 232, 239, 0.75);
      }
      .cell-node.one {
        top: -39px;
        left: 54px;
      }
      .cell-node.two {
        right: -29px;
        top: 56px;
        background: #f7bf42;
        box-shadow: 0 0 24px rgba(247, 191, 66, 0.72);
      }
      .cell-node.three {
        bottom: -29px;
        left: 25px;
      }
      .eyebrow {
        margin: 0 0 10px;
        color: #f7bf42;
        font-size: 12px;
        font-weight: 800;
        letter-spacing: 0;
        text-transform: uppercase;
      }
      h1 {
        margin: 0 0 10px;
        font-size: 28px;
        line-height: 1.25;
      }
      p {
        margin: 0 auto;
        max-width: 500px;
        color: #c8eef1;
        font-size: 14px;
        line-height: 1.6;
      }
      .bar {
        height: 4px;
        width: min(260px, 68vw);
        overflow: hidden;
        border-radius: 999px;
        margin: 32px auto 0;
        background: rgba(255, 255, 255, 0.12);
        box-shadow: inset 0 0 0 1px rgba(124, 232, 239, 0.12);
      }
      .bar::after {
        content: "";
        display: block;
        height: 100%;
        width: 42%;
        border-radius: inherit;
        background: linear-gradient(90deg, #7ce8ef, #f7bf42);
        animation: slide 1.1s ease-in-out infinite;
      }
      @keyframes spin {
        to { transform: rotate(360deg); }
      }
      @keyframes slide {
        0% { transform: translateX(-110%); }
        100% { transform: translateX(260%); }
      }
    </style>
  </head>
  <body>
    <main class="shell">
      <div class="mark">
        Q
        <span class="cell-node one"></span>
        <span class="cell-node two"></span>
        <span class="cell-node three"></span>
      </div>
      <p class="eyebrow">QSDM Hive / CELL Network</p>
      <h1>${escapeHtml(title)}</h1>
      <p>${escapeHtml(detail)}</p>
      <div class="bar"></div>
    </main>
  </body>
</html>
`;

const isLoadAbortError = (error: any) => {
  const code = String(error?.code || '');
  const message = String(error?.message || error || '');
  return (
    code === 'ERR_ABORTED' ||
    error?.errno === -3 ||
    error?.errorCode === -3 ||
    message.includes('ERR_ABORTED') ||
    message.includes('(-3)')
  );
};

const isStartupScreenLoadErrorIgnorable = (error: any) =>
  isLoadAbortError(error) || String(error?.code || '') === 'ERR_FAILED';

const loadWindowUrl = async (url: string, attempts = 1) => {
  let lastError: any;

  for (let attempt = 1; attempt <= attempts; attempt += 1) {
    try {
      await mainWindow?.loadURL(url);
      return;
    } catch (error: any) {
      lastError = error;
      if (isLoadAbortError(error) && attempt < attempts) {
        console.warn(
          `Hive window load aborted; retrying ${attempt}/${attempts}`,
          url
        );
        await sleep(250);
      } else {
        throw error;
      }
    }
  }

  throw lastError;
};

const loadStartupScreen = async (title: string, detail: string) => {
  if (!mainWindow || mainWindow.isDestroyed()) return;
  try {
    await loadWindowUrl(
      `data:text/html;charset=utf-8,${encodeURIComponent(
        startupHtml(title, detail)
      )}`,
      2
    );
  } catch (error: any) {
    if (!isStartupScreenLoadErrorIgnorable(error)) {
      console.warn('Hive startup screen failed to load', error);
    }
  }
};

const canReachUrl = (url: string): Promise<boolean> =>
  new Promise((resolve) => {
    const client = url.startsWith('https:') ? https : http;
    const request = client.get(url, (response) => {
      response.resume();
      resolve(Boolean(response.statusCode && response.statusCode < 500));
    });

    request.setTimeout(1000, () => {
      request.destroy();
      resolve(false);
    });
    request.on('error', () => resolve(false));
  });

const waitForRenderer = async (url: string) => {
  for (let attempt = 1; attempt <= DEV_RENDERER_WAIT_ATTEMPTS; attempt += 1) {
    if (await canReachUrl(url)) {
      return true;
    }

    if (attempt === 1 || attempt % 10 === 0) {
      await loadStartupScreen(
        'Starting QSDM Hive',
        `Waiting for the Hive interface at ${url}. Attempt ${attempt}/${DEV_RENDERER_WAIT_ATTEMPTS}.`
      );
    }
    await sleep(DEV_RENDERER_WAIT_MS);
  }

  return false;
};

const loadRenderer = async () => {
  const rendererUrl = resolveHtmlPath('index.html');

  if (isDev) {
    const rendererReady = await waitForRenderer(rendererUrl);
    if (!rendererReady) {
      await loadStartupScreen(
        'Hive UI is not running',
        'The Electron shell is alive, but the React renderer did not answer on 127.0.0.1:1212. Start Hive with npm start, or restart the Hive launcher so it starts both the renderer and desktop window.'
      );
      return;
    }
  }

  try {
    await loadWindowUrl(rendererUrl, 3);
  } catch (error: any) {
    if (isLoadAbortError(error)) {
      console.warn('Hive renderer load was aborted after retries', error);
      return;
    }

    console.error('Hive renderer failed to load', error);
    await loadStartupScreen(
      'Hive UI failed to load',
      error?.message || 'The renderer could not be loaded. Restart QSDM Hive.'
    );
  }
};

type RendererSmokeProbe = {
  documentUrl: string;
  hasRoot: boolean;
  rootChildren: number;
  hasMainBridge: boolean;
  hasCoreStatusApi: boolean;
  hasBrandingFolderPathApi: boolean;
};

const waitForRendererSmokeProbe = async (): Promise<RendererSmokeProbe> => {
  let lastProbe: RendererSmokeProbe | undefined;
  let lastError: unknown;

  for (let attempt = 1; attempt <= SMOKE_RENDERER_WAIT_ATTEMPTS; attempt += 1) {
    if (!mainWindow || mainWindow.isDestroyed()) {
      throw new Error(
        'Hive smoke window closed before the renderer was ready.'
      );
    }

    try {
      const probe: RendererSmokeProbe =
        await mainWindow.webContents.executeJavaScript(
          `(() => {
          const root = document.getElementById('root');
          return {
            documentUrl: window.location.href,
            hasRoot: Boolean(root),
            rootChildren: root?.childElementCount ?? 0,
            hasMainBridge: typeof window.main === 'object',
            hasCoreStatusApi:
              typeof window.main?.getQsdmCoreStatus === 'function',
            hasBrandingFolderPathApi:
              typeof window.main?.getBrandingFolderPath === 'function',
          };
        })()`,
          true
        );
      lastProbe = probe;
      if (
        probe.hasRoot &&
        probe.rootChildren > 0 &&
        probe.hasMainBridge &&
        probe.hasCoreStatusApi &&
        probe.hasBrandingFolderPathApi
      ) {
        return probe;
      }
    } catch (error) {
      lastError = error;
    }

    await sleep(SMOKE_RENDERER_WAIT_MS);
  }

  throw new Error(
    `Hive renderer smoke probe timed out: ${JSON.stringify({
      lastProbe,
      lastError: lastError instanceof Error ? lastError.message : lastError,
    })}`
  );
};

export const appCleanup = async () => {
  console.log('Cleaning up the app');
  try {
    /**
     * processes cleanup
     */
    qsdmHiveTasks.stopTasksOnAppQuit();
    await sleep(2500);
    await closePortConnection();
  } catch (error) {
    console.log(error);
  }
  console.timeEnd('Session duration');
};

const setLaunchOnRestartOnByDefault = async (userConfig: UserAppConfig) => {
  const launchOnRestart = get(userConfig, 'launchOnRestart');
  console.log({ launchOnRestart });
  const launchOnRestartWasNeverSet = launchOnRestart === undefined;

  if (launchOnRestartWasNeverSet) {
    app.setLoginItemSettings({
      openAtLogin: true,
    });

    await storeUserConfig({} as Event, {
      settings: { ...userConfig, launchOnRestart: true },
    });
  }
};

const installExtensions = async () => {
  // eslint-disable-next-line global-require
  const installer = require('electron-devtools-installer');
  const forceDownload = !!process.env.UPGRADE_EXTENSIONS;
  const extensions = ['REACT_DEVELOPER_TOOLS'];

  return installer
    .default(
      extensions.map((name) => installer[name]),
      forceDownload
    )
    .catch(console.log);
};

const main = async (): Promise<void> => {
  initHandlers();

  if (isProductionRuntime && !isSmokeTest) {
    try {
      const registration = registerQsdmWalletProviderNativeHost();
      console.log('QSDM browser wallet bridge registered', registration);
    } catch (error) {
      console.warn(
        'QSDM browser wallet bridge registration failed; Hive wallet remains available locally.',
        error
      );
    }
  }

  const signerSecretStatus = initializeQsdmSignerSecretStore();
  console.log('QSDM signer secret storage initialized', {
    configured: signerSecretStatus.configured,
    protectedAtRest: signerSecretStatus.protectedAtRest,
    reason:
      'reason' in signerSecretStatus ? signerSecretStatus.reason : undefined,
  });

  const showHiveWindow = () => {
    if (!mainWindow) return;
    if (mainWindow.isMinimized()) mainWindow.restore();
    mainWindow.show();
    mainWindow.focus();
  };
  await startQsdmWalletProviderBroker({
    showHive: showHiveWindow,
    openWallet: () => {
      showHiveWindow();
      mainWindow?.webContents.send(
        RendererEndpoints.NAVIDATE_TO_ROUTE,
        '/settings/wallet'
      );
    },
  });

  let allAccounts = await getAllAccounts({} as Event);
  await getCurrentActiveAccountName().catch(async () => {
    console.warn(
      'NO ACTIVE ACCOUNT IN DB - setting first available account as active'
    );

    if (allAccounts[0]) {
      await setActiveAccount({} as Event, {
        accountName: allAccounts[0].accountName,
      });
    }
  });

  const userConfig = await getUserConfig();
  const hasCompletedOnboarding = !!userConfig.onboardingCompleted;
  const hasUnlockPin = !!userConfig.pin;

  if (!hasCompletedOnboarding && hasUnlockPin) {
    if (allAccounts.length === 0) {
      allAccounts = await getAllAccounts({} as Event);
    }
    if (allAccounts.length > 0) {
      await storeUserConfig({} as Event, {
        settings: { ...userConfig, onboardingCompleted: true },
      });
    }
  }
};

// Handle creating/removing shortcuts on Windows when installing/uninstalling.
// eslint-disable-next-line global-require
if (require('electron-squirrel-startup')) {
  // eslint-disable-line global-require
  writeStartupLog('electron-squirrel-startup requested quit', {
    argv: process.argv,
  });
  app.quit();
}

const createWindow = async () => {
  writeStartupLog('createWindow start');
  if (isDebug && process.env.QSDM_HIVE_INSTALL_DEVTOOLS === '1') {
    await installExtensions();
  }

  const RESOURCES_PATH = getRuntimePath('assets');

  const userConfig = await getUserConfig();

  const getAssetPath = (...paths: string[]): string => {
    return path.join(RESOURCES_PATH, ...paths);
  };

  // Create the browser window.
  mainWindow = new BrowserWindow({
    show: false,
    width: 1740,
    height: 1040,
    minWidth: 1152,
    minHeight: 810,
    backgroundColor: '#082b35',

    icon: getAssetPath('icon.png'),
    webPreferences: {
      preload: getPreloadPath(),
      nodeIntegration: false,
      contextIsolation: true,
      sandbox: true,
      webviewTag: false,
      webSecurity: true,
      allowRunningInsecureContent: false,
      devTools: isDebug,
      spellcheck: false,
    },
  });
  configureMainWindowSecurity(mainWindow);
  writeStartupLog('browser window created');

  let mainWindowWasShown = false;
  const showMainWindow = () => {
    if (isSmokeTest || !mainWindow || mainWindowWasShown) {
      return;
    }
    mainWindowWasShown = true;
    if (process.env.START_MINIMIZED) {
      console.log('QSDM Hive window start minimized');
      mainWindow.minimize();
    } else {
      console.log('QSDM Hive window show');
      mainWindow.show();
      mainWindow.focus();
      mainWindow.moveTop();
    }
  };

  mainWindow.once('ready-to-show', showMainWindow);
  mainWindow.webContents.once('did-finish-load', showMainWindow);
  mainWindow.webContents.on(
    'console-message',
    (_event, level, message, line, sourceId) => {
      if (level >= 2) {
        console.log(`[renderer:${level}] ${message} (${sourceId}:${line})`);
      }
    }
  );
  await loadStartupScreen(
    'Starting QSDM Hive',
    'Preparing the desktop shell, local wallet, task state, and QSDM Core connection.'
  );
  if (isSmokeTest) {
    try {
      await loadRenderer();
      const renderer = await waitForRendererSmokeProbe();
      writeSmokeResult('ok', {
        resourcesPath: getRuntimePath('assets'),
        preloadPath: getPreloadPath(),
        renderer,
      });
      writeStartupLog('renderer smoke test complete; quitting', { renderer });
      app.isQuitting = true;
      setTimeout(() => app.quit(), 250);
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      writeSmokeResult('failed', { message });
      writeStartupLog('renderer smoke test failed; exiting', { message });
      app.isQuitting = true;
      setTimeout(() => app.exit(1), 250);
    }
    return;
  }
  setTimeout(showMainWindow, 10000);

  await main().catch((err): void => {
    writeStartupLog('main bootstrap failed', {
      message: err?.message,
      stack: err?.stack,
    });
    dialog.showErrorBox('Something went wrong!', err.message);
    trackEvent('app_error', { error: err.message });
    app.quit();
  });
  writeStartupLog('main bootstrap complete');

  await loadRenderer();
  writeStartupLog('renderer load complete');

  // Set initial zoom level
  mainWindow.webContents.setZoomLevel(-0.3);

  // Force zoom level back to -0.3 when it changes
  mainWindow.webContents.on('zoom-changed', () => {
    mainWindow?.webContents.setZoomLevel(-0.3);
  });

  // Maintain zoom level after navigation
  mainWindow.webContents.on('did-navigate', () => {
    mainWindow?.webContents.setZoomLevel(-0.3);
  });

  // Maintain zoom level after navigation within the page
  mainWindow.webContents.on('did-navigate-in-page', () => {
    mainWindow?.webContents.setZoomLevel(-0.3);
  });

  // Handle maximize/unmaximize events
  mainWindow.on('maximize', () => {
    mainWindow?.webContents.setZoomLevel(-0.3);
  });

  mainWindow.on('unmaximize', () => {
    mainWindow?.webContents.setZoomLevel(-0.3);
  });

  // Handle resize events
  mainWindow.on('resize', () => {
    mainWindow?.webContents.setZoomLevel(-0.3);
  });

  startExecutableMonitor(mainWindow);

  Menu.setApplicationMenu(null);

  app.on('before-quit', async () => {
    if (app.isQuitting) return;
    stopQsdmWalletProviderBroker();
    cleanupQsdmSignerSecretStore();
    await appCleanup();

    trackEvent('app_closed');
    app.isQuitting = true;
    app.quit();
  });

  const limitLogsSize = get(userConfig, 'limitLogsSize', false);

  configureLogger(limitLogsSize);

  console.time('Session duration');

  await setLaunchOnRestartOnByDefault(userConfig);

  await initializeAppUpdater(mainWindow, appCleanup);

  await setUpPowerStateManagement();

  try {
    const brandingSourcePath = getRuntimePath('branding');

    // Check if branding folder exists
    await fs.access(brandingSourcePath);

    // Copy branding folder to app data
    await copyBrandingFolder({} as Event, brandingSourcePath);
    console.log('Successfully copied branding folder to app data');
  } catch (error) {
    console.error('Branding folder error:', {
      error: (error as Error).message,
      stack: (error as Error).stack,
    });
  }

  mainWindow.on('closed', () => {
    mainWindow = null;
  });

  try {
    const protocolRegistered = process.defaultApp
      ? process.argv.length >= 2 &&
        app.setAsDefaultProtocolClient('qsdm-hive', process.execPath, [
          path.resolve(process.argv[1]),
        ])
      : app.setAsDefaultProtocolClient('qsdm-hive');

    writeStartupLog('protocol registration checked', {
      protocol: 'qsdm-hive',
      registered: Boolean(protocolRegistered),
    });
  } catch (error) {
    writeStartupLog('protocol registration skipped', {
      protocol: 'qsdm-hive',
      error: (error as Error).message,
    });
    console.warn('QSDM Hive protocol registration skipped:', error);
  }

  mainWindow.on('close', (event) => {
    if (!app.isQuitting) {
      event.preventDefault();
      if (mainWindow) {
        mainWindow.hide();
      }
      // Minimize the window instead of closing
    }
  });
};

process.on('uncaughtException', (error) => {
  console.error('Uncaught Exception ->', error);
});

dialog.showErrorBox = (title, content) => {
  console.log(`[Error Box]: ${title}\n${content}`);
};

const createMenu = () => {
  const template = [
    ...(process.platform === 'darwin'
      ? ([
          {
            label: app.getName(),
            submenu: [
              {
                label: 'FAQ',
                click: () => {
                  shell.openExternal('https://qsdm.tech/docs');
                },
              },
              {
                label: 'Report an issue',
                click: () => {
                  shell.openExternal('https://qsdm.tech');
                },
              },
              {
                label: 'Hide',
                accelerator: 'CmdOrCtrl+H',
                click: () => {
                  mainWindow?.hide();
                },
              },
              {
                label: 'Quit',
                accelerator: 'CmdOrCtrl+Q',
                click: () => {
                  app.quit();
                },
              },
            ],
          },
          {
            label: 'Edit',
            submenu: [
              { label: 'Undo', role: 'undo' },
              { label: 'Redo', role: 'redo' },
              { type: 'separator' },
              { label: 'Cut', role: 'cut' },
              { label: 'Copy', role: 'copy' },
              { label: 'Paste', role: 'paste' },
              { type: 'separator' },
              // eslint-disable-next-line @cspell/spellchecker
              { label: 'Select All', role: 'selectall' },
            ],
          },
        ] as MenuItemConstructorOptions[])
      : []),
    {
      label: 'Window',
      submenu: [
        {
          label: 'Zoom In',
          // eslint-disable-next-line @cspell/spellchecker
          role: 'zoomin',
          accelerator: 'CommandOrControl+=',
        },
        {
          label: 'Zoom Out',
          // eslint-disable-next-line @cspell/spellchecker
          role: 'zoomout',
          accelerator: 'CommandOrControl+-',
        },
        {
          label: 'Hide',
          accelerator: 'CmdOrCtrl+H',
          click: () => {
            mainWindow?.hide();
          },
        },
        {
          label: 'Exit',
          accelerator: 'CmdOrCtrl+Q',
          click: () => {
            app.quit();
          },
        },
      ],
    } as MenuItemConstructorOptions,
    {
      label: 'Help',
      submenu: [
        {
          label: 'FAQ',
          click: () => {
            shell.openExternal('https://qsdm.tech/docs');
          },
        },
        {
          label: 'Report an issue',
          click: () => {
            shell.openExternal('https://qsdm.tech');
          },
        },
      ],
    },
  ];

  const menu = Menu.buildFromTemplate(template);
  Menu.setApplicationMenu(menu);
};

function createTray() {
  const RESOURCES_PATH = getRuntimePath('assets');

  const getAssetPath = (...paths: string[]): string => {
    return path.join(RESOURCES_PATH, ...paths);
  };
  const iconPath = getAssetPath('icons/trayIcon.png');
  tray = new Tray(iconPath);

  const contextMenu = Menu.buildFromTemplate([
    {
      label: 'Open',
      click: () => {
        if (mainWindow) mainWindow.show();
      },
    },
    {
      label: 'Quit',
      click: () => {
        app.quit();
      },
    },
  ]);

  tray.setToolTip('QSDM Hive');
  tray.setContextMenu(contextMenu);
  tray.on('click', () => {
    if (mainWindow) {
      mainWindow.focus();
      mainWindow.show();
    }
  });
  tray.on('right-click', () => {
    if (tray) tray.popUpContextMenu();
  });
  tray.on('double-click', () => {
    if (mainWindow) mainWindow.show();
  });
}
app.on('activate', () => {
  if (mainWindow) {
    if (mainWindow.isMinimized()) {
      mainWindow.restore();
    } else if (!mainWindow.isVisible()) {
      mainWindow.show();
    }
    mainWindow.focus();
  } else {
    // Recreate the window if it doesn't exist
    createWindow();
  }
});

app.on('open-url', (event, url) => {
  event.preventDefault();
  handleDeepLinks([url]);
});

// Check for a single instance
const isSingleInstance = isSmokeTest || app.requestSingleInstanceLock();
writeStartupLog('single instance lock checked', { isSingleInstance });

if (!isSingleInstance) {
  writeStartupLog('single instance lock denied; quitting');
  app.quit();
}

const startAppAfterReady = async () => {
  writeStartupLog('app ready');
  /**
   * @dev remove selected tasks from the the DB cache
   * Some users might still have cache reciods in the neDB, so we need to remove them
   * The DB cache is not used anymore because of its performance issues
   */
  await db.put('startedTasksCache', '');
  // We manually compact the datafile to avoid the file growing indefinitely until app restart,
  // as caching task states handles big chunks of data.
  db.compactDatafile();
  createWindow();
  createMenu();
  createTray();
  trackEvent('app_started');

  app.on('second-instance', async (_e: Electron.Event, argv: string[]) => {
    if (mainWindow) {
      if (mainWindow.isMinimized()) mainWindow.restore();
      mainWindow.focus();
    }
    await handleDeepLinks(argv);
  });

  await handleDeepLinks(process.argv);
};

const reportReadyFailure = (error: unknown) => {
  writeStartupLog('app ready bootstrap failed', {
    message: error instanceof Error ? error.message : String(error),
    stack: error instanceof Error ? error.stack : undefined,
  });
  app.quit();
};

// Some packaged startups can finish Electron initialization before this module
// reaches the old ready event handler. app.whenReady() covers both paths.
if (isSingleInstance) {
  if (typeof app.isReady === 'function' && app.isReady()) {
    startAppAfterReady().catch(reportReadyFailure);
  } else {
    app.whenReady().then(startAppAfterReady).catch(reportReadyFailure);
  }
}

// Quit when all windows are closed, except on macOS. There, it's common
// for applications and their menu bar to stay active until the user quits
// explicitly with Cmd + Q.
app.on('window-all-closed', () => {
  if (process.platform !== 'darwin') {
    app.quit();
  }
});

app.on('activate', () => {
  // On OS X it's common to re-create a window in the app when the
  // dock icon is clicked and there are no other windows open.
  if (BrowserWindow.getAllWindows().length === 0) {
    createWindow();
    createMenu();
    createTray();
  }
});

app.on('will-quit', () => {
  stopQsdmWalletProviderBroker();
  cleanupQsdmSignerSecretStore();
  app.isQuitting = true;
});

const TEN_MINUTES = 10 * 60 * 1000;

setInterval(() => {
  console.log('memory usage', process.memoryUsage());
}, TEN_MINUTES);
