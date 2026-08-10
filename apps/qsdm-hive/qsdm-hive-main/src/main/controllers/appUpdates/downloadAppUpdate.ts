import { downloadTrustedAppUpdate } from '../../AppUpdater';

export const downloadAppUpdate = async (): Promise<string[]> => {
  return downloadTrustedAppUpdate();
};
