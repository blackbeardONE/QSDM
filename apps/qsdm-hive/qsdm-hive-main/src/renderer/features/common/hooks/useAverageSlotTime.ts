import { useQuery } from 'react-query';

import { QSDM_BRIDGE_CONFIG } from 'config/qsdm';
import { AVERAGE_SLOT_TIME_DEFAULT_STALE_TIME } from 'config/refetchIntervals';
import { getAverageSlotTime, QueryKeys } from 'renderer/services';

const QSDM_NATIVE_AVERAGE_SLOT_STALE_TIME = 10 * 1000;
const QSDM_NATIVE_AVERAGE_SLOT_REFETCH_INTERVAL = 60 * 1000;

export const useAverageSlotTime = ({
  enabled = true,
}: {
  enabled?: boolean;
} = {}) => {
  const isQsdmNative = QSDM_BRIDGE_CONFIG.runtimeMode === 'qsdm-native';

  return useQuery([QueryKeys.AverageSlotTime], getAverageSlotTime, {
    staleTime: isQsdmNative
      ? QSDM_NATIVE_AVERAGE_SLOT_STALE_TIME
      : AVERAGE_SLOT_TIME_DEFAULT_STALE_TIME,
    refetchInterval: isQsdmNative
      ? QSDM_NATIVE_AVERAGE_SLOT_REFETCH_INTERVAL
      : AVERAGE_SLOT_TIME_DEFAULT_STALE_TIME,
    enabled,
  });
};
