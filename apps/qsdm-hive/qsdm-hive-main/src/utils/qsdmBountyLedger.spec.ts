import {
  formatQsdmBountyCell,
  getQsdmBountyLeaderboard,
  getQsdmBountyMonthlyTop,
  getQsdmBountyTotal,
  parseQsdmBountyLedger,
  shortenQsdmAddress,
} from './qsdmBountyLedger';

const A = 'a'.repeat(60) + '1111';
const B = 'b'.repeat(60) + '2222';
const C = `abcd1234${'c'.repeat(52)}ef01`;
const D = 'd'.repeat(64);

const payout = (overrides: Record<string, unknown> = {}) => ({
  tx: 'tx-1',
  block: 1109014,
  time: '2026-11-15T01:47:52Z',
  address: A,
  amount_cell: 1000,
  kind: 'bounty',
  hunter: 'tokenist / swaplaner',
  advisory:
    'https://github.com/qsdm-tech/QSDM/security/advisories/GHSA-324j-w3cm-g7h4',
  ...overrides,
});

const ledgerJson = (
  payouts: unknown[],
  overrides: Record<string, unknown> = {}
) => ({
  schema: 'qsdm.bounty-ledger.v1',
  updated: '2026-11-20T03:00:00Z',
  wallet: '4146b9a5cdb287c46c1ce67f6e377af7d5bc0c5f83cbef7403f0346b5433e301',
  pool_cell: 50000,
  monthly_prizes_cell: [1000, 500, 250],
  links: {
    report: 'https://github.com/qsdm-tech/QSDM/security/advisories/new',
    advisories:
      'https://github.com/qsdm-tech/QSDM/security/advisories?state=published',
    rules: 'https://qsdm.tech/security.html',
    page: 'https://qsdm.tech/bounty.html',
  },
  payouts,
  ...overrides,
});

describe('parseQsdmBountyLedger', () => {
  it('accepts the version 1 ledger and drops unknown fields', () => {
    const ledger = parseQsdmBountyLedger(
      ledgerJson(
        [payout({ extra: 'x' }), payout({ hunter: null, advisory: null })],
        {
          future: true,
        }
      )
    );
    expect(ledger.payouts).toHaveLength(2);
    expect(ledger.payouts[0]).not.toHaveProperty('extra');
    expect(ledger).not.toHaveProperty('future');
    expect(ledger.payouts[1].hunter).toBeNull();
    expect(ledger.pool_cell).toBe(50000);
  });

  it.each([
    ['a wrong schema', ledgerJson([], { schema: 'qsdm.bounty-ledger.v2' })],
    ['a non-object', 'nope'],
    ['a bad wallet', ledgerJson([], { wallet: 'ABC' })],
    ['payouts that are not a list', ledgerJson([], { payouts: {} })],
    ['an http link', ledgerJson([], { links: { report: 'http://x' } })],
    [
      'an uppercase address',
      ledgerJson([payout({ address: A.toUpperCase() })]),
    ],
    ['an unknown kind', ledgerJson([payout({ kind: 'tip' })])],
    ['a zero amount', ledgerJson([payout({ amount_cell: 0 })])],
    ['a string amount', ledgerJson([payout({ amount_cell: '1000' })])],
    ['a bad time', ledgerJson([payout({ time: 'yesterday' })])],
    ['a fractional block', ledgerJson([payout({ block: 1.5 })])],
    [
      'a non-https advisory',
      ledgerJson([payout({ advisory: 'javascript:alert(1)' })]),
    ],
    ['a missing hunter field', ledgerJson([payout({ hunter: undefined })])],
  ])('rejects %s', (_label, raw) => {
    expect(() => parseQsdmBountyLedger(raw)).toThrow(/Bounty ledger/);
  });
});

describe('bounty sums', () => {
  const ledger = parseQsdmBountyLedger(
    ledgerJson([
      payout({
        tx: 't1',
        address: A,
        amount_cell: 1000,
        time: '2026-10-02T00:00:00Z',
      }),
      payout({
        tx: 't2',
        address: A,
        amount_cell: 500,
        kind: 'prize',
        time: '2026-11-01T00:00:00Z',
      }),
      payout({
        tx: 't3',
        address: B,
        amount_cell: 1000,
        hunter: null,
        time: '2026-11-03T00:00:00Z',
      }),
      payout({
        tx: 't4',
        address: B,
        amount_cell: 250,
        hunter: null,
        time: '2026-11-04T00:00:00Z',
      }),
      payout({
        tx: 't5',
        address: C,
        amount_cell: 300,
        hunter: null,
        time: '2026-11-30T23:59:59Z',
      }),
      payout({
        tx: 't6',
        address: D,
        amount_cell: 100,
        hunter: 'old',
        time: '2026-11-10T00:00:00Z',
      }),
      payout({
        tx: 't7',
        address: D,
        amount_cell: 100,
        hunter: 'new',
        time: '2026-12-01T00:00:00Z',
      }),
    ])
  );

  it('totals one wallet, with the prize part apart', () => {
    expect(getQsdmBountyTotal(ledger, [A])).toEqual({
      totalCell: 1500,
      bountyCell: 1000,
      prizeCell: 500,
      payouts: 2,
    });
  });

  it('totals several wallets and ignores case and blanks', () => {
    expect(
      getQsdmBountyTotal(ledger, [A.toUpperCase(), B, '', undefined]).totalCell
    ).toBe(2750);
  });

  it('gives 0 for a wallet with no payouts', () => {
    expect(getQsdmBountyTotal(ledger, ['e'.repeat(64)]).totalCell).toBe(0);
    expect(getQsdmBountyTotal(ledger, []).payouts).toBe(0);
  });

  it('ranks hunters by total CELL and marks the user', () => {
    const rows = getQsdmBountyLeaderboard(ledger, [B]);
    expect(
      rows.map((row) => [row.rank, row.hunter, row.totalCell, row.reports])
    ).toEqual([
      [1, 'tokenist / swaplaner', 1500, 1],
      [2, shortenQsdmAddress(B), 1250, 2],
      [3, shortenQsdmAddress(C), 300, 1],
      [4, 'new', 200, 2],
    ]);
    expect(rows.map((row) => row.isYou)).toEqual([false, true, false, false]);
  });

  it('keeps the leaderboard to the asked size', () => {
    expect(getQsdmBountyLeaderboard(ledger, [], 2)).toHaveLength(2);
  });

  it('picks this UTC month top 3 from bounty payouts only', () => {
    const rows = getQsdmBountyMonthlyTop(
      ledger,
      [C],
      new Date('2026-11-20T12:00:00Z')
    );
    expect(
      rows.map((row) => [row.hunter, row.totalCell, row.prizeCell, row.isYou])
    ).toEqual([
      [shortenQsdmAddress(B), 1250, 1000, false],
      [shortenQsdmAddress(C), 300, 500, true],
      ['old', 100, 250, false],
    ]);
  });

  it('is empty for a month with no bounties', () => {
    expect(
      getQsdmBountyMonthlyTop(ledger, [], new Date('2027-02-01T00:00:00Z'))
    ).toEqual([]);
  });
});

describe('formatting', () => {
  it('shortens addresses as abcd1234…wxyz', () => {
    expect(shortenQsdmAddress(C)).toBe('abcd1234…ef01');
  });

  it('shows whole CELL with thousands separators', () => {
    expect(formatQsdmBountyCell(1000)).toBe('1,000 CELL');
    expect(formatQsdmBountyCell(0)).toBe('0 CELL');
    expect(formatQsdmBountyCell(50000.4)).toBe('50,000 CELL');
  });
});
