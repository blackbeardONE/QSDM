import { clipboard } from 'electron';

import type { Event } from 'electron';
import type { ClipboardWriteRequest, ClipboardWriteResponse } from 'models/api';

export const copyTextToClipboard = async (
  _: Event,
  payload: ClipboardWriteRequest
): Promise<ClipboardWriteResponse> => {
  clipboard.writeText(payload.text);
  return { copied: true };
};
