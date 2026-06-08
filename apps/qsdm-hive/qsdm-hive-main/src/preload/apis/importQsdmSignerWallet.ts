import config from 'config';
import {
  QsdmSignerWalletImportRequest,
  QsdmSignerWalletImportResponse,
} from 'models/api/qsdm';
import sendMessage from 'preload/sendMessage';

export default (
  payload: QsdmSignerWalletImportRequest
): Promise<QsdmSignerWalletImportResponse> =>
  sendMessage(config.endpoints.IMPORT_QSDM_SIGNER_WALLET, payload);
