import { Event } from 'electron';

import { QsdmSignedCellLoopResponse } from 'models/api/qsdm';

import storeUserConfig from './storeUserConfig';
import getUserConfig from './getUserConfig';
import { runQsdmSignedCellLoop } from './qsdm/runQsdmSignedCellLoop';

export interface RecoverLostTokensResult {
  status: 'skipped' | 'completed';
  message: string;
  recoveredRewards: number;
  recoveredStakes: number;
  actions?: string[];
  finalBalance?: number;
}

const formatCellAmount = (amount?: number) => {
  if (amount === undefined) return undefined;
  return new Intl.NumberFormat('en-US', {
    maximumFractionDigits: 4,
  }).format(amount);
};

const buildCompletedMessage = (result: QsdmSignedCellLoopResponse) => {
  const actionList = result.actions.map(({ action }) => action).join(' -> ');
  const finalBalance = formatCellAmount(result.finalBalance);

  return [
    `CELL recovery completed through QSDM Core (${actionList}).`,
    finalBalance ? `Signer balance is now ${finalBalance} CELL.` : '',
  ]
    .filter(Boolean)
    .join(' ');
};

export const recoverLostTokens =
  async (): Promise<RecoverLostTokensResult> => {
    try {
      const result = await runQsdmSignedCellLoop({} as Event, {
        skipFund: true,
      });

      await updateLastLostTokensClaimDate();

      return {
        status: 'completed',
        message: buildCompletedMessage(result),
        recoveredRewards: result.actions.filter(
          ({ action }) => action === 'claim'
        ).length,
        recoveredStakes: result.actions.filter(({ action }) => action === 'stake')
          .length,
        actions: result.actions.map(({ action }) => action),
        finalBalance: result.finalBalance,
      };
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      console.warn('CELL recovery skipped; QSDM signed loop is not ready.', error);
      return {
        status: 'skipped',
        message: `CELL recovery needs QSDM Core and a ready signer. ${message}`,
        recoveredRewards: 0,
        recoveredStakes: 0,
      };
    }
  };

const updateLastLostTokensClaimDate = async () => {
  const userConfig = await getUserConfig();
  const lastLostTokensClaimDate = new Date().toISOString();
  await storeUserConfig({} as Event, {
    settings: { ...userConfig, lastLostTokensClaimDate },
  });
};
