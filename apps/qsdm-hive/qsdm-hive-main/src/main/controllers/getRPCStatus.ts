import axios from 'axios';
import { DASHBOARD_RPC_STATUS_URL } from 'config/dashboard';
import { MAINNET_QUERY_PARAM } from 'config/server';
import { SystemDbKeys } from 'config/systemDbKeys';
import { namespaceInstance } from 'main/node/helpers/Namespace';
import { RPCCachedData, RPCStatusResponse } from 'models/api/RPCStatus';
import { MAINNET_RPC_URL } from 'renderer/features/shared/constants';

import { getNetworkUrl } from './getNetworkUrl';

export const getRPCStatus = async (): Promise<RPCStatusResponse[] | null> => {
  let cachedData: RPCCachedData | null = null;

  try {
    cachedData = await namespaceInstance.storeGet(
      SystemDbKeys.RPCStatusInformation
    );

    if (cachedData) {
      const now = new Date();
      const cachedTime = new Date(cachedData.timestamp);
      const difference = (now.getTime() - cachedTime.getTime()) / 60000;
      const cachedDataIsFresh = difference < 10;

      if (cachedDataIsFresh) {
        return cachedData.data;
      }
    }

    const queryParams =
      getNetworkUrl() === MAINNET_RPC_URL
        ? MAINNET_QUERY_PARAM
        : '?isMainnet=false';

    const response = await axios.get(
      `${DASHBOARD_RPC_STATUS_URL}${queryParams}`
    );
    const data = response.data.rpcLoad;

    const formattedData: RPCStatusResponse[] = Object.entries(data).map(
      ([key, value]) => ({
        id: key,
        value: value as number,
        label: key.charAt(0).toUpperCase() + key.slice(1),
      })
    );

    const timestampedData: any = {
      timestamp: new Date(),
      data: formattedData,
    };

    namespaceInstance.storeSet(
      SystemDbKeys.RPCStatusInformation,
      timestampedData
    );

    return formattedData;
  } catch (error) {
    console.warn('RPC status lookup failed; keeping the app online.', error);
    return cachedData?.data || null;
  }
};
