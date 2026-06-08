import { useQuery } from 'react-query';

import { getCurrentSlot, QueryKeys } from 'renderer/services';
import { Task } from 'renderer/types';

export const useTaskRoundNumber = (task: Task) => {
  const { data: roundNumber } = useQuery(
    [QueryKeys.RoundTime, task.publicKey],
    async () => {
      const currentSlot = await getCurrentSlot();
      const startingSlot = Math.max(0, Math.floor(task.startingSlot || 0));
      const roundTime = Math.max(1, Math.floor(task.roundTime || 1));
      const currentRound = Math.floor(
        Math.max(0, currentSlot - startingSlot) / roundTime
      );

      return currentRound;
    },
    { refetchInterval: Math.max(5000, Math.min(60000, task.roundTime * 400)) }
  );

  return roundNumber;
};
