/**
 * @jest-environment node
 */

import { clipboard } from 'electron';

import { copyTextToClipboard } from './copyTextToClipboard';

import type { Event } from 'electron';

jest.mock('electron', () => ({
  clipboard: {
    writeText: jest.fn(),
  },
}));

describe('copyTextToClipboard', () => {
  it('writes text through the Electron native clipboard', async () => {
    await expect(
      copyTextToClipboard({} as Event, { text: 'qsdm-wallet-address' })
    ).resolves.toEqual({ copied: true });

    expect(clipboard.writeText).toHaveBeenCalledWith('qsdm-wallet-address');
  });
});
