import { getAverageSlotTime } from 'vendor/qsdm-chain/taskNode';
import { buildQsdmCoreApiUrl, QSDM_TASK_RUNTIME_MODE } from 'config/qsdm';
import {
  getQsdmReadErrorMessage,
  qsdmGetJson,
} from 'main/services/qsdmHttpRead';
import sdk from 'main/services/sdk';

const FALLBACK_AVERAGE_SLOT_TIME_MS = 420;

type QsdmStatusResponse = {
  tokenomics?: {
    target_block_time_seconds?: number;
  };
};

const getAvgSlotTime = async (): Promise<number> => {
  if (QSDM_TASK_RUNTIME_MODE === 'qsdm-native') {
    try {
      const response = await qsdmGetJson<QsdmStatusResponse>(
        buildQsdmCoreApiUrl('/status'),
        { timeout: 4000 }
      );
      const targetBlockTimeSeconds =
        response.tokenomics?.target_block_time_seconds;

      return targetBlockTimeSeconds
        ? targetBlockTimeSeconds * 1000
        : FALLBACK_AVERAGE_SLOT_TIME_MS;
    } catch (error) {
      console.warn(
        `QSDM average block time is temporarily unavailable; using ${FALLBACK_AVERAGE_SLOT_TIME_MS}ms. ${getQsdmReadErrorMessage(
          error
        )}`
      );
      return FALLBACK_AVERAGE_SLOT_TIME_MS;
    }
  }

  const avgSlotTime = await getAverageSlotTime(sdk.k2Connection as any);
  return avgSlotTime || FALLBACK_AVERAGE_SLOT_TIME_MS;
};

export default getAvgSlotTime;
