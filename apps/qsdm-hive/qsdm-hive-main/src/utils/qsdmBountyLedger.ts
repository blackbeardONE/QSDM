// QSDM Bug Bounty ledger (https://qsdm.tech/bounty/payouts.json): the strict
// parser and the sums Hive shows. Pure functions, shared by the main process
// (which fetches and caches the ledger) and the renderer (which shows it).

export const QSDM_BOUNTY_LEDGER_SCHEMA = 'qsdm.bounty-ledger.v1';
export const QSDM_BOUNTY_LEDGER_URL = 'https://qsdm.tech/bounty/payouts.json';
export const QSDM_BOUNTY_DEFAULT_MONTHLY_PRIZES_CELL = [1000, 500, 250];
export const QSDM_BOUNTY_LEADERBOARD_SIZE = 10;

// The panel's buttons always use these fixed links, never the ledger's own.
export const QSDM_BOUNTY_LINKS = {
  report: 'https://github.com/qsdm-tech/QSDM/security/advisories/new',
  advisories:
    'https://github.com/qsdm-tech/QSDM/security/advisories?state=published',
  rules: 'https://qsdm.tech/security.html',
  page: 'https://qsdm.tech/bounty.html',
} as const;

export type QsdmBountyKind = 'bounty' | 'prize';

export interface QsdmBountyPayout {
  tx: string;
  block: number;
  time: string;
  address: string;
  amount_cell: number;
  kind: QsdmBountyKind;
  hunter: string | null;
  advisory: string | null;
}

export interface QsdmBountyLedger {
  schema: typeof QSDM_BOUNTY_LEDGER_SCHEMA;
  updated: string;
  wallet: string;
  pool_cell: number;
  monthly_prizes_cell: number[];
  links: {
    report: string;
    advisories: string;
    rules: string;
    page: string;
  };
  payouts: QsdmBountyPayout[];
}

const ADDRESS = /^[0-9a-f]{64}$/;
const ISO_TIME =
  /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})$/;
const MAX_PAYOUTS = 10_000;
const MAX_TEXT = 200;

const fail = (where: string, why: string): never => {
  throw new Error(`Bounty ledger: ${where} ${why}`);
};

const isObject = (value: unknown): value is Record<string, unknown> =>
  typeof value === 'object' && value !== null && !Array.isArray(value);

const readText = (value: unknown, where: string) => {
  if (typeof value !== 'string' || !value.trim() || value.length > MAX_TEXT) {
    return fail(where, 'must be a short non-empty string');
  }
  return value;
};

const readTime = (value: unknown, where: string) => {
  const text = readText(value, where);
  if (!ISO_TIME.test(text) || Number.isNaN(Date.parse(text))) {
    return fail(where, 'must be an ISO 8601 time');
  }
  return text;
};

const readAmount = (value: unknown, where: string, allowZero: boolean) => {
  if (
    typeof value !== 'number' ||
    !Number.isFinite(value) ||
    value < 0 ||
    (!allowZero && value === 0)
  ) {
    return fail(where, allowZero ? 'must be 0 or more' : 'must be above 0');
  }
  return value;
};

const readAddress = (value: unknown, where: string) => {
  if (typeof value !== 'string' || !ADDRESS.test(value)) {
    return fail(where, 'must be a lowercase 64-hex address');
  }
  return value;
};

const readHttpsUrl = (value: unknown, where: string) => {
  const text = readText(value, where);
  try {
    if (new URL(text).protocol === 'https:') return text;
  } catch {
    // Falls through to the error below.
  }
  return fail(where, 'must be an https link');
};

const readPayout = (value: unknown, index: number): QsdmBountyPayout => {
  const where = `payouts[${index}]`;
  if (!isObject(value)) return fail(where, 'must be an object');
  const { block, kind, hunter, advisory } = value;
  if (typeof block !== 'number' || !Number.isInteger(block) || block < 0) {
    fail(`${where}.block`, 'must be a whole number');
  }
  if (kind !== 'bounty' && kind !== 'prize') {
    fail(`${where}.kind`, 'must be "bounty" or "prize"');
  }
  return {
    tx: readText(value.tx, `${where}.tx`),
    block: block as number,
    time: readTime(value.time, `${where}.time`),
    address: readAddress(value.address, `${where}.address`),
    amount_cell: readAmount(value.amount_cell, `${where}.amount_cell`, false),
    kind: kind as QsdmBountyKind,
    hunter: hunter === null ? null : readText(hunter, `${where}.hunter`),
    advisory:
      advisory === null ? null : readHttpsUrl(advisory, `${where}.advisory`),
  };
};

/** Checks a downloaded ledger field by field. Unknown fields are dropped; anything malformed throws. */
export const parseQsdmBountyLedger = (raw: unknown): QsdmBountyLedger => {
  if (!isObject(raw)) return fail('document', 'must be an object');
  if (raw.schema !== QSDM_BOUNTY_LEDGER_SCHEMA) {
    fail('schema', `must be "${QSDM_BOUNTY_LEDGER_SCHEMA}"`);
  }

  const prizes = raw.monthly_prizes_cell;
  if (!Array.isArray(prizes) || prizes.length > 10) {
    fail('monthly_prizes_cell', 'must be a short list');
  }

  const { links } = raw;
  if (!isObject(links)) fail('links', 'must be an object');
  const linkObject = links as Record<string, unknown>;

  const { payouts } = raw;
  if (!Array.isArray(payouts) || payouts.length > MAX_PAYOUTS) {
    fail('payouts', 'must be a list');
  }

  return {
    schema: QSDM_BOUNTY_LEDGER_SCHEMA,
    updated: readTime(raw.updated, 'updated'),
    wallet: readAddress(raw.wallet, 'wallet'),
    pool_cell: readAmount(raw.pool_cell, 'pool_cell', true),
    monthly_prizes_cell: (prizes as unknown[]).map((prize, index) =>
      readAmount(prize, `monthly_prizes_cell[${index}]`, true)
    ),
    links: {
      report: readHttpsUrl(linkObject.report, 'links.report'),
      advisories: readHttpsUrl(linkObject.advisories, 'links.advisories'),
      rules: readHttpsUrl(linkObject.rules, 'links.rules'),
      page: readHttpsUrl(linkObject.page, 'links.page'),
    },
    payouts: (payouts as unknown[]).map(readPayout),
  };
};

