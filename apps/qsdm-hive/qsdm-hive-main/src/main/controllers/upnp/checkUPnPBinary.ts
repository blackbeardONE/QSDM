import type { Event } from 'electron';
import type { UPnPBinaryStatus } from 'models';

// eslint-disable-next-line @typescript-eslint/no-unused-vars
export async function checkUPnPBinary(_: Event): Promise<UPnPBinaryStatus> {
  return {
    exists: true,
    path: 'Built-in QSDM UPnP client',
    downloadConfigured: false,
  };
}
