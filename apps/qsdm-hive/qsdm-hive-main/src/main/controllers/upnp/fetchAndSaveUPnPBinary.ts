/* eslint-disable @cspell/spellchecker */
import fs from 'fs';

import axios from 'axios';
import { Event } from 'electron';
import {
  getDownloadedExecutableName,
  getUpnpFileName,
} from 'config/upnpPathResolver';
import { getAppDataPath } from 'main/node/helpers/getAppDataPath';

const executableName = getUpnpFileName();

// eslint-disable-next-line @typescript-eslint/no-unused-vars
export async function fetchAndSaveUPnPBinary(_: Event) {
  const appDataPath: string = getAppDataPath();
  const binaryDirectory = `${appDataPath}/upnp-bin`;
  const binaryPath = `${binaryDirectory}/${getDownloadedExecutableName()}`;

  await ensureDirectoryExists(binaryDirectory);

  const binaryBaseUrl = process.env.QSDM_UPNP_BINARY_BASE_URL;
  if (!binaryBaseUrl) {
    throw new Error(
      `QSDM UPnP binary download is not configured. Place ${getDownloadedExecutableName()} at ${binaryPath}, or use Network Tunneling.`
    );
  }

  const parsedBaseUrl = new URL(binaryBaseUrl);
  const isLocalDownloadHost =
    parsedBaseUrl.hostname === 'localhost' ||
    parsedBaseUrl.hostname === '127.0.0.1';
  if (parsedBaseUrl.protocol !== 'https:' && !isLocalDownloadHost) {
    throw new Error(
      'QSDM UPnP binary downloads must use HTTPS. Use Network Tunneling instead.'
    );
  }

  const binaryUrl = `${binaryBaseUrl.replace(/\/+$/, '')}/${executableName}`;
  const response = await axios
    .get(binaryUrl, {
      responseType: 'stream',
      maxRedirects: 3,
      onDownloadProgress: (progressEvent) => {
        if (progressEvent.total) {
          const percentCompleted = Math.round(
            (progressEvent.loaded * 100) / progressEvent.total
          );
          console.log(`${percentCompleted}% downloaded`);
        }
      },
    })
    .catch((error) => {
      if (error?.code === 'ERR_FR_TOO_MANY_REDIRECTS') {
        throw new Error(
          'QSDM UPnP binary URL redirects too many times. Use Network Tunneling or configure QSDM_UPNP_BINARY_BASE_URL to a direct QSDM-owned file host.'
        );
      }
      throw error;
    });

  const writer = fs.createWriteStream(binaryPath);

  response.data.pipe(writer);

  return new Promise<string>((resolve, reject) => {
    writer.on('finish', () => {
      console.log('Download completed.');
      resolve(binaryPath);
    });

    writer.on('error', (err: NodeJS.ErrnoException) => {
      console.error('Error downloading the binary:', err);
      reject(err);
    });
  });
}

async function ensureDirectoryExists(path: string): Promise<void> {
  return new Promise((resolve, reject) => {
    fs.mkdir(path, { recursive: true }, (err) => {
      if (err) {
        reject(err);
      } else {
        resolve();
      }
    });
  });
}
