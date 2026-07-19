const windowMock = {
  webContents: {
    send: jest.fn(),
    setWindowOpenHandler: jest.fn(),
    on: jest.fn(),
  },
  loadURL: jest.fn(),
  on: jest.fn(),
  show: jest.fn(),
  close: jest.fn(),
};

const BrowserWindowMock: any = jest.fn(() => windowMock);
BrowserWindowMock.getAllWindows = jest.fn(() => []);
BrowserWindowMock.getFocusedWindow = jest.fn(() => windowMock);
BrowserWindowMock.fromWebContents = jest.fn(() => windowMock);

export const BrowserWindow = BrowserWindowMock;

export const app = {
  getPath: jest.fn(() => process.cwd()),
  getVersion: jest.fn(() => '0.0.0'),
  getName: jest.fn(() => 'QSDM Hive'),
  isPackaged: false,
  on: jest.fn(),
  once: jest.fn(),
  whenReady: jest.fn(() => Promise.resolve()),
  quit: jest.fn(),
  relaunch: jest.fn(),
  requestSingleInstanceLock: jest.fn(() => true),
  setLoginItemSettings: jest.fn(),
  getLoginItemSettings: jest.fn(() => ({ openAtLogin: false })),
};

export const dialog = {
  showErrorBox: jest.fn(),
  showMessageBox: jest.fn(),
};

export const shell = {
  openExternal: jest.fn(),
  openPath: jest.fn(),
};

export const safeStorage = {
  isEncryptionAvailable: jest.fn(() => true),
  getSelectedStorageBackend: jest.fn(() => 'dpapi'),
  encryptString: jest.fn((value: string) => Buffer.from(value, 'utf-8')),
  decryptString: jest.fn((value: Buffer) => value.toString('utf-8')),
};

export const ipcMain = {
  on: jest.fn(),
  handle: jest.fn(),
  removeHandler: jest.fn(),
};

export const ipcRenderer = {
  on: jest.fn(),
  once: jest.fn(),
  send: jest.fn(),
  invoke: jest.fn(),
  removeListener: jest.fn(),
};

export const contextBridge = {
  exposeInMainWorld: jest.fn(),
};

export const powerMonitor = {
  on: jest.fn(),
};

export const powerSaveBlocker = {
  start: jest.fn(() => 1),
  stop: jest.fn(),
  isStarted: jest.fn(() => false),
};

export type Event = unknown;
export type IpcRendererEvent = unknown;
