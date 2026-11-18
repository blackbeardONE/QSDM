import React, { useEffect, useState } from 'react';
import { toast } from 'react-hot-toast';
import { useMutation, useQuery, useQueryClient } from 'react-query';

import { QsdmPulsePoolSettings } from 'models/api/qsdm';
import {
  getQsdmPulsePool,
  QueryKeys,
  runQsdmPulsePoolAction,
  setQsdmPulsePool,
} from 'renderer/services';
import { getErrorToDisplay } from 'renderer/utils';

const formatCell = (value?: number) =>
  typeof value === 'number' && Number.isFinite(value)
    ? `${value.toLocaleString(undefined, { maximumFractionDigits: 8 })} CELL`
    : '-';

const formatPercent = (bps?: number) =>
  typeof bps === 'number' ? `${(bps / 100).toLocaleString()}%` : '-';

const shortId = (value?: string) =>
  value && value.length > 16
    ? `${value.slice(0, 8)}...${value.slice(-8)}`
    : value || '-';

function Stat({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <div className="flex min-w-0 flex-col gap-1">
      <span className="text-[10.5px] font-semibold uppercase tracking-[0.08em] text-qsdm-muted">
        {label}
      </span>
      <div className="break-words text-[13px] text-qsdm-text">{children}</div>
    </div>
  );
}

const BUTTON =
  'rounded-lg border border-qsdm-gold/60 bg-qsdm-gold px-3 py-1 font-semibold text-qsdm-bg transition hover:brightness-105 disabled:opacity-50';
const BUTTON_QUIET =
  'rounded-lg border border-qsdm-border px-3 py-1 font-semibold text-qsdm-text transition hover:border-qsdm-gold/60 disabled:opacity-50';
const INPUT =
  'w-full rounded-md border border-qsdm-border bg-transparent px-2 py-1 font-mono text-[12px] text-qsdm-text';

