import { IpcRendererEvent, ipcRenderer } from 'electron';

import { RendererEndpoints } from 'config/endpoints';

export default function onAppUpdate(
  callback: (event: IpcRendererEvent, ...args: unknown[]) => void
) {
  ipcRenderer.on(RendererEndpoints.UPDATE_AVAILABLE, callback);
  return () =>
    ipcRenderer.removeListener(RendererEndpoints.UPDATE_AVAILABLE, callback);
}
