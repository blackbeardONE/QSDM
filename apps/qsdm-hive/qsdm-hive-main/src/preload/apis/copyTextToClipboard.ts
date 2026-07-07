import config from 'config';
import sendMessage from 'preload/sendMessage';

import type { ClipboardWriteRequest, ClipboardWriteResponse } from 'models/api';

export default (
  payload: ClipboardWriteRequest
): Promise<ClipboardWriteResponse> =>
  sendMessage(config.endpoints.COPY_TEXT_TO_CLIPBOARD, payload);
