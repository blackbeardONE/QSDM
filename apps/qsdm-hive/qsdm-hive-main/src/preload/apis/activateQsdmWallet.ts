import config from 'config';
import {
  QsdmWalletActivateRequest,
  QsdmWalletActivateResponse,
} from 'models/api/qsdm';
import sendMessage from 'preload/sendMessage';

export default (
  payload: QsdmWalletActivateRequest
): Promise<QsdmWalletActivateResponse> =>
  sendMessage(config.endpoints.ACTIVATE_QSDM_WALLET, payload);
