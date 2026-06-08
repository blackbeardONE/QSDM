import axios from 'axios';
import { getAverageSlotTime } from 'vendor/qsdm-chain/taskNode';
import { buildQsdmCoreApiUrl, QSDM_TASK_RUNTIME_MODE } from 'config/qsdm';
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
      const response = await axios.get<QsdmStatusResponse>(
        buildQsdmCoreApiUrl('/status'),
        { timeout: 10000 }
      );
      const targetBlockTimeSeconds =
        response.data.tokenomics?.target_block_time_seconds;

      return targetBlockTimeSeconds
        ? targetBlockTimeSeconds * 1000
        : FALLBACK_AVERAGE_SLOT_TIME_MS;
    } catch (error) {
      console.error('Error while fetching native average slot time', error);
      return FALLBACK_AVERAGE_SLOT_TIME_MS;
    }
  }

  const avgSlotTime = await getAverageSlotTime(sdk.k2Connection as any);
  return avgSlotTime || FALLBACK_AVERAGE_SLOT_TIME_MS;
};

export default getAvgSlotTime;
