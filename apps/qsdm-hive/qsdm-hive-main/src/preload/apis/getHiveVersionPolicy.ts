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
  // True when the required version comes from the signed release envelope
  // Hive saved earlier, because the release feed could not be used just now.
  fromCache?: boolean;
  // False on macOS: no signed Mac release exists for Hive to install itself,
  // so the update screen sends the user to the download page.
  selfUpdates?: boolean;
};

export default (
  options: { forceRefresh?: boolean } = {}
): Promise<HiveVersionPolicyStatus> =>
  sendMessage(config.endpoints.GET_HIVE_VERSION_POLICY, options);