const normalizeAddresses = (addresses: (string | null | undefined)[]) =>
  new Set(
    addresses
      .map((address) => (address || '').trim().toLowerCase())
      .filter((address) => ADDRESS.test(address))
  );

export interface QsdmBountyTotal {
  totalCell: number;
  bountyCell: number;
  prizeCell: number;
  payouts: number;
}

/** What the given wallet addresses received: bounties and prizes together, and the prize part on its own. */
export const getQsdmBountyTotal = (
  ledger: QsdmBountyLedger,
  addresses: (string | null | undefined)[]
): QsdmBountyTotal => {
  const mine = normalizeAddresses(addresses);
  const total: QsdmBountyTotal = {
    totalCell: 0,
    bountyCell: 0,
    prizeCell: 0,
    payouts: 0,
  };
  ledger.payouts.forEach((payout) => {
    if (!mine.has(payout.address)) return;
    total.totalCell += payout.amount_cell;
    total.payouts += 1;
    if (payout.kind === 'prize') total.prizeCell += payout.amount_cell;
    else total.bountyCell += payout.amount_cell;
  });
  return total;
};

/** abcd1234…wxyz */
export const shortenQsdmAddress = (address: string) =>
  address.length > 12 ? `${address.slice(0, 8)}…${address.slice(-4)}` : address;

export interface QsdmBountyHunterRow {
  rank: number;
  address: string;
  hunter: string;
  totalCell: number;
  reports: number;
  isYou: boolean;
}

interface HunterSum {
  address: string;
  hunter: string | null;
  hunterTime: string;
  totalCell: number;
  reports: number;
  firstTime: number;
}

// One hunter is one payout address. The name is the newest non-empty
// `hunter` given for that address.
const sumByAddress = (payouts: QsdmBountyPayout[]) => {
  const sums = new Map<string, HunterSum>();
  payouts.forEach((payout) => {
    const time = Date.parse(payout.time);
    const sum = sums.get(payout.address) || {
      address: payout.address,
      hunter: null,
      hunterTime: '',
      totalCell: 0,
      reports: 0,
      firstTime: time,
    };
    sum.totalCell += payout.amount_cell;
    if (payout.kind === 'bounty') sum.reports += 1;
    sum.firstTime = Math.min(sum.firstTime, time);
    if (payout.hunter && payout.time >= sum.hunterTime) {
      sum.hunter = payout.hunter;
      sum.hunterTime = payout.time;
    }
    sums.set(payout.address, sum);
  });
  // Most CELL first; then most reports; then whoever was paid first.
  return Array.from(sums.values()).sort(
    (a, b) =>
      b.totalCell - a.totalCell ||
      b.reports - a.reports ||
      a.firstTime - b.firstTime ||
      a.address.localeCompare(b.address)
  );
};

/** The top hunters by all CELL received (bounties and prizes). */
export const getQsdmBountyLeaderboard = (
  ledger: QsdmBountyLedger,
  myAddresses: (string | null | undefined)[],
  size = QSDM_BOUNTY_LEADERBOARD_SIZE
): QsdmBountyHunterRow[] => {
  const mine = normalizeAddresses(myAddresses);
  return sumByAddress(ledger.payouts)
    .slice(0, size)
    .map((sum, index) => ({
      rank: index + 1,
      address: sum.address,
      hunter: sum.hunter || shortenQsdmAddress(sum.address),
      totalCell: sum.totalCell,
      reports: sum.reports,
      isYou: mine.has(sum.address),
    }));
};

export interface QsdmBountyMonthlyRow extends QsdmBountyHunterRow {
  prizeCell: number;
}

/** The current UTC calendar month's top 3 by bounty payouts, with the prize each would get. */
export const getQsdmBountyMonthlyTop = (
  ledger: QsdmBountyLedger,
  myAddresses: (string | null | undefined)[],
  now: Date = new Date()
): QsdmBountyMonthlyRow[] => {
  const prizes = ledger.monthly_prizes_cell.length
    ? ledger.monthly_prizes_cell
    : QSDM_BOUNTY_DEFAULT_MONTHLY_PRIZES_CELL;
  const monthStart = Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), 1);
  const monthEnd = Date.UTC(now.getUTCFullYear(), now.getUTCMonth() + 1, 1);
  const thisMonth = ledger.payouts.filter((payout) => {
    const time = Date.parse(payout.time);
    return payout.kind === 'bounty' && time >= monthStart && time < monthEnd;
  });
  const mine = normalizeAddresses(myAddresses);
  return sumByAddress(thisMonth)
    .slice(0, 3)
    .map((sum, index) => ({
      rank: index + 1,
      address: sum.address,
      hunter: sum.hunter || shortenQsdmAddress(sum.address),
      totalCell: sum.totalCell,
      reports: sum.reports,
      isYou: mine.has(sum.address),
      prizeCell: prizes[index] ?? 0,
    }));
};

/** Whole CELL with thousands separators: 1,000 CELL. */
export const formatQsdmBountyCell = (value: number) =>
  `${Math.round(Number.isFinite(value) ? value : 0).toLocaleString(
    'en-US'
  )} CELL`;
