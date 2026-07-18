import { webUtils } from 'electron';

import config from 'config';
import sendMessage from 'preload/sendMessage';

export const getBrandingFolderPath = (file: File): string => {
  const filePath = webUtils.getPathForFile(file);
  const separatorIndex = Math.max(
    filePath.lastIndexOf('/'),
    filePath.lastIndexOf('\\')
  );

  return separatorIndex > 0 ? filePath.slice(0, separatorIndex) : '';
};

export const validateBrandingFolder = (folderPath: string): Promise<boolean> =>
  sendMessage(config.endpoints.VALIDATE_BRANDING_FOLDER, folderPath);

export const copyBrandingFolder = (folderPath: string): Promise<void> =>
  sendMessage(config.endpoints.COPY_BRANDING_FOLDER, folderPath);

export const getBrandingConfig = (): Promise<any | null> =>
  sendMessage(config.endpoints.GET_BRANDING_CONFIG, {});

export const getBrandLogo = (): Promise<string | null> =>
  sendMessage(config.endpoints.GET_BRAND_LOGO, {});
