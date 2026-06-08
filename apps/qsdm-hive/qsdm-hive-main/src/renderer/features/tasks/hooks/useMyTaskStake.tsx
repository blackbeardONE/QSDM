import { useCallback, useMemo } from 'react';
import { useQuery, useQueryClient } from 'react-query';

import { QueryKeys } from 'renderer/services';

export function useMyTaskStake(
  taskAccountPubKey: string,
  taskType: 'CELL' | 'KPL' = 'CELL',
  shouldCache = true,
  revalidate = false
) {
  const queryClient = useQueryClient();
  const queryKey = useMemo(
    () => [QueryKeys.TaskStake, taskAccountPubKey, taskType, shouldCache],
    [taskAccountPubKey, taskType, shouldCache]
  );

  const { data, isLoading, error } = useQuery(
    queryKey,
    () => {
      return window.main.getMyTaskStake({
        taskAccountPubKey,
        shouldCache,
        revalidate,
        taskType,
      });
    },
    {
      enabled: !!taskAccountPubKey,
      staleTime: Infinity,
      cacheTime: Infinity,
    }
  );

  const refetchWithRevalidate = useCallback(async () => {
    const newStake = await window.main.getMyTaskStake({
      taskAccountPubKey,
      revalidate: true,
      shouldCache,
      taskType,
    });

    queryClient.setQueryData(queryKey, () => {
      return newStake;
    });

    return newStake;
  }, [queryClient, queryKey, shouldCache, taskAccountPubKey, taskType]);

  return { data, isLoading, error, refetch: refetchWithRevalidate };
}
