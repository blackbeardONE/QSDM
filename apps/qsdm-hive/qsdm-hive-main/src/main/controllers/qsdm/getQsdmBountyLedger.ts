import { getQsdmBountyLedgerStatus } from 'main/services/qsdmBountyLedger';
import { QsdmBountyLedgerStatus } from 'models/api/qsdm';

export const getQsdmBountyLedger = async (): Promise<QsdmBountyLedgerStatus> =>
  getQsdmBountyLedgerStatus();
