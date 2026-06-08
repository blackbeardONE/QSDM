import axios from 'axios';
import { getCurrentSlot as getCurrentSlotTaskNode } from 'vendor/qsdm-chain/taskNode';
import { buildQsdmCoreApiUrl, QSDM_TASK_RUNTIME_MODE } from 'config/qsdm';
import sdk from 'main/services/sdk';

type QsdmStatusResponse = {
  chain_tip?: number;
};

const getCurrentSlot = async (): Promise<number> => {
  if (QSDM_TASK_RUNTIME_MODE === 'qsdm-native') {
    try {
      const response = await axios.get<QsdmStatusResponse>(
        buildQsdmCoreApiUrl('/status'),
        { timeout: 10000 }
      );
      return response.data.chain_tip || 0;
    } catch (error) {
      console.error('Error while fetching native current slot', error);
      return 0;
    }
  }

  return getCurrentSlotTaskNode(sdk.k2Connection as any);
};

export default getCurrentSlot;
