import React, { useState } from 'react';
import { toast } from 'react-hot-toast';
import { useMutation, useQuery, useQueryClient } from 'react-query';

import {
  getQsdmPulsePool,
  openBrowserWindow,
  QueryKeys,
  runQsdmPulsePoolAction,
} from 'renderer/services';
import { getErrorToDisplay } from 'renderer/utils';

import {
  describePoolMinerState,
  formatCellAmount,
  formatWindowShare,
  poolStatsUrl,
} from './poolMinerState';

const BUTTON_QUIET =
  'rounded-lg border border-qsdm-border px-3 py-1 font-semibold text-qsdm-text transition hover:border-qsdm-gold/60 disabled:opacity-50';
const BUTTON =
  'rounded-lg border border-qsdm-gold/60 bg-qsdm-gold px-3 py-1 font-semibold text-qsdm-bg transition hover:brightness-105 disabled:opacity-50';

const TONE_DOT: Record<'good' | 'warn' | 'idle', string> = {
  good: 'bg-qsdm-success',
  warn: 'bg-qsdm-gold',
  idle: 'bg-qsdm-muted',
};

function Stat({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div className="flex min-w-0 flex-col gap-1">
      <span className="text-[10.5px] font-semibold uppercase tracking-[0.08em] text-qsdm-muted">
        {label}
      </span>
      <span className="break-words text-[13px] text-qsdm-text">{value}</span>
    </div>
  );
}

// QSDM Pool Miner: a simple status card. Everything is set up by the task's
// Start button; this card only shows how it is going and offers Leave and
// Reclaim.
export function PoolMinerCard({ isRunning }: { isRunning: boolean }) {
  const queryClient = useQueryClient();
  const { data: status, isLoading } = useQuery(
    [QueryKeys.QsdmPulsePool],
    getQsdmPulsePool,
    { refetchInterval: 15000 }
  );
  const [confirmLeave, setConfirmLeave] = useState(false);

  const { mutate: act, isLoading: acting } = useMutation(
    runQsdmPulsePoolAction,
    {
      onSuccess: (_result, op) => {
        setConfirmLeave(false);
        toast.success(
          op === 'leave'
            ? 'You left the pool. Your next rewards come to you directly.'
            : 'Reclaim sent. Your escrowed rewards come back to your wallet.'
        );
        queryClient.invalidateQueries([QueryKeys.QsdmPulsePool]);
      },
      onError: (error: Error) => {
        toast.error(
          getErrorToDisplay(error) || 'That did not work. Try again.'
        );
      },
    }
  );

  if (isLoading) {
    return (
      <div className="rounded-lg border border-qsdm-border bg-qsdm-panel px-4 py-3 text-xs text-qsdm-muted">
        Checking the QSDM pool...
      </div>
    );
  }

  const state = describePoolMinerState(isRunning, status);
  const reclaimable = status?.reclaimableProofs?.length || 0;

  return (
    <div
      id="pool-miner-card"
      className="flex flex-col gap-3 rounded-lg border border-qsdm-border bg-qsdm-panel px-4 py-3 text-xs"
    >
      <div className="flex flex-wrap items-center justify-between gap-3">
        <span className="inline-flex items-center gap-2 text-[13px] font-semibold text-qsdm-text">
          <span
            className={`inline-block h-2 w-2 rounded-full ${
              TONE_DOT[state.tone]
            }`}
          />
          {state.label}
        </span>
        <button
          type="button"
          className={BUTTON_QUIET}
          onClick={() => openBrowserWindow(poolStatsUrl(status?.address))}
        >
          Pool stats
        </button>
      </div>

      {state.note && <p className="text-qsdm-muted">{state.note}</p>}

      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
        <Stat
          label="Shares today"
          value={(status?.receiptsToday ?? 0).toLocaleString('en-US')}
        />
        <Stat
          label="Your share of the window"
          value={formatWindowShare(status?.poolStats?.window_share)}
        />
        <Stat
          label="In escrow"
          value={formatCellAmount(status?.escrowedCell)}
        />
        <Stat
          label="Owed (paid at 0.1 CELL)"
          value={formatCellAmount(status?.owedCell)}
        />
      </div>

      {(status?.member || reclaimable > 0) && (
        <div className="flex flex-wrap items-center gap-2">
          {reclaimable > 0 && (
            <button
              type="button"
              className={BUTTON}
              disabled={acting}
              onClick={() => act('reclaim')}
            >
              Reclaim
            </button>
          )}
          {status?.member && !confirmLeave && (
            <button
              type="button"
              className={BUTTON_QUIET}
              disabled={acting}
              onClick={() => setConfirmLeave(true)}
            >
              Leave pool
            </button>
          )}
          {status?.member && confirmLeave && (
            <>
              <span className="text-qsdm-text">
                Leave the pool? Your next rewards come to you directly.
              </span>
              <button
                type="button"
                className={BUTTON}
                disabled={acting}
                onClick={() => act('leave')}
              >
                Leave
              </button>
              <button
                type="button"
                className={BUTTON_QUIET}
                disabled={acting}
                onClick={() => setConfirmLeave(false)}
              >
                Cancel
              </button>
            </>
          )}
        </div>
      )}
    </div>
  );
}
