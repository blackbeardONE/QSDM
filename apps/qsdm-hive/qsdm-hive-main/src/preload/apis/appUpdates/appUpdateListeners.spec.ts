/**
 * @jest-environment node
 */

import { ipcRenderer } from 'electron';

import { RendererEndpoints } from 'config/endpoints';

import onAppDownloaded from './onAppDownloaded';
import onAppUpdate from './onAppUpdate';

describe('app update preload listeners', () => {
  beforeEach(() => {
    (ipcRenderer.on as jest.Mock).mockClear();
    (ipcRenderer.removeListener as jest.Mock).mockClear();
  });

  it('removes only the update-available callback that subscribed', () => {
    const callback = jest.fn();
    const unsubscribe = onAppUpdate(callback);

    expect(ipcRenderer.on).toHaveBeenCalledWith(
      RendererEndpoints.UPDATE_AVAILABLE,
      callback
    );
    unsubscribe();
    expect(ipcRenderer.removeListener).toHaveBeenCalledWith(
      RendererEndpoints.UPDATE_AVAILABLE,
      callback
    );
  });

  it('removes only the update-downloaded callback that subscribed', () => {
    const callback = jest.fn();
    const unsubscribe = onAppDownloaded(callback);

    expect(ipcRenderer.on).toHaveBeenCalledWith(
      RendererEndpoints.UPDATE_DOWNLOADED,
      callback
    );
    unsubscribe();
    expect(ipcRenderer.removeListener).toHaveBeenCalledWith(
      RendererEndpoints.UPDATE_DOWNLOADED,
      callback
    );
  });
});
