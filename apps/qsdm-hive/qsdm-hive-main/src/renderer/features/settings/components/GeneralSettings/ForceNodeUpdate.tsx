import React from 'react';
import { useMutation } from 'react-query';

import { useAppVersion } from 'renderer/features/common/hooks/useAppVersion';
import { downloadAppUpdate, openBrowserWindow } from 'renderer/services';

import { useUpdateCheck } from '../../hooks';
import { usePlatformCheck } from '../../hooks/usePlatform';

export function ForceNodeUpdate() {
  const { checkForUpdates, isCheckingForTheUpdate } = useUpdateCheck();

  const { platformInfo, refetchPlatform } = usePlatformCheck();

  const mutation = useMutation(downloadAppUpdate);

  const [hasChecked, setHasChecked] = React.useState(false);
  const [checkedUpdateInfo, setCheckedUpdateInfo] =
    React.useState<{ version?: string } | null>(null);
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
    setCheckedUpdateInfo(null);
    setIsDownloaded(false);
    const updateResult = await checkForUpdates();
    const platformResult = await refetchPlatform();
    const nextUpdateInfo = updateResult.data?.updateInfo ?? null;
    setHasChecked(true);

    if (platformResult.data?.image === 'unsupported') {
      openBrowserWindow('https://qsdm.tech');
      return;
    }

    setCheckedUpdateInfo(nextUpdateInfo);

    if (nextUpdateInfo?.version && nextUpdateInfo.version !== appVersion) {
      mutation.mutate();
    }
  };

  const { appVersion } = useAppVersion();

  const thereIsAnUpdateAvailable =
    hasChecked &&
    checkedUpdateInfo?.version &&
    checkedUpdateInfo.version !== appVersion;

  const isAutoUpdateSupported =
    platformInfo && platformInfo.image === 'supported';
  const isBusy = isCheckingForTheUpdate || mutation.isLoading;

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
      {isCheckingForTheUpdate && (
        <div className="text-sm text-white">Checking for update...</div>
      )}
      {hasChecked && !isAutoUpdateSupported && (
        <div className="text-sm text-white">
          Check the QSDM Hive website for an update!
        </div>
      )}
      {hasChecked && !thereIsAnUpdateAvailable && isAutoUpdateSupported && (
        <div className="text-sm text-white">No newer update available.</div>
      )}
      {thereIsAnUpdateAvailable && isAutoUpdateSupported && (
        <div className="text-sm text-white">
          Downloading update {checkedUpdateInfo?.version}.
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
