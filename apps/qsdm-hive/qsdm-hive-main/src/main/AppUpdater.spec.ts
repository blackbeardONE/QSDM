jest.mock('electron-updater', () => ({
  autoUpdater: {},
}));

jest.mock('./controllers/getUserConfig', () => jest.fn());

import {
  getQsdmHiveUpdateFeedUrl,
  shouldEnableAutoUpdates,
} from './AppUpdater';

describe('AppUpdater release channels', () => {
  it('keeps stable builds on the production feed', () => {
    expect(getQsdmHiveUpdateFeedUrl({}, '1.3.95')).toBe(
      'https://qsdm.tech/downloads'
    );
  });

  it('isolates unsigned previews and disables their automatic updater', () => {
    const previewVersion = '1.3.95-unsigned-preview.1';

    expect(getQsdmHiveUpdateFeedUrl({}, previewVersion)).toBe(
      'https://qsdm.tech/downloads/unsigned-preview'
    );
    expect(shouldEnableAutoUpdates({}, previewVersion)).toBe(false);
    expect(
      shouldEnableAutoUpdates({ QSDM_ENABLE_AUTO_UPDATES: '1' }, previewVersion)
    ).toBe(false);
  });
});
