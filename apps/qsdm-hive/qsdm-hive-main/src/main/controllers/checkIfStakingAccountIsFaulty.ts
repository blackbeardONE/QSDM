import { KPL_CONTRACT_ID, TASK_CONTRACT_ID } from 'vendor/qsdm-chain/taskNode';
import { PublicKey } from 'vendor/qsdm-chain/qsdmWeb3Adapter';
import { QSDM_TASK_RUNTIME_MODE } from 'config/qsdm';
import sdk from 'main/services/sdk';

import getAccountBalance from './getAccountBalance';

export const checkIfStakingAccountIsFaulty = async (
  _: Event,
  {
    stakingPublicKey,
    isKPLStakingAccount,
  }: { stakingPublicKey: string; isKPLStakingAccount: boolean }
): Promise<boolean> => {
  if (QSDM_TASK_RUNTIME_MODE === 'qsdm-native') {
    return false;
  }

  try {
    const accountInfo = await sdk.k2Connection.getAccountInfo(
      new PublicKey(stakingPublicKey)
    );
    const correspondingProgramId = isKPLStakingAccount
      ? KPL_CONTRACT_ID
      : TASK_CONTRACT_ID;
    const isOwnedByCorrespondingProgram =
      accountInfo?.owner?.toBase58() === correspondingProgramId.toBase58();
    if (isOwnedByCorrespondingProgram) return false;
    const stakingAccountBalance = await getAccountBalance(
      {} as Event,
      stakingPublicKey
    );
    const stakingAccountHasBalance = stakingAccountBalance > 0;

    const stakingAccountIsFaulty =
      !isOwnedByCorrespondingProgram && stakingAccountHasBalance;

    return stakingAccountIsFaulty;
  } catch (error) {
    console.warn(
      'Staking account ownership check failed; skipping recovery warning.',
      error
    );
    return false;
  }
};
