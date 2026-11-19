import React, { useMemo, useState } from 'react';
import { useQuery } from 'react-query';

import {
  getQsdmBountyLedger,
  listQsdmWallets,
  openBrowserWindow,
  QueryKeys,
} from 'renderer/services';
import {
  formatQsdmBountyCell,
  getQsdmBountyLeaderboard,
  getQsdmBountyMonthlyTop,
  getQsdmBountyTotal,
  QSDM_BOUNTY_LINKS,
  QsdmBountyHunterRow,
} from 'utils/qsdmBountyLedger';

const LEDGER_REFETCH_MS = 10 * 60 * 1000;

const BUTTON_QUIET =
  'rounded-lg border border-qsdm-border px-3 py-1 font-semibold text-qsdm-text transition hover:border-qsdm-gold/60';
const BUTTON =
  'rounded-lg border border-qsdm-gold/60 bg-qsdm-gold px-3 py-1 font-semibold text-qsdm-bg transition hover:brightness-105';

const LINKS: { label: string; url: string; primary?: boolean }[] = [
  {
    label: 'Report a vulnerability',
    url: QSDM_BOUNTY_LINKS.report,
    primary: true,
  },
  { label: 'Published advisories', url: QSDM_BOUNTY_LINKS.advisories },
  { label: 'Bounty rules', url: QSDM_BOUNTY_LINKS.rules },
  { label: 'Leaderboard', url: QSDM_BOUNTY_LINKS.page },
];

function Label({ children }: { children: React.ReactNode }) {
  return (
    <span className="text-[10.5px] font-semibold uppercase tracking-[0.08em] text-qsdm-muted">
      {children}
    </span>
  );
}

function HunterTable({
  rows,
  prizeColumn,
}: {
  rows: (QsdmBountyHunterRow & { prizeCell?: number })[];
  prizeColumn?: boolean;
}) {
  return (
    <table className="w-full text-left text-[12px] text-qsdm-text">
      <thead>
        <tr className="text-qsdm-muted">
          <th className="py-1 pr-2 font-semibold">#</th>
          <th className="py-1 pr-2 font-semibold">Hunter</th>
          <th className="py-1 pr-2 text-right font-semibold">
            {prizeColumn ? 'CELL this month' : 'Total CELL'}
          </th>
          <th className="py-1 pr-2 text-right font-semibold">Reports</th>
          {prizeColumn && (
            <th className="py-1 text-right font-semibold">Prize</th>
          )}
        </tr>
      </thead>
      <tbody>
        {rows.map((row) => (
          <tr
            key={row.address}
            className={
              row.isYou
                ? 'bg-qsdm-gold/10 font-semibold text-qsdm-gold'
                : 'border-t border-qsdm-border'
            }
          >
            <td className="py-1 pr-2">{row.rank}</td>
            <td
              className="max-w-[260px] truncate py-1 pr-2"
              title={row.address}
            >
              {row.hunter}
              {row.isYou && ' (you)'}
            </td>
            <td className="py-1 pr-2 text-right">
              {formatQsdmBountyCell(row.totalCell)}
            </td>
            <td className="py-1 pr-2 text-right">{row.reports}</td>
            {prizeColumn && (
              <td className="py-1 text-right">
                {formatQsdmBountyCell(row.prizeCell ?? 0)}
              </td>
            )}
          </tr>
        ))}
      </tbody>
    </table>
  );
}

