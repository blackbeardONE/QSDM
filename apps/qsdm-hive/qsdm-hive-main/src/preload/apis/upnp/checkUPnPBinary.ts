import config from 'config';
import type { UPnPBinaryStatus } from 'models';
import sendMessage from 'preload/sendMessage';

export const checkUPnPBinary = (): Promise<UPnPBinaryStatus> =>
  sendMessage(config.endpoints.CHECK_UPNP_BINARY, {});
