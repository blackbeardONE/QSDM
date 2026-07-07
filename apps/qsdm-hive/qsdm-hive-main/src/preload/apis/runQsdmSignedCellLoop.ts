import config from 'config';
import {
  QsdmSignedCellLoopRequest,
  QsdmSignedCellLoopResponse,
} from 'models/api/qsdm';
import sendMessage from 'preload/sendMessage';

export default (
  payload?: QsdmSignedCellLoopRequest
): Promise<QsdmSignedCellLoopResponse> =>
  sendMessage(config.endpoints.RUN_QSDM_SIGNED_CELL_LOOP, payload);