// PULSE, the non-custodial QSDM mining pool, inside the Miner task. Off by
// default: solo mining stays the default.
export function PulsePoolPanel() {
  const queryClient = useQueryClient();
  const { data: status, isLoading } = useQuery(
    [QueryKeys.QsdmPulsePool],
    getQsdmPulsePool,
    { refetchInterval: 15000 }
  );
  const [draft, setDraft] = useState<QsdmPulsePoolSettings | undefined>();
  const [confirming, setConfirming] = useState<'join' | 'leave' | undefined>();

  useEffect(() => {
    if (status?.settings && !draft) setDraft(status.settings);
  }, [status, draft]);

  const refresh = () =>
    queryClient.invalidateQueries([QueryKeys.QsdmPulsePool]);

  const { mutate: save, isLoading: saving } = useMutation(setQsdmPulsePool, {
    onSuccess: (next) => {
      setDraft(next.settings);
      toast.success(
        'Pool settings saved. They apply the next time the Miner starts.'
      );
      refresh();
    },
    onError: (error: Error) => {
      toast.error(getErrorToDisplay(error) || 'The request failed');
    },
  });

  const { mutate: act, isLoading: acting } = useMutation(
    runQsdmPulsePoolAction,
    {
      onSuccess: (_result, op) => {
        setConfirming(undefined);
        toast.success(
          op === 'join'
            ? 'Joined the pool. Your next mining rewards are pooled.'
            : op === 'leave'
            ? 'Left the pool. Your next mining rewards come to you directly.'
            : 'Reclaim sent. The escrowed rewards come back to your wallet.'
        );
        refresh();
      },
      onError: (error: Error) => {
        toast.error(getErrorToDisplay(error) || 'The request failed');
      },
    }
  );

  if (isLoading || !status || !draft) {
    return (
      <div className="rounded-lg border border-qsdm-border bg-qsdm-panel px-4 py-3 text-xs text-qsdm-muted">
        Loading the QSDM pool...
      </div>
    );
  }

  const windowShare = status.poolStats?.window_share;
  const reclaimable = status.reclaimableProofs?.length || 0;
  const feeLine =
    typeof status.feeBps === 'number'
      ? `${formatPercent(
          status.feeBps
        )}, capped by the network at ${formatPercent(status.networkFeeCapBps)}`
      : '-';

  return (
    <div
      id="pulse-pool"
      className="flex flex-col gap-3 rounded-lg border border-qsdm-border bg-qsdm-panel px-4 py-3 text-xs"
    >
      <div className="flex flex-wrap items-center justify-between gap-3">
        <span className="text-[13px] font-semibold text-qsdm-text">
          QSDM pool (PULSE)
        </span>
        <label className="inline-flex cursor-pointer items-center gap-2 text-[12px] text-qsdm-text">
          <input
            type="checkbox"
            checked={draft.enabled}
            disabled={saving}
            onChange={(event) =>
              save({ ...draft, enabled: event.target.checked })
            }
          />
          Mine with the QSDM pool
        </label>
      </div>
      <p className="text-qsdm-muted">
        Pooled mining pays you a share of every pooled block reward by your
        work, instead of waiting for your own lucky proof. The pool never holds
        your CELL: the network pays your rewards into an escrow only the chain
        can move, and the pool&apos;s signed settlement pays every member
        directly. Solo mining stays the default; if the pool is unreachable, the
        Miner mines solo and returns to the pool by itself.
      </p>

      <div className="grid grid-cols-[repeat(auto-fill,minmax(180px,1fr))] gap-x-6 gap-y-3">
        <Stat label="Pool">
          {status.poolError
            ? `Not answering (${status.poolError})`
            : status.poolMode === 'active'
            ? 'Settling on the chain'
            : 'Preview: pays nothing yet'}
        </Stat>
        <Stat label="Pool fee">{feeLine}</Stat>
        <Stat label="Membership">
          {status.member
            ? `Member of ${shortId(status.memberPool)}`
            : 'Not a member'}
        </Stat>
        <Stat label="Your share of the window">
          {typeof windowShare === 'number'
            ? `${(windowShare * 100).toLocaleString(undefined, {
                maximumFractionDigits: 4,
              })}%`
            : '-'}
        </Stat>
        <Stat label="In escrow">{formatCell(status.escrowedCell)}</Stat>
        <Stat label="Owed (below the payout floor)">
          {formatCell(status.owedCell)}
        </Stat>
        <Stat label="Share receipts kept">
          {status.receiptsTotal.toLocaleString()}
        </Stat>
        <Stat label="Payout floor">{formatCell(status.payoutFloorCell)}</Stat>
      </div>

      {status.poolNote && <p className="text-qsdm-muted">{status.poolNote}</p>}
      {status.chainError && (
        <p className="text-finnieOrange">Network: {status.chainError}</p>
      )}
      {!status.ruleActive && status.activationHeight ? (
        <p className="text-qsdm-muted">
          Joining opens at block {status.activationHeight.toLocaleString()}.
        </p>
      ) : null}

      <div className="flex flex-wrap items-center gap-3">
        {!status.member && (
          <button
            type="button"
            className={BUTTON}
            disabled={acting || !status.ruleActive || !status.poolId}
            onClick={() => setConfirming('join')}
          >
            Join the pool
          </button>
        )}
        {status.member && (
          <button
            type="button"
            className={BUTTON_QUIET}
            disabled={acting}
            onClick={() => setConfirming('leave')}
          >
            Leave the pool
          </button>
        )}
        {reclaimable > 0 && (
          <button
            type="button"
            className={BUTTON}
            disabled={acting}
            onClick={() => act('reclaim')}
          >
            {`Reclaim ${reclaimable} unsettled reward${
              reclaimable === 1 ? '' : 's'
            }`}
          </button>
        )}
      </div>

      {confirming === 'join' && (
        <div className="flex flex-col gap-2 rounded-lg border border-qsdm-gold/40 px-3 py-2">
          <p className="text-qsdm-text">
            Join pool {shortId(status.poolId)} with a signed transaction from
            your wallet?
          </p>
          <ul className="list-disc pl-5 text-qsdm-muted">
            <li>
              From your next mining reward on, rewards go into an escrow that
              nobody can spend, the pool included. Only the chain moves it.
            </li>
            <li>
              The pool&apos;s settlement then pays you by your share of the
              window, minus the pool fee of {formatPercent(status.feeBps)}.
            </li>
            <li>
              You can leave the pool at any time with one more transaction.
            </li>
            <li>
              If the pool never settles a reward, you can reclaim it yourself
              after{' '}
              {status.reclaimAfterBlocks
                ? `${status.reclaimAfterBlocks.toLocaleString()} blocks (about ${Math.round(
                    (status.reclaimAfterBlocks * 10) / 3600
                  )} hours)`
                : 'the reclaim timeout'}
              .
            </li>
          </ul>
          <div className="flex gap-3">
            <button
              type="button"
              className={BUTTON}
              disabled={acting}
              onClick={() => act('join')}
            >
              {acting ? 'Joining...' : 'Confirm and join'}
            </button>
            <button
              type="button"
              className={BUTTON_QUIET}
              onClick={() => setConfirming(undefined)}
            >
              Cancel
            </button>
          </div>
        </div>
      )}
      {confirming === 'leave' && (
        <div className="flex flex-col gap-2 rounded-lg border border-qsdm-border px-3 py-2">
          <p className="text-qsdm-text">
            Leave the pool? Your next mining rewards come to you directly.
            Rewards already in escrow are still settled by the pool, or you can
            reclaim them after the timeout.
          </p>
          <div className="flex gap-3">
            <button
              type="button"
              className={BUTTON}
              disabled={acting}
              onClick={() => act('leave')}
            >
              {acting ? 'Leaving...' : 'Confirm and leave'}
            </button>
            <button
              type="button"
              className={BUTTON_QUIET}
              onClick={() => setConfirming(undefined)}
            >
              Cancel
            </button>
          </div>
        </div>
      )}

      <details>
        <summary className="cursor-pointer text-qsdm-muted">
          Pool settings
        </summary>
        <div className="mt-2 grid grid-cols-[repeat(auto-fill,minmax(220px,1fr))] gap-3">
          <label className="flex flex-col gap-1">
            <span className="text-qsdm-muted">Pool URL</span>
            <input
              className={INPUT}
              value={draft.poolUrl}
              onChange={(event) =>
                setDraft({ ...draft, poolUrl: event.target.value })
              }
            />
          </label>
          <label className="flex flex-col gap-1">
            <span className="text-qsdm-muted">Backup pool URL (optional)</span>
            <input
              className={INPUT}
              value={draft.backupUrl}
              onChange={(event) =>
                setDraft({ ...draft, backupUrl: event.target.value })
              }
            />
          </label>
          <label className="flex flex-col gap-1">
            <span className="text-qsdm-muted">
              Mine solo after the pool is unreachable (minutes)
            </span>
            <input
              className={INPUT}
              type="number"
              min={1}
              max={1440}
              value={draft.fallbackMinutes}
              onChange={(event) =>
                setDraft({
                  ...draft,
                  fallbackMinutes: Number(event.target.value),
                })
              }
            />
          </label>
          <label className="flex flex-col gap-1">
            <span className="text-qsdm-muted">Worker name (optional)</span>
            <input
              className={INPUT}
              value={draft.worker}
              onChange={(event) =>
                setDraft({ ...draft, worker: event.target.value })
              }
            />
          </label>
        </div>
        <div className="mt-2 flex items-center gap-3">
          <button
            type="button"
            className={BUTTON}
            disabled={saving}
            onClick={() => save(draft)}
          >
            {saving ? 'Saving...' : 'Save'}
          </button>
          <span className="text-qsdm-muted">
            Settings apply the next time the Miner starts.
          </span>
        </div>
      </details>

      {status.recentReceipts.length > 0 && (
        <details>
          <summary className="cursor-pointer text-qsdm-muted">
            Latest share receipts (signed by the pool, kept on this computer)
          </summary>
          <ul className="mt-2 flex flex-col gap-1 font-mono text-[11.5px] text-qsdm-text">
            {status.recentReceipts
              .slice()
              .reverse()
              .map((receipt) => (
                <li key={`${receipt.job_id}-${receipt.index}`}>
                  #{receipt.index} difficulty {receipt.difficulty}{' '}
                  {receipt.at_unix_ms
                    ? new Date(receipt.at_unix_ms).toLocaleString()
                    : ''}
                </li>
              ))}
          </ul>
        </details>
      )}
    </div>
  );
}
