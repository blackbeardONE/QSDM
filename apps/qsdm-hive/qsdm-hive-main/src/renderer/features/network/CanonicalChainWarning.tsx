import React from 'react';
import { useQuery } from 'react-query';

import { getQsdmCoreStatus, QueryKeys } from 'renderer/services';

const POLL_INTERVAL_MS = 30_000;

export function CanonicalChainWarning(): JSX.Element | null {
  const { data, isFetching, refetch } = useQuery(
    QueryKeys.QsdmCoreStatus,
    getQsdmCoreStatus,
    {
      refetchInterval: POLL_INTERVAL_MS,
      retry: false,
    }
  );
  const safety = data?.canonicalSafety;

  if (!safety || (safety.safe && !safety.usingGatewayFallback)) {
    return null;
  }

  const unsafe = !safety.safe;
  return (
    <aside
      className={`fixed inset-x-4 bottom-4 z-[10000] mx-auto flex max-w-[980px] items-center justify-between gap-4 rounded border px-4 py-3 shadow-2xl ${
        unsafe
          ? 'border-[#ff8a8a]/60 bg-[#4a1820] text-white'
          : 'border-[#f7bf42]/60 bg-[#4a3a12] text-white'
      }`}
      role={unsafe ? 'alert' : 'status'}
    >
      <div className="min-w-0">
        <div className="font-semibold">
          {unsafe
            ? 'CELL actions blocked: canonical chain not verified'
            : 'Using the verified QSDM gateway'}
        </div>
        <div className="mt-1 text-sm text-white/80">
          {safety.detail ||
            'The configured local Core is unavailable or unsafe. Hive switched to the canonical gateway.'}
        </div>
      </div>
      <button
        type="button"
        className="h-9 shrink-0 rounded border border-white/30 px-4 text-sm font-semibold hover:border-white disabled:opacity-50"
        disabled={isFetching}
        onClick={() => refetch()}
      >
        {isFetching ? 'Checking...' : 'Recheck'}
      </button>
    </aside>
  );
}
