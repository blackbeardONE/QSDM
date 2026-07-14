import React from 'react';
import { useMutation } from 'react-query';

import {
  downloadAppUpdate,
  getHiveVersionPolicy,
  openBrowserWindow,
} from 'renderer/services';
import { formatHiveVersion } from 'utils';

import { usePlatformCheck } from '../../hooks/usePlatform';

export function ForceNodeUpdate() {
  const { refetchPlatform } = usePlatformCheck();

  const mutation = useMutation(downloadAppUpdate);

  const [hasChecked, setHasChecked] = React.useState(false);
  const [policy, setPolicy] = React.useState<Awaited<
    ReturnType<typeof getHiveVersionPolicy>
  > | null>(null);
  const [checkedPlatformImage, setCheckedPlatformImage] = React.useState<
    'supported' | 'unsupported' | null
  >(null);
  const [isChecking, setIsChecking] = React.useState(false);
  const [isDownloaded, setIsDownloaded] = React.useState(false);

  React.useEffect(() => {
    const destroy = window.main.onAppDownloaded(() => {
      setIsDownloaded(true);
    });

    return () => {
      destroy();
    };
  }, []);

  const handleForceUpdate = async () => {
    setHasChecked(false);
    setPolicy(null);
    setCheckedPlatformImage(null);
    setIsDownloaded(false);
    setIsChecking(true);
    try {
      const [policyResult, platformResult] = await Promise.all([
        getHiveVersionPolicy({ forceRefresh: true }),
        refetchPlatform(),
      ]);
      setPolicy(policyResult);
      const platformImage = platformResult.data?.image;
      setCheckedPlatformImage(
        platformImage === 'supported' || platformImage === 'unsupported'
          ? platformImage
          : null
      );
      setHasChecked(true);

      if (policyResult.updateRequired) {
        if (platformResult.data?.image === 'supported') {
          mutation.mutate(undefined, {
            onError: () => {
              openBrowserWindow(policyResult.downloadUrl);
            },
          });
        } else {
          openBrowserWindow(policyResult.downloadUrl);
        }
      }
    } finally {
      setIsChecking(false);
    }
  };

  const updateRequired = hasChecked && !!policy?.updateRequired;
  const isCurrent = hasChecked && !!policy?.compatible;

  const isAutoUpdateSupported = checkedPlatformImage === 'supported';
  const isAutoUpdateUnsupported = checkedPlatformImage === 'unsupported';
  const isBusy = isChecking || mutation.isLoading;

  return (
    <div className="text-right w-[280px]">
      <button
        onClick={handleForceUpdate}
        disabled={isBusy}
        className="mb-2 text-sm underline text-finnieEmerald-light underline-offset-2 disabled:opacity-60"
      >
        Update QSDM Hive to Latest Version
      </button>
      {mutation.isError && (
        <div className="text-sm text-finnieRed">
          An error occurred: {JSON.stringify(mutation.error)}
        </div>
      )}
      {isChecking && (
        <div className="text-sm text-white">
          Checking approved Hive release...
        </div>
      )}
      {hasChecked && isAutoUpdateUnsupported && (
        <div className="text-sm text-white">
          Opened the approved Hive download page.
        </div>
      )}
      {isCurrent && isAutoUpdateSupported && (
        <div className="text-sm text-white">
          QSDM Hive is current: {formatHiveVersion(policy?.currentVersion)}.
        </div>
      )}
      {updateRequired && isAutoUpdateSupported && (
        <div className="text-sm text-white">
          Downloading Hive{' '}
          {formatHiveVersion(policy?.requiredVersion) ?? 'release'}.
        </div>
      )}
      {mutation.isLoading && (
        <div className="text-sm text-white">Updating...</div>
      )}
      {isDownloaded && (
        <div className="text-sm text-white">Update downloaded.</div>
      )}
    </div>
  );
}
