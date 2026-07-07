import config from 'config';
import {
  QsdmCellAccountRequest,
  QsdmCellAccountResponse,
} from 'models/api/qsdm';
import sendMessage from 'preload/sendMessage';

export default (
  payload?: QsdmCellAccountRequest
): Promise<QsdmCellAccountResponse> =>
  sendMessage(config.endpoints.GET_QSDM_CELL_ACCOUNT, payload);
