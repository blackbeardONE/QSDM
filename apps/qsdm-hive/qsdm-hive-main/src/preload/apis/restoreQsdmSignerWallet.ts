import config from 'config';
import sendMessage from 'preload/sendMessage';

import type {
  QsdmSignerWalletImportResponse,
  QsdmSignerWalletRecoveryRequest,
} from 'models/api/qsdm';

export default (
  payload: QsdmSignerWalletRecoveryRequest
): Promise<QsdmSignerWalletImportResponse> =>
  sendMessage(config.endpoints.RESTORE_QSDM_SIGNER_WALLET, payload);
