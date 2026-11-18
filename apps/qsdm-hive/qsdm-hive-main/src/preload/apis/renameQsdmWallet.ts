import config from 'config';
import { QsdmWalletRenameRequest, QsdmStoredWallet } from 'models/api/qsdm';
import sendMessage from 'preload/sendMessage';

export default (payload: QsdmWalletRenameRequest): Promise<QsdmStoredWallet> =>
  sendMessage(config.endpoints.RENAME_QSDM_WALLET, payload);
