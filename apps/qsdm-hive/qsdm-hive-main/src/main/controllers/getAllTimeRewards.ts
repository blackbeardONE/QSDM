import { SystemDbKeys } from 'config/systemDbKeys';
import { namespaceInstance } from 'main/node/helpers/Namespace';
import { GetAllTimeRewardsResponse } from 'models/api';

export const getAllTimeRewards =
  async (): Promise<GetAllTimeRewardsResponse> => {
    try {
      const allTimeRewardsStringified =
        await namespaceInstance.storeGet(SystemDbKeys.AllTimeRewards);
      if (!allTimeRewardsStringified) {
        return {};
      }

      if (typeof allTimeRewardsStringified !== 'string') {
        return allTimeRewardsStringified as GetAllTimeRewardsResponse;
      }

      const allTimeRewards = JSON.parse(allTimeRewardsStringified) as Record<
        string,
        number
      >;
      return allTimeRewards || {};
    } catch (err: any) {
      console.warn('All-time rewards cache is unavailable; using empty cache.', err);
      return {};
    }
  };
