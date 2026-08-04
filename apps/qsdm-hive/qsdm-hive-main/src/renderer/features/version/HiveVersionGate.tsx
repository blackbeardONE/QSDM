import React from 'react';
import { useQuery } from 'react-query';

import { LoadingScreen } from 'renderer/components';
import {
  QueryKeys,
  checkAppUpdate,
  getHiveVersionPolicy,
  openBrowserWindow,
  quitApp,
} from 'renderer/services';
import { formatHiveVersion } from 'utils';

type Props = {
  children: React.ReactNode;
};

const FALLBACK_DOWNLOAD_URL = 'https://qsdm.tech/download.html';
const VERSION_POLICY_POLL_MS = 60 * 1000;
type UpdateStage =
  | 'idle'
  | 'checking'
  | 'downloading'
  | 'ready'
  | 'manual'
  | 'error';

export function HiveVersionGate({ children }: Props): JSX.Element {
  const {
    data: policy,
    isLoading,
    isFetching,
    refetch,
  } = useQuery(
    QueryKeys.HiveVersionPolicy,
    () => getHiveVersionPolicy({ forceRefresh: true }),
    {
      retry: 1,
      staleTime: 0,
      cacheTime: 0,
      refetchInterval: VERSION_POLICY_POLL_MS,
      refetchIntervalInBackground: true,
      refetchOnWindowFocus: false,
    }
  );

  const [isDownloading, setIsDownloading] = React.useState(false);
  const [updateStage, setUpdateStage] = React.useState<UpdateStage>('idle');
  const [updateError, setUpdateError] = React.useState('');
  const attemptedPolicy = React.useRef('');

  React.useEffect(() => {
    const removeAvailableListener = window.main.onAppUpdate(() => {
      setUpdateStage('downloading');
      setUpdateError('');
    });
    const removeDownloadedListener = window.main.onAppDownloaded(() => {
      setUpdateStage('ready');
      setUpdateError('');
    });

    return () => {
      removeAvailableListener();
      removeDownloadedListener();
    };
  }, []);

  const startAutomaticUpdate = React.useCallback(async () => {
    setUpdateStage('checking');
    setUpdateError('');
    try {
      await checkAppUpdate();
      setUpdateStage((current) =>
        current === 'ready' ? 'ready' : 'downloading'
      );
    } catch (error) {
      setUpdateStage('error');
      setUpdateError(error instanceof Error ? error.message : String(error));
    }
  }, []);

  React.useEffect(() => {
    if (!policy) {
      return;
    }
    if (policy.compatible) {
      attemptedPolicy.current = '';
      setUpdateStage('idle');
      setUpdateError('');
      return;
    }

    const policyKey = `${policy.currentVersion}:${policy.requiredVersion}`;
    const versionRelation = compareHiveVersions(
      policy.currentVersion,
      policy.requiredVersion
    );
    if (policy.reason !== 'version-mismatch' || versionRelation !== -1) {
      setUpdateStage('manual');
      return;
    }
    if (attemptedPolicy.current === policyKey) {
      return;
    }

    attemptedPolicy.current = policyKey;
    startAutomaticUpdate();
  }, [policy, startAutomaticUpdate]);

  const handleDownload = async () => {
    setIsDownloading(true);
    const downloadUrl = policy?.downloadUrl || FALLBACK_DOWNLOAD_URL;
    await openBrowserWindow(downloadUrl);
    setTimeout(() => {
      quitApp().catch((error) => {
        console.error(
          'Failed to quit stale QSDM Hive after update link',
          error
        );
      });
    }, 800);
  };

  const handleRetry = () => {
    attemptedPolicy.current = '';
    startAutomaticUpdate();
  };

  if (isLoading) {
    return <LoadingScreen />;
  }

  if (policy?.compatible) {
    return children as JSX.Element;
  }

  const requiredVersion =
    formatHiveVersion(policy?.requiredVersion) || 'latest approved release';
  const currentVersion = formatHiveVersion(policy?.currentVersion) || 'unknown';
  const reason =
    policy?.reason === 'manifest-unavailable'
      ? 'Hive could not verify the approved release manifest.'
      : 'This Hive build does not match the approved release.';
  const canInstallAutomatically =
    policy?.reason === 'version-mismatch' &&
    compareHiveVersions(policy.currentVersion, policy.requiredVersion) === -1;
  const updateStatus = getUpdateStatus(updateStage, requiredVersion);

  return (
    <main className="qsdm-cell-screen flex min-h-screen flex-col items-center justify-center px-6 text-white">
      <section className="relative z-10 w-full max-w-[620px] rounded-lg border border-white/15 bg-[#0c3a46]/95 p-8 shadow-2xl">
        <p className="mb-3 text-xs font-bold uppercase tracking-[0.22em] text-[#f7bf42]">
          QSDM Hive Update Required
        </p>
        <h1 className="mb-4 text-[32px] font-semibold leading-tight">
          Install the current Hive before continuing.
        </h1>
        <p className="mb-6 text-base leading-7 text-white/85">
          {reason} QSDM Hive only unlocks when the installed version exactly
          matches the current approved version. Older and newer builds are both
          blocked to protect wallet, task, and CELL action compatibility.
        </p>

        <div className="mb-6 grid gap-3 sm:grid-cols-2">
          <div className="rounded border border-white/10 bg-[#092a34] p-4">
            <div className="text-xs uppercase text-white/60">Installed</div>
            <div className="mt-1 text-xl font-semibold">{currentVersion}</div>
          </div>
          <div className="rounded border border-white/10 bg-[#092a34] p-4">
            <div className="text-xs uppercase text-white/60">Required</div>
            <div className="mt-1 text-xl font-semibold">{requiredVersion}</div>
          </div>
        </div>

        {policy?.error && (
          <p className="mb-6 rounded border border-[#ff8a8a]/30 bg-[#401820] p-3 text-sm text-[#ffb4b4]">
            {policy.error}
          </p>
        )}

        {canInstallAutomatically && (
          <p
            className={`mb-6 rounded border p-3 text-sm ${
              updateStage === 'error'
                ? 'border-[#ff8a8a]/30 bg-[#401820] text-[#ffb4b4]'
                : 'border-[#9fe3e6]/30 bg-[#092a34] text-white/90'
            }`}
          >
            {updateStatus}
            {updateError ? ` ${updateError}` : ''}
          </p>
        )}

        <div className="flex flex-wrap gap-3">
          {canInstallAutomatically && updateStage === 'error' && (
            <button
              className="h-11 rounded bg-[#9fe3e6] px-6 font-semibold text-[#062832]"
              onClick={handleRetry}
            >
              Retry Automatic Update
            </button>
          )}
          <button
            className="h-11 rounded border border-white/25 px-6 font-semibold text-white disabled:opacity-60"
            disabled={isDownloading}
            onClick={handleDownload}
          >
            {isDownloading ? 'Opening download...' : 'Download Installer'}
          </button>
          <button
            className="h-11 rounded border border-white/25 px-6 font-semibold text-white disabled:opacity-60"
            disabled={isFetching || isDownloading}
            onClick={() => refetch()}
          >
            {isFetching ? 'Checking...' : 'Check Again'}
          </button>
        </div>
      </section>
    </main>
  );
}

function compareHiveVersions(
  currentVersion?: string | null,
  requiredVersion?: string | null
) {
  const current = parseHiveVersion(currentVersion);
  const required = parseHiveVersion(requiredVersion);
  if (!current || !required) {
    return null;
  }

  for (let index = 0; index < current.length; index += 1) {
    if (current[index] < required[index]) return -1;
    if (current[index] > required[index]) return 1;
  }
  return 0;
}

function parseHiveVersion(version?: string | null) {
  if (!version || !/^\d+\.\d+\.\d+$/.test(version)) {
    return null;
  }
  return version.split('.').map(Number);
}

function getUpdateStatus(stage: UpdateStage, requiredVersion: string) {
  switch (stage) {
    case 'checking':
      return 'Checking the signed QSDM Hive release...';
    case 'downloading':
      return `Downloading and verifying QSDM Hive ${requiredVersion}. The restart prompt will appear when it is ready.`;
    case 'ready':
      return 'The update is verified and ready. Approve the Update and Restart prompt.';
    case 'error':
      return 'Automatic update failed and will retry in one minute.';
    default:
      return 'Starting the required automatic update...';
  }
}
