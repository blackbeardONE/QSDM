// QSDM Bug Bounty: fetches the public payout ledger from qsdm.tech and keeps
// the last good copy on disk, so the panel still shows totals offline.
import fs from 'fs';
import path from 'path';

import axios from 'axios';

import { getAppDataPath } from 'main/node/helpers/getAppDataPath';
import { QsdmBountyLedgerStatus } from 'models/api/qsdm';
import {
  parseQsdmBountyLedger,
  QSDM_BOUNTY_LEDGER_URL,
  QsdmBountyLedger,
} from 'utils/qsdmBountyLedger';

const FETCH_TIMEOUT_MS = 8000;
const MAX_LEDGER_BYTES = 2 * 1024 * 1024;

export const getQsdmBountyLedgerCachePath = () =>
  path.join(getAppDataPath(), 'namespace', 'qsdm-bounty', 'payouts.json');

const readCachedLedger = (): QsdmBountyLedger | null => {
  try {
    const text = fs.readFileSync(getQsdmBountyLedgerCachePath(), 'utf8');
    return parseQsdmBountyLedger(JSON.parse(text));
  } catch {
    return null;
  }
};

const writeCachedLedger = (ledger: QsdmBountyLedger) => {
  try {
    const file = getQsdmBountyLedgerCachePath();
    fs.mkdirSync(path.dirname(file), { recursive: true });
    const temp = `${file}.tmp`;
    fs.writeFileSync(temp, JSON.stringify(ledger));
    fs.renameSync(temp, file);
  } catch (error) {
    console.warn('Could not cache the bounty ledger', (error as Error).message);
  }
};

export const getQsdmBountyLedgerStatus =
  async (): Promise<QsdmBountyLedgerStatus> => {
    const checkedAt = new Date().toISOString();
    try {
      const { data } = await axios.get<unknown>(QSDM_BOUNTY_LEDGER_URL, {
        timeout: FETCH_TIMEOUT_MS,
        maxContentLength: MAX_LEDGER_BYTES,
        responseType: 'json',
        headers: { Accept: 'application/json' },
      });
      const ledger = parseQsdmBountyLedger(data);
      writeCachedLedger(ledger);
      return { ledger, source: 'live', checkedAt };
    } catch (error) {
      const message = (error as Error).message || 'could not load';
      const cached = readCachedLedger();
      return cached
        ? { ledger: cached, source: 'cache', error: message, checkedAt }
        : { ledger: null, source: 'none', error: message, checkedAt };
    }
  };
