import { getCurrentSlot as getCurrentSlotTaskNode } from 'vendor/qsdm-chain/taskNode';
import { buildQsdmCoreApiUrl, QSDM_TASK_RUNTIME_MODE } from 'config/qsdm';
import {
  getQsdmReadErrorMessage,
  qsdmGetJson,
} from 'main/services/qsdmHttpRead';
import sdk from 'main/services/sdk';

type QsdmStatusResponse = {
  chain_tip?: number;
};

let lastKnownNativeCurrentSlot = 0;

const getCurrentSlot = async (): Promise<number> => {
  if (QSDM_TASK_RUNTIME_MODE === 'qsdm-native') {
    try {
      const response = await qsdmGetJson<QsdmStatusResponse>(
        buildQsdmCoreApiUrl('/status'),
        { timeout: 4000 }
      );
      const chainTip = Number(response.chain_tip);
      if (Number.isFinite(chainTip) && chainTip >= 0) {
        lastKnownNativeCurrentSlot = chainTip;
      }
      return lastKnownNativeCurrentSlot;
    } catch (error) {
      console.warn(
        `QSDM Core status is temporarily unavailable; retaining chain height ${lastKnownNativeCurrentSlot}. ${
          getQsdmReadErrorMessage(error)
        }`
      );
      return lastKnownNativeCurrentSlot;
    }
  }

  return getCurrentSlotTaskNode(sdk.k2Connection as any);
};

export default getCurrentSlot;
