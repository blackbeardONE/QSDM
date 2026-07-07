import { Event } from 'electron';

import { autoUpdater } from 'electron-updater';

import { ensureAppUpdaterConfigured } from '../../AppUpdater';

export const downloadAppUpdate = async (event: Event): Promise<string[]> => {
  await ensureAppUpdaterConfigured();
  return autoUpdater.downloadUpdate();
};
