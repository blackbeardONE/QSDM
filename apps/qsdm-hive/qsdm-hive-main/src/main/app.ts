import { app as _app, App } from 'electron';
import fs from 'fs';
import path from 'path';

interface ExtendedApp extends App {
  isQuitting: boolean;
}

const appDataRootOverride = process.env.QSDM_HIVE_APPDATA_ROOT?.trim();

if (appDataRootOverride) {
  const appDataRoot = path.resolve(appDataRootOverride);
  fs.mkdirSync(appDataRoot, { recursive: true });
  _app.setPath('appData', appDataRoot);
  _app.setPath('userData', path.join(appDataRoot, 'qsdm-hive-user-data'));
}

export const app: ExtendedApp = Object.assign(_app, {
  isQuitting: false,
});
