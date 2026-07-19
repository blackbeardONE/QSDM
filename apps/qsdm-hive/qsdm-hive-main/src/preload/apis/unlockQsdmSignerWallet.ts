import config from 'config';
import {
  QsdmSignerWalletImportResponse,
  QsdmSignerWalletUnlockRequest,
} from 'models/api/qsdm';
import sendMessage from 'preload/sendMessage';

export default (
  payload: QsdmSignerWalletUnlockRequest
): Promise<QsdmSignerWalletImportResponse> =>
  sendMessage(config.endpoints.UNLOCK_QSDM_SIGNER_WALLET, payload);
