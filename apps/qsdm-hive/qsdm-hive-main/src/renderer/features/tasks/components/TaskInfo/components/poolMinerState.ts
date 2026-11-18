import {
  QSDM_PULSE_ACTIVATION_DATE_TEXT,
  QSDM_PULSE_ACTIVATION_HEIGHT,
} from 'config/qsdmSystemTasks';
import { QsdmPulsePoolStatus } from 'models/api/qsdm';

export type PoolMinerState = {
  label: string;
  tone: 'good' | 'warn' | 'idle';
  note?: string;
};

export const QSDM_POOL_STATS_URL = 'https://qsdm.tech/pool/stats';

export const poolStatsUrl = (address?: string) =>
  address
    ? `${QSDM_POOL_STATS_URL}?address=${encodeURIComponent(address)}`
    : QSDM_POOL_STATS_URL;

export const poolPayoutsStartText = (activationHeight?: number) =>
  `Pool payouts start at block ${(
    activationHeight || QSDM_PULSE_ACTIVATION_HEIGHT
  ).toLocaleString(
    'en-US'
  )} (${QSDM_PULSE_ACTIVATION_DATE_TEXT}). Until then this task mines solo.`;

// What the Pool Miner card says, in plain words.
export const describePoolMinerState = (
  isRunning: boolean,
  status?: QsdmPulsePoolStatus
): PoolMinerState => {
  if (!isRunning) {
    return { label: 'Stopped', tone: 'idle' };
  }
  if (!status?.ruleActive) {
    return {
      label: 'Mining solo',
      tone: 'warn',
      note: poolPayoutsStartText(status?.activationHeight),
    };
  }
  if (!status.member) {
    return {
      label: 'Mining solo',
      tone: 'warn',
      note: 'This wallet is not in the pool. Stop and start the task to join.',
    };
  }
  if (status.poolError || !status.poolAnswering) {
    return {
      label: 'Mining solo (pool unreachable)',
      tone: 'warn',
      note: 'Pool unreachable — mining solo, will return to the pool automatically.',
    };
  }
  return { label: 'Mining in pool', tone: 'good' };
};

// Share of the current window, as a percent with up to 4 decimals.
export const formatWindowShare = (share?: unknown) =>
  typeof share === 'number' && Number.isFinite(share)
    ? `${(share * 100).toLocaleString('en-US', {
        maximumFractionDigits: 4,
      })}%`
    : '—';

export const formatCellAmount = (value?: number) =>
  typeof value === 'number' && Number.isFinite(value)
    ? `${value.toLocaleString('en-US', { maximumFractionDigits: 8 })} CELL`
    : '—';
