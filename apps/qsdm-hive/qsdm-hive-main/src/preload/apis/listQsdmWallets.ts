import config from 'config';
import { QsdmWalletListResponse } from 'models/api/qsdm';
import sendMessage from 'preload/sendMessage';

export default (): Promise<QsdmWalletListResponse> =>
  sendMessage(config.endpoints.LIST_QSDM_WALLETS, undefined);
