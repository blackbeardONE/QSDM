import {
  getQsdmWalletProviderPermissions,
  revokeQsdmWalletProviderPermission,
} from 'main/services/qsdmWalletProviderBroker';

import type {
  QsdmWalletProviderPermissionsResponse,
  QsdmWalletProviderRevokeRequest,
  QsdmWalletProviderRevokeResponse,
} from 'models/api/qsdm';

export const getQsdmWalletProviderPermissionsController =
  (): QsdmWalletProviderPermissionsResponse =>
    getQsdmWalletProviderPermissions();

export const revokeQsdmWalletProviderPermissionController = (
  _: unknown,
  payload: QsdmWalletProviderRevokeRequest
): QsdmWalletProviderRevokeResponse =>
  revokeQsdmWalletProviderPermission(payload.origin);
