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
import type { Event } from 'electron';
import fsSync from 'fs';
import fs from 'fs/promises';
import http from 'http';
import https from 'https';
import path from 'path';

import { RendererEndpoints } from 'config/endpoints';
import { get } from 'lodash';
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
import { trackEvent } from './services/analytics';
import ExecutableMonitor from './services/ExecutableMonitorService';
import qsdmHiveTasks from './services/qsdmHiveTasks';
import { resolveHtmlPath, sleep } from './util';

const isDev = process.env.NODE_ENV === 'development';
const isDebug = isDev || process.env.DEBUG_PROD === 'true';
const isProductionRuntime = app.isPackaged || process.env.NODE_ENV === 'production';
const DEV_RENDERER_WAIT_ATTEMPTS = 90;
const DEV_RENDERER_WAIT_MS = 1000;

const isMac = process.platform === 'darwin';
let tray: Tray | null = null;

let mainWindow: BrowserWindow | null = null;

const writeStartupLog = (
  message: string,
  details?: Record<string, unknown>
): void => {
  try {
    const appDataRoot =
      process.env.QSDM_HIVE_APPDATA_ROOT ||
      process.env.APPDATA || path.join(app.getPath('appData'), 'Roaming');
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

writeStartupLog('main module loaded', {
  isPackaged: app.isPackaged,
  nodeEnv: process.env.NODE_ENV,
  defaultApp: process.defaultApp,
  argv: process.argv,
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
        background: #082b35;
        color: #f4fbfd;
      }
      body {
        margin: 0;
        min-height: 100vh;
        display: grid;
        place-items: center;
        background:
          radial-gradient(circle at 20% 10%, rgba(57, 205, 215, 0.18), transparent 30%),
          linear-gradient(135deg, #041b23 0%, #0b3d4a 58%, #104f61 100%);
      }
      .shell {
        width: min(520px, calc(100vw - 64px));
        text-align: center;
      }
      .mark {
        width: 56px;
        height: 56px;
        margin: 0 auto 24px;
        display: grid;
        place-items: center;
        border-radius: 14px;
        background: #07151b;
        color: #51e3cb;
        font-size: 34px;
        font-weight: 800;
        box-shadow: 0 16px 45px rgba(0, 0, 0, 0.28);
      }
      h1 {
        margin: 0 0 10px;
        font-size: 24px;
        line-height: 1.25;
      }
      p {
        margin: 0 auto;
        max-width: 460px;
        color: #bce7ed;
        font-size: 14px;
        line-height: 1.6;
      }
      .bar {
        height: 4px;
        width: 190px;
        overflow: hidden;
        border-radius: 999px;
        margin: 28px auto 0;
        background: rgba(255, 255, 255, 0.14);
      }
      .bar::after {
        content: "";
        display: block;
        height: 100%;
        width: 42%;
        border-radius: inherit;
        background: #7be7ef;
        animation: slide 1.1s ease-in-out infinite;
      }
      @keyframes slide {
        0% { transform: translateX(-110%); }
        100% { transform: translateX(260%); }
      }
    </style>
  </head>
  <body>
    <main class="shell">
      <div class="mark">Q</div>
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
        continue;
      }

      throw error;
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
    },
  });
  writeStartupLog('browser window created');

  let mainWindowWasShown = false;
  const showMainWindow = () => {
    if (!mainWindow || mainWindowWasShown) {
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
  await loadStartupScreen(
    'Starting QSDM Hive',
    'Preparing the desktop shell, local wallet, task state, and QSDM Core connection.'
  );
  mainWindow.webContents.on(
    'console-message',
    (_event, level, message, line, sourceId) => {
      if (level >= 2) {
        console.log(`[renderer:${level}] ${message} (${sourceId}:${line})`);
      }
    }
  );
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

  if (process.defaultApp) {
    if (process.argv.length >= 2) {
      app.setAsDefaultProtocolClient('qsdm-hive', process.execPath, [
        path.resolve(process.argv[1]),
      ]);
    }
  } else {
    app.setAsDefaultProtocolClient('qsdm-hive');
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
const isSingleInstance = app.requestSingleInstanceLock();
writeStartupLog('single instance lock checked', { isSingleInstance });

if (!isSingleInstance) {
  writeStartupLog('single instance lock denied; quitting');
  app.quit();
}

// This method will be called when Electron has finished
// initialization and is ready to create browser windows.
// Some APIs can only be used after this event occurs.
app.on('ready', async () => {
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
});

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
  app.isQuitting = true;
});

const TEN_MINUTES = 10 * 60 * 1000;

setInterval(() => {
  console.log('memory usage', process.memoryUsage());
}, TEN_MINUTES);
