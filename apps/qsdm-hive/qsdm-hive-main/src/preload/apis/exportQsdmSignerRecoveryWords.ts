import config from 'config';
import sendMessage from 'preload/sendMessage';

import type {
  QsdmSignerWalletRecoveryExportRequest,
  QsdmSignerWalletRecoveryExportResponse,
} from 'models/api/qsdm';

export default (
  payload: QsdmSignerWalletRecoveryExportRequest
): Promise<QsdmSignerWalletRecoveryExportResponse> =>
  sendMessage(config.endpoints.EXPORT_QSDM_SIGNER_RECOVERY_WORDS, payload);