// QSDM Bug Bounty: an information row at the top of the available tasks.
// It is not a runnable task: no process, no stake, no start or stop.
export function BugBountyRow() {
  const [open, setOpen] = useState(false);

  const { data: status, isLoading } = useQuery(
    [QueryKeys.QsdmBountyLedger],
    getQsdmBountyLedger,
    { refetchInterval: LEDGER_REFETCH_MS, staleTime: LEDGER_REFETCH_MS }
  );
  const { data: wallets } = useQuery(
    [QueryKeys.QsdmWalletList],
    listQsdmWallets,
    { refetchInterval: 15000, retry: false }
  );

  const activeAddress = wallets?.active || '';
  const allAddresses = useMemo(
    () => (wallets?.wallets || []).map((wallet) => wallet.address),
    [wallets]
  );
  const ledger = status?.ledger || null;

  const view = useMemo(() => {
    if (!ledger) return null;
    return {
      active: getQsdmBountyTotal(ledger, [activeAddress]),
      all: getQsdmBountyTotal(ledger, [activeAddress, ...allAddresses]),
      leaderboard: getQsdmBountyLeaderboard(
        ledger,
        allAddresses.concat(activeAddress)
      ),
      month: getQsdmBountyMonthlyTop(
        ledger,
        allAddresses.concat(activeAddress)
      ),
    };
  }, [ledger, activeAddress, allAddresses]);

  const showAllWallets = allAddresses.length > 1 && !!view;

  let summary = 'Checking...';
  if (view) {
    summary = `Your bounty: ${formatQsdmBountyCell(view.active.totalCell)}`;
  } else if (!isLoading) {
    summary = 'Could not load bounty totals';
  }

  return (
    <div
      id="bug-bounty-row"
      className="mx-2 my-2 flex flex-col gap-3 rounded-lg border border-qsdm-border bg-qsdm-panel px-4 py-3 text-xs text-qsdm-text"
    >
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex min-w-0 flex-col gap-1">
          <span className="flex items-center gap-2 text-sm font-semibold">
            QSDM Bug Bounty
            <span className="rounded-full border border-qsdm-gold/60 px-2 text-[10px] font-semibold uppercase text-qsdm-gold">
              Info
            </span>
          </span>
          <span className="text-qsdm-muted">
            Find a security bug in QSDM, report it privately on GitHub, and get
            paid in CELL. The top 3 hunters each month get an extra 1,000 / 500
            / 250 CELL.
          </span>
        </div>
        <div className="flex items-center gap-3">
          <span className="font-semibold">{summary}</span>
          <button
            type="button"
            className={BUTTON_QUIET}
            aria-expanded={open}
            onClick={() => setOpen(!open)}
          >
            {open ? 'Hide' : 'Details'}
          </button>
        </div>
      </div>

      {open && (
        <div className="flex flex-col gap-4 border-t border-qsdm-border pt-3">
          <div className="flex flex-wrap gap-2">
            {LINKS.map((link) => (
              <button
                key={link.url}
                type="button"
                className={link.primary ? BUTTON : BUTTON_QUIET}
                onClick={() => openBrowserWindow(link.url)}
              >
                {link.label}
              </button>
            ))}
          </div>

          {!view && (
            <p className="text-qsdm-muted">
              {isLoading
                ? 'Checking the bounty ledger...'
                : 'Could not load the bounty ledger. Try again later.'}
            </p>
          )}

          {view && (
            <>
              <div className="flex flex-col gap-1">
                <Label>Your bounty total</Label>
                <span className="text-sm font-semibold">
                  {formatQsdmBountyCell(view.active.totalCell)}
                  {!view.active.totalCell && (
                    <span className="ml-2 font-normal text-qsdm-muted">
                      no bounty received yet
                    </span>
                  )}
                </span>
                {view.active.prizeCell > 0 && (
                  <span className="text-qsdm-muted">
                    Includes {formatQsdmBountyCell(view.active.prizeCell)} in
                    monthly prizes
                  </span>
                )}
                {showAllWallets && (
                  <span className="text-qsdm-muted">
                    All your wallets: {formatQsdmBountyCell(view.all.totalCell)}
                    {view.all.prizeCell > 0 &&
                      ` (${formatQsdmBountyCell(
                        view.all.prizeCell
                      )} in prizes)`}
                  </span>
                )}
                {!activeAddress && (
                  <span className="text-qsdm-muted">
                    Add a QSDM wallet to see your total.
                  </span>
                )}
              </div>

              <div className="flex flex-col gap-1">
                <Label>This month</Label>
                {view.month.length ? (
                  <HunterTable rows={view.month} prizeColumn />
                ) : (
                  <span className="text-qsdm-muted">
                    No bounties paid this month yet.
                  </span>
                )}
              </div>

              <div className="flex flex-col gap-1">
                <Label>Leaderboard</Label>
                {view.leaderboard.length ? (
                  <HunterTable rows={view.leaderboard} />
                ) : (
                  <span className="text-qsdm-muted">No payouts yet.</span>
                )}
              </div>

              {status?.source === 'cache' && (
                <span className="text-qsdm-muted">
                  Could not reach qsdm.tech. Showing the copy from{' '}
                  {new Date(ledger?.updated || '').toLocaleString()}.
                </span>
              )}
            </>
          )}
        </div>
      )}
    </div>
  );
}
