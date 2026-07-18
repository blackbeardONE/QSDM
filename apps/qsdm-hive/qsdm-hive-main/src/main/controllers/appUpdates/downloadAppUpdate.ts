import { autoUpdater } from 'electron-updater';

import {
  ensureAppUpdaterConfigured,
  ensureTrustedReleaseForUpdate,
} from '../../AppUpdater';

export const downloadAppUpdate = async (): Promise<string[]> => {
  await ensureAppUpdaterConfigured();
  await ensureTrustedReleaseForUpdate(true);
  return autoUpdater.downloadUpdate();
};
