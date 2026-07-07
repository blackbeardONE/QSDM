import { BrowserWindow, shell } from 'electron';

const MAX_EXTERNAL_URL_LENGTH = 2048;
const DEV_RENDERER_PORT = process.env.PORT || '1212';
const isDev = process.env.NODE_ENV === 'development';

export const isAllowedExternalUrl = (rawUrl: string): boolean => {
  if (typeof rawUrl !== 'string') return false;
  if (!rawUrl || rawUrl.length > MAX_EXTERNAL_URL_LENGTH) return false;

  try {
    const url = new URL(rawUrl);
    return url.protocol === 'https:' || url.protocol === 'http:';
  } catch {
    return false;
  }
};

export const isAllowedInternalNavigationUrl = (rawUrl: string): boolean => {
  if (typeof rawUrl !== 'string' || !rawUrl) return false;

  if (rawUrl.startsWith('data:text/html;charset=utf-8,')) {
    return true;
  }

  try {
    const url = new URL(rawUrl);

    if (url.protocol === 'file:') {
      return true;
    }

    if (
      isDev &&
      url.protocol === 'http:' &&
      (url.hostname === '127.0.0.1' || url.hostname === 'localhost') &&
      url.port === DEV_RENDERER_PORT
    ) {
      return true;
    }

    return false;
  } catch {
    return false;
  }
};

export const openValidatedExternalUrl = async (rawUrl: string) => {
  if (!isAllowedExternalUrl(rawUrl)) {
    throw new Error('Only HTTP and HTTPS links can be opened from QSDM Hive.');
  }

  await shell.openExternal(rawUrl);
};

export const configureMainWindowSecurity = (
  browserWindow: BrowserWindow
): void => {
  const { webContents } = browserWindow;

  webContents.setWindowOpenHandler(({ url }) => {
    void openValidatedExternalUrl(url).catch((error) => {
      console.warn('Blocked external window open', {
        url,
        error: (error as Error).message,
      });
    });

    return { action: 'deny' };
  });

  webContents.on('will-navigate', (event, url) => {
    if (isAllowedInternalNavigationUrl(url)) {
      return;
    }

    event.preventDefault();

    if (isAllowedExternalUrl(url)) {
      void openValidatedExternalUrl(url).catch((error) => {
        console.warn('Blocked external navigation', {
          url,
          error: (error as Error).message,
        });
      });
      return;
    }

    console.warn('Blocked unsafe navigation', { url });
  });

  webContents.on('will-attach-webview', (event) => {
    event.preventDefault();
  });

  webContents.session.setPermissionRequestHandler(
    (_webContents, _permission, callback) => {
      callback(false);
    }
  );

  webContents.session.setPermissionCheckHandler(() => false);
};
