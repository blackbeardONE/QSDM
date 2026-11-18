import config from 'config';
import { QsdmWalletRecoverRequest } from 'models/api/qsdm';
import sendMessage from 'preload/sendMessage';

export default (
  payload: QsdmWalletRecoverRequest
): Promise<{ address: string }> =>
  sendMessage(config.endpoints.RECOVER_QSDM_WALLET, payload);
