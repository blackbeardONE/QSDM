import config from 'config';
import {
  QsdmWalletProviderRevokeRequest,
  QsdmWalletProviderRevokeResponse,
} from 'models/api/qsdm';
import sendMessage from 'preload/sendMessage';

export default (
  payload: QsdmWalletProviderRevokeRequest
): Promise<QsdmWalletProviderRevokeResponse> =>
  sendMessage(config.endpoints.REVOKE_QSDM_WALLET_PROVIDER_PERMISSION, payload);
