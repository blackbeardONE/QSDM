import config from 'config';
import { QsdmWalletForgetRequest } from 'models/api/qsdm';
import sendMessage from 'preload/sendMessage';

export default (
  payload: QsdmWalletForgetRequest
): Promise<{ address: string }> =>
  sendMessage(config.endpoints.FORGET_QSDM_WALLET, payload);
