import { QSDM_MINER_SYSTEM_TASK_ID } from 'config/qsdmSystemTasks';
import { setQsdmMinerRewardAddressToSigner as updateRewardAddress } from 'main/services/qsdmSystemTasks';
import { QsdmMinerRewardAddressUpdateResponse } from 'models/api/qsdm';

export const setQsdmMinerRewardAddressToSigner =
  async (): Promise<QsdmMinerRewardAddressUpdateResponse> => {
    const result = updateRewardAddress();
    const rewardAddressMatchesSigner =
      result.address.toLowerCase() === result.signer?.toLowerCase();

    return {
      configured: true,
      taskId: QSDM_MINER_SYSTEM_TASK_ID,
      rewardAddress: result.address,
      rewardAddressSource: result.source,
      signerAddress: result.signer,
      rewardAddressMatchesSigner,
      configPath: result.configPath,
      updated: result.updated,
      backupPath: result.backupPath,
      requiresMinerRestart: result.requiresMinerRestart,
      warning: result.requiresMinerRestart
        ? 'Miner reward address was updated. Restart the miner task for the running miner process to pick it up.'
        : undefined,
      checkedAt: new Date().toISOString(),
    };
  };
