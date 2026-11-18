import { QsdmPulsePoolStatus } from 'models/api/qsdm';

import {
  describePoolMinerState,
  formatCellAmount,
  formatWindowShare,
  poolStatsUrl,
} from './poolMinerState';

const base: QsdmPulsePoolStatus = {
  settings: {
    enabled: false,
    poolUrl: 'https://qsdm.tech/pool/api',
    backupUrl: '',
    fallbackMinutes: 5,
    worker: '',
  },
  address: 'ab'.repeat(32),
  checkedAt: '2026-11-16T00:00:00.000Z',
  receiptsTotal: 0,
  recentReceipts: [],
};

describe('Pool Miner card state', () => {
  it('Stopped when the task is not running', () => {
    expect(describePoolMinerState(false, base)).toEqual({
      label: 'Stopped',
      tone: 'idle',
    });
  });

  it('Mining solo before PULSE, with when payouts start', () => {
    const state = describePoolMinerState(true, {
      ...base,
      ruleActive: false,
      activationHeight: 1210000,
    });
    expect(state.label).toBe('Mining solo');
    expect(state.note).toBe(
      'Pool payouts start at block 1,210,000 (about 26-27 November). Until then this task mines solo.'
    );
  });

  it('Mining solo (pool unreachable) for a member while the pool is down', () => {
    const state = describePoolMinerState(true, {
      ...base,
      ruleActive: true,
      member: true,
      poolError: 'timeout',
    });
    expect(state.label).toBe('Mining solo (pool unreachable)');
    expect(state.note).toBe(
      'Pool unreachable — mining solo, will return to the pool automatically.'
    );
  });

  it('Mining in pool for a member with the pool answering', () => {
    expect(
      describePoolMinerState(true, {
        ...base,
        ruleActive: true,
        member: true,
        poolAnswering: 'https://qsdm.tech/pool/api',
      })
    ).toEqual({ label: 'Mining in pool', tone: 'good' });
  });

  it('formats the stats and the link plainly', () => {
    expect(formatWindowShare(0.0125)).toBe('1.25%');
    expect(formatWindowShare(undefined)).toBe('—');
    expect(formatCellAmount(0.5)).toBe('0.5 CELL');
    expect(formatCellAmount(undefined)).toBe('—');
    expect(poolStatsUrl('ab')).toBe('https://qsdm.tech/pool/stats?address=ab');
    expect(poolStatsUrl('')).toBe('https://qsdm.tech/pool/stats');
  });
});
