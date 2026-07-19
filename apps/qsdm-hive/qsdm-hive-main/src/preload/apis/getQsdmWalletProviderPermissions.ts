import config from 'config';
import { QsdmWalletProviderPermissionsResponse } from 'models/api/qsdm';
import sendMessage from 'preload/sendMessage';

export default (): Promise<QsdmWalletProviderPermissionsResponse> =>
  sendMessage(config.endpoints.GET_QSDM_WALLET_PROVIDER_PERMISSIONS, undefined);
