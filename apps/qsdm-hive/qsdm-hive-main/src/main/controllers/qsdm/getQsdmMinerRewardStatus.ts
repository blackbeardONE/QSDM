import { QSDM_MINER_SYSTEM_TASK_ID } from 'config/qsdmSystemTasks';
import { getQsdmMinerProtocolRewardInfo } from 'main/services/qsdmMinerProtocolRewards';
import { getQsdmMinerEnrollmentStatus } from 'main/services/qsdmMinerEnrollment';
import {
  getQsdmMinerRewardAddressInfo,
  getQsdmMinerSystemProcessInfo,
} from 'main/services/qsdmSystemTasks';
import { QsdmMinerRewardStatusResponse } from 'models/api/qsdm';

const sameAddress = (left?: string, right?: string) =>
  !!left?.trim() && left.trim().toLowerCase() === right?.trim().toLowerCase();

export const getQsdmMinerRewardStatus =
  async (): Promise<QsdmMinerRewardStatusResponse> => {
    const checkedAt = new Date().toISOString();
    const rewardAddressInfo = getQsdmMinerRewardAddressInfo();
    const enrollment = await getQsdmMinerEnrollmentStatus();
    const minerProcess = await getQsdmMinerSystemProcessInfo();
    enrollment.computeBackend = 'cuda';
    enrollment.gpuComputeActive = Boolean(minerProcess);

    if (!rewardAddressInfo) {
      return {
        configured: false,
        taskId: QSDM_MINER_SYSTEM_TASK_ID,
        rewardAddressMatchesSigner: false,
        warning:
          'QSDM Miner reward address is not configured. Configure a QSDM signer or miner reward_address before relying on mining rewards.',
        checkedAt,
        enrollment,
      };
    }

    const rewardAddressMatchesSigner = sameAddress(
      rewardAddressInfo.address,
      rewardAddressInfo.signer
    );

    try {
      const rewardInfo = await getQsdmMinerProtocolRewardInfo();

      return {
        configured: true,
        taskId: QSDM_MINER_SYSTEM_TASK_ID,
        rewardAddress: rewardAddressInfo.address,
        rewardAddressSource: rewardAddressInfo.source,
        signerAddress: rewardAddressInfo.signer,
        rewardAddressMatchesSigner,
        configPath: rewardAddressInfo.configPath,
        balanceCell: rewardInfo?.balanceCell,
        baselineCell: rewardInfo?.baselineCell,
        earnedCell: rewardInfo?.earnedCell,
        earnedDenomination: rewardInfo?.earnedDenomination,
        warning: rewardAddressMatchesSigner
          ? undefined
          : 'Mining rewards are paid to the configured miner reward address, which is different from the active Hive signer.',
        checkedAt,
        enrollment,
      };
    } catch (error: any) {
      return {
        configured: true,
        taskId: QSDM_MINER_SYSTEM_TASK_ID,
        rewardAddress: rewardAddressInfo.address,
        rewardAddressSource: rewardAddressInfo.source,
        signerAddress: rewardAddressInfo.signer,
        rewardAddressMatchesSigner,
        configPath: rewardAddressInfo.configPath,
        warning: rewardAddressMatchesSigner
          ? undefined
          : 'Mining rewards are paid to the configured miner reward address, which is different from the active Hive signer.',
        error: error?.message || String(error),
        checkedAt,
        enrollment,
      };
    }
  };
