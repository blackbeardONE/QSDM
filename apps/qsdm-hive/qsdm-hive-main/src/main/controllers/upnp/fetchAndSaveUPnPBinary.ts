import type { Event } from 'electron';

// Kept for IPC compatibility with older renderer code. UPnP no longer needs a
// downloaded helper executable; Hive uses the built-in SSDP/IGD client.
// eslint-disable-next-line @typescript-eslint/no-unused-vars
export async function fetchAndSaveUPnPBinary(_: Event) {
  return 'Built-in QSDM UPnP client';
}
