import config from 'config';
import sendMessage from 'preload/sendMessage';

import type {
  QsdmSignerWalletCreateRequest,
  QsdmSignerWalletImportResponse,
} from 'models/api/qsdm';

export default (
  payload: QsdmSignerWalletCreateRequest
): Promise<QsdmSignerWalletImportResponse> =>
  sendMessage(config.endpoints.CREATE_QSDM_SIGNER_WALLET, payload);
