import config from 'config';
import sendMessage from 'preload/sendMessage';

import type {
  QsdmSignerLegacyRecoveryEnableRequest,
  QsdmSignerLegacyRecoveryEnableResponse,
} from 'models/api/qsdm';

export default (
  payload: QsdmSignerLegacyRecoveryEnableRequest
): Promise<QsdmSignerLegacyRecoveryEnableResponse> =>
  sendMessage(config.endpoints.ENABLE_QSDM_SIGNER_LEGACY_RECOVERY, payload);
