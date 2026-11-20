import config from 'config';
import { QsdmBountyLedgerStatus } from 'models/api/qsdm';
import sendMessage from 'preload/sendMessage';

export const getQsdmBountyLedger = (): Promise<QsdmBountyLedgerStatus> =>
  sendMessage(config.endpoints.GET_QSDM_BOUNTY_LEDGER, undefined);
