import { UpdateCheckResult } from 'electron-updater';

import { checkForUpdates } from '../../AppUpdater';

export const checkAppUpdate = async (): Promise<UpdateCheckResult | null> => {
  return checkForUpdates();
};
