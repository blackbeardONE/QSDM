import { Event } from 'electron';

import { autoUpdater, UpdateCheckResult } from 'electron-updater';

import { ensureAppUpdaterConfigured } from '../../AppUpdater';

export const checkAppUpdate = async (
  event: Event
): Promise<UpdateCheckResult | null> => {
  await ensureAppUpdaterConfigured();
  return autoUpdater.checkForUpdates();
};
