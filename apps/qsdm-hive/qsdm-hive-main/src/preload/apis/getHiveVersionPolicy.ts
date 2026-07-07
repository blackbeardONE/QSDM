import config from 'config';
import sendMessage from 'preload/sendMessage';

export type HiveVersionPolicyStatus = {
  compatible: boolean;
  updateRequired: boolean;
  currentVersion: string;
  requiredVersion: string | null;
  manifestUrl: string;
  downloadUrl: string;
  checkedAt: string;
  reason:
    | 'current'
    | 'version-mismatch'
    | 'manifest-unavailable'
    | 'policy-disabled';
  error?: string;
};

export default (
  options: { forceRefresh?: boolean } = {}
): Promise<HiveVersionPolicyStatus> =>
  sendMessage(config.endpoints.GET_HIVE_VERSION_POLICY, options);
