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
  const backup = safety.backupRead;
  return (
    <aside
      className={`fixed inset-x-4 bottom-4 z-[10000] mx-auto flex max-w-[980px] items-center justify-between gap-4 rounded border px-4 py-3 shadow-2xl ${
        unsafe && !backup
          ? 'border-[#ff8a8a]/60 bg-[#4a1820] text-white'
          : 'border-[#f7bf42]/60 bg-[#4a3a12] text-white'
      }`}
      role={unsafe ? 'alert' : 'status'}
    >
      <div className="min-w-0">
        <div className="font-semibold">
          {backup
            ? 'Backup history available — CELL actions blocked'
            : unsafe
            ? 'CELL actions blocked: canonical chain not verified'
            : 'Using the verified QSDM gateway'}
        </div>
        <div className="mt-1 text-sm text-white/80">
          {safety.detail ||
            'The configured local Core is unavailable or unsafe. Hive switched to the canonical gateway.'}
        </div>
        {backup && (
          <div className="mt-2 text-sm text-white/90">
            <p>
              Backup responds at {backup.sourceApiUrl}. Confirmed history ends
              at block {backup.checkpointHeight.toLocaleString()}; last checked
              against the primary on{' '}
              {new Date(backup.confirmedAt).toLocaleString()}. This is
              historical data. Transactions, balances and mining remain
              unavailable.
            </p>
            <details className="mt-2">
              <summary className="cursor-pointer">
                Recent confirmed blocks ({backup.blocks.length})
              </summary>
              <div className="mt-2 max-h-48 overflow-auto">
                <table className="w-full text-left text-xs">
                  <thead>
                    <tr>
                      <th>Height</th>
                      <th>Block hash</th>
                      <th>Block time</th>
                    </tr>
                  </thead>
                  <tbody>
                    {[...backup.blocks].reverse().map((block) => (
                      <tr key={block.height}>
                        <td className="pr-3">
                          {block.height.toLocaleString()}
                        </td>
                        <td className="break-all pr-3 font-mono">
                          {block.hash}
                        </td>
                        <td>
                          {block.timestamp
                            ? new Date(block.timestamp).toLocaleString()
                            : 'Unavailable'}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </details>
          </div>
        )}
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
