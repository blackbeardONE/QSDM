import config from 'config';
import { QsdmSignerWalletBackupResponse } from 'models/api/qsdm';
import sendMessage from 'preload/sendMessage';

export default (): Promise<QsdmSignerWalletBackupResponse> =>
  sendMessage(config.endpoints.EXPORT_QSDM_SIGNER_WALLET_BACKUP, undefined);
