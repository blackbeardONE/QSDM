import { QSDM_TASK_RUNTIME_MODE } from 'config/qsdm';
import { isQsdmMinerSystemTaskId } from 'config/qsdmSystemTasks';
import { getQsdmMinerProtocolRewardInfo } from 'main/services/qsdmMinerProtocolRewards';
import { getQsdmTaskActionSender } from 'main/services/qsdmTaskActionSigner';
import {
  getConfirmedQsdmTaskState,
  getQsdmTaskParticipantBySender,
  qsdmCellToDenomination,
  readFiniteNumber,
} from 'main/services/qsdmTaskStake';
import { GetAllTimeRewardsParam } from 'models/api';

import { ErrorType } from '../../models';
import { throwDetailedError } from '../../utils';

import { getAllTimeRewards } from './getAllTimeRewards';

export const getAllTimeRewardsByTask = async (
  _: Event,
  { taskId }: GetAllTimeRewardsParam
): Promise<number> => {
  try {
    if (QSDM_TASK_RUNTIME_MODE === 'qsdm-native') {
      if (isQsdmMinerSystemTaskId(taskId)) {
        try {
          const minerRewards = await getQsdmMinerProtocolRewardInfo();
          return minerRewards?.earnedDenomination || 0;
        } catch (error: any) {
          console.warn(
            `QSDM protocol rewards are temporarily unavailable for ${taskId}; retaining the local rewards cache.`,
            error?.message || error
          );
        }
      }

      const sender = getQsdmTaskActionSender();
      if (sender) {
        try {
          const state = await getConfirmedQsdmTaskState(taskId);
          const participant = getQsdmTaskParticipantBySender(
            state.task.participants,
            sender
          );
          const claimedRewards =
            readFiniteNumber(participant?.total_reward_claimed_amount) || 0;
          return qsdmCellToDenomination(claimedRewards);
        } catch (error) {
          console.warn(
            `Unable to load native QSDM all-time rewards for ${taskId}; falling back to local rewards cache.`,
            error instanceof Error ? error.message : String(error)
          );
        }
      }
    }

    const allTimeRewards = await getAllTimeRewards();
    const allTimeRewardsForTask = allTimeRewards[taskId] || 0;
    return allTimeRewardsForTask;
  } catch (err: any) {
    console.error('getting all time rewards: ', err);
    return throwDetailedError({
      detailed: err,
      type: ErrorType.GENERIC,
    });
  }
};
