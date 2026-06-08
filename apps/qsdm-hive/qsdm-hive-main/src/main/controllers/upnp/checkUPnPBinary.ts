/* eslint-disable @cspell/spellchecker */

import {
  checkUpnpBinaryExists,
  UPNP_BINARY_PATH,
} from 'config/upnpPathResolver';
import type { Event } from 'electron';
import type { UPnPBinaryStatus } from 'models';

// eslint-disable-next-line @typescript-eslint/no-unused-vars
export async function checkUPnPBinary(_: Event): Promise<UPnPBinaryStatus> {
  const binaryExists = await checkUpnpBinaryExists();
  return {
    exists: binaryExists,
    path: UPNP_BINARY_PATH,
    downloadConfigured: !!process.env.QSDM_UPNP_BINARY_BASE_URL,
  };
}
