import { QsdmPulsePoolStatus } from 'models/api/qsdm';

import {
  buildQsdmPoolJoinDetail,
  describeQsdmPulseActivation,
  planQsdmPoolMinerStart,
} from './qsdmPoolMinerTask';
import { normalizeQsdmPulseSettings } from './qsdmPulsePool';

jest.mock('main/node/helpers/getAppDataPath', () => ({
  getAppDataPath: () => '/tmp/qsdm-pool-miner-spec',
}));

const me = 'ab'.repeat(32);
const pool = 'cd'.repeat(32);
const otherPool = 'ef'.repeat(32);

const status = (
  overrides: Partial<QsdmPulsePoolStatus> = {}
): QsdmPulsePoolStatus => ({
  settings: normalizeQsdmPulseSettings({}),
  address: me,
  checkedAt: '2026-11-16T00:00:00.000Z',
  receiptsTotal: 0,
  recentReceipts: [],
  ...overrides,
});

const run = (s: QsdmPulsePoolStatus, answer = true) => {
  const confirmJoin = jest.fn(async () => answer);
  const join = jest.fn(async () => ({ nonce: 1 }));
  return {
    confirmJoin,
    join,
    result: planQsdmPoolMinerStart({
      getStatus: async () => s,
      confirmJoin,
      join,
    }),
  };
};

describe('QSDM Pool Miner start plan', () => {
  it('mines solo before the PULSE rule is active and says when payouts start', async () => {
    const { result, confirmJoin, join } = run(
      status({ ruleActive: false, activationHeight: 1210000, poolId: pool })
    );
    await expect(result).resolves.toEqual({
      pulse: 'off',
      message:
        'Pool payouts start at block 1,210,000 (about 26-27 November). Until then this task mines solo.',
    });
    expect(confirmJoin).not.toHaveBeenCalled();
    expect(join).not.toHaveBeenCalled();
  });

  it('mines solo when the network cannot be read (rule state unknown)', async () => {
    const { result, join } = run(status({ chainError: 'HTTP 404' }));
    await expect(result).resolves.toMatchObject({ pulse: 'off' });
    expect(join).not.toHaveBeenCalled();
  });

  it('asks once, joins and mines in the pool when the wallet is not a member', async () => {
    const { result, confirmJoin, join } = run(
      status({
        ruleActive: true,
        poolId: pool,
        poolAnswering: 'https://qsdm.tech/pool/api',
        member: false,
        feeBps: 100,
        networkFeeCapBps: 300,
      })
    );
    await expect(result).resolves.toEqual({
      pulse: 'force-on',
      message: 'Joined the QSDM pool. Mining in the pool.',
      joined: true,
    });
    expect(confirmJoin).toHaveBeenCalledWith({
      feeBps: 100,
      networkFeeCapBps: 300,
    });
    expect(join).toHaveBeenCalledTimes(1);
  });

  it('changes nothing when the join question is cancelled', async () => {
    const { result, join } = run(
      status({ ruleActive: true, poolId: pool, member: false }),
      false
    );
    await expect(result).rejects.toThrow(
      'Joining the QSDM pool was cancelled. Nothing was changed.'
    );
    expect(join).not.toHaveBeenCalled();
  });

  it('starts straight in the pool on later starts (already a member)', async () => {
    const { result, confirmJoin, join } = run(
      status({
        ruleActive: true,
        poolId: pool,
        member: true,
        memberPool: pool,
      })
    );
    await expect(result).resolves.toEqual({
      pulse: 'force-on',
      message: 'Mining in the QSDM pool.',
    });
    expect(confirmJoin).not.toHaveBeenCalled();
    expect(join).not.toHaveBeenCalled();
  });

  it('a member keeps pool mode while the pool is down (the miner falls back by itself)', async () => {
    const { result } = run(
      status({
        ruleActive: true,
        member: true,
        memberPool: pool,
        poolError: 'timeout',
      })
    );
    await expect(result).resolves.toMatchObject({ pulse: 'force-on' });
  });

  it('cannot join while the pool does not answer, so it mines solo', async () => {
    const { result, confirmJoin } = run(
      status({ ruleActive: true, member: false, poolError: 'timeout' })
    );
    await expect(result).resolves.toMatchObject({ pulse: 'off' });
    expect(confirmJoin).not.toHaveBeenCalled();
  });

  it('refuses a wallet that is in another pool', async () => {
    const { result } = run(
      status({
        ruleActive: true,
        poolId: pool,
        member: true,
        memberPool: otherPool,
      })
    );
    await expect(result).rejects.toThrow(
      'This wallet is in another PULSE pool'
    );
  });

  it('explains the escrow, the fee and leaving in plain words', () => {
    const detail = buildQsdmPoolJoinDetail({
      feeBps: 100,
      networkFeeCapBps: 300,
    });
    expect(detail).toContain('escrow that nobody can spend');
    expect(detail).toContain('every block');
    expect(detail).toContain('Pool fee: 1% (the network caps pool fees at 3%)');
    expect(detail).toContain('You can leave any time');
    expect(detail).toContain('after about a day');
    expect(describeQsdmPulseActivation()).toContain('block 1,210,000');
  });
});
