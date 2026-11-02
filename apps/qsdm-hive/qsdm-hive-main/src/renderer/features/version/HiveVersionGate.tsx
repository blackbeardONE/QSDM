import React from 'react';
import { useQuery } from 'react-query';

import QsdmLogo from 'assets/svgs/qsdm-hive-logo.svg';
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
      const result = await checkAppUpdate();
      if (!result?.isUpdateAvailable) {
        setUpdateStage('manual');
        setUpdateError(
          'The automatic updater did not offer the required release. Use Download Installer below.'
        );
        return;
      }
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
      <section className="relative z-10 w-full max-w-[620px] rounded-xl border border-qsdm-border bg-qsdm-panel p-8 shadow-qsdm-card">
        <div className="mb-4 flex items-center gap-3">
          <QsdmLogo className="h-9 w-9 shrink-0" aria-hidden="true" />
          <p className="text-xs font-semibold uppercase tracking-[0.16em] text-qsdm-gold">
            QSDM Hive Update Required
          </p>
        </div>
        <h1 className="mb-4 text-[32px] font-semibold leading-tight">
          Install the current Hive before continuing.
        </h1>
        <p className="mb-6 text-base leading-7 text-qsdm-text-2">
          {reason} QSDM Hive only unlocks when the installed version exactly
          matches the current approved version. Older and newer builds are both
          blocked to protect wallet, task, and CELL action compatibility.
        </p>

        <div className="mb-6 grid gap-3 sm:grid-cols-2">
          <div className="rounded-lg border border-qsdm-border bg-black/20 p-4">
            <div className="text-[11px] font-semibold uppercase tracking-[0.08em] text-qsdm-muted">
              Installed
            </div>
            <div className="mt-1 font-mono text-xl font-medium">
              {currentVersion}
            </div>
          </div>
          <div className="rounded-lg border border-qsdm-border bg-black/20 p-4">
            <div className="text-[11px] font-semibold uppercase tracking-[0.08em] text-qsdm-muted">
              Required
            </div>
            <div
              className={`mt-1 text-xl font-medium ${
                formatHiveVersion(policy?.requiredVersion) ? 'font-mono' : ''
              }`}
            >
              {requiredVersion}
            </div>
          </div>
        </div>

        {policy?.error && (
          <p className="mb-6 rounded-lg border border-qsdm-danger/40 bg-qsdm-danger/10 p-3 text-sm text-[#ffb4b4]">
            {policy.error}
          </p>
        )}

        {canInstallAutomatically && (
          <p
            className={`mb-6 rounded-lg border p-3 text-sm ${
              updateStage === 'error'
                ? 'border-qsdm-danger/40 bg-qsdm-danger/10 text-[#ffb4b4]'
                : 'border-qsdm-teal/30 bg-qsdm-teal/5 text-qsdm-text'
            }`}
          >
            {updateStatus}
            {updateError ? ` ${updateError}` : ''}
          </p>
        )}

        <div className="flex flex-wrap gap-3">
          {canInstallAutomatically && updateStage === 'error' && (
            <button
              className="h-11 rounded-lg border border-qsdm-teal/40 bg-qsdm-teal px-6 font-semibold text-qsdm-bg transition hover:brightness-105"
              onClick={handleRetry}
            >
              Retry Automatic Update
            </button>
          )}
          <button
            className="h-11 rounded-lg border border-qsdm-gold/60 bg-qsdm-gold px-6 font-semibold text-qsdm-bg transition hover:brightness-105 disabled:opacity-60"
            disabled={isDownloading}
            onClick={handleDownload}
          >
            {isDownloading ? 'Opening download...' : 'Download Installer'}
          </button>
          <button
            className="h-11 rounded-lg border border-qsdm-teal/30 bg-qsdm-panel-2 px-6 font-semibold text-qsdm-text transition hover:border-qsdm-teal/60 disabled:opacity-60"
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
    case 'manual':
      return 'Automatic installation is unavailable for this release.';
    default:
      return 'Starting the required automatic update...';
  }
}
