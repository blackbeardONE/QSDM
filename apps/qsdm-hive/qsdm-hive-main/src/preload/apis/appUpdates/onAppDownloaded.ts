import { IpcRendererEvent, ipcRenderer } from 'electron';

import { RendererEndpoints } from 'config/endpoints';

export default function onAppDownloaded(
  callback: (event: IpcRendererEvent, ...args: unknown[]) => void
) {
  ipcRenderer.on(RendererEndpoints.UPDATE_DOWNLOADED, callback);
  return () =>
    ipcRenderer.removeListener(RendererEndpoints.UPDATE_DOWNLOADED, callback);
}
