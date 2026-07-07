import React from 'react';
import { useQuery } from 'react-query';

import { LoadingScreen } from 'renderer/components';
import {
  QueryKeys,
  getHiveVersionPolicy,
  openBrowserWindow,
  quitApp,
} from 'renderer/services';

type Props = {
  children: React.ReactNode;
};

const FALLBACK_DOWNLOAD_URL = 'https://qsdm.tech/download.html';
const VERSION_POLICY_POLL_MS = 60 * 1000;

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

  const handleDownload = async () => {
    setIsDownloading(true);
    const downloadUrl = policy?.downloadUrl || FALLBACK_DOWNLOAD_URL;
    await openBrowserWindow(downloadUrl);
    setTimeout(() => {
      quitApp().catch((error) => {
        console.error('Failed to quit stale QSDM Hive after update link', error);
      });
    }, 800);
  };

  if (isLoading) {
    return <LoadingScreen />;
  }

  if (policy?.compatible) {
    return <>{children}</>;
  }

  const requiredVersion = policy?.requiredVersion || 'latest approved release';
  const currentVersion = policy?.currentVersion || 'unknown';
  const reason =
    policy?.reason === 'manifest-unavailable'
      ? 'Hive could not verify the approved release manifest.'
      : 'This Hive build does not match the approved release.';

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

        <div className="flex flex-wrap gap-3">
          <button
            className="h-11 rounded bg-[#9fe3e6] px-6 font-semibold text-[#062832] disabled:opacity-60"
            disabled={isDownloading}
            onClick={handleDownload}
          >
            {isDownloading ? 'Opening download...' : 'Download Latest Hive'}
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
